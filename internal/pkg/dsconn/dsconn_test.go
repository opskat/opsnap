package dsconn

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opsnap/internal/pkg/fakessh"
	"github.com/opskat/opsnap/internal/pkg/netchain"
)

func serverFileConfig(t *testing.T, s *fakessh.Server) Config {
	host, port := splitAddr(t, s.Addr())
	return Config{ID: 7, Name: "web-1", Type: TypeServerFile, Host: host, Port: port, User: "root", Password: testPassword, HostKey: s.Fingerprint()}
}

func TestServerFile(t *testing.T) {
	t.Run("经链路确认主机密钥、认证后执行 uname -sm", func(t *testing.T) {
		tun, bastion := sshTunnel(t)
		target := startSSH(t, fakessh.Config{})
		c, err := Open(context.Background(), tun, serverFileConfig(t, target))
		require.NoError(t, err)
		defer func() { _ = c.Close() }()
		assert.Equal(t, "Linux x86_64", c.Info.System)
		assert.Nil(t, c.Info.TLS)
		assert.Equal(t, []string{"uname -sm"}, target.Commands())
		assert.Equal(t, []string{target.Addr()}, bastion.Forwards(), "应经由跳板连接目标")
		require.NotNil(t, c.SSH, "探测需要保持打开的 SSH 客户端")
	})

	t.Run("目标主机密钥未确认或已变化时不认证", func(t *testing.T) {
		tun := directTunnel(t)
		target := startSSH(t, fakessh.Config{})
		unknown := serverFileConfig(t, target)
		unknown.HostKey = ""
		changed := serverFileConfig(t, target)
		changed.HostKey = "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
		for _, cfg := range []Config{unknown, changed} {
			_, err := Test(context.Background(), tun, cfg)
			var he *netchain.HopError
			require.ErrorAs(t, err, &he)
			assert.Equal(t, 1, he.Index, "直连时目标主机是第 1 跳")
			assert.Equal(t, "web-1", he.Name)
			var hk *netchain.HostKeyError
			require.ErrorAs(t, err, &hk)
			assert.Equal(t, target.Fingerprint(), hk.Fingerprint)
		}
		assert.Zero(t, target.AuthAttempts(), "未确认主机密钥时不应发送凭据")
		assert.Empty(t, target.Commands())
	})

	t.Run("uname 失败时报告标准错误原文", func(t *testing.T) {
		target := startSSH(t, fakessh.Config{Exec: func(string) (string, string, int) { return "", "uname: permission denied\n", 1 }})
		_, err := Test(context.Background(), directTunnel(t), serverFileConfig(t, target))
		var e *Error
		require.ErrorAs(t, err, &e)
		assert.Equal(t, ReasonFailed, e.Reason)
		assert.Contains(t, e.Error(), "uname: permission denied")
	})
}

// 经 SSH 转发的连接不支持读写截止时间：超时必须仍能打断卡住的握手
func TestTimeoutThroughSSHTunnel(t *testing.T) {
	for _, typ := range []Type{TypeMySQL, TypePostgreSQL} {
		t.Run(string(typ), func(t *testing.T) {
			tun, _ := sshTunnel(t)
			host, port := silentListener(t)
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			start := time.Now()
			_, err := Test(ctx, tun, Config{Type: typ, Host: host, Port: port, User: "u", Password: testPassword, TLS: TLSConfig{Mode: TLSDisable}})
			var e *Error
			require.ErrorAs(t, err, &e)
			assert.Equal(t, ReasonTimeout, e.Reason)
			assert.Less(t, time.Since(start), 5*time.Second)
		})
	}
}

