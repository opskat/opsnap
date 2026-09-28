package datasource_svc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opsnap/internal/pkg/dsconn"
	"github.com/opskat/opsnap/internal/pkg/netchain"
	"github.com/opskat/opsnap/internal/pkg/testenv"
)

// 以下用例连接 docker.lan 上 opsnap-test 的 MySQL 8.0 / PG 16，全部为只读查询；
// 未在 e2e/.env 配置时跳过（见 internal/pkg/testenv）。用于验证 ListDatabases 的 SQL 能在真实数据库上跑通：
// MySQL 排除系统库，PostgreSQL 只列允许连接的非模板库（docs/specs/2026-09-27-backup-jobs.md「第 2 步：内容与方式」）

func directTunnel(t *testing.T) *netchain.Tunnel {
	t.Helper()
	chain, err := netchain.NewChain(nil)
	require.NoError(t, err)
	tun, err := chain.Connect(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { _ = tun.Close() })
	return tun
}

func openRealConn(t *testing.T, typ dsconn.Type, svc testenv.Service) *dsconn.Conn {
	t.Helper()
	cfg := dsconn.Config{Type: typ, Host: svc.Host, Port: svc.Port, User: svc.User, Password: svc.Password}
	conn, err := dsconn.Open(context.Background(), directTunnel(t), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func TestRealMySQLDatabasesExcludeSystemSchemas(t *testing.T) {
	svc := testenv.MySQL(t)
	conn := openRealConn(t, dsconn.TypeMySQL, svc)

	dbs, err := ListDatabases(context.Background(), dsconn.TypeMySQL, conn)
	require.NoError(t, err)
	for _, d := range dbs {
		assert.NotContains(t, []string{"mysql", "information_schema", "performance_schema", "sys"}, d.Name)
		assert.GreaterOrEqual(t, d.Size, int64(0))
	}
}

func TestRealPostgresDatabasesListConnectableNonTemplate(t *testing.T) {
	svc := testenv.Postgres(t)
	conn := openRealConn(t, dsconn.TypePostgreSQL, svc)

	dbs, err := ListDatabases(context.Background(), dsconn.TypePostgreSQL, conn)
	require.NoError(t, err)
	names := make([]string, 0, len(dbs))
	for _, d := range dbs {
		names = append(names, d.Name)
		assert.NotEqual(t, "template0", d.Name)
		assert.NotEqual(t, "template1", d.Name)
	}
	assert.Contains(t, names, "postgres", "默认的 postgres 库允许连接且不是模板")
	for _, d := range dbs {
		if d.Name == "postgres" {
			assert.Greater(t, d.Size, int64(0), "postgres 账号是超级用户，能读到实际占用")
		}
	}
}
