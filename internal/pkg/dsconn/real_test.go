package dsconn

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opsnap/internal/pkg/testenv"
)

// 以下用例连接 docker.lan 上 opsnap-test 的 MySQL 8.0 / PG 16，只读；未在 e2e/.env 配置时跳过。
// 经进程内 SSH 跳板连接，覆盖“转发的连接不支持截止时间”的真实驱动路径。

func dbConfig(typ Type, s testenv.Service, mode TLSMode) Config {
	return Config{Type: typ, Host: s.Host, Port: s.Port, User: s.User, Password: s.Password, TLS: TLSConfig{Mode: mode}}
}

func TestRealMySQL(t *testing.T) {
	svc := testenv.MySQL(t)
	tun, bastion := sshTunnel(t)
	ctx := context.Background()

	t.Run("优先加密：MySQL 8.0 自带证书，加密但不校验", func(t *testing.T) {
		c, err := Open(ctx, tun, dbConfig(TypeMySQL, svc, TLSPrefer))
		require.NoError(t, err)
		defer func() { _ = c.Close() }()
		assert.True(t, strings.HasPrefix(c.Info.Version, "8.0."), c.Info.Version)
		require.NotNil(t, c.Info.TLS)
		assert.True(t, strings.HasPrefix(c.Info.TLS.Version, "TLSv1."), c.Info.TLS.Version)
		assert.False(t, c.Info.TLS.Verified)
		var one int
		require.NoError(t, c.DB.QueryRowContext(ctx, "SELECT 1").Scan(&one), "探测应能继续使用连接池")
		assert.Contains(t, bastion.Forwards(), svc.Addr())
	})

	t.Run("不加密", func(t *testing.T) {
		info, err := Test(ctx, tun, dbConfig(TypeMySQL, svc, TLSDisable))
		require.NoError(t, err)
		assert.Nil(t, info.TLS)
	})

	t.Run("校验 CA 与主机名：自签名证书按系统信任库校验失败", func(t *testing.T) {
		_, err := Test(ctx, tun, dbConfig(TypeMySQL, svc, TLSVerifyFull))
		var e *Error
		require.ErrorAs(t, err, &e)
		assert.Equal(t, ReasonCertificate, e.Reason, e.Msg)
	})

	t.Run("密码错误", func(t *testing.T) {
		cfg := dbConfig(TypeMySQL, svc, TLSPrefer)
		cfg.Password = "wrong-" + svc.Password
		_, err := Test(ctx, tun, cfg)
		var e *Error
		require.ErrorAs(t, err, &e)
		assert.Equal(t, ReasonAuthFailed, e.Reason, e.Msg)
		assert.Contains(t, e.Msg, "Access denied")
		assert.NotContains(t, e.Msg, svc.Password)
	})
}

func TestRealPostgres(t *testing.T) {
	svc := testenv.Postgres(t)
	tun, bastion := sshTunnel(t)
	ctx := context.Background()

	t.Run("优先加密：服务端未开启 TLS 时不加密", func(t *testing.T) {
		c, err := Open(ctx, tun, dbConfig(TypePostgreSQL, svc, TLSPrefer))
		require.NoError(t, err)
		defer func() { _ = c.Close() }()
		assert.True(t, strings.HasPrefix(c.Info.Version, "16."), c.Info.Version)
		assert.Nil(t, c.Info.TLS)
		var db string
		require.NoError(t, c.DB.QueryRowContext(ctx, "SELECT current_database()").Scan(&db))
		assert.Equal(t, DefaultPGDatabase, db)
		assert.Contains(t, bastion.Forwards(), svc.Addr())
	})

	t.Run("必须加密：服务端不支持 TLS", func(t *testing.T) {
		_, err := Test(ctx, tun, dbConfig(TypePostgreSQL, svc, TLSRequire))
		var e *Error
		require.ErrorAs(t, err, &e)
		assert.Equal(t, ReasonTLS, e.Reason, e.Msg)
	})

	t.Run("密码错误", func(t *testing.T) {
		cfg := dbConfig(TypePostgreSQL, svc, TLSDisable)
		cfg.Password = "wrong-" + svc.Password
		_, err := Test(ctx, tun, cfg)
		var e *Error
		require.ErrorAs(t, err, &e)
		assert.Equal(t, ReasonAuthFailed, e.Reason, e.Msg)
		assert.Contains(t, e.Msg, "password authentication failed")
		assert.NotContains(t, e.Msg, svc.Password)
	})
}
