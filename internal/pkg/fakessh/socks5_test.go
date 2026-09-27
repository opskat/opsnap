package fakessh

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSOCKS5(t *testing.T) {
	echo := echoServer(t)
	_, port, _ := net.SplitHostPort(echo)
	p, err := ListenSOCKS5("127.0.0.1:0", SOCKS5Config{User: "u", Password: "p"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })

	handshake := func(t *testing.T, user, pass string) (net.Conn, byte) {
		t.Helper()
		c, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(context.Background(), "tcp", p.Addr())
		require.NoError(t, err)
		_, err = c.Write([]byte{5, 1, 2})
		require.NoError(t, err)
		reply := make([]byte, 2)
		_, err = io.ReadFull(c, reply)
		require.NoError(t, err)
		require.Equal(t, []byte{5, 2}, reply)
		msg := append([]byte{1, lenByte(user)}, user...)
		msg = append(append(msg, lenByte(pass)), pass...)
		_, err = c.Write(msg)
		require.NoError(t, err)
		_, err = io.ReadFull(c, reply)
		require.NoError(t, err)
		return c, reply[1]
	}

	t.Run("用户名密码正确后按域名 CONNECT，目标名原样记录", func(t *testing.T) {
		c, status := handshake(t, "u", "p")
		defer func() { _ = c.Close() }()
		require.Equal(t, byte(0), status)
		host := "localhost"
		req := append([]byte{5, 1, 0, 3, lenByte(host)}, host...)
		pn, err := strconv.Atoi(port)
		require.NoError(t, err)
		req = binary.BigEndian.AppendUint16(req, uint16(pn)) //nolint:gosec // 端口来自本地监听，范围 1–65535
		_, err = c.Write(req)
		require.NoError(t, err)
		reply := make([]byte, 10)
		_, err = io.ReadFull(c, reply)
		require.NoError(t, err)
		require.Equal(t, byte(0), reply[1])
		_, err = c.Write([]byte("hi"))
		require.NoError(t, err)
		buf := make([]byte, 2)
		_, err = io.ReadFull(c, buf)
		require.NoError(t, err)
		assert.Equal(t, "hi", string(buf))
		assert.Contains(t, p.Requests(), "localhost:"+port)
	})

	t.Run("密码错误返回失败状态", func(t *testing.T) {
		c, status := handshake(t, "u", "bad")
		defer func() { _ = c.Close() }()
		assert.NotEqual(t, byte(0), status)
	})

	t.Run("要求认证时不接受无认证方式", func(t *testing.T) {
		c, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(context.Background(), "tcp", p.Addr())
		require.NoError(t, err)
		defer func() { _ = c.Close() }()
		_, err = c.Write([]byte{5, 1, 0})
		require.NoError(t, err)
		reply := make([]byte, 2)
		_, err = io.ReadFull(c, reply)
		require.NoError(t, err)
		assert.Equal(t, []byte{5, 0xFF}, reply)
	})
}

// lenByte 测试中的字段都很短
func lenByte(s string) byte {
	return byte(len(s)) //nolint:gosec // 测试字段长度远小于 255
}
