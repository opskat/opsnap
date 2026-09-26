// Package netchain 沿网络通道链路（SSH 跳板、SOCKS5 代理，最多 5 跳）建立连接。
// 主机密钥未确认或与保存的不一致时在认证前中止；失败以 *HopError 指出第几跳、哪个通道与原因。
package netchain

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"golang.org/x/crypto/ssh"
)

// MaxHops 从 OpsNap 出发的整条链路最多几跳
const MaxHops = 5

// DefaultTimeout ctx 没有截止时间时，一次建链（含全部跳）的整体超时
const DefaultTimeout = 30 * time.Second

// Kind 通道类型
type Kind string

const (
	KindSSH    Kind = "ssh"
	KindSOCKS5 Kind = "socks5"
)

// Hop 链路中的一跳（也用于描述最终的 SSH 目标主机）
type Hop struct {
	// ID 通道或数据源的标识，0 表示尚未保存；用于成环检查与错误定位
	ID   int64
	Name string
	Kind Kind
	Host string
	Port int

	// User SSH 用户名，或 SOCKS5 用户名（为空表示 SOCKS5 不认证）
	User string
	// Password SSH 密码或 SOCKS5 密码
	Password string
	// PrivateKey SSH 私钥（OpenSSH 或 PEM），与 Password 二选一
	PrivateKey []byte
	// Passphrase 私钥口令，私钥未加密时忽略
	Passphrase []byte
	// HostKey 保存的 SSH 主机密钥 SHA256 指纹（SHA256:...），为空表示尚未确认
	HostKey string
}

// String 只输出名称、地址与类型，避免秘密被 %v 打进日志
func (h Hop) String() string {
	return fmt.Sprintf("%s %s（%s）", h.Name, h.addr(), h.Kind.label())
}

// GoString 同 String，%#v 也不输出秘密
func (h Hop) GoString() string { return "netchain.Hop{" + h.String() + "}" }

func (h Hop) addr() string { return net.JoinHostPort(h.Host, strconv.Itoa(h.Port)) }

func (h Hop) validate() error {
	switch {
	case h.Kind != KindSSH && h.Kind != KindSOCKS5:
		return fmt.Errorf("%w：未知的类型 %q", ErrInvalidHop, h.Kind)
	case h.Host == "":
		return fmt.Errorf("%w：缺少主机", ErrInvalidHop)
	case h.Port < 1 || h.Port > 65535:
		return fmt.Errorf("%w：端口 %d 超出 1–65535", ErrInvalidHop, h.Port)
	case h.Kind == KindSSH && h.User == "":
		return fmt.Errorf("%w：缺少 SSH 用户名", ErrInvalidHop)
	case h.Kind == KindSSH && (h.Password == "") == (len(h.PrivateKey) == 0):
		return fmt.Errorf("%w：SSH 认证需要密码或私钥之一", ErrInvalidHop)
	}
	return nil
}

// signer 解析 SSH 私钥；密码认证或 SOCKS5 返回 nil
func (h Hop) signer() (ssh.Signer, error) {
	if h.Kind != KindSSH || len(h.PrivateKey) == 0 {
		return nil, nil
	}
	return ParsePrivateKey(h.PrivateKey, h.Passphrase)
}

// Chain 校验过的链路
type Chain struct {
	hops    []Hop
	signers []ssh.Signer
}

// NewChain 校验并组成链路，hops 按从 OpsNap 出发的顺序排列。
// 拒绝超过 MaxHops 跳（ErrTooManyHops）、同一通道出现两次（ErrCycle）与不完整的跳（ErrInvalidHop）；
// 私钥问题在联网前以 *HopError 报告。
func NewChain(hops []Hop) (*Chain, error) {
	if len(hops) > MaxHops {
		return nil, fmt.Errorf("%w：当前 %d 跳", ErrTooManyHops, len(hops))
	}
	c := &Chain{hops: append([]Hop(nil), hops...), signers: make([]ssh.Signer, len(hops))}
	seen := map[int64]bool{}
	for i, h := range hops {
		if h.ID != 0 {
			if seen[h.ID] {
				return nil, fmt.Errorf("%w：通道 %s 出现了两次", ErrCycle, h.Name)
			}
			seen[h.ID] = true
		}
		if err := h.validate(); err != nil {
			return nil, fmt.Errorf("第 %d 跳 %s：%w", i+1, h.Name, err)
		}
		s, err := h.signer()
		if err != nil {
			return nil, newHopError(context.Background(), i+1, h, err)
		}
		c.signers[i] = s
	}
	return c, nil
}

