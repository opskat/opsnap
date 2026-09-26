package fakessh

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

func dial(t *testing.T, s *Server, auth ssh.AuthMethod) (*ssh.Client, error) {
	t.Helper()
	return ssh.Dial("tcp", s.Addr(), &ssh.ClientConfig{
		User:            "root",
		Auth:            []ssh.AuthMethod{auth},
		HostKeyCallback: ssh.FixedHostKey(s.HostKey()),
		Timeout:         5 * time.Second,
	})
}

func TestServer(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(priv)
	require.NoError(t, err)

	s, err := Listen("127.0.0.1:0", Config{
		User:           "root",
		Password:       "pw",
		AuthorizedKeys: []ssh.PublicKey{signer.PublicKey()},
		Exec: func(cmd string) (string, string, int) {
			if cmd == "false" {
				return "", "boom", 3
			}
			return DefaultExec(cmd)
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	t.Run("密码认证后执行命令，默认 uname -sm 输出 Linux x86_64", func(t *testing.T) {
		c, err := dial(t, s, ssh.Password("pw"))
		require.NoError(t, err)
		defer func() { _ = c.Close() }()
		sess, err := c.NewSession()
		require.NoError(t, err)
		defer func() { _ = sess.Close() }()
		out, err := sess.Output("uname -sm")
		require.NoError(t, err)
		assert.Equal(t, "Linux x86_64\n", string(out))
		assert.Contains(t, s.Commands(), "uname -sm")
	})

	t.Run("命令的退出码与 stderr 原样返回", func(t *testing.T) {
		c, err := dial(t, s, ssh.PublicKeys(signer))
		require.NoError(t, err)
		defer func() { _ = c.Close() }()
		sess, err := c.NewSession()
		require.NoError(t, err)
		var stderr bytes.Buffer
		sess.Stderr = &stderr
		err = sess.Run("false")
		var exitErr *ssh.ExitError
		require.True(t, errors.As(err, &exitErr), "err=%v", err)
		assert.Equal(t, 3, exitErr.ExitStatus())
		assert.Equal(t, "boom", stderr.String())
	})

	t.Run("认证失败被拒绝并计入认证尝试", func(t *testing.T) {
		before := s.AuthAttempts()
		_, err := dial(t, s, ssh.Password("wrong"))
		require.Error(t, err)
		assert.Greater(t, s.AuthAttempts(), before)
	})

	t.Run("direct-tcpip 转发到目标并记录", func(t *testing.T) {
		echo := echoServer(t)
		c, err := dial(t, s, ssh.Password("pw"))
		require.NoError(t, err)
		defer func() { _ = c.Close() }()
		conn, err := c.Dial("tcp", echo)
		require.NoError(t, err)
		defer func() { _ = conn.Close() }()
		_, err = conn.Write([]byte("ping"))
		require.NoError(t, err)
		buf := make([]byte, 4)
		_, err = io.ReadFull(conn, buf)
		require.NoError(t, err)
		assert.Equal(t, "ping", string(buf))
		assert.Contains(t, s.Forwards(), echo)
	})

	t.Run("更换主机密钥后出示新密钥", func(t *testing.T) {
		old := s.Fingerprint()
		require.NoError(t, s.RotateHostKey())
		assert.NotEqual(t, old, s.Fingerprint())
		assert.Equal(t, ssh.FingerprintSHA256(s.HostKey()), s.Fingerprint())
		c, err := dial(t, s, ssh.Password("pw"))
		require.NoError(t, err)
		_ = c.Close()
	})
}

func echoServer(t *testing.T) string {
	t.Helper()
	ln, err := listen("127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				_, _ = io.Copy(c, c)
			}()
		}
	}()
	return ln.Addr().String()
}
