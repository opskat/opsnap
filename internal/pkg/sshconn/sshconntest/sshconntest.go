// Package sshconntest 提供测试用的进程内 SSH 服务、SOCKS5 与 HTTP CONNECT 代理、
// 只接受连接不应答的黑洞监听和回显服务，全部只监听 127.0.0.1。
package sshconntest

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/pkg/sftp"
)

// listen 在 127.0.0.1 的随机端口监听，测试结束时关闭
func listen(t testing.TB) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln
}

// serve 接受连接并交给 handle，监听关闭后退出
func serve(ln net.Listener, handle func(net.Conn)) {
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go handle(c)
		}
	}()
}

// recorder 记录服务收到的请求，handler 在各自的 goroutine 里执行，所以加锁
type recorder struct {
	mu    sync.Mutex
	items []string
	n     int
}

func (r *recorder) add(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items = append(r.items, s)
}

func (r *recorder) inc() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.n++
}

func (r *recorder) list() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.items...)
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.n
}

// pipe 双向转发直到任一方向结束
func pipe(a, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(a, b); done <- struct{}{} }()
	go func() { _, _ = io.Copy(b, a); done <- struct{}{} }()
	<-done
	_ = a.Close()
	_ = b.Close()
}

// StartEcho 启动回显服务，返回监听地址
func StartEcho(t testing.TB) string {
	t.Helper()
	ln := listen(t)
	serve(ln, func(c net.Conn) {
		defer func() { _ = c.Close() }()
		_, _ = io.Copy(c, c)
	})
	return ln.Addr().String()
}

// StartBlackhole 启动只接受连接、从不应答的服务，用来制造握手超时
func StartBlackhole(t testing.TB) string {
	t.Helper()
	ln := listen(t)
	var mu sync.Mutex
	var held []net.Conn
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		for _, c := range held {
			_ = c.Close()
		}
	})
	serve(ln, func(c net.Conn) {
		mu.Lock()
		defer mu.Unlock()
		held = append(held, c)
	})
	return ln.Addr().String()
}

// Key 测试用的密钥对
type Key struct {
	PrivatePEM string // OpenSSH 格式私钥
	Public     ssh.PublicKey
}

// GenerateKey 生成 ed25519 密钥；passphrase 非空时私钥用它加密
func GenerateKey(t testing.TB, passphrase string) Key {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成密钥失败: %v", err)
	}
	var block *pem.Block
	if passphrase == "" {
		block, err = ssh.MarshalPrivateKey(priv, "")
	} else {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(passphrase))
	}
	if err != nil {
		t.Fatalf("编码私钥失败: %v", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("转换公钥失败: %v", err)
	}
	return Key{PrivatePEM: string(pem.EncodeToMemory(block)), Public: sshPub}
}

// SSHServer 进程内 SSH 服务：支持密码与公钥认证、direct-tcpip 通道（端口转发）
type SSHServer struct {
	Addr        string
	Fingerprint string // 主机密钥的 SHA256 指纹

	authAttempts recorder
	directTCPIP  recorder
	conns        recorder
	sessions     recorder

	sftpRoot string
	noSFTP   bool
}

// SSHOptions SSH 服务的认证设置；Password 与 AuthorizedKey 都为空时拒绝一切认证
type SSHOptions struct {
	User          string
	Password      string
	AuthorizedKey ssh.PublicKey
	// ExtraECDSAHostKey 另外再出示一把 ECDSA 主机密钥（像默认安装的 OpenSSH 那样同时有多种主机密钥）；
	// SSHServer.Fingerprint 仍是 ed25519 主机密钥的指纹
	ExtraECDSAHostKey bool
	// SFTPRoot 非空时提供 sftp 子系统，服务这个目录（作为 /）。
	// 留空时像没有 `Subsystem sftp` 的服务器那样拒绝子系统请求。
	SFTPRoot string
	// NoSFTP 显式拒绝 sftp 子系统，即使 SFTPRoot 有值
	NoSFTP bool
}

// StartSSH 启动 SSH 服务
func StartSSH(t testing.TB, opts SSHOptions) *SSHServer {
	t.Helper()
	_, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成主机密钥失败: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(hostPriv)
	if err != nil {
		t.Fatalf("创建主机密钥失败: %v", err)
	}
	s := &SSHServer{Fingerprint: ssh.FingerprintSHA256(signer.PublicKey())}
	s.sftpRoot = opts.SFTPRoot
	s.noSFTP = opts.NoSFTP
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(meta ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			s.authAttempts.inc()
			if opts.Password != "" && meta.User() == opts.User && string(pw) == opts.Password {
				return &ssh.Permissions{}, nil
			}
			return nil, fmt.Errorf("密码错误")
		},
		PublicKeyCallback: func(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			s.authAttempts.inc()
			if opts.AuthorizedKey != nil && meta.User() == opts.User &&
				string(key.Marshal()) == string(opts.AuthorizedKey.Marshal()) {
				return &ssh.Permissions{}, nil
			}
			return nil, fmt.Errorf("公钥未授权")
		},
	}
	cfg.AddHostKey(signer)
	if opts.ExtraECDSAHostKey {
		ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatalf("生成 ECDSA 主机密钥失败: %v", err)
		}
		ecSigner, err := ssh.NewSignerFromKey(ecKey)
		if err != nil {
			t.Fatalf("创建 ECDSA 主机密钥失败: %v", err)
		}
		cfg.AddHostKey(ecSigner)
	}
	ln := listen(t)
	s.Addr = ln.Addr().String()
	serve(ln, func(c net.Conn) { s.handle(c, cfg) })
	return s
}

