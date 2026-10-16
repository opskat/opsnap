// Package sshconn 按「本机 → 第 1 跳 → … → 目标」逐跳建立到目标 SSH 主机的连接。
// 每一跳可以是 SSH 跳板、SOCKS5 或 HTTP CONNECT 代理；逐跳记录延迟，
// 首个失败处停止并给出原因（超时、认证失败、主机指纹不符）。
// SSH 主机的指纹必须与已核对的一致：没有已核对指纹时只返回指纹、不发送凭据。
package sshconn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// DefaultTimeout 建立每一跳的默认时限
const DefaultTimeout = 10 * time.Second

// Kind 一跳的类型
type Kind string

const (
	KindSSH    Kind = "ssh"    // SSH 跳板或目标主机
	KindSOCKS5 Kind = "socks5" // SOCKS5 代理
	KindHTTP   Kind = "http"   // HTTP CONNECT 代理
)

// Valid 是否是支持的类型
func (k Kind) Valid() bool {
	return k == KindSSH || k == KindSOCKS5 || k == KindHTTP
}

// Reason 一跳失败的原因
type Reason string

const (
	ReasonTimeout                Reason = "timeout"                 // 超时
	ReasonAuthFailed             Reason = "auth_failed"             // 认证失败
	ReasonFingerprintMismatch    Reason = "fingerprint_mismatch"    // 主机指纹与已核对的不一致
	ReasonFingerprintUnconfirmed Reason = "fingerprint_unconfirmed" // 首次连接，指纹待核对
	ReasonUnreachable            Reason = "unreachable"             // 连接被拒绝、地址不可达等
	ReasonCanceled               Reason = "canceled"                // 调用方取消
)

// Label 原因的中文名称
func (r Reason) Label() string {
	switch r {
	case ReasonTimeout:
		return "超时"
	case ReasonAuthFailed:
		return "认证失败"
	case ReasonFingerprintMismatch:
		return "指纹不符"
	case ReasonFingerprintUnconfirmed:
		return "指纹待核对"
	case ReasonCanceled:
		return "已取消"
	}
	return "连接失败"
}

var (
	// ErrHostKeyChanged SSH 主机出示的指纹与已核对的不一致，拒绝连接
	ErrHostKeyChanged = errors.New("主机指纹已变化")
	// ErrHostKeyUnconfirmed SSH 主机还没有已核对的指纹，只返回指纹供核对，不发送凭据
	ErrHostKeyUnconfirmed = errors.New("首次连接，请核对主机指纹")

	// errAuth 标记认证类失败（SSH 认证、私钥口令、代理用户名密码）
	errAuth = errors.New("认证失败")
)

// Hop 一跳的连接参数，凭据均为明文，只在内存中使用
type Hop struct {
	Kind Kind
	Name string // 展示用名称
	Host string
	Port int
	User string
	// Password SSH 密码，或代理的密码
	Password string
	// PrivateKey、Passphrase SSH 私钥（PEM/OpenSSH 格式）及其口令
	PrivateKey string
	Passphrase string
	// Fingerprint 已核对的 SSH 主机指纹（SHA256:...）；为空表示首次连接，只取指纹不认证
	Fingerprint string
}

// Addr 主机:端口
func (h Hop) Addr() string { return net.JoinHostPort(h.Host, strconv.Itoa(h.Port)) }

// Config 一条完整的连接路径
type Config struct {
	Hops    []Hop         // 代理链，按连接顺序，可以为空
	Target  Hop           // 目标 SSH 主机
	Timeout time.Duration // 建立每一跳的时限，0 表示 DefaultTimeout
}

// HopResult 一跳的连接结果。Index 从 1 开始，目标是最后一跳。
type HopResult struct {
	Index       int           `json:"index"`
	Kind        Kind          `json:"kind"`
	Name        string        `json:"name"`
	Addr        string        `json:"addr"`
	OK          bool          `json:"ok"`
	Latency     time.Duration `json:"-"`
	LatencyMs   int64         `json:"latency_ms"`
	Reason      Reason        `json:"reason,omitempty"`
	Message     string        `json:"message,omitempty"`
	Fingerprint string        `json:"fingerprint,omitempty"` // SSH 主机实际出示的指纹
}

// HopError 某一跳失败。errors.Is 可判断 ErrHostKeyChanged、ErrHostKeyUnconfirmed。
type HopError struct {
	Index   int
	Kind    Kind
	Name    string
	Addr    string
	Reason  Reason
	Message string
	Err     error
}

func (e *HopError) Error() string {
	name := ""
	if e.Name != "" {
		name = " " + e.Name
	}
	return fmt.Sprintf("第 %d 跳%s（%s %s）%s", e.Index, name, e.Kind, e.Addr, e.Message)
}

func (e *HopError) Unwrap() error { return e.Err }

