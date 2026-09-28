package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opsnap/internal/pkg/dsconn"
	"github.com/opskat/opsnap/internal/pkg/dump"
	"github.com/opskat/opsnap/internal/pkg/netchain"
	"github.com/opskat/opsnap/internal/pkg/probe"
	"github.com/opskat/opsnap/internal/service/datasource_svc"
)

const (
	testUser     = "opsnap"
	testPassword = "fake:pg\\password"
)

// TestMain 测试二进制被链接成 pg_dump / pg_dumpall 执行时（见 toolsOnPath），直接充当假导出工具
func TestMain(m *testing.M) {
	if code, ok := runAsTool(os.Args, os.Stdout, os.Stderr); ok {
		os.Exit(code)
	}
	os.Exit(m.Run())
}

func startServer(t *testing.T) *Server {
	t.Helper()
	srv, err := Listen("127.0.0.1:0", Config{User: testUser, Password: testPassword})
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Close() })
	return srv
}

// tunnel 不经任何跳板的直连链路，与没有网络通道的数据源相同
func tunnel(t *testing.T) *netchain.Tunnel {
	t.Helper()
	chain, err := netchain.NewChain(nil)
	require.NoError(t, err)
	tun, err := chain.Connect(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { _ = tun.Close() })
	return tun
}

func config(srv *Server, password string) dsconn.Config {
	return dsconn.Config{Type: dsconn.TypePostgreSQL, Host: "127.0.0.1", Port: srv.Port(), User: testUser, Password: password}
}

// toolsOnPath 把测试二进制以 pg_dump、pg_dumpall 的名字放到 PATH 最前面
func toolsOnPath(t *testing.T) {
	t.Helper()
	self, err := os.Executable()
	require.NoError(t, err)
	dir := t.TempDir()
	for _, name := range []string{"pg_dump", "pg_dumpall"} {
		require.NoError(t, os.Symlink(self, filepath.Join(dir, name)))
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestConnectionTest(t *testing.T) {
	srv := startServer(t)
	ctx := context.Background()

	for _, mode := range []dsconn.TLSMode{"", dsconn.TLSPrefer, dsconn.TLSDisable} {
		cfg := config(srv, testPassword)
		cfg.TLS.Mode = mode
		info, err := dsconn.Test(ctx, tunnel(t), cfg)
		require.NoError(t, err, "TLS 模式 %q", mode)
		assert.Equal(t, Version, info.Version)
		assert.Nil(t, info.TLS, "假服务端不提供 TLS")
	}

	_, err := dsconn.Test(ctx, tunnel(t), config(srv, "wrong"))
	var de *dsconn.Error
	require.ErrorAs(t, err, &de)
	assert.Equal(t, dsconn.ReasonAuthFailed, de.Reason)

	cfg := config(srv, testPassword)
	cfg.Database = "missing"
	_, err = dsconn.Test(ctx, tunnel(t), cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `database "missing" does not exist`)
}

func TestProbeAndDatabaseList(t *testing.T) {
	toolsOnPath(t)
	srv := startServer(t)
	ctx := context.Background()
	conn, err := dsconn.Open(ctx, tunnel(t), config(srv, testPassword))
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	items := probe.Run(ctx, dsconn.TypePostgreSQL, conn)
	require.Len(t, items, 6)
	for _, it := range items {
		assert.Equal(t, probe.TierOK, it.Tier, "%s: %s", it.Key, it.Detail.ZhCN)
	}

	dbs, err := datasource_svc.ListDatabases(ctx, dsconn.TypePostgreSQL, conn)
	require.NoError(t, err)
	names := make([]string, len(dbs))
	for i, d := range dbs {
		names[i] = d.Name
		assert.Positive(t, d.Size, d.Name)
	}
	assert.Equal(t, []string{"app", FailingDatabase, "postgres"}, names)
}

func TestUnknownQueryIsAnErrorNotAHang(t *testing.T) {
	srv := startServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := dsconn.Open(ctx, tunnel(t), config(srv, testPassword))
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	_, err = conn.DB.ExecContext(ctx, "DROP TABLE users")
	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	assert.Equal(t, "0A000", pgErr.Code)
	// 出错之后同一连接池仍然可用
	var version string
	require.NoError(t, conn.DB.QueryRowContext(ctx, "SHOW server_version").Scan(&version))
	assert.Equal(t, Version, version)
}

func startDump(t *testing.T, srv *Server, opts dump.Options) *dump.Session {
	t.Helper()
	toolsOnPath(t)
	sess, err := dump.Start(context.Background(), t.TempDir(),
		dump.Source{Dialer: tunnel(t), Config: config(srv, testPassword), ServerVersion: Version}, opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

func TestDumpThroughForward(t *testing.T) {
	srv := startServer(t)
	sess := startDump(t, srv, dump.Options{Databases: []string{"app"}, Globals: true})

	got := map[string][]byte{}
	for _, f := range sess.Files() {
		b, err := io.ReadAll(f)
		require.NoError(t, err, f.Name)
		got[f.Name] = b
	}
	require.Len(t, got, 2)
	assert.True(t, bytes.HasPrefix(got[dump.PostgresArchiveName("app")], []byte("PGDMP")))
	assert.Contains(t, string(got[dump.PostgresGlobalsFile]), "PostgreSQL database cluster dump complete")

	// 两个工具都真正经本机转发端口连上了假服务端：密码来自 pgpass 文件，库名来自连接串或 --database
	seen := srv.Sessions()
	assert.Contains(t, seen, Session{User: testUser, Database: "app", Application: "opsnap"})
	assert.Contains(t, seen, Session{User: testUser, Database: "postgres", Application: "opsnap"})
}

func TestDumpFailingDatabase(t *testing.T) {
	srv := startServer(t)
	sess := startDump(t, srv, dump.Options{Databases: []string{FailingDatabase}})

	files := sess.Files()
	require.Len(t, files, 1)
	_, err := io.ReadAll(files[0])
	var te *dump.ToolError
	require.ErrorAs(t, err, &te)
	assert.Equal(t, "pg_dump", te.Tool)
	assert.Equal(t, 1, te.ExitCode)
	assert.Contains(t, te.Stderr, "could not read block")
	assert.False(t, errors.Is(err, dump.ErrPrivilege))
}

func TestToolVersionAndUsage(t *testing.T) {
	var out, errOut bytes.Buffer
	code, ok := runAsTool([]string{"/x/pg_dump", "--version"}, &out, &errOut)
	require.True(t, ok)
	assert.Equal(t, 0, code)
	major, _, ok := probe.ParseMajorMinor(out.String())
	require.True(t, ok)
	sMajor, _, _ := probe.ParseMajorMinor(Version)
	assert.GreaterOrEqual(t, major, sMajor)

	out.Reset()
	code, ok = runAsTool([]string{"pg_dumpall", "--version"}, &out, &errOut)
	require.True(t, ok)
	assert.Equal(t, 0, code)
	assert.True(t, strings.HasPrefix(out.String(), "pg_dumpall (PostgreSQL) "))

	// 不支持的用法明确失败，不产生看似完整的输出
	out.Reset()
	errOut.Reset()
	code, _ = runAsTool([]string{"pg_dump", "--format=plain", "--dbname=host=127.0.0.1"}, &out, &errOut)
	assert.Equal(t, 1, code)
	assert.Empty(t, out.String())
	assert.Contains(t, errOut.String(), "--format=custom")

	_, ok = runAsTool([]string{"/usr/local/bin/fakepg", "-addr", ":0"}, &out, &errOut)
	assert.False(t, ok, "以 fakepg 自己的名字运行时是服务端")
}
