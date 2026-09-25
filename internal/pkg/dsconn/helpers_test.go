package dsconn

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"

	"github.com/opskat/opsnap/internal/pkg/fakessh"
	"github.com/opskat/opsnap/internal/pkg/netchain"
)

const testPassword = "s3cret-pw"

// testCA 测试用的自建 CA
type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

func newCA(t *testing.T) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "opsnap test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return &testCA{cert: cert, key: key, pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

// issue 签发叶子证书，返回证书与私钥的 PEM
func (ca *testCA) issue(t *testing.T, cn string, usage x509.ExtKeyUsage, hosts ...string) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{usage},
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tpl.IPAddresses = append(tpl.IPAddresses, ip)
		} else {
			tpl.DNSNames = append(tpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, ca.cert, &key.PublicKey, ca.key)
	require.NoError(t, err)
	kder, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder})
}

func (ca *testCA) pool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(ca.cert)
	return p
}

// tlsServer 用于握手测试的服务端配置
func tlsServer(t *testing.T, ca *testCA, hosts ...string) *tls.Config {
	t.Helper()
	certPEM, keyPEM := ca.issue(t, "db", x509.ExtKeyUsageServerAuth, hosts...)
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	require.NoError(t, err)
	return &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
}

// handshake 在本机回环上完成一次 TLS 握手并读取服务端发来的一个字节，返回客户端遇到的第一个错误。
// TLS 1.3 中服务端在客户端握手完成后才校验客户端证书，拒绝只能在随后的读取中看到。
func handshake(t *testing.T, client, server *tls.Config) error {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", server)
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		if c.(*tls.Conn).HandshakeContext(ctx) == nil {
			_, _ = c.Write([]byte{1})
		}
	}()
	raw, err := (&net.Dialer{}).DialContext(ctx, "tcp", ln.Addr().String())
	require.NoError(t, err)
	c := tls.Client(raw, client)
	defer func() { _ = c.Close() }()
	if err := c.HandshakeContext(ctx); err != nil {
		return err
	}
	_, err = c.Read(make([]byte, 1))
	return err
}

func splitAddr(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, p, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	port, err := strconv.Atoi(p)
	require.NoError(t, err)
	return host, port
}

// startSSH 启动接受 root/testPassword 与给定公钥的假 SSH 服务端
func startSSH(t *testing.T, cfg fakessh.Config) *fakessh.Server {
	t.Helper()
	if cfg.User == "" {
		cfg.User = "root"
	}
	if cfg.Password == "" && len(cfg.AuthorizedKeys) == 0 {
		cfg.Password = testPassword
	}
	s, err := fakessh.Listen("127.0.0.1:0", cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// sshTunnel 经一台假 SSH 跳板建立的链路（转发的连接不支持读写截止时间）
func sshTunnel(t *testing.T) (*netchain.Tunnel, *fakessh.Server) {
	t.Helper()
	s := startSSH(t, fakessh.Config{})
	host, port := splitAddr(t, s.Addr())
	c, err := netchain.NewChain([]netchain.Hop{{ID: 1, Name: "bastion", Kind: netchain.KindSSH, Host: host, Port: port, User: "root", Password: testPassword, HostKey: s.Fingerprint()}})
	require.NoError(t, err)
	tun, err := c.Connect(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { _ = tun.Close() })
	return tun, s
}

// directTunnel 不经任何通道的直连链路
func directTunnel(t *testing.T) *netchain.Tunnel {
	t.Helper()
	c, err := netchain.NewChain(nil)
	require.NoError(t, err)
	tun, err := c.Connect(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { _ = tun.Close() })
	return tun
}

// silentListener 接受连接但从不回应，模拟卡住的数据库
func silentListener(t *testing.T) (string, int) {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	var (
		mu    sync.Mutex
		conns []net.Conn
	)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			_ = c.Close()
		}
	})
	return splitAddr(t, ln.Addr().String())
}

// closedPort 一个没有监听的本地端口
func closedPort(t *testing.T) int {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	_, port := splitAddr(t, ln.Addr().String())
	require.NoError(t, ln.Close())
	return port
}

// stubDialer 按给定错误失败的 Dialer
type stubDialer struct{ err error }

func (d stubDialer) Dial(context.Context, string, string) (net.Conn, error) { return nil, d.err }

func (d stubDialer) DialSSH(context.Context, netchain.Hop) (*ssh.Client, error) { return nil, d.err }
