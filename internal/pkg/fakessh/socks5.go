package fakessh

import (
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"syscall"
)

// SOCKS5Config SOCKS5 代理配置；User 为空时只接受无认证方式
type SOCKS5Config struct {
	User     string
	Password string
}

// SOCKS5 进程内 SOCKS5 代理，只支持 CONNECT
type SOCKS5 struct {
	ln  net.Listener
	cfg SOCKS5Config
	lt  tracker

	mu           sync.Mutex
	authAttempts int
	requests     []string
}

// ListenSOCKS5 在 addr 上启动 SOCKS5 代理
func ListenSOCKS5(addr string, cfg SOCKS5Config) (*SOCKS5, error) {
	ln, err := listen(addr)
	if err != nil {
		return nil, err
	}
	p := &SOCKS5{ln: ln, cfg: cfg}
	p.lt.serve(ln, p.handle)
	return p, nil
}

// Addr 监听地址 host:port
func (p *SOCKS5) Addr() string { return p.ln.Addr().String() }

// AuthAttempts 收到的用户名密码认证次数
func (p *SOCKS5) AuthAttempts() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.authAttempts
}

// Requests CONNECT 请求的目标，域名按收到的原样记录（未解析）
func (p *SOCKS5) Requests() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.requests...)
}

// Close 停止监听并断开所有连接
func (p *SOCKS5) Close() error { return p.lt.close(p.ln) }

const (
	methodNone     = 0x00
	methodUserPass = 0x02
	methodRefused  = 0xFF
)

func (p *SOCKS5) handle(c net.Conn) {
	if err := p.negotiate(c); err != nil {
		return
	}
	target, err := readRequest(c)
	if err != nil {
		return
	}
	p.mu.Lock()
	p.requests = append(p.requests, target)
	p.mu.Unlock()
	tc, err := dialTarget(target)
	if err != nil {
		code := byte(0x04) // 主机不可达
		if errors.Is(err, syscall.ECONNREFUSED) {
			code = 0x05
		}
		_, _ = c.Write([]byte{5, code, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	if _, err := c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		_ = tc.Close()
		return
	}
	pipe(c, tc)
}

// negotiate 方法协商与 RFC 1929 用户名密码认证
func (p *SOCKS5) negotiate(c net.Conn) error {
	head := make([]byte, 2)
	if _, err := io.ReadFull(c, head); err != nil {
		return err
	}
	if head[0] != 5 {
		return errors.New("不是 SOCKS5")
	}
	methods := make([]byte, head[1])
	if _, err := io.ReadFull(c, methods); err != nil {
		return err
	}
	want := byte(methodNone)
	if p.cfg.User != "" {
		want = methodUserPass
	}
	offered := false
	for _, m := range methods {
		offered = offered || m == want
	}
	if !offered {
		_, _ = c.Write([]byte{5, methodRefused})
		return errors.New("没有可接受的认证方式")
	}
	if _, err := c.Write([]byte{5, want}); err != nil {
		return err
	}
	if want == methodNone {
		return nil
	}
	user, pass, err := readUserPass(c)
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.authAttempts++
	p.mu.Unlock()
	if user != p.cfg.User || subtle.ConstantTimeCompare([]byte(pass), []byte(p.cfg.Password)) != 1 {
		_, _ = c.Write([]byte{1, 1})
		return errors.New("认证失败")
	}
	_, err = c.Write([]byte{1, 0})
	return err
}

func readUserPass(r io.Reader) (string, string, error) {
	readField := func() (string, error) {
		n := make([]byte, 1)
		if _, err := io.ReadFull(r, n); err != nil {
			return "", err
		}
		b := make([]byte, n[0])
		_, err := io.ReadFull(r, b)
		return string(b), err
	}
	ver := make([]byte, 1)
	if _, err := io.ReadFull(r, ver); err != nil {
		return "", "", err
	}
	user, err := readField()
	if err != nil {
		return "", "", err
	}
	pass, err := readField()
	return user, pass, err
}

// readRequest 读取 CONNECT 请求，返回目标 host:port；域名不解析
func readRequest(c net.Conn) (string, error) {
	head := make([]byte, 4)
	if _, err := io.ReadFull(c, head); err != nil {
		return "", err
	}
	if head[0] != 5 || head[1] != 1 {
		_, _ = c.Write([]byte{5, 0x07, 0, 1, 0, 0, 0, 0, 0, 0})
		return "", errors.New("只支持 CONNECT")
	}
	var host string
	switch head[3] {
	case 1, 4:
		ip := make([]byte, 4)
		if head[3] == 4 {
			ip = make([]byte, 16)
		}
		if _, err := io.ReadFull(c, ip); err != nil {
			return "", err
		}
		host = net.IP(ip).String()
	case 3:
		n := make([]byte, 1)
		if _, err := io.ReadFull(c, n); err != nil {
			return "", err
		}
		name := make([]byte, n[0])
		if _, err := io.ReadFull(c, name); err != nil {
			return "", err
		}
		host = string(name)
	default:
		_, _ = c.Write([]byte{5, 0x08, 0, 1, 0, 0, 0, 0, 0, 0})
		return "", errors.New("不支持的地址类型")
	}
	port := make([]byte, 2)
	if _, err := io.ReadFull(c, port); err != nil {
		return "", err
	}
	return net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(port)))), nil
}
