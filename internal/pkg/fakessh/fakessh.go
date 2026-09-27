// Package fakessh 是测试用的进程内 SSH 服务端与 SOCKS5 代理。
// SSH 支持密码与公钥认证、exec 命令、direct-tcpip 转发，并记录认证尝试、执行过的命令与转发目标，
// 供链路拨号、控制器测试与 e2e 假服务二进制复用。
package fakessh

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// ExecFunc 处理一条 exec 命令，返回标准输出、标准错误与退出码
type ExecFunc func(cmd string) (stdout, stderr string, exitCode int)

// Config SSH 服务端配置
type Config struct {
	// User 唯一接受的用户名
	User string
	// Password 为空时不接受密码认证
	Password string
	// AuthorizedKeys 接受的公钥
	AuthorizedKeys []ssh.PublicKey
	// HostKey 为空时生成一把 Ed25519 主机密钥
	HostKey ssh.Signer
	// Exec 为空时使用 DefaultExec
	Exec ExecFunc
}

// DefaultExec 对 `uname -sm` 输出 "Linux x86_64"，其他命令返回 127
func DefaultExec(cmd string) (string, string, int) {
	if cmd == "uname -sm" {
		return "Linux x86_64\n", "", 0
	}
	return "", cmd + ": command not found\n", 127
}

// Server 进程内 SSH 服务端
type Server struct {
	ln  net.Listener
	cfg Config
	lt  tracker

	mu           sync.Mutex
	hostKey      ssh.Signer
	authAttempts int
	commands     []string
	forwards     []string
}

// Listen 在 addr（如 127.0.0.1:0）上启动 SSH 服务端
func Listen(addr string, cfg Config) (*Server, error) {
	key := cfg.HostKey
	if key == nil {
		var err error
		if key, err = newHostKey(); err != nil {
			return nil, err
		}
	}
	if cfg.Exec == nil {
		cfg.Exec = DefaultExec
	}
	ln, err := listen(addr)
	if err != nil {
		return nil, err
	}
	s := &Server{ln: ln, cfg: cfg, hostKey: key}
	s.lt.serve(ln, s.handle)
	return s, nil
}

func newHostKey() (ssh.Signer, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return ssh.NewSignerFromKey(priv)
}

// Addr 监听地址 host:port
func (s *Server) Addr() string { return s.ln.Addr().String() }

// HostKey 当前出示的主机公钥
func (s *Server) HostKey() ssh.PublicKey {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hostKey.PublicKey()
}

// Fingerprint 当前主机密钥的 SHA256 指纹
func (s *Server) Fingerprint() string { return ssh.FingerprintSHA256(s.HostKey()) }

// RotateHostKey 换一把新的 Ed25519 主机密钥，模拟服务器重装；之后的新连接生效
func (s *Server) RotateHostKey() error {
	key, err := newHostKey()
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.hostKey = key
	s.mu.Unlock()
	return nil
}

// AuthAttempts 收到的认证尝试次数（含 none）
func (s *Server) AuthAttempts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.authAttempts
}

// Commands 执行过的命令
func (s *Server) Commands() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.commands...)
}

// Forwards direct-tcpip 转发过的目标 host:port
func (s *Server) Forwards() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.forwards...)
}

// Close 停止监听并断开所有连接
func (s *Server) Close() error { return s.lt.close(s.ln) }

func (s *Server) serverConfig() *ssh.ServerConfig {
	cfg := &ssh.ServerConfig{
		AuthLogCallback: func(ssh.ConnMetadata, string, error) {
			s.mu.Lock()
			s.authAttempts++
			s.mu.Unlock()
		},
		PublicKeyCallback: func(c ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if c.User() == s.cfg.User {
				for _, k := range s.cfg.AuthorizedKeys {
					if bytes.Equal(k.Marshal(), key.Marshal()) {
						return &ssh.Permissions{}, nil
					}
				}
			}
			return nil, errors.New("公钥未授权")
		},
	}
	if s.cfg.Password != "" {
		cfg.PasswordCallback = func(c ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			if c.User() == s.cfg.User && subtle.ConstantTimeCompare(pw, []byte(s.cfg.Password)) == 1 {
				return &ssh.Permissions{}, nil
			}
			return nil, errors.New("密码错误")
		}
	}
	s.mu.Lock()
	cfg.AddHostKey(s.hostKey)
	s.mu.Unlock()
	return cfg
}