func TestServerFileTimeout(t *testing.T) {
	target := startSSH(t, fakessh.Config{Exec: func(string) (string, string, int) {
		time.Sleep(3 * time.Second)
		return "Linux x86_64\n", "", 0
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := Test(ctx, directTunnel(t), serverFileConfig(t, target))
	var e *Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, ReasonTimeout, e.Reason)
	assert.Less(t, time.Since(start), 2*time.Second)
}

func TestDatabaseErrors(t *testing.T) {
	for _, typ := range []Type{TypeMySQL, TypePostgreSQL} {
		t.Run(string(typ)+" 连不上时为 unreachable 并保留原文", func(t *testing.T) {
			_, err := Test(context.Background(), directTunnel(t), Config{Type: typ, Host: "127.0.0.1", Port: closedPort(t), User: "u", Password: testPassword})
			var e *Error
			require.ErrorAs(t, err, &e)
			assert.Equal(t, ReasonUnreachable, e.Reason)
			assert.Contains(t, e.Error(), "refused")
		})

		t.Run(string(typ)+" 主机不存在", func(t *testing.T) {
			dnsErr := &net.DNSError{Err: "no such host", Name: "db.invalid", IsNotFound: true}
			_, err := Test(context.Background(), stubDialer{err: dnsErr}, Config{Type: typ, Host: "db.invalid", Port: 3306, User: "u"})
			var e *Error
			require.ErrorAs(t, err, &e)
			assert.Equal(t, ReasonUnreachable, e.Reason)
			assert.Contains(t, e.Error(), "no such host")
		})

		t.Run(string(typ)+" 错误原文中去掉密码", func(t *testing.T) {
			leak := errors.New("dial failed for u:" + testPassword + "@db")
			_, err := Test(context.Background(), stubDialer{err: leak}, Config{Type: typ, Host: "db", Port: 3306, User: "u", Password: testPassword})
			require.Error(t, err)
			assert.NotContains(t, err.Error(), testPassword)
			assert.Contains(t, err.Error(), "dial failed")
		})

		t.Run(string(typ)+" 链路中某一跳失败时原样返回 HopError", func(t *testing.T) {
			p, err := fakessh.ListenSOCKS5("127.0.0.1:0", fakessh.SOCKS5Config{})
			require.NoError(t, err)
			host, port := splitAddr(t, p.Addr())
			c, err := netchain.NewChain([]netchain.Hop{{ID: 3, Name: "office-socks", Kind: netchain.KindSOCKS5, Host: host, Port: port}})
			require.NoError(t, err)
			tun, err := c.Connect(context.Background())
			require.NoError(t, err)
			defer func() { _ = tun.Close() }()
			require.NoError(t, p.Close())

			_, err = Test(context.Background(), tun, Config{Type: typ, Host: "127.0.0.1", Port: 1, User: "u"})
			var he *netchain.HopError
			require.ErrorAs(t, err, &he)
			assert.Equal(t, 1, he.Index)
			assert.Equal(t, "office-socks", he.Name)
		})
	}

	t.Run("调用方取消", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := Test(ctx, directTunnel(t), Config{Type: TypeMySQL, Host: "127.0.0.1", Port: closedPort(t), User: "u"})
		var e *Error
		require.ErrorAs(t, err, &e)
		assert.Equal(t, ReasonCanceled, e.Reason)
	})
}

func TestValidate(t *testing.T) {
	ok := Config{Type: TypeMySQL, Host: "db", Port: 3306, User: "root"}
	require.NoError(t, ok.Validate())
	cases := map[string]func(*Config){
		"未知类型":  func(c *Config) { c.Type = "oracle" },
		"缺少主机":  func(c *Config) { c.Host = "" },
		"端口为 0": func(c *Config) { c.Port = 0 },
		"端口过大":  func(c *Config) { c.Port = 65536 },
		"缺少用户名": func(c *Config) { c.User = "" },
		"服务器文件既无密码也无私钥": func(c *Config) { c.Type = TypeServerFile },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := ok
			mutate(&cfg)
			assert.ErrorIs(t, cfg.Validate(), ErrInvalidConfig)
			_, err := Test(context.Background(), stubDialer{err: errors.New("不应拨号")}, cfg)
			assert.ErrorIs(t, err, ErrInvalidConfig, "无效配置不应联网")
		})
	}
}

func TestConfigStringHidesSecrets(t *testing.T) {
	cfg := Config{Type: TypeMySQL, Host: "db", Port: 3306, User: "root", Password: testPassword, TLS: TLSConfig{ClientKey: []byte("KEYDATA")}}
	for _, s := range []string{cfg.String(), cfg.GoString()} {
		assert.False(t, strings.Contains(s, testPassword) || strings.Contains(s, "KEYDATA"), s)
	}
}