// Conn 到目标 SSH 主机的连接，关闭时先停止端口转发，再按相反顺序关闭整条链
type Conn struct {
	client  *ssh.Client
	closers []io.Closer
	once    sync.Once

	mu         sync.Mutex
	closed     bool
	forwarders []io.Closer
}

// Client 目标主机的 SSH 客户端，可用来开会话执行命令（例如 rsync --server）
func (c *Conn) Client() *ssh.Client { return c.client }

// Close 关闭目标连接以及链上的所有跳板与代理连接
func (c *Conn) Close() error {
	c.once.Do(func() {
		c.mu.Lock()
		c.closed = true
		forwarders := c.forwarders
		c.forwarders = nil
		c.mu.Unlock()
		closeAll(forwarders)
		closeAll(c.closers)
	})
	return nil
}

func closeAll(closers []io.Closer) {
	for i := len(closers) - 1; i >= 0; i-- {
		_ = closers[i].Close()
	}
}

// dialFunc 经已建立的链连接下一地址
type dialFunc func(ctx context.Context, addr string) (net.Conn, error)

func directDial(ctx context.Context, addr string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "tcp", addr)
}

// prevHopError 经代理请求下一地址时，失败原因属于代理这一跳（例如 HTTP 407）
type prevHopError struct{ err error }

func (e *prevHopError) Error() string { return e.err.Error() }
func (e *prevHopError) Unwrap() error { return e.err }

// DialConfig 按配置逐跳连接。成功时返回连接与每一跳的结果；
// 失败时返回到失败那一跳为止的结果（最后一项是失败的那一跳）和 *HopError，后面的跳不再尝试。
func DialConfig(ctx context.Context, cfg Config) (*Conn, []HopResult, error) {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	target := cfg.Target
	if target.Kind == "" {
		target.Kind = KindSSH
	}
	if target.Kind != KindSSH {
		return nil, nil, fmt.Errorf("目标必须是 SSH 主机，不能是 %s", target.Kind)
	}
	hops := append(append(make([]Hop, 0, len(cfg.Hops)+1), cfg.Hops...), target)

	var (
		closers []io.Closer
		results          = make([]HopResult, 0, len(hops))
		via     dialFunc = directDial
		client  *ssh.Client
	)
	fail := func(r HopResult, err error) (*Conn, []HopResult, error) {
		closeAll(closers)
		reason, msg, wrapped := classify(ctx, err)
		r.OK, r.Reason, r.Message = false, reason, msg
		r.LatencyMs = r.Latency.Milliseconds()
		results = append(results, r)
		return nil, results, &HopError{
			Index: r.Index, Kind: r.Kind, Name: r.Name, Addr: r.Addr,
			Reason: reason, Message: msg, Err: wrapped,
		}
	}

	for i, h := range hops {
		if !h.Kind.Valid() {
			closeAll(closers)
			return nil, results, fmt.Errorf("第 %d 跳的类型 %q 不受支持", i+1, h.Kind)
		}
		r := HopResult{Index: i + 1, Kind: h.Kind, Name: h.Name, Addr: h.Addr()}
		start := time.Now()
		stepCtx, cancel := context.WithTimeout(ctx, timeout)
		conn, err := via(stepCtx, h.Addr())
		if err != nil {
			cancel()
			var pe *prevHopError
			if errors.As(err, &pe) && len(results) > 0 {
				// 失败属于上一跳（代理）：把它改记为失败，本跳不计入
				prev := results[len(results)-1]
				results = results[:len(results)-1]
				return fail(prev, pe.err)
			}
			r.Latency = time.Since(start)
			return fail(r, err)
		}
		closers = append(closers, conn)

		switch h.Kind {
		case KindSSH:
			c, fp, err := sshHandshake(stepCtx, conn, h)
			r.Fingerprint = fp
			if err != nil {
				cancel()
				r.Latency = time.Since(start)
				return fail(r, err)
			}
			closers = append(closers, c)
			client = c
			via = func(ctx context.Context, addr string) (net.Conn, error) {
				return c.DialContext(ctx, "tcp", addr)
			}
		case KindSOCKS5:
			if err := socks5Greet(stepCtx, conn, h.User, h.Password); err != nil {
				cancel()
				r.Latency = time.Since(start)
				return fail(r, err)
			}
			via = func(ctx context.Context, addr string) (net.Conn, error) {
				return socks5Connect(ctx, conn, addr)
			}
		case KindHTTP:
			user, pass := h.User, h.Password
			via = func(ctx context.Context, addr string) (net.Conn, error) {
				return httpConnect(ctx, conn, addr, user, pass)
			}
		}
		cancel()
		r.OK = true
		r.Latency = time.Since(start)
		r.LatencyMs = r.Latency.Milliseconds()
		results = append(results, r)
	}
	return &Conn{client: client, closers: closers}, results, nil
}

// withDeadline 在 ctx 结束时关闭 conn，让阻塞的握手读写返回。
// 返回的 stop 报告 ctx 是否已在握手期间结束。
func withDeadline(ctx context.Context, conn net.Conn) (expired func() bool) {
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	return func() bool { return !stop() }
}