// dialFunc 经已建立的部分链路拨号；自身某一跳失败时返回 *HopError，否则返回普通错误（归因于被拨的目标）
type dialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// Tunnel 已建立的链路，可并发拨号，用完后 Close
type Tunnel struct {
	hops    int
	dial    dialFunc
	clients []*ssh.Client
}

// withDefaultTimeout ctx 没有截止时间时加上 DefaultTimeout
func withDefaultTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, DefaultTimeout)
}

// Connect 沿链路依次建立每一跳：SSH 跳校验主机密钥后认证，SOCKS5 跳完成方法协商与认证。
// 整体受 ctx 约束，ctx 没有截止时间时为 DefaultTimeout。失败返回 *HopError。
func (c *Chain) Connect(ctx context.Context) (*Tunnel, error) {
	ctx, cancel := withDefaultTimeout(ctx)
	defer cancel()
	var d net.Dialer
	t := &Tunnel{dial: d.DialContext}
	for i, h := range c.hops {
		if err := t.extend(ctx, i+1, h, c.signers[i]); err != nil {
			_ = t.Close()
			return nil, err
		}
	}
	return t, nil
}

// extend 把第 index 跳接到链路末端
func (t *Tunnel) extend(ctx context.Context, index int, h Hop, signer ssh.Signer) error {
	switch h.Kind {
	case KindSSH:
		cl, err := t.sshClient(ctx, index, h, signer)
		if err != nil {
			return err
		}
		t.clients = append(t.clients, cl)
		t.dial = cl.DialContext
	case KindSOCKS5:
		s := &socksDialer{prev: t.dial, index: index, hop: h}
		conn, err := s.open(ctx)
		if err != nil {
			return err
		}
		_ = conn.Close()
		t.dial = s.dial
	}
	t.hops = index
	return nil
}

func (t *Tunnel) sshClient(ctx context.Context, index int, h Hop, signer ssh.Signer) (*ssh.Client, error) {
	conn, err := t.dial(ctx, "tcp", h.addr())
	if err != nil {
		return nil, newHopError(ctx, index, h, err)
	}
	cl, err := sshHandshake(ctx, conn, h, signer)
	if err != nil {
		_ = conn.Close()
		return nil, newHopError(ctx, index, h, err)
	}
	return cl, nil
}

// Dial 经链路连接 addr，签名与 pgx 的 DialFunc 一致。
// 链路中某一跳失败时返回 *HopError，连不上 addr 本身时返回普通错误。
func (t *Tunnel) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	conn, err := t.dial(ctx, network, addr)
	if err != nil {
		var he *HopError
		if errors.As(err, &he) {
			return nil, err
		}
		return nil, fmt.Errorf("连接 %s 失败: %w", addr, err)
	}
	return conn, nil
}

// DialSSH 经链路登录最终的 SSH 主机，规则与链路中的 SSH 跳相同：主机密钥未确认或已变化时不认证。
// 失败返回 *HopError，目标的序号为链路跳数加 1。返回的客户端需在 Tunnel 之前关闭。
func (t *Tunnel) DialSSH(ctx context.Context, target Hop) (*ssh.Client, error) {
	if target.Kind != KindSSH {
		return nil, fmt.Errorf("%w：目标不是 SSH 主机", ErrInvalidHop)
	}
	if err := target.validate(); err != nil {
		return nil, err
	}
	index := t.hops + 1
	signer, err := target.signer()
	if err != nil {
		return nil, newHopError(ctx, index, target, err)
	}
	ctx, cancel := withDefaultTimeout(ctx)
	defer cancel()
	return t.sshClient(ctx, index, target, signer)
}

// Close 从末端开始断开链路上的所有 SSH 连接
func (t *Tunnel) Close() error {
	var errs []error
	for i := len(t.clients) - 1; i >= 0; i-- {
		if err := t.clients[i].Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			errs = append(errs, err)
		}
	}
	t.clients = nil
	return errors.Join(errs...)
}
