package dump

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	kfs "github.com/kopia/kopia/fs"
	"github.com/kopia/kopia/repo"
	"github.com/kopia/kopia/repo/blob/filesystem"
	"github.com/kopia/kopia/repo/manifest"
	"github.com/kopia/kopia/snapshot"
	"github.com/kopia/kopia/snapshot/snapshotfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opsnap/internal/pkg/dsconn"
	"github.com/opskat/opsnap/internal/pkg/kopiarepo"
	"github.com/opskat/opsnap/internal/pkg/netchain"
	"github.com/opskat/opsnap/internal/pkg/testenv"
)

// 以下用例在 docker.internal 上 opsnap-test 的 MySQL 8.0 / PG 16 中建两个 opsnap_it_<随机> 临时库（源库与恢复目标），
// 经本包导出、写进临时目录中的真实 kopia 仓库，再从快照读出文件，用官方客户端导入目标库并比对数据；
// 无论成功与否都删除这两个库。不读写其他库，也不导入账号与全局对象（会改动共享实例上的账号）。
// 未在 e2e/.env 配置或主控端缺少客户端工具时跳过。
// TestRealMySQL57ExportRestore 额外针对一台临时起的 MySQL 5.7（不是 opsnap-test 常驻的 8.0），
// 验证用 8.0 的 mysqldump 备份 5.7 服务端这一组合，同样未配置 TEST_ENV_MYSQL57_PORT 时跳过。

const realRepoKey = "opsnap-it-repo-key"

func requireTools(t *testing.T, names ...string) {
	t.Helper()
	for _, n := range names {
		if _, err := exec.LookPath(n); err != nil {
			t.Skipf("主控端没有 %s，跳过真实导出测试", n)
		}
	}
}

func directTunnel(t *testing.T) *netchain.Tunnel {
	t.Helper()
	chain, err := netchain.NewChain(nil)
	require.NoError(t, err)
	tun, err := chain.Connect(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { _ = tun.Close() })
	return tun
}

func openReal(t *testing.T, tun *netchain.Tunnel, cfg dsconn.Config) *dsconn.Conn {
	t.Helper()
	c, err := dsconn.Open(context.Background(), tun, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func mustExec(t *testing.T, db *sql.DB, stmts ...string) {
	t.Helper()
	for _, q := range stmts {
		_, err := db.ExecContext(context.Background(), q)
		require.NoError(t, err, q)
	}
}

// snapshotRoundTrip 把会话的文件写成真实 kopia 仓库中的一份快照，再用独立的只读连接把每个文件读出来
func snapshotRoundTrip(t *testing.T, s *Session, kind string) map[string][]byte {
	t.Helper()
	ctx := context.Background()
	loc := kopiarepo.Location{Kind: kopiarepo.KindLocal, Path: filepath.Join(t.TempDir(), "repo")}
	require.NoError(t, kopiarepo.Create(ctx, loc, realRepoKey))
	w, err := kopiarepo.NewManager(t.TempDir()).OpenWriter(ctx, loc, realRepoKey)
	require.NoError(t, err)
	defer func() { _ = w.Close(ctx) }()
	files := make([]kopiarepo.SnapshotFile, len(s.Files()))
	for i, f := range s.Files() {
		files[i] = kopiarepo.SnapshotFile{Name: f.Name, Reader: f}
	}
	res, err := w.WriteSnapshot(ctx, kopiarepo.SnapshotRequest{
		Prefix: "it/" + kind, Tags: kopiarepo.SnapshotTags{JobID: 1, RunID: 1, Type: "full", Kind: kind}, Files: files,
	})
	require.NoError(t, err)
	for i, fr := range res.Files {
		assert.Equal(t, s.Files()[i].Bytes(), fr.Size, "快照中的大小等于导出的字节数：%s", fr.Name)
	}

	cfg := filepath.Join(t.TempDir(), "read.config")
	st, err := filesystem.New(ctx, &filesystem.Options{Path: loc.Path}, false)
	require.NoError(t, err)
	defer func() { _ = st.Close(ctx) }()
	require.NoError(t, repo.Connect(ctx, cfg, st, realRepoKey, &repo.ConnectOptions{ClientOptions: repo.ClientOptions{ReadOnly: true}}))
	r, err := repo.Open(ctx, cfg, realRepoKey, &repo.Options{DisableRepositoryLog: true, OnFatalError: func(error) {}})
	require.NoError(t, err)
	defer func() { _ = r.Close(ctx) }()
	man, err := snapshot.LoadSnapshot(ctx, r, manifest.ID(res.ID))
	require.NoError(t, err)
	root, err := snapshotfs.SnapshotRoot(r, man)
	require.NoError(t, err)
	dir, ok := root.(kfs.Directory)
	require.True(t, ok)
	entries, err := kfs.GetAllEntries(ctx, dir)
	require.NoError(t, err)
	out := map[string][]byte{}
	for _, e := range entries {
		de, ok := e.(snapshot.HasDirEntry)
		require.True(t, ok)
		rd, err := r.OpenObject(ctx, de.DirEntry().ObjectID)
		require.NoError(t, err)
		b, err := io.ReadAll(rd)
		_ = rd.Close()
		require.NoError(t, err)
		out[e.Name()] = b
	}
	return out
}

// runClient 用官方客户端导入；stdin 为导出内容，密码只在 0600 的临时文件中
func runClient(t *testing.T, env []string, stdin []byte, name string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), name, args...) //nolint:gosec // 测试调用官方客户端
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "LC_ALL=C"}, env...)
	cmd.Stdin = bytes.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s: %s", name, out)
}

