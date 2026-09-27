package probe

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
// 未在 e2e/.env 配置时跳过（见 internal/pkg/testenv）。用于验证探测 SQL 本身能在真实数据库上跑通，
// 三档判定与修复方法的组合已由 mysql_test.go / postgres_test.go 的纯函数单元测试覆盖。

func directDialer(t *testing.T) *netchain.Tunnel {
	t.Helper()
	chain, err := netchain.NewChain(nil)
	require.NoError(t, err)
	tun, err := chain.Connect(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { _ = tun.Close() })
	return tun
}

func openDB(t *testing.T, typ dsconn.Type, svc testenv.Service) *dsconn.Conn {
	t.Helper()
	cfg := dsconn.Config{Type: typ, Host: svc.Host, Port: svc.Port, User: svc.User, Password: svc.Password}
	conn, err := dsconn.Open(context.Background(), directDialer(t), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func TestRealMySQLItems(t *testing.T) {
	svc := testenv.MySQL(t)
	conn := openDB(t, dsconn.TypeMySQL, svc)

	items := Run(context.Background(), dsconn.TypeMySQL, conn)

	require.Len(t, items, 8)
	byKey := map[string]Item{}
	for _, it := range items {
		byKey[it.Key] = it
		assert.NotEqual(t, Tier(""), it.Tier, it.Key)
		assert.NotEmpty(t, it.Detail.ZhCN, it.Key)
		assert.NotEmpty(t, it.Detail.En, it.Key)
		if it.Tier != TierOK {
			t.Logf("%s: %s（%s）", it.Key, it.Detail.ZhCN, it.Tier)
		}
	}
	assert.Equal(t, TierOK, byKey["mysql.version"].Tier)
	assert.Contains(t, byKey["mysql.version"].Detail.ZhCN, "8.0")
	// root 账号在测试镜像里通常有 ALL PRIVILEGES
	assert.Equal(t, TierOK, byKey["mysql.replication_privileges"].Tier, byKey["mysql.replication_privileges"].Detail.ZhCN)
}

func TestRealPostgresItems(t *testing.T) {
	svc := testenv.Postgres(t)
	conn := openDB(t, dsconn.TypePostgreSQL, svc)

	items := Run(context.Background(), dsconn.TypePostgreSQL, conn)

	require.Len(t, items, 6)
	byKey := map[string]Item{}
	for _, it := range items {
		byKey[it.Key] = it
		assert.NotEqual(t, Tier(""), it.Tier, it.Key)
		assert.NotEmpty(t, it.Detail.ZhCN, it.Key)
		assert.NotEmpty(t, it.Detail.En, it.Key)
		if it.Tier != TierOK {
			t.Logf("%s: %s（%s）", it.Key, it.Detail.ZhCN, it.Tier)
		}
	}
	assert.Equal(t, TierOK, byKey["postgres.version"].Tier)
	assert.Contains(t, byKey["postgres.version"].Detail.ZhCN, "16")
	// postgres 账号在测试镜像里是超级用户
	assert.Equal(t, TierOK, byKey["postgres.replication_attr"].Tier, byKey["postgres.replication_attr"].Detail.ZhCN)
}