func (s *SSHServer) handle(c net.Conn, cfg *ssh.ServerConfig) {
	s.conns.inc()
	sc, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		_ = c.Close()
		return
	}
	defer func() { _ = sc.Close() }()
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		switch nc.ChannelType() {
		case "direct-tcpip":
			s.handleDirectTCPIP(nc)
		case "session":
			s.handleSession(nc, cfg)
		default:
			_ = nc.Reject(ssh.UnknownChannelType, "只支持 direct-tcpip 与 session")
		}
	}
}

// handleDirectTCPIP 处理端口转发通道
func (s *SSHServer) handleDirectTCPIP(nc ssh.NewChannel) {
	{
		var payload struct {
			Host     string
			Port     uint32
			OrigHost string
			OrigPort uint32
		}
		if err := ssh.Unmarshal(nc.ExtraData(), &payload); err != nil {
			_ = nc.Reject(ssh.ConnectionFailed, "请求格式错误")
			return
		}
		target := net.JoinHostPort(payload.Host, strconv.Itoa(int(payload.Port)))
		s.directTCPIP.add(target)
		up, err := net.DialTimeout("tcp", target, 5*time.Second)
		if err != nil {
			_ = nc.Reject(ssh.ConnectionFailed, err.Error())
			return
		}
		ch, chReqs, err := nc.Accept()
		if err != nil {
			_ = up.Close()
			return
		}
		go ssh.DiscardRequests(chReqs)
		go pipe(chanConn{ch}, up)
	}
}

// Sessions 收到的 session 通道数
func (s *SSHServer) Sessions() int { return s.sessions.count() }

// handleSession 处理 session 通道：目前只支持 sftp 子系统
func (s *SSHServer) handleSession(nc ssh.NewChannel, _ *ssh.ServerConfig) {
	s.sessions.inc()
	ch, reqs, err := nc.Accept()
	if err != nil {
		return
	}
	for req := range reqs {
		switch req.Type {
		case "subsystem":
			var payload struct{ Name string }
			_ = ssh.Unmarshal(req.Payload, &payload)
			if payload.Name != "sftp" || s.noSFTP || s.sftpRoot == "" {
				_ = req.Reply(false, nil)
				_ = ch.Close()
				return
			}
			if err := req.Reply(true, nil); err != nil {
				_ = ch.Close()
				return
			}
			srv := sftp.NewRequestServer(ch, sftp.Handlers{
				FileList: sftpHandler{root: s.sftpRoot},
			})
			go func() {
				defer func() { _ = ch.Close() }()
				_ = srv.Serve()
			}()
			return
		default:
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
		}
	}
	_ = ch.Close()
}

// AuthAttempts 收到的认证尝试次数（密码与公钥）
func (s *SSHServer) AuthAttempts() int { return s.authAttempts.count() }

// Connections 收到的 TCP 连接数
func (s *SSHServer) Connections() int { return s.conns.count() }

// DirectTCPIP 经它转发过的目标地址，按请求顺序
func (s *SSHServer) DirectTCPIP() []string { return s.directTCPIP.list() }

// chanConn 把 ssh.Channel 包成 net.Conn，只用于 pipe 的读写与关闭
type chanConn struct{ ssh.Channel }

func (chanConn) LocalAddr() net.Addr              { return nil }
func (chanConn) RemoteAddr() net.Addr             { return nil }
func (chanConn) SetDeadline(time.Time) error      { return nil }
func (chanConn) SetReadDeadline(time.Time) error  { return nil }
func (chanConn) SetWriteDeadline(time.Time) error { return nil }
func (c chanConn) Write(p []byte) (int, error)    { return c.Channel.Write(p) }
func (c chanConn) Read(p []byte) (int, error)     { return c.Channel.Read(p) }
func (c chanConn) Close() error                   { return c.Channel.Close() }

var _ net.Conn = chanConn{}

// Proxy 进程内代理（SOCKS5 或 HTTP CONNECT）
type Proxy struct {
	Addr    string
	targets recorder
}

// Targets 经代理请求连接的目标地址，按请求顺序
func (p *Proxy) Targets() []string { return p.targets.list() }

// ProxyOptions 代理设置；User 为空表示不要求认证。Delay 是应答握手前的等待，用来验证逐跳延迟。
type ProxyOptions struct {
	User     string
	Password string
	Delay    time.Duration
}