// rowsOf 以文本形式读出查询结果，便于比对源库与目标库
func rowsOf(t *testing.T, db *sql.DB, q string) []string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), q)
	require.NoError(t, err, q)
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	require.NoError(t, err)
	var out []string
	for rows.Next() {
		vals := make([]sql.RawBytes, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		require.NoError(t, rows.Scan(ptrs...))
		parts := make([]string, len(vals))
		for i, v := range vals {
			if v == nil {
				parts[i] = "NULL"
			} else {
				parts[i] = fmt.Sprintf("%q", string(v))
			}
		}
		out = append(out, strings.Join(parts, "|"))
	}
	require.NoError(t, rows.Err())
	return out
}

func TestRealMySQLExportRestore(t *testing.T) {
	svc := testenv.MySQL(t)
	requireTools(t, "mysqldump", "mysql")
	ctx := context.Background()
	tun := directTunnel(t)
	cfg := dsconn.Config{Type: dsconn.TypeMySQL, Host: svc.Host, Port: svc.Port, User: svc.User, Password: svc.Password}
	admin := openReal(t, tun, cfg)
	base := testenv.TempDBName(t)
	src, dst := base+"_src", base+"_dst"
	t.Cleanup(func() {
		for _, d := range []string{src, dst} {
			_, err := admin.DB.ExecContext(context.Background(), "DROP DATABASE IF EXISTS `"+d+"`")
			assert.NoError(t, err, "删除临时库 %s", d)
		}
	})
	mustExec(t, admin.DB,
		"CREATE DATABASE `"+src+"` CHARACTER SET utf8mb4",
		"CREATE DATABASE `"+dst+"` CHARACTER SET utf8mb4",
		"CREATE TABLE `"+src+"`.items (id INT PRIMARY KEY, name VARCHAR(50), data VARBINARY(16), price DECIMAL(10,2), at DATETIME) ENGINE=InnoDB",
		"INSERT INTO `"+src+"`.items VALUES (1, '苹果 🍎', 0x00FF10, 1.50, '2026-09-28 10:00:00'), (2, 'it''s \"q\" \\\\ x', NULL, 0, NULL)",
		"CREATE TABLE `"+src+"`.cache (k INT) ENGINE=MyISAM",
		"INSERT INTO `"+src+"`.cache VALUES (7)",
		"CREATE TABLE `"+src+"`.skipme (x INT)",
		"INSERT INTO `"+src+"`.skipme VALUES (1)",
		"CREATE VIEW `"+src+"`.v_items AS SELECT id, name FROM `"+src+"`.items",
		"CREATE PROCEDURE `"+src+"`.p_count() SELECT COUNT(*) FROM items",
		"CREATE TRIGGER `"+src+"`.trg BEFORE INSERT ON `"+src+"`.items FOR EACH ROW SET NEW.price = NEW.price",
		"CREATE EVENT `"+src+"`.ev ON SCHEDULE EVERY 1 DAY DISABLE DO SELECT 1",
	)

	var lg logs
	s, err := Start(ctx, t.TempDir(), Source{Dialer: tun, Config: cfg, ServerVersion: admin.Info.Version}, Options{
		Databases: []string{src}, Routines: true, Triggers: true, Events: true, Accounts: true,
		ExcludeTables: []string{src + ".skipme", src + ".nothere"}, Log: lg.add,
	})
	require.NoError(t, err)
	defer func() { _ = s.Close() }()
	files := snapshotRoundTrip(t, s, "mysql")
	require.NoError(t, s.Close())

	require.Len(t, files, 2)
	accounts := string(files[MySQLAccountsFile])
	assert.NotContains(t, accounts, "mysql.sys")
	assert.NotContains(t, accounts, "`root`@`localhost`")
	if strings.Contains(accounts, "CREATE USER") {
		assert.Contains(t, accounts, "CREATE USER IF NOT EXISTS")
		assert.Contains(t, accounts, "GRANT ")
	}
	assert.Contains(t, lg.text(), src+".nothere")
	assert.Contains(t, lg.text(), src+".cache")
	assert.NotContains(t, lg.text(), svc.Password)

	dump := files[MySQLDatabasesFile]
	assert.NotContains(t, string(dump), "GTID_PURGED")
	// 目标库与源库在同一实例上：把库名换成目标库后再导入，确保导入不会写到源库
	dump = bytes.ReplaceAll(dump, []byte("`"+src+"`"), []byte("`"+dst+"`"))
	for _, l := range strings.Split(string(dump), "\n") {
		if !strings.HasPrefix(l, "--") {
			require.NotContains(t, l, src, "导入内容不能引用源库")
		}
	}
	cnf := filepath.Join(t.TempDir(), "client.cnf")
	require.NoError(t, os.WriteFile(cnf, []byte(fmt.Sprintf("[client]\nuser=%s\npassword=%s\nhost=%s\nport=%d\nprotocol=TCP\nloose-ssl\n",
		svc.User, optionQuote(svc.Password), svc.Host, svc.Port)), 0o600))
	runClient(t, nil, dump, "mysql", "--defaults-file="+cnf)

	for _, q := range []string{"SELECT * FROM `%s`.items ORDER BY id", "SELECT * FROM `%s`.cache", "SELECT * FROM `%s`.v_items ORDER BY id"} {
		want := rowsOf(t, admin.DB, fmt.Sprintf(q, src))
		assert.NotEmpty(t, want)
		assert.Equal(t, want, rowsOf(t, admin.DB, fmt.Sprintf(q, dst)), q)
	}
	count := func(q string) int {
		var n int
		require.NoError(t, admin.DB.QueryRowContext(ctx, q, dst).Scan(&n), q)
		return n
	}
	assert.Equal(t, 0, count("SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? AND TABLE_NAME = 'skipme'"), "排除表不导出")
	assert.Equal(t, 1, count("SELECT COUNT(*) FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA = ? AND ROUTINE_NAME = 'p_count'"))
	assert.Equal(t, 1, count("SELECT COUNT(*) FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA = ? AND TRIGGER_NAME = 'trg'"))
	assert.Equal(t, 1, count("SELECT COUNT(*) FROM information_schema.EVENTS WHERE EVENT_SCHEMA = ? AND EVENT_NAME = 'ev'"))
}

