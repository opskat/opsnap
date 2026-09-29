package netchain

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"

	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/l10n"
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
		return nil, l10n.Errorf(code.NetSocksNetwork, network)
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
		return l10n.Errorf(code.NetSocksClosed, ErrProtocol)
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
		return l10n.Errorf(code.NetSocksNotSocks, ErrProtocol)
	case reply[1] == socksMethodRefused && method == socksMethodNone:
		return l10n.Errorf(code.NetSocksAuthRequired, ErrNegotiation)
	case reply[1] == socksMethodRefused:
		return l10n.Errorf(code.NetSocksNoUserPass, ErrNegotiation)
	case reply[1] != method:
		return l10n.Errorf(code.NetSocksBadMethod, ErrProtocol, reply[1])
	case method == socksMethodNone:
		return nil
	}
	if len(user) > 255 || len(password) > 255 {
		return l10n.Errorf(code.NetSocksCredTooLong, ErrAuthFailed)
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

var socksReplies = map[byte]int{
	1: code.NetSocksReply1,
	2: code.NetSocksReply2,
	3: code.NetSocksReply3,
	4: code.NetSocksReply4,
	5: code.NetSocksReply5,
	6: code.NetSocksReply6,
	7: code.NetSocksReply7,
	8: code.NetSocksReply8,
}

// socksConnect 发送 CONNECT；主机名不在本地解析
func socksConnect(conn net.Conn, addr string) error {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return l10n.Errorf(code.NetSocksBadPort, portStr)
	}
	req := []byte{socksVersion, 1, 0}
	switch ip := net.ParseIP(host); {
	case ip != nil && ip.To4() != nil:
		req = append(append(req, 1), ip.To4()...)
	case ip != nil:
		req = append(append(req, 4), ip.To16()...)
	case len(host) > 255:
		return l10n.Errorf(code.NetSocksHostTooLong)
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
		return l10n.Errorf(code.NetSocksBadReply, ErrProtocol)
	}
	if head[1] != 0 {
		msg := l10n.New(code.NetSocksReplyCode, head[1])
		if c, ok := socksReplies[head[1]]; ok {
			msg = l10n.New(c)
		}
		return l10n.Errorf(code.NetSocksConnectFailed, addr, msg)
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
		return l10n.Errorf(code.NetSocksBadAddrType, ErrProtocol)
	}
	if _, err := io.ReadFull(conn, make([]byte, skip+2)); err != nil {
		return protocolErr(err)
	}
	return nil
}
