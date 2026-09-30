package dsconn

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeMySQL 只实现连接测试用到的那部分 MySQL 协议：握手包声明支持 TLS（CLIENT_SSL），
// 客户端请求 TLS 时读完 ClientHello 就以 handshake_failure 告警中止握手，模拟 Go 的 crypto/tls 与服务端
// 没有共同密码套件的情形（官方 mysql:5.7 镜像只协商 DHE-RSA-AES256-GCM-SHA384）；不加密时接受任何账号，
// 回答 SELECT VERSION() 与 Ssl_version 查询
type fakeMySQL struct {
	// authErr 为真时不加密的登录以 1045 拒绝
	authErr bool
	// tlsAborted 中止的 TLS 握手次数，plain 不加密的登录次数
	tlsAborted atomic.Int32
	plain      atomic.Int32
}

const (
	fakeMySQLVersion = "5.7.44-fake"
	capClientSSL     = 0x800
	// 服务端能力：LONG_PASSWORD、PROTOCOL_41、SSL、TRANSACTIONS、SECURE_CONNECTION、PLUGIN_AUTH（不含 DEPRECATE_EOF）
	fakeMySQLCaps = 0x1 | 0x200 | capClientSSL | 0x2000 | 0x8000 | 0x80000
)

func startFakeMySQL(t *testing.T, s *fakeMySQL) (string, int) {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	var wg sync.WaitGroup
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { _ = c.Close() }()
				_ = s.serve(c)
			}()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		wg.Wait()
	})
	return splitAddr(t, ln.Addr().String())
}

func (s *fakeMySQL) serve(c net.Conn) error {
	greeting := []byte{10}
	greeting = append(greeting, fakeMySQLVersion...)
	greeting = append(greeting, 0, 1, 0, 0, 0) // 连接编号
	greeting = append(greeting, "abcdefgh"...) // 挑战的前 8 字节
	greeting = append(greeting, 0)             // 填充
	greeting = binary.LittleEndian.AppendUint16(greeting, fakeMySQLCaps&0xffff)
	greeting = append(greeting, 0x21, 0x02, 0x00) // 字符集、状态
	greeting = binary.LittleEndian.AppendUint16(greeting, fakeMySQLCaps>>16)
	greeting = append(greeting, 21)
	greeting = append(greeting, make([]byte, 10)...)
	greeting = append(greeting, "ijklmnopqrst\x00"...) // 挑战的后 12 字节
	greeting = append(greeting, "mysql_native_password\x00"...)
	if err := writeMyPacket(c, 0, greeting); err != nil {
		return err
	}
	resp, err := readMyPacket(c)
	if err != nil {
		return err
	}
	if len(resp) == 32 && binary.LittleEndian.Uint32(resp)&capClientSSL != 0 {
		// 读完 ClientHello 所在的记录后回 fatal handshake_failure 告警并断开
		hdr := make([]byte, 5)
		if _, err := io.ReadFull(c, hdr); err != nil {
			return err
		}
		if _, err := io.CopyN(io.Discard, c, int64(binary.BigEndian.Uint16(hdr[3:]))); err != nil {
			return err
		}
		s.tlsAborted.Add(1)
		_, err := c.Write([]byte{0x15, 0x03, 0x03, 0x00, 0x02, 0x02, 0x28})
		return err
	}
	s.plain.Add(1)
	if s.authErr {
		return writeMyPacket(c, 2, myErrPacket(1045, "28000", "Access denied for user 'root'@'127.0.0.1' (using password: YES)"))
	}
	if err := writeMyPacket(c, 2, myOKPacket()); err != nil {
		return err
	}
	for {
		cmd, err := readMyPacket(c)
		if err != nil || len(cmd) == 0 || cmd[0] == 0x01 { // COM_QUIT
			return err
		}
		switch q := string(cmd[1:]); {
		case cmd[0] == 0x0e: // COM_PING
			err = writeMyPacket(c, 1, myOKPacket())
		case cmd[0] != 0x03:
			err = writeMyPacket(c, 1, myErrPacket(1047, "08S01", "Unknown command"))
		case q == "SELECT VERSION()":
			err = writeMyResultSet(c, []string{"VERSION()"}, []string{fakeMySQLVersion})
		case q == "SHOW SESSION STATUS LIKE 'Ssl_version'":
			err = writeMyResultSet(c, []string{"Variable_name", "Value"}, []string{"Ssl_version", ""})
		case q == "SELECT @@max_allowed_packet":
			err = writeMyResultSet(c, []string{"@@max_allowed_packet"}, []string{"67108864"})
		default:
			err = writeMyPacket(c, 1, myErrPacket(1064, "42000", "unsupported query"))
		}
		if err != nil {
			return err
		}
	}
}

func readMyPacket(r io.Reader) ([]byte, error) {
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return nil, err
	}
	n := int(hdr[0]) | int(hdr[1])<<8 | int(hdr[2])<<16
	buf := make([]byte, n)
	_, err := io.ReadFull(r, buf)
	return buf, err
}

func writeMyPacket(w io.Writer, seq byte, payload []byte) error {
	n := len(payload)
	_, err := w.Write(append([]byte{u8(n), u8(n >> 8), u8(n >> 16), seq}, payload...))
	return err
}

func myOKPacket() []byte { return []byte{0x00, 0, 0, 0x02, 0x00, 0, 0} }

func myEOFPacket() []byte { return []byte{0xfe, 0, 0, 0x02, 0x00} }

