package dsconn

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 服务端证书由测试 CA 签发给 db.internal，客户端按 host 连接
func tlsFor(t *testing.T, host string, tc TLSConfig) *tls.Config {
	t.Helper()
	cfg, err := Config{Type: TypeMySQL, Host: host, Port: 3306, User: "root", TLS: tc}.tlsConfig()
	require.NoError(t, err)
	return cfg
}

func TestTLSModes(t *testing.T) {
	ca := newCA(t)
	other := newCA(t)
	server := tlsServer(t, ca, "db.internal")

	t.Run("不加密不构造 TLS 配置", func(t *testing.T) {
		assert.Nil(t, tlsFor(t, "db.internal", TLSConfig{Mode: TLSDisable}))
	})

	t.Run("默认为优先加密，与必须加密一样不校验证书", func(t *testing.T) {
		for _, mode := range []TLSMode{"", TLSPrefer, TLSRequire} {
			cfg := tlsFor(t, "10.0.0.9", TLSConfig{Mode: mode, CA: other.pem})
			require.NotNil(t, cfg, mode)
			assert.NoError(t, handshake(t, cfg, server), "模式 %q 不应校验证书", mode)
		}
	})

	t.Run("校验 CA 只校验证书链，不校验主机名", func(t *testing.T) {
		assert.NoError(t, handshake(t, tlsFor(t, "10.0.0.9", TLSConfig{Mode: TLSVerifyCA, CA: ca.pem}), server))

		err := handshake(t, tlsFor(t, "db.internal", TLSConfig{Mode: TLSVerifyCA, CA: other.pem}), server)
		require.Error(t, err)
		assert.Equal(t, ReasonCertificate, reasonOf(err))
	})

	t.Run("校验 CA 与主机名", func(t *testing.T) {
		assert.NoError(t, handshake(t, tlsFor(t, "db.internal", TLSConfig{Mode: TLSVerifyFull, CA: ca.pem}), server))

		err := handshake(t, tlsFor(t, "10.0.0.9", TLSConfig{Mode: TLSVerifyFull, CA: ca.pem}), server)
		require.Error(t, err)
		var he x509.HostnameError
		assert.ErrorAs(t, err, &he)
		assert.Equal(t, ReasonCertificate, reasonOf(err))

		err = handshake(t, tlsFor(t, "db.internal", TLSConfig{Mode: TLSVerifyFull, CA: other.pem}), server)
		require.Error(t, err)
		assert.Equal(t, ReasonCertificate, reasonOf(err))
	})

	t.Run("两种校验模式未提供 CA 时使用系统信任库", func(t *testing.T) {
		for _, mode := range []TLSMode{TLSVerifyCA, TLSVerifyFull} {
			err := handshake(t, tlsFor(t, "db.internal", TLSConfig{Mode: mode}), server)
			var ua x509.UnknownAuthorityError
			assert.ErrorAs(t, err, &ua, "模式 %q 应按系统信任库拒绝自建 CA", mode)
		}
	})
}

func TestTLSClientCertificate(t *testing.T) {
	ca := newCA(t)
	server := tlsServer(t, ca, "db.internal")
	server.ClientAuth = tls.RequireAndVerifyClientCert
	server.ClientCAs = ca.pool()
	certPEM, keyPEM := ca.issue(t, "opsnap", x509.ExtKeyUsageClientAuth)
	_, otherKey := ca.issue(t, "other", x509.ExtKeyUsageClientAuth)

	t.Run("提供证书与私钥时出示客户端证书", func(t *testing.T) {
		cfg := tlsFor(t, "db.internal", TLSConfig{Mode: TLSVerifyFull, CA: ca.pem, ClientCert: certPEM, ClientKey: keyPEM})
		assert.NoError(t, handshake(t, cfg, server))

		cfg = tlsFor(t, "db.internal", TLSConfig{Mode: TLSVerifyFull, CA: ca.pem})
		assert.Error(t, handshake(t, cfg, server), "未出示客户端证书时服务端应拒绝")
	})

	cases := []struct {
		name  string
		tc    TLSConfig
		field string
	}{
		{"只有证书", TLSConfig{ClientCert: certPEM}, "client_key"},
		{"只有私钥", TLSConfig{ClientKey: keyPEM}, "client_cert"},
		{"证书无法解析", TLSConfig{ClientCert: []byte("not a cert"), ClientKey: keyPEM}, "client_cert"},
		{"私钥无法解析", TLSConfig{ClientCert: certPEM, ClientKey: []byte("not a key")}, "client_key"},
		{"证书与私钥不匹配", TLSConfig{ClientCert: certPEM, ClientKey: otherKey}, "client_key"},
		{"CA 无法解析", TLSConfig{Mode: TLSVerifyCA, CA: []byte("garbage")}, "ca"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := Config{Type: TypePostgreSQL, Host: "db.internal", Port: 5432, User: "postgres", TLS: c.tc}
			_, err := cfg.tlsConfig()
			var fe *FieldError
			require.ErrorAs(t, err, &fe)
			assert.Equal(t, c.field, fe.Field)
			assert.ErrorAs(t, cfg.Validate(), &fe, "Validate 应报告同样的字段问题")
		})
	}
}

// reasonOf 按数据源连接失败的规则归类
func reasonOf(err error) Reason { return classify(context.Background(), err) }

func TestTLSInvalidMode(t *testing.T) {
	cfg := Config{Type: TypeMySQL, Host: "db", Port: 3306, User: "root", TLS: TLSConfig{Mode: "bogus"}}
	assert.True(t, errors.Is(cfg.Validate(), ErrInvalidConfig))
}
