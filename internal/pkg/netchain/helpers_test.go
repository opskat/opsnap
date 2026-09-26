package netchain

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"

	"github.com/opskat/opsnap/internal/pkg/fakessh"
)

const testPassword = "s3cret-pw"

func splitAddr(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, p, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	port, err := strconv.Atoi(p)
	require.NoError(t, err)
	return host, port
}

// startSSH 启动接受 root/testPassword 与给定公钥的假 SSH 服务端
func startSSH(t *testing.T, keys ...ssh.PublicKey) *fakessh.Server {
	t.Helper()
	s, err := fakessh.Listen("127.0.0.1:0", fakessh.Config{User: "root", Password: testPassword, AuthorizedKeys: keys})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// sshHop 指向 s 的 SSH 跳，密码认证，主机密钥已确认
func sshHop(t *testing.T, s *fakessh.Server, id int64, name string) Hop {
	host, port := splitAddr(t, s.Addr())
	return Hop{ID: id, Name: name, Kind: KindSSH, Host: host, Port: port, User: "root", Password: testPassword, HostKey: s.Fingerprint()}
}

func startSOCKS(t *testing.T, cfg fakessh.SOCKS5Config) *fakessh.SOCKS5 {
	t.Helper()
	p, err := fakessh.ListenSOCKS5("127.0.0.1:0", cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	return p
}

func socksHop(t *testing.T, p *fakessh.SOCKS5, id int64, name, user, pass string) Hop {
	host, port := splitAddr(t, p.Addr())
	return Hop{ID: id, Name: name, Kind: KindSOCKS5, Host: host, Port: port, User: user, Password: pass}
}

func listen(addr string) (net.Listener, error) {
	return (&net.ListenConfig{}).Listen(context.Background(), "tcp", addr)
}

// echoServer 原样回写的 TCP 服务
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

// silentServer 接受连接但从不说话
func silentServer(t *testing.T) string {
	t.Helper()
	ln, err := listen("127.0.0.1:0")
	require.NoError(t, err)
	var conns []net.Conn
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			conns = append(conns, c)
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		<-done
		for _, c := range conns {
			_ = c.Close()
		}
	})
	return ln.Addr().String()
}

// closedAddr 一个当前无人监听的本地地址
func closedAddr(t *testing.T) string {
	t.Helper()
	ln, err := listen("127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	return addr
}

func assertEcho(t *testing.T, c net.Conn) {
	t.Helper()
	_, err := c.Write([]byte("ping"))
	require.NoError(t, err)
	buf := make([]byte, 4)
	_, err = io.ReadFull(c, buf)
	require.NoError(t, err)
	require.Equal(t, "ping", string(buf))
}

type testKeys struct {
	ed25519 ed25519.PrivateKey
	rsa     *rsa.PrivateKey
	ecdsa   *ecdsa.PrivateKey
}

func genKeys(t *testing.T) testKeys {
	t.Helper()
	_, ed, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	rk, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	ek, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	return testKeys{ed25519: ed, rsa: rk, ecdsa: ek}
}

func pubKey(t *testing.T, priv any) ssh.PublicKey {
	t.Helper()
	s, err := ssh.NewSignerFromKey(priv)
	require.NoError(t, err)
	return s.PublicKey()
}

func openSSHPEM(t *testing.T, priv any, passphrase string) []byte {
	t.Helper()
	var (
		b   *pem.Block
		err error
	)
	if passphrase == "" {
		b, err = ssh.MarshalPrivateKey(priv, "")
	} else {
		b, err = ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(passphrase))
	}
	require.NoError(t, err)
	return pem.EncodeToMemory(b)
}

func pkcs1PEM(k *rsa.PrivateKey) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)})
}

func ecPEM(t *testing.T, k *ecdsa.PrivateKey) []byte {
	der, err := x509.MarshalECPrivateKey(k)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
}

func pkcs8PEM(t *testing.T, k any) []byte {
	der, err := x509.MarshalPKCS8PrivateKey(k)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

// encryptedPKCS1PEM 旧式加密 PEM（Proc-Type: 4,ENCRYPTED），用户仍可能粘贴这种私钥
func encryptedPKCS1PEM(t *testing.T, k *rsa.PrivateKey, passphrase string) []byte {
	//nolint:staticcheck // 测试需要构造旧式加密 PEM 私钥，标准库只剩这个已弃用的函数
	b, err := x509.EncryptPEMBlock(rand.Reader, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(k), []byte(passphrase), x509.PEMCipherAES256)
	require.NoError(t, err)
	return pem.EncodeToMemory(b)
}