// StartSOCKS5 启动只支持 CONNECT 的 SOCKS5 代理
func StartSOCKS5(t testing.TB, opts ProxyOptions) *Proxy {
	t.Helper()
	ln := listen(t)
	p := &Proxy{Addr: ln.Addr().String()}
	serve(ln, func(c net.Conn) { p.handleSOCKS5(c, opts) })
	return p
}

func (p *Proxy) handleSOCKS5(c net.Conn, opts ProxyOptions) {
	defer func() { _ = c.Close() }()
	head := make([]byte, 2)
	if _, err := io.ReadFull(c, head); err != nil || head[0] != 5 {
		return
	}
	if _, err := io.ReadFull(c, make([]byte, head[1])); err != nil {
		return
	}
	time.Sleep(opts.Delay)
	if opts.User == "" {
		if _, err := c.Write([]byte{5, 0}); err != nil {
			return
		}
	} else {
		if _, err := c.Write([]byte{5, 2}); err != nil {
			return
		}
		ah := make([]byte, 2)
		if _, err := io.ReadFull(c, ah); err != nil || ah[0] != 1 {
			return
		}
		user := make([]byte, ah[1])
		if _, err := io.ReadFull(c, user); err != nil {
			return
		}
		pl := make([]byte, 1)
		if _, err := io.ReadFull(c, pl); err != nil {
			return
		}
		pass := make([]byte, pl[0])
		if _, err := io.ReadFull(c, pass); err != nil {
			return
		}
		if string(user) != opts.User || string(pass) != opts.Password {
			_, _ = c.Write([]byte{1, 1})
			return
		}
		if _, err := c.Write([]byte{1, 0}); err != nil {
			return
		}
	}
	req := make([]byte, 4)
	if _, err := io.ReadFull(c, req); err != nil || req[0] != 5 || req[1] != 1 {
		return
	}
	var host string
	switch req[3] {
	case 1:
		b := make([]byte, 4)
		if _, err := io.ReadFull(c, b); err != nil {
			return
		}
		host = net.IP(b).String()
	case 3:
		l := make([]byte, 1)
		if _, err := io.ReadFull(c, l); err != nil {
			return
		}
		b := make([]byte, l[0])
		if _, err := io.ReadFull(c, b); err != nil {
			return
		}
		host = string(b)
	case 4:
		b := make([]byte, 16)
		if _, err := io.ReadFull(c, b); err != nil {
			return
		}
		host = net.IP(b).String()
	default:
		return
	}
	pb := make([]byte, 2)
	if _, err := io.ReadFull(c, pb); err != nil {
		return
	}
	target := net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(pb))))
	p.targets.add(target)
	up, err := net.DialTimeout("tcp", target, 5*time.Second)
	if err != nil {
		_, _ = c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	if _, err := c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		_ = up.Close()
		return
	}
	pipe(c, up)
}

// StartHTTPConnect 启动 HTTP CONNECT 代理；要求认证时校验 Proxy-Authorization 的 Basic 凭据
func StartHTTPConnect(t testing.TB, opts ProxyOptions) *Proxy {
	t.Helper()
	ln := listen(t)
	p := &Proxy{Addr: ln.Addr().String()}
	serve(ln, func(c net.Conn) { p.handleHTTP(c, opts) })
	return p
}

func (p *Proxy) handleHTTP(c net.Conn, opts ProxyOptions) {
	br := bufio.NewReader(c)
	req, err := http.ReadRequest(br)
	if err != nil || req.Method != http.MethodConnect {
		_ = c.Close()
		return
	}
	time.Sleep(opts.Delay)
	if opts.User != "" {
		want := "Basic " + base64.StdEncoding.EncodeToString([]byte(opts.User+":"+opts.Password))
		if strings.TrimSpace(req.Header.Get("Proxy-Authorization")) != want {
			_, _ = io.WriteString(c, "HTTP/1.1 407 Proxy Authentication Required\r\nContent-Length: 0\r\n\r\n")
			_ = c.Close()
			return
		}
	}
	p.targets.add(req.Host)
	up, err := net.DialTimeout("tcp", req.Host, 5*time.Second)
	if err != nil {
		_, _ = io.WriteString(c, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
		_ = c.Close()
		return
	}
	if _, err := io.WriteString(c, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		_ = up.Close()
		_ = c.Close()
		return
	}
	pipe(bufferedConn{Conn: c, r: br}, up)
}

// bufferedConn 读取时先读完 bufio 里已缓存的数据
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (b bufferedConn) Read(p []byte) (int, error) { return b.r.Read(p) }

// HostPort 拆出地址的主机与端口，方便测试组装配置
func HostPort(t testing.TB, addr string) (string, int) {
	t.Helper()
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("地址格式错误 %q: %v", addr, err)
	}
	port, err := strconv.Atoi(p)
	if err != nil {
		t.Fatalf("端口格式错误 %q: %v", addr, err)
	}
	return h, port
}