// TestRealMySQL57ExportRestore 验证「MySQL 5.7 服务端用 8.0 的 mysqldump」这一组合本身能跑通：导出不因
// information_schema.COLUMN_STATISTICS 在 5.7 上不存在而报 1109 失败，导出内容能导入一个空库并与源数据一致。
// 这是一台临时起的 MySQL 5.7（不是 opsnap-test 里常驻的 8.0），未在 e2e/.env 配置 TEST_ENV_MYSQL57_PORT 时跳过
func TestRealMySQL57ExportRestore(t *testing.T) {
	svc := testenv.MySQL57(t)
	requireTools(t, "mysqldump", "mysql")
	ctx := context.Background()
	tun := directTunnel(t)
	cfg := dsconn.Config{Type: dsconn.TypeMySQL, Host: svc.Host, Port: svc.Port, User: svc.User, Password: svc.Password}
	admin := openReal(t, tun, cfg)
	require.True(t, strings.HasPrefix(admin.Info.Version, "5.7"), "配置的 TEST_ENV_MYSQL57_PORT 应指向 MySQL 5.7，读到的版本是 %s", admin.Info.Version)
	base := testenv.TempDBName(t)
	src, dst := base+"_src", base+"_dst"
	t.Cleanup(func() {
		for _, d := range []string{src, dst} {
			_, err := admin.DB.ExecContext(context.Background(), "DROP DATABASE IF EXISTS `"+d+"`")
			assert.NoError(t, err, "删除临时库 %s", d)
		}
	})
	mustExec(t, admin.DB,
		"CREATE DATABASE `"+src+"` CHARACTER SET utf8mb4",
		"CREATE DATABASE `"+dst+"` CHARACTER SET utf8mb4", // 导入目标必须是空库
		"CREATE TABLE `"+src+"`.items (id INT PRIMARY KEY, name VARCHAR(50), data VARBINARY(16), price DECIMAL(10,2), at DATETIME) ENGINE=InnoDB",
		"INSERT INTO `"+src+"`.items VALUES (1, '苹果 🍎', 0x00FF10, 1.50, '2026-09-28 10:00:00'), (2, 'it''s \"q\" \\\\ x', NULL, 0, NULL)",
	)

	var lg logs
	s, err := Start(ctx, t.TempDir(), Source{Dialer: tun, Config: cfg, ServerVersion: admin.Info.Version}, Options{
		Databases: []string{src}, Log: lg.add,
	})
	require.NoError(t, err)
	defer func() { _ = s.Close() }()
	files := snapshotRoundTrip(t, s, "mysql57")
	require.NoError(t, s.Close())
	assert.NotContains(t, lg.text(), svc.Password)

	dump := files[MySQLDatabasesFile]
	require.NotEmpty(t, dump, "导出必须成功产出内容：5.7 服务端上 8.0 的 mysqldump 不能因 COLUMN_STATISTICS 报错而失败")
	// 目标库与源库在同一实例上：把库名换成目标库后再导入
	dump = bytes.ReplaceAll(dump, []byte("`"+src+"`"), []byte("`"+dst+"`"))
	cnf := filepath.Join(t.TempDir(), "client.cnf")
	require.NoError(t, os.WriteFile(cnf, []byte(fmt.Sprintf("[client]\nuser=%s\npassword=%s\nhost=%s\nport=%d\nprotocol=TCP\nloose-ssl\n",
		svc.User, optionQuote(svc.Password), svc.Host, svc.Port)), 0o600))
	runClient(t, nil, dump, "mysql", "--defaults-file="+cnf)

	want := rowsOf(t, admin.DB, fmt.Sprintf("SELECT * FROM `%s`.items ORDER BY id", src))
	assert.NotEmpty(t, want)
	assert.Equal(t, want, rowsOf(t, admin.DB, fmt.Sprintf("SELECT * FROM `%s`.items ORDER BY id", dst)), "导入空库后的数据须与源库一致")
}