// hostKeyAlgorithms 优先让服务器出示 ed25519 主机密钥：面板让用户用 ssh_host_ed25519_key.pub 核对指纹，
// 而 x/crypto/ssh 默认先选 ECDSA。服务器没有 ed25519 密钥时依次退到 ECDSA、RSA。
var hostKeyAlgorithms = []string{
	ssh.KeyAlgoED25519,
	ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521,
	ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA,
}

// sshHandshake 在已建立的流上完成 SSH 握手与认证，返回客户端和主机实际出示的指纹
func sshHandshake(ctx context.Context, conn net.Conn, h Hop) (*ssh.Client, string, error) {
	auth, err := authMethods(h)
	if err != nil {
		return nil, "", err
	}
	var presented string
	var keyErr error
	cfg := &ssh.ClientConfig{
		User:              h.User,
		Auth:              auth,
		HostKeyAlgorithms: hostKeyAlgorithms,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			presented = ssh.FingerprintSHA256(key)
			switch {
			case h.Fingerprint == "":
				keyErr = ErrHostKeyUnconfirmed
			case h.Fingerprint != presented:
				keyErr = ErrHostKeyChanged
			}
			return keyErr
		},
	}
	expired := withDeadline(ctx, conn)
	c, chans, reqs, err := ssh.NewClientConn(conn, h.Addr(), cfg)
	if expired() {
		if c != nil {
			_ = c.Close()
		}
		return nil, presented, ctx.Err()
	}
	if err != nil {
		if keyErr != nil {
			return nil, presented, &fingerprintError{sentinel: keyErr, want: h.Fingerprint, got: presented}
		}
		if strings.Contains(err.Error(), "unable to authenticate") {
			return nil, presented, fmt.Errorf("%w：%v", errAuth, err)
		}
		return nil, presented, err
	}
	return ssh.NewClient(c, chans, reqs), presented, nil
}

// fingerprintError 指纹不符或待核对，带上已核对与实际的指纹
type fingerprintError struct {
	sentinel  error
	want, got string
}

func (e *fingerprintError) Error() string {
	if errors.Is(e.sentinel, ErrHostKeyChanged) {
		return fmt.Sprintf("%v：已核对 %s，实际 %s", e.sentinel, e.want, e.got)
	}
	return fmt.Sprintf("%v %s", e.sentinel, e.got)
}

func (e *fingerprintError) Unwrap() error { return e.sentinel }

// authMethods 按配置生成认证方式：私钥优先，其次密码（同时应答 keyboard-interactive）
func authMethods(h Hop) ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod
	if h.PrivateKey != "" {
		signer, err := ParsePrivateKey(h.PrivateKey, h.Passphrase)
		if err != nil {
			return nil, err
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}
	if h.Password != "" {
		pw := h.Password
		methods = append(methods,
			ssh.Password(pw),
			ssh.KeyboardInteractive(func(_, _ string, questions []string, _ []bool) ([]string, error) {
				answers := make([]string, len(questions))
				for i := range answers {
					answers[i] = pw
				}
				return answers, nil
			}),
		)
	}
	return methods, nil
}

// ParsePrivateKey 解析私钥；有口令时用口令解密。错误信息不含私钥内容。
func ParsePrivateKey(pemText, passphrase string) (ssh.Signer, error) {
	var (
		signer ssh.Signer
		err    error
	)
	if passphrase != "" {
		signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(pemText), []byte(passphrase))
	} else {
		signer, err = ssh.ParsePrivateKey([]byte(pemText))
	}
	if err != nil {
		var missing *ssh.PassphraseMissingError
		if errors.As(err, &missing) {
			return nil, fmt.Errorf("%w：私钥有口令，但没有提供", errAuth)
		}
		return nil, fmt.Errorf("%w：私钥无法解析或口令错误", errAuth)
	}
	return signer, nil
}

// classify 把一跳的错误归类成原因和展示文案，并返回供 errors.Is 判断的错误
func classify(ctx context.Context, err error) (Reason, string, error) {
	var fe *fingerprintError
	switch {
	case errors.As(err, &fe) && errors.Is(fe.sentinel, ErrHostKeyChanged):
		return ReasonFingerprintMismatch, fe.Error(), err
	case errors.As(err, &fe):
		return ReasonFingerprintUnconfirmed, fe.Error(), err
	case errors.Is(err, errAuth):
		return ReasonAuthFailed, err.Error(), err
	case errors.Is(ctx.Err(), context.Canceled):
		return ReasonCanceled, ReasonCanceled.Label(), ctx.Err()
	case isTimeout(err):
		return ReasonTimeout, ReasonTimeout.Label(), err
	}
	return ReasonUnreachable, fmt.Sprintf("%s：%v", ReasonUnreachable.Label(), err), err
}

func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