func (s *Server) handle(nc net.Conn) {
	sc, chans, reqs, err := ssh.NewServerConn(nc, s.serverConfig())
	if err != nil {
		return
	}
	defer func() { _ = sc.Close() }()
	go ssh.DiscardRequests(reqs)
	for nch := range chans {
		switch nch.ChannelType() {
		case "session":
			go s.session(nch)
		case "direct-tcpip":
			go s.forward(nch)
		default:
			_ = nch.Reject(ssh.UnknownChannelType, "不支持的通道类型")
		}
	}
}

func (s *Server) session(nch ssh.NewChannel) {
	ch, reqs, err := nch.Accept()
	if err != nil {
		return
	}
	defer func() { _ = ch.Close() }()
	for req := range reqs {
		if req.Type != "exec" {
			_ = req.Reply(false, nil)
			continue
		}
		var p struct{ Command string }
		if err := ssh.Unmarshal(req.Payload, &p); err != nil {
			_ = req.Reply(false, nil)
			continue
		}
		_ = req.Reply(true, nil)
		s.mu.Lock()
		s.commands = append(s.commands, p.Command)
		s.mu.Unlock()
		stdout, stderr, code := s.cfg.Exec(p.Command)
		_, _ = io.WriteString(ch, stdout)
		_, _ = io.WriteString(ch.Stderr(), stderr)
		_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{uint32(code)})) //nolint:gosec // 退出码由测试给定，范围 0–255
		return
	}
}

func (s *Server) forward(nch ssh.NewChannel) {
	var p struct {
		Host     string
		Port     uint32
		OrigHost string
		OrigPort uint32
	}
	if err := ssh.Unmarshal(nch.ExtraData(), &p); err != nil {
		_ = nch.Reject(ssh.ConnectionFailed, "无效的转发请求")
		return
	}
	target := net.JoinHostPort(p.Host, strconv.FormatUint(uint64(p.Port), 10))
	s.mu.Lock()
	s.forwards = append(s.forwards, target)
	s.mu.Unlock()
	tc, err := dialTarget(target)
	if err != nil {
		_ = nch.Reject(ssh.ConnectionFailed, fmt.Sprintf("连接 %s 失败", target))
		return
	}
	ch, reqs, err := nch.Accept()
	if err != nil {
		_ = tc.Close()
		return
	}
	go ssh.DiscardRequests(reqs)
	pipe(ch, tc)
}

func listen(addr string) (net.Listener, error) {
	return (&net.ListenConfig{}).Listen(context.Background(), "tcp", addr)
}

// dialTarget 代服务端连接转发目标
func dialTarget(target string) (net.Conn, error) {
	return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(context.Background(), "tcp", target)
}

// pipe 双向转发，任一方向结束即关闭两端
func pipe(a, b io.ReadWriteCloser) {
	var once sync.Once
	closeBoth := func() {
		once.Do(func() {
			_ = a.Close()
			_ = b.Close()
		})
	}
	go func() {
		_, _ = io.Copy(a, b)
		closeBoth()
	}()
	_, _ = io.Copy(b, a)
	closeBoth()
}

// tracker 跟踪监听循环与活动连接，Close 时全部断开
type tracker struct {
	mu     sync.Mutex
	conns  map[net.Conn]struct{}
	closed bool
	wg     sync.WaitGroup
}

func (t *tracker) serve(ln net.Listener, handle func(net.Conn)) {
	t.conns = map[net.Conn]struct{}{}
	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			t.mu.Lock()
			if t.closed {
				t.mu.Unlock()
				_ = c.Close()
				return
			}
			t.conns[c] = struct{}{}
			t.wg.Add(1)
			t.mu.Unlock()
			go func() {
				defer t.wg.Done()
				defer func() {
					_ = c.Close()
					t.mu.Lock()
					delete(t.conns, c)
					t.mu.Unlock()
				}()
				handle(c)
			}()
		}
	}()
}

func (t *tracker) close(ln net.Listener) error {
	t.mu.Lock()
	t.closed = true
	err := ln.Close()
	for c := range t.conns {
		_ = c.Close()
	}
	t.mu.Unlock()
	t.wg.Wait()
	return err
}