func TestRealPostgresExportRestore(t *testing.T) {
	svc := testenv.Postgres(t)
	requireTools(t, "pg_dump", "pg_dumpall", "pg_restore")
	ctx := context.Background()
	tun := directTunnel(t)
	cfg := dsconn.Config{Type: dsconn.TypePostgreSQL, Host: svc.Host, Port: svc.Port, User: svc.User, Password: svc.Password}
	admin := openReal(t, tun, cfg)
	base := testenv.TempDBName(t)
	src, dst := base+"_src", base+"_dst"
	t.Cleanup(func() {
		for _, d := range []string{src, dst} {
			_, err := admin.DB.ExecContext(context.Background(), `DROP DATABASE IF EXISTS "`+d+`" WITH (FORCE)`)
			assert.NoError(t, err, "删除临时库 %s", d)
		}
	})
	mustExec(t, admin.DB, `CREATE DATABASE "`+src+`"`, `CREATE DATABASE "`+dst+`"`)
	srcCfg := cfg
	srcCfg.Database = src
	srcDB := openReal(t, tun, srcCfg).DB
	mustExec(t, srcDB,
		`CREATE TABLE items (id int PRIMARY KEY, name text, data bytea, price numeric(10,2), tags text[], at timestamptz)`,
		`INSERT INTO items VALUES (1, '苹果 🍎', '\x00ff10', 1.50, '{a,b}', '2026-09-28 10:00:00+08'), (2, 'it''s "q" \ x', NULL, 0, NULL, NULL)`,
		`CREATE SCHEMA sales`,
		`CREATE TABLE sales."Orders" (id serial PRIMARY KEY, note text)`,
		`INSERT INTO sales."Orders" (note) VALUES ('one'), ('two')`,
		`CREATE TABLE skipme (x int)`,
		`INSERT INTO skipme VALUES (1)`,
		`CREATE VIEW v_items AS SELECT id, name FROM items`,
		`CREATE FUNCTION item_count() RETURNS bigint LANGUAGE sql AS 'SELECT count(*) FROM items'`,
	)

	var lg logs
	s, err := Start(ctx, t.TempDir(), Source{Dialer: tun, Config: cfg, ServerVersion: admin.Info.Version}, Options{
		Databases: []string{src}, Globals: true,
		ExcludeTables: []string{src + ".public.skipme", src + ".public.nothere"}, Log: lg.add,
	})
	require.NoError(t, err)
	defer func() { _ = s.Close() }()
	files := snapshotRoundTrip(t, s, "postgres")
	require.NoError(t, s.Close())

	require.Len(t, files, 2)
	assert.Contains(t, string(files[PostgresGlobalsFile]), "CREATE ROLE postgres")
	assert.Contains(t, lg.text(), src+".public.nothere")
	assert.NotContains(t, lg.text(), src+".public.skipme")
	assert.NotContains(t, lg.text(), svc.Password)

	pass := filepath.Join(t.TempDir(), "pgpass")
	require.NoError(t, os.WriteFile(pass, []byte("*:*:*:*:"+pgpassEscape(svc.Password)+"\n"), 0o600))
	conn := fmt.Sprintf("host='%s' port='%d' user='%s' dbname='%s'", svc.Host, svc.Port, svc.User, dst)
	runClient(t, []string{"PGPASSFILE=" + pass}, files[PostgresArchiveName(src)],
		"pg_restore", "--no-password", "--exit-on-error", "--dbname="+conn)

	dstCfg := cfg
	dstCfg.Database = dst
	dstDB := openReal(t, tun, dstCfg).DB
	for _, q := range []string{
		"SELECT * FROM items ORDER BY id", `SELECT * FROM sales."Orders" ORDER BY id`,
		"SELECT * FROM v_items ORDER BY id", "SELECT item_count()",
		`SELECT nextval('sales."Orders_id_seq"')`,
	} {
		want := rowsOf(t, srcDB, q)
		assert.NotEmpty(t, want)
		assert.Equal(t, want, rowsOf(t, dstDB, q), q)
	}
	var n int
	require.NoError(t, dstDB.QueryRowContext(ctx, "SELECT count(*) FROM pg_tables WHERE tablename = 'skipme'").Scan(&n))
	assert.Equal(t, 0, n, "排除表不导出")
}
