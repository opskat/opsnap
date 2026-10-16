package sshconn

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
)

// socks5Greet 与 SOCKS5 代理协商认证方式（RFC 1928/1929）。用户名为空时只提供免认证。
func socks5Greet(ctx context.Context, conn net.Conn, user, password string) (err error) {
	expired := withDeadline(ctx, conn)
	defer func() {
		if expired() {
			err = ctx.Err()
		}
	}()
	methods := []byte{0x00}
	if user != "" {
		methods = []byte{0x00, 0x02}
	}
	if _, err := conn.Write(append([]byte{0x05, byte(len(methods))}, methods...)); err != nil {
		return err
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		return err
	}
	if reply[0] != 0x05 {
		return errors.New("对方不是 SOCKS5 代理")
	}
	switch reply[1] {
	case 0x00:
		return nil
	case 0x02:
		if user == "" {
			return fmt.Errorf("%w：SOCKS5 代理要求用户名和密码", errAuth)
		}
	default:
		return fmt.Errorf("%w：SOCKS5 代理不接受提供的认证方式", errAuth)
	}
	if len(user) > 255 || len(password) > 255 {
		return fmt.Errorf("%w：SOCKS5 用户名或密码超过 255 字节", errAuth)
	}
	msg := make([]byte, 0, 3+len(user)+len(password))
	msg = append(msg, 0x01, byte(len(user)))
	msg = append(msg, user...)
	msg = append(msg, byte(len(password)))
	msg = append(msg, password...)
	if _, err := conn.Write(msg); err != nil {
		return err
	}
	if _, err := io.ReadFull(conn, reply); err != nil {
		return err
	}
	if reply[1] != 0x00 {
		return fmt.Errorf("%w：SOCKS5 代理拒绝了用户名或密码", errAuth)
	}
	return nil
}

// socks5Replies SOCKS5 CONNECT 失败应答码的含义
var socks5Replies = map[byte]string{
	0x01: "代理内部错误",
	0x02: "代理规则不允许",
	0x03: "网络不可达",
	0x04: "主机不可达",
	0x05: "连接被拒绝",
	0x06: "TTL 过期",
	0x07: "不支持的命令",
	0x08: "不支持的地址类型",
}

// socks5Connect 请求代理连接 addr，成功后 conn 即为到 addr 的流
func socks5Connect(ctx context.Context, conn net.Conn, addr string) (_ net.Conn, err error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fmt.Errorf("端口 %q 不合法", portStr)
	}
	expired := withDeadline(ctx, conn)
	defer func() {
		if expired() {
			err = ctx.Err()
		}
	}()
	req := []byte{0x05, 0x01, 0x00}
	if ip := net.ParseIP(host); ip != nil && ip.To4() != nil {
		req = append(req, 0x01)
		req = append(req, ip.To4()...)
	} else if ip != nil {
		req = append(req, 0x04)
		req = append(req, ip.To16()...)
	} else {
		if len(host) > 255 {
			return nil, errors.New("主机名超过 255 字节")
		}
		req = append(req, 0x03, byte(len(host)))
		req = append(req, host...)
	}
	req = binary.BigEndian.AppendUint16(req, uint16(port))
	if _, err := conn.Write(req); err != nil {
		return nil, err
	}
	head := make([]byte, 4)
	if _, err := io.ReadFull(conn, head); err != nil {
		return nil, err
	}
	if head[1] != 0x00 {
		msg, ok := socks5Replies[head[1]]
		if !ok {
			msg = fmt.Sprintf("应答码 %d", head[1])
		}
		return nil, fmt.Errorf("SOCKS5 代理无法连接 %s：%s", addr, msg)
	}
	var skip int
	switch head[3] {
	case 0x01:
		skip = 4
	case 0x04:
		skip = 16
	case 0x03:
		l := make([]byte, 1)
		if _, err := io.ReadFull(conn, l); err != nil {
			return nil, err
		}
		skip = int(l[0])
	default:
		return nil, errors.New("SOCKS5 应答的地址类型不合法")
	}
	if _, err := io.ReadFull(conn, make([]byte, skip+2)); err != nil {
		return nil, err
	}
	return conn, nil
}

// httpConnect 请求 HTTP 代理用 CONNECT 打通到 addr 的隧道。
// 407 属于代理这一跳的认证失败，其他非 2xx 视为代理无法连接下一跳。
func httpConnect(ctx context.Context, conn net.Conn, addr, user, password string) (_ net.Conn, err error) {
	expired := withDeadline(ctx, conn)
	defer func() {
		if expired() {
			err = ctx.Err()
		}
	}()
	raw := "CONNECT " + addr + " HTTP/1.1\r\nHost: " + addr + "\r\n"
	if user != "" {
		cred := base64.StdEncoding.EncodeToString([]byte(user + ":" + password))
		raw += "Proxy-Authorization: Basic " + cred + "\r\n"
	}
	raw += "\r\n"
	if _, err := io.WriteString(conn, raw); err != nil {
		return nil, err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		return nil, err
	}
	_ = resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusProxyAuthRequired:
		return nil, &prevHopError{err: fmt.Errorf("%w：HTTP 代理返回 407，用户名或密码不正确", errAuth)}
	case resp.StatusCode/100 != 2:
		return nil, fmt.Errorf("HTTP 代理无法连接 %s：%s", addr, resp.Status)
	}
	if br.Buffered() > 0 {
		return &bufferedConn{Conn: conn, r: br}, nil
	}
	return conn, nil
}

// bufferedConn 先读出 bufio 中已缓存的隧道数据
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (b *bufferedConn) Read(p []byte) (int, error) { return b.r.Read(p) }
