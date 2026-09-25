package netchain

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
)

// socksDialer 经上一段链路连到 SOCKS5 代理再 CONNECT 目标。
// 目标主机名原样交给代理解析（socks5h 语义），本地不做 DNS 查询。
type socksDialer struct {
	prev  dialFunc
	index int
	hop   Hop
}

// open 连到代理并完成方法协商与认证；失败归因于本跳
func (s *socksDialer) open(ctx context.Context) (net.Conn, error) {
	conn, err := s.prev(ctx, "tcp", s.hop.addr())
	if err != nil {
		return nil, newHopError(ctx, s.index, s.hop, err)
	}
	if err := withConn(ctx, conn, func() error { return socksNegotiate(conn, s.hop.User, s.hop.Password) }); err != nil {
		_ = conn.Close()
		return nil, newHopError(ctx, s.index, s.hop, err)
	}
	return conn, nil
}

// dial 经代理连接 addr；代理自身的问题返回 *HopError，目标的问题返回普通错误
func (s *socksDialer) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	switch network {
	case "tcp", "tcp4", "tcp6":
	default:
		return nil, fmt.Errorf("SOCKS5 不支持 %s", network)
	}
	conn, err := s.open(ctx)
	if err != nil {
		return nil, err
	}
	if err := withConn(ctx, conn, func() error { return socksConnect(conn, addr) }); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

// withConn 执行 fn，ctx 结束时关闭 conn 以打断阻塞的读写
func withConn(ctx context.Context, conn net.Conn, fn func() error) error {
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	err := fn()
	if !stop() && err == nil {
		err = ctx.Err()
	}
	if err != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

const (
	socksVersion       = 5
	socksMethodNone    = 0x00
	socksMethodUser    = 0x02
	socksMethodRefused = 0xFF
)

func protocolErr(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return fmt.Errorf("%w：代理断开了连接", ErrProtocol)
	}
	return err
}

// socksNegotiate 方法协商；配置了用户名时只提供用户名密码方式（RFC 1929）
func socksNegotiate(conn net.Conn, user, password string) error {
	method := byte(socksMethodNone)
	if user != "" {
		method = socksMethodUser
	}
	if _, err := conn.Write([]byte{socksVersion, 1, method}); err != nil {
		return err
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		return protocolErr(err)
	}
	switch {
	case reply[0] != socksVersion:
		return fmt.Errorf("%w：对端不是 SOCKS5 代理", ErrProtocol)
	case reply[1] == socksMethodRefused && method == socksMethodNone:
		return fmt.Errorf("%w：代理要求认证", ErrNegotiation)
	case reply[1] == socksMethodRefused:
		return fmt.Errorf("%w：代理不接受用户名密码认证", ErrNegotiation)
	case reply[1] != method:
		return fmt.Errorf("%w：代理选择了未提供的认证方式 %d", ErrProtocol, reply[1])
	case method == socksMethodNone:
		return nil
	}
	if len(user) > 255 || len(password) > 255 {
		return fmt.Errorf("%w：用户名或密码超过 255 字节", ErrAuthFailed)
	}
	msg := make([]byte, 0, 3+len(user)+len(password))
	msg = append(msg, 1, byte(len(user))) //nolint:gosec // 上面已限制在 255 字节内
	msg = append(msg, user...)
	msg = append(msg, byte(len(password))) //nolint:gosec // 上面已限制在 255 字节内
	msg = append(msg, password...)
	if _, err := conn.Write(msg); err != nil {
		return err
	}
	if _, err := io.ReadFull(conn, reply); err != nil {
		return protocolErr(err)
	}
	if reply[1] != 0 {
		return ErrAuthFailed
	}
	return nil
}

var socksReplies = map[byte]string{
	1: "代理内部错误",
	2: "代理规则不允许",
	3: "网络不可达",
	4: "主机不可达",
	5: "连接被拒绝",
	6: "TTL 过期",
	7: "代理不支持 CONNECT",
	8: "代理不支持该地址类型",
}

// socksConnect 发送 CONNECT；主机名不在本地解析
func socksConnect(conn net.Conn, addr string) error {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return fmt.Errorf("无效的端口 %q", portStr)
	}
	req := []byte{socksVersion, 1, 0}
	switch ip := net.ParseIP(host); {
	case ip != nil && ip.To4() != nil:
		req = append(append(req, 1), ip.To4()...)
	case ip != nil:
		req = append(append(req, 4), ip.To16()...)
	case len(host) > 255:
		return fmt.Errorf("主机名超过 255 字节")
	default:
		req = append(append(req, 3, byte(len(host))), host...) //nolint:gosec // 上一分支已限制在 255 字节内
	}
	req = binary.BigEndian.AppendUint16(req, uint16(port))
	if _, err := conn.Write(req); err != nil {
		return err
	}
	head := make([]byte, 4)
	if _, err := io.ReadFull(conn, head); err != nil {
		return protocolErr(err)
	}
	if head[0] != socksVersion {
		return fmt.Errorf("%w：CONNECT 应答无效", ErrProtocol)
	}
	if head[1] != 0 {
		msg, ok := socksReplies[head[1]]
		if !ok {
			msg = fmt.Sprintf("错误码 %d", head[1])
		}
		return fmt.Errorf("SOCKS5 代理无法连接 %s：%s", addr, msg)
	}
	var skip int
	switch head[3] {
	case 1:
		skip = 4
	case 4:
		skip = 16
	case 3:
		n := make([]byte, 1)
		if _, err := io.ReadFull(conn, n); err != nil {
			return protocolErr(err)
		}
		skip = int(n[0])
	default:
		return fmt.Errorf("%w：CONNECT 应答地址类型无效", ErrProtocol)
	}
	if _, err := io.ReadFull(conn, make([]byte, skip+2)); err != nil {
		return protocolErr(err)
	}
	return nil
}