func myErrPacket(num uint16, state, msg string) []byte {
	p := binary.LittleEndian.AppendUint16([]byte{0xff}, num)
	p = append(p, '#')
	p = append(p, state...)
	return append(p, msg...)
}

// u8 取 n 的最低字节；测试包里的长度都远小于 2^24，字符串与列数都小于 251（一字节长度编码）
func u8(n int) byte { return byte(n & 0xff) }

func appendLenencStr(b []byte, s string) []byte { return append(append(b, u8(len(s))), s...) }

// writeMyResultSet 回答一个只有一行、各列均为字符串的结果集（带 EOF 包的旧格式）
func writeMyResultSet(w io.Writer, cols, row []string) error {
	packets := make([][]byte, 0, len(cols)+4)
	packets = append(packets, []byte{u8(len(cols))})
	for _, name := range cols {
		def := appendLenencStr(nil, "def")
		for _, s := range []string{"", "", "", name, name} {
			def = appendLenencStr(def, s)
		}
		def = append(def, 0x0c, 0x21, 0x00, 0xff, 0, 0, 0, 0xfd, 0, 0, 0, 0, 0)
		packets = append(packets, def)
	}
	packets = append(packets, myEOFPacket())
	var r []byte
	for _, v := range row {
		r = appendLenencStr(r, v)
	}
	packets = append(packets, r, myEOFPacket())
	for i, p := range packets {
		if err := writeMyPacket(w, byte(i+1), p); err != nil {
			return err
		}
	}
	return nil
}

func TestMySQLPreferFallsBackWhenTLSHandshakeFails(t *testing.T) {
	cfgFor := func(host string, port int, mode TLSMode) Config {
		return Config{Type: TypeMySQL, Host: host, Port: port, User: "root", Password: testPassword, TLS: TLSConfig{Mode: mode}}
	}

	for _, mode := range []TLSMode{"", TLSPrefer} {
		t.Run("优先加密 "+string(mode)+" 握手失败后改用不加密重试，照实报告未加密", func(t *testing.T) {
			s := &fakeMySQL{}
			host, port := startFakeMySQL(t, s)
			info, err := Test(context.Background(), directTunnel(t), cfgFor(host, port, mode))
			require.NoError(t, err)
			assert.Equal(t, fakeMySQLVersion, info.Version)
			assert.Nil(t, info.TLS, "退回不加密后不应报告 TLS 版本")
			assert.GreaterOrEqual(t, s.tlsAborted.Load(), int32(1), "应先尝试加密")
			assert.GreaterOrEqual(t, s.plain.Load(), int32(1))
		})
	}

	t.Run("优先加密退回后认证失败，报告认证失败且只重试一次", func(t *testing.T) {
		s := &fakeMySQL{authErr: true}
		host, port := startFakeMySQL(t, s)
		_, err := Test(context.Background(), directTunnel(t), cfgFor(host, port, TLSPrefer))
		var e *Error
		require.ErrorAs(t, err, &e)
		assert.Equal(t, ReasonAuthFailed, e.Reason)
		assert.NotContains(t, e.Error()+e.MsgEn, testPassword)
		assert.Equal(t, int32(1), s.tlsAborted.Load())
		assert.Equal(t, int32(1), s.plain.Load())
	})

	for _, mode := range []TLSMode{TLSRequire, TLSVerifyCA, TLSVerifyFull} {
		t.Run(string(mode)+" 握手失败仍报错，不改用不加密", func(t *testing.T) {
			s := &fakeMySQL{}
			host, port := startFakeMySQL(t, s)
			_, err := Test(context.Background(), directTunnel(t), cfgFor(host, port, mode))
			var e *Error
			require.ErrorAs(t, err, &e)
			assert.Contains(t, e.Error(), "handshake failure")
			assert.Equal(t, int32(0), s.plain.Load(), "不应尝试不加密的连接")
		})
	}

	t.Run("不加密直接连接，不尝试 TLS", func(t *testing.T) {
		s := &fakeMySQL{}
		host, port := startFakeMySQL(t, s)
		info, err := Test(context.Background(), directTunnel(t), cfgFor(host, port, TLSDisable))
		require.NoError(t, err)
		assert.Nil(t, info.TLS)
		assert.Equal(t, int32(0), s.tlsAborted.Load())
	})
}

func TestTLSHandshakeFailed(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"服务端告警中止握手", &net.OpError{Op: "remote error", Err: errors.New("tls: handshake failure")}, true},
		{"客户端告警中止握手", &net.OpError{Op: "local error", Err: errors.New("tls: protocol version not supported")}, true},
		{"服务端的应答不是 TLS", tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"}, true},
		{"服务端选定的版本不被接受", errors.New("tls: server selected unsupported protocol version 302"), true},
		{"认证失败", &mysql.MySQLError{Number: 1045, Message: "Access denied"}, false},
		{"超时", context.DeadlineExceeded, false},
		{"取消", context.Canceled, false},
		{"读超时", &net.OpError{Op: "read", Err: os.ErrDeadlineExceeded}, false},
		{"连接被重置", &net.OpError{Op: "read", Err: syscall.ECONNRESET}, false},
		{"连接中断", io.EOF, false},
		{"证书校验失败", &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}, false},
		{"连不上", &dialError{err: syscall.ECONNREFUSED}, false},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, tlsHandshakeFailed(c.err), c.name)
	}
}
