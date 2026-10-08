package dump

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opsnap/internal/pkg/dsconn"
)

const (
	oracleVersion  = "mysqldump  Ver 8.0.40 for Linux on x86_64 (MySQL Community Server - GPL)"
	mariadbVersion = "mysqldump  Ver 10.19 Distrib 10.11.14-MariaDB, for debian-linux-gnu (x86_64)"
	pgDumpVersion  = "pg_dump (PostgreSQL) 16.4"
	pgAllVersion   = "pg_dumpall (PostgreSQL) 16.4"

	mysqlOK  = "printf '%s\\n' '-- MySQL dump 10.13' 'CREATE TABLE t (id int);' '-- Dump completed on 2026-09-28 10:00:00'"
	pgDumpOK = "printf 'PGDMP\\001\\017\\000\\004\\010\\001archive-body'"
	pgAllOK  = "printf '%s\\n' '--' 'CREATE ROLE app;' '--' '-- PostgreSQL database cluster dump complete' '--' ''"
	//nolint:gosec // 假工具在错误输出中打印测试密码，用于验证去除秘密
	secretLog = "printf '%s\\n' 'mysqldump: Got error: 1045: Access denied (password s3cret#pw\"x)' >&2; exit 2"
)

// tablesOnly 只回答 MySQL 的表清单查询
func tablesOnly(rows ...[]driver.Value) fakeAnswer {
	return func(q string, _ []driver.Value) ([]string, [][]driver.Value, error) {
		if strings.Contains(q, "information_schema.TABLES") {
			return []string{"TABLE_SCHEMA", "TABLE_NAME", "TABLE_TYPE", "ENGINE"}, rows, nil
		}
		return nil, nil, fmt.Errorf("意外的查询 %q", q)
	}
}

func forwardPort(t *testing.T, s *Session) string {
	t.Helper()
	host, port, err := net.SplitHostPort(s.fwd.addr())
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1", host)
	return port
}

// assertNoSecret 命令行与环境变量中都没有密码
func assertNoSecret(t *testing.T, r fakeRun) {
	t.Helper()
	for _, a := range r.argv {
		assert.NotContains(t, a, "s3cret", "命令行不能含密码")
	}
	for k, v := range r.env {
		assert.NotContains(t, v, "s3cret", "环境变量 %s 不能含密码", k)
	}
}

func TestMySQLExport(t *testing.T) {
	e := newFakeEnv(t)
	e.tool("mysqldump", oracleVersion, mysqlOK)
	db := &fakeDB{answer: tablesOnly(
		[]driver.Value{"app", "logs", "BASE TABLE", "InnoDB"},
		[]driver.Value{"app", "cache", "BASE TABLE", "MyISAM"},
		[]driver.Value{"shop", "v1", "VIEW", nil},
	)}
	db.install(t)
	var lg logs

	s, err := Start(context.Background(), e.base, mysqlSource(), Options{
		Databases: []string{"app", "shop"}, Routines: true, Events: true, Triggers: false,
		ExcludeTables: []string{"app.logs", "app.missing"}, Log: lg.add,
	})
	require.NoError(t, err)
	port := forwardPort(t, s)
	got, err := readAll(t, s)
	require.NoError(t, err)

	require.Len(t, s.Files(), 1)
	f := s.Files()[0]
	assert.Equal(t, "databases.sql", f.Name)
	assert.Contains(t, string(got["databases.sql"]), "CREATE TABLE t")
	assert.Equal(t, int64(len(got["databases.sql"])), f.Bytes())

	runs := e.runs("mysqldump")
	require.Len(t, runs, 1)
	r := runs[0]
	assertNoSecret(t, r)
	assert.True(t, strings.HasPrefix(r.argv[0], "--defaults-file="), "选项文件必须是第一个参数：%v", r.argv)
	for _, a := range []string{"--single-transaction", "--routines", "--events", "--skip-triggers", "--set-gtid-purged=OFF",
		"--ignore-table=app.logs", "--ignore-table=app.missing"} {
		assert.Contains(t, r.argv, a)
	}
	assert.Equal(t, []string{"--databases", "--", "app", "shop"}, r.argv[len(r.argv)-4:])

	cnf, mode := r.copied(t, r.arg("--defaults-file="))
	assert.Equal(t, os.FileMode(0o600), mode)
	assert.Contains(t, cnf, `password="s3cret#pw\"x"`)
	assert.Contains(t, cnf, "host=127.0.0.1\n")
	assert.Contains(t, cnf, "port="+port+"\n")
	assert.Contains(t, cnf, "ssl-mode=PREFERRED\n")

	assert.Contains(t, lg.text(), "app.missing", "未匹配的排除规则写进日志")
	assert.NotContains(t, lg.text(), "app.logs")
	assert.Contains(t, lg.text(), "app.cache", "非 InnoDB 表写进日志")
	assert.NotContains(t, lg.text(), "s3cret")

	require.NoError(t, s.Close())
	assert.Empty(t, e.leftEntries(), "关闭后删除运行临时目录")
}

func TestMySQLTLS(t *testing.T) {
	ca, _ := testCert(t)
	cert, key := testCert(t)
	cases := []struct {
		mode     dsconn.TLSMode
		sslMode  string
		withFile bool
		note     bool
	}{
		{dsconn.TLSDisable, "DISABLED", false, false},
		{dsconn.TLSRequire, "REQUIRED", false, false},
		{dsconn.TLSVerifyCA, "VERIFY_CA", true, false},
		{dsconn.TLSVerifyFull, "VERIFY_CA", true, true},
	}
	for _, c := range cases {
		t.Run(string(c.mode), func(t *testing.T) {
			e := newFakeEnv(t)
			e.tool("mysqldump", oracleVersion, mysqlOK)
			(&fakeDB{answer: tablesOnly()}).install(t)
			src := mysqlSource()
			src.Config.TLS = dsconn.TLSConfig{Mode: c.mode, CA: ca, ClientCert: cert, ClientKey: key}
			var lg logs
			s, err := Start(context.Background(), e.base, src, Options{Databases: []string{"app"}, Log: lg.add})
			require.NoError(t, err)
			_, err = readAll(t, s)
			require.NoError(t, err)
			require.NoError(t, s.Close())

			r := e.runs("mysqldump")[0]
			cnf, _ := r.copied(t, r.arg("--defaults-file="))
			assert.Contains(t, cnf, "ssl-mode="+c.sslMode+"\n")
			if c.mode == dsconn.TLSDisable {
				assert.NotContains(t, cnf, "ssl-key")
				return
			}
			for k, want := range map[string][]byte{"ssl-cert": cert, "ssl-key": key} {
				path := optionValue(t, cnf, k)
				body, mode := r.copied(t, path)
				assert.Equal(t, string(want), body, k)
				assert.Equal(t, os.FileMode(0o600), mode, k)
			}
			if c.withFile {
				body, _ := r.copied(t, optionValue(t, cnf, "ssl-ca"))
				assert.Equal(t, string(ca), body)
			} else {
				assert.NotContains(t, cnf, "ssl-ca", "不校验证书的模式不给 CA，避免客户端改为校验")
			}
			assert.Equal(t, c.note, strings.Contains(lg.text(), "主机名"), lg.text())
			assert.Empty(t, e.leftEntries())
		})
	}
}

// optionValue 读取选项文件中 key 的值（去掉引号）
func optionValue(t *testing.T, cnf, key string) string {
	t.Helper()
	for _, l := range strings.Split(cnf, "\n") {
		if v, ok := strings.CutPrefix(l, key+"="); ok {
			return strings.Trim(v, "'\"")
		}
	}
	t.Fatalf("选项文件中没有 %s：\n%s", key, cnf)
	return ""
}

// TestMySQLColumnStatistics Oracle mysqldump 8.0 起一律加 --column-statistics=0：该参数只在 8.0 起的客户端上存在，
// 8.0 之前的服务端没有 information_schema.COLUMN_STATISTICS，8.0 客户端不加这个参数会在导出 5.7 服务端时报 1109 失败；
// 直方图不是恢复数据必须的内容，不区分服务端版本一律关闭更简单，也不必信赖服务端版本号的格式。MariaDB 客户端不认识该参数
func TestMySQLColumnStatistics(t *testing.T) {
	t.Run("Oracle 8.0 对 5.7 服务端加上该参数", func(t *testing.T) {
		e := newFakeEnv(t)
		e.tool("mysqldump", oracleVersion, mysqlOK)
		(&fakeDB{answer: tablesOnly()}).install(t)
		src := mysqlSource()
		src.ServerVersion = "5.7.44"
		s, err := Start(context.Background(), e.base, src, Options{Databases: []string{"app"}})
		require.NoError(t, err)
		_, err = readAll(t, s)
		require.NoError(t, err)
		require.NoError(t, s.Close())
		assert.Contains(t, e.runs("mysqldump")[0].argv, "--column-statistics=0")
	})

	t.Run("Oracle 8.0 对 8.0 服务端也一律加上", func(t *testing.T) {
		e := newFakeEnv(t)
		e.tool("mysqldump", oracleVersion, mysqlOK)
		(&fakeDB{answer: tablesOnly()}).install(t)
		s, err := Start(context.Background(), e.base, mysqlSource(), Options{Databases: []string{"app"}})
		require.NoError(t, err)
		_, err = readAll(t, s)
		require.NoError(t, err)
		require.NoError(t, s.Close())
		assert.Contains(t, e.runs("mysqldump")[0].argv, "--column-statistics=0")
	})

	t.Run("MariaDB 客户端不认识该参数，不加", func(t *testing.T) {
		e := newFakeEnv(t)
		e.tool("mysqldump", mariadbVersion, mysqlOK)
		(&fakeDB{answer: tablesOnly()}).install(t)
		s, err := Start(context.Background(), e.base, mysqlSource(), Options{Databases: []string{"app"}})
		require.NoError(t, err)
		_, err = readAll(t, s)
		require.NoError(t, err)
		require.NoError(t, s.Close())
		assert.NotContains(t, e.runs("mysqldump")[0].argv, "--column-statistics=0")
	})
}

func TestMySQLMariaDBClient(t *testing.T) {
	t.Run("按 MariaDB 的选项写法", func(t *testing.T) {
		e := newFakeEnv(t)
		e.tool("mysqldump", mariadbVersion, mysqlOK)
		(&fakeDB{answer: tablesOnly()}).install(t)
		s, err := Start(context.Background(), e.base, mysqlSource(), Options{Databases: []string{"app"}})
		require.NoError(t, err)
		_, err = readAll(t, s)
		require.NoError(t, err)
		require.NoError(t, s.Close())

		r := e.runs("mysqldump")[0]
		assert.NotContains(t, strings.Join(r.argv, " "), "gtid", "MariaDB 客户端不导出 GTID 清除语句，也不认识该选项")
		cnf, _ := r.copied(t, r.arg("--defaults-file="))
		assert.Contains(t, cnf, "\nssl\n")
		assert.NotContains(t, cnf, "ssl-mode")
	})

	t.Run("去掉 MySQL 官方客户端无法导入的沙箱行", func(t *testing.T) {
		e := newFakeEnv(t)
		e.tool("mysqldump", mariadbVersion, "printf '%s\\n' '/*M!999999\\- enable the sandbox mode */ ' '-- MariaDB dump 10.19' 'CREATE TABLE t (id int);' '-- Dump completed on 2026-09-28 10:00:00'")
		(&fakeDB{answer: tablesOnly()}).install(t)
		s, err := Start(context.Background(), e.base, mysqlSource(), Options{Databases: []string{"app"}})
		require.NoError(t, err)
		got, err := readAll(t, s)
		require.NoError(t, err)
		require.NoError(t, s.Close())
		assert.True(t, strings.HasPrefix(string(got[MySQLDatabasesFile]), "-- MariaDB dump"), "%q", got[MySQLDatabasesFile])
		assert.Equal(t, int64(len(got[MySQLDatabasesFile])), s.Files()[0].Bytes())
	})

	t.Run("无法保证的 TLS 模式直接失败", func(t *testing.T) {
		e := newFakeEnv(t)
		e.tool("mysqldump", mariadbVersion, mysqlOK)
		(&fakeDB{answer: tablesOnly()}).install(t)
		src := mysqlSource()
		src.Config.TLS.Mode = dsconn.TLSRequire
		_, err := Start(context.Background(), e.base, src, Options{Databases: []string{"app"}})
		require.ErrorIs(t, err, ErrUnsupportedTLS)
		assert.Empty(t, e.runs("mysqldump"))
		assert.Empty(t, e.leftEntries())
	})
}

func TestMySQLIncompleteDump(t *testing.T) {
	e := newFakeEnv(t)
	e.tool("mysqldump", oracleVersion, "printf '%s\\n' '-- MySQL dump' 'INSERT INTO t VALUES (1);'")
	(&fakeDB{answer: tablesOnly()}).install(t)
	s, err := Start(context.Background(), e.base, mysqlSource(), Options{Databases: []string{"app"}})
	require.NoError(t, err)
	_, err = readAll(t, s)
	require.ErrorIs(t, err, ErrIncomplete)
	require.NoError(t, s.Close())
	assert.Empty(t, e.leftEntries())
}

func TestToolFailureScrubsSecrets(t *testing.T) {
	e := newFakeEnv(t)
	e.tool("mysqldump", oracleVersion, "printf '%s\\n' 'partial'; "+secretLog)
	(&fakeDB{answer: tablesOnly()}).install(t)
	var lg logs
	s, err := Start(context.Background(), e.base, mysqlSource(), Options{Databases: []string{"app"}, Log: lg.add})
	require.NoError(t, err)
	_, err = readAll(t, s)
	require.Error(t, err)
	var te *ToolError
	require.ErrorAs(t, err, &te)
	assert.Equal(t, "mysqldump", te.Tool)
	assert.Equal(t, 2, te.ExitCode)
	assert.Contains(t, err.Error(), "Access denied")
	assert.Contains(t, err.Error(), "******")
	assert.NotContains(t, err.Error(), "s3cret")
	assert.NotContains(t, lg.text(), "s3cret")
	require.NoError(t, s.Close())
	assert.Empty(t, e.leftEntries())
}

func TestPostgresGlobalsPrivilege(t *testing.T) {
	e := newFakeEnv(t)
	e.tool("pg_dump", pgDumpVersion, pgDumpOK)
	e.tool("pg_dumpall", pgAllVersion, "printf '%s\\n' 'pg_dumpall: error: query failed: ERROR:  permission denied for table pg_authid' >&2; exit 1")
	s, err := Start(context.Background(), e.base, pgSource(), Options{Databases: []string{"app"}, Globals: true})
	require.NoError(t, err)
	_, err = readAll(t, s)
	require.ErrorIs(t, err, ErrPrivilege)
	assert.Contains(t, err.Error(), "超级用户")
	var te *ToolError
	require.ErrorAs(t, err, &te)
	assert.Equal(t, 1, te.ExitCode)
	require.NoError(t, s.Close())
}

// slowTool 输出一行后记录进程号并阻塞
func slowTool(e *fakeEnv) string {
	pidFile := filepath.Join(e.rec, "pid")
	e.tool("mysqldump", oracleVersion, "printf '%s\\n' 'first'; echo $$ > "+pidFile+"; exec sleep 30")
	return pidFile
}

func readPid(t *testing.T, pidFile string) int {
	t.Helper()
	var pid int
	require.Eventually(t, func() bool {
		b, err := os.ReadFile(pidFile) //nolint:gosec // 测试临时目录
		if err != nil {
			return false
		}
		pid, err = strconv.Atoi(strings.TrimSpace(string(b)))
		return err == nil
	}, 5*time.Second, 10*time.Millisecond)
	return pid
}

func assertExited(t *testing.T, pid int) {
	t.Helper()
	assert.Eventually(t, func() bool {
		return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
	}, 5*time.Second, 20*time.Millisecond, "导出工具进程应已结束")
}

func TestCancelKillsTool(t *testing.T) {
	e := newFakeEnv(t)
	pidFile := slowTool(e)
	(&fakeDB{answer: tablesOnly()}).install(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := Start(ctx, e.base, mysqlSource(), Options{Databases: []string{"app"}})
	require.NoError(t, err)
	f := s.Files()[0]
	buf := make([]byte, 64)
	n, err := f.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, "first\n", string(buf[:n]))
	pid := readPid(t, pidFile)

	cancel()
	done := make(chan error, 1)
	go func() { _, err := io.ReadAll(f); done <- err }()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(10 * time.Second):
		t.Fatal("取消后读取没有结束")
	}
	require.NoError(t, s.Close())
	assertExited(t, pid)
	assert.Empty(t, e.leftEntries())
}

func TestCloseKillsRunningTool(t *testing.T) {
	e := newFakeEnv(t)
	pidFile := slowTool(e)
	(&fakeDB{answer: tablesOnly()}).install(t)
	s, err := Start(context.Background(), e.base, mysqlSource(), Options{Databases: []string{"app"}})
	require.NoError(t, err)
	f := s.Files()[0]
	_, err = f.Read(make([]byte, 64))
	require.NoError(t, err)
	pid := readPid(t, pidFile)

	done := make(chan error, 1)
	go func() { _, err := io.ReadAll(f); done <- err }()
	require.NoError(t, s.Close())
	select {
	case err := <-done:
		require.Error(t, err, "关闭后读取不能以 EOF 结束，否则会被当作完整导出")
	case <-time.After(10 * time.Second):
		t.Fatal("关闭后读取没有结束")
	}
	assertExited(t, pid)
	assert.Empty(t, e.leftEntries())
	require.NoError(t, s.Close(), "Close 可重复调用")
}

func TestMySQLAccounts(t *testing.T) {
	answer := func(denied bool) fakeAnswer {
		return func(q string, _ []driver.Value) ([]string, [][]driver.Value, error) {
			one := func(col string, v string) ([]string, [][]driver.Value, error) {
				return []string{col}, [][]driver.Value{{v}}, nil
			}
			switch {
			case strings.Contains(q, "information_schema.TABLES"):
				return []string{"a", "b", "c", "d"}, nil, nil
			case q == "SELECT CURRENT_USER()":
				return one("u", "backup@%")
			case strings.HasPrefix(q, "SET SESSION print_identified_with_as_hex"):
				return nil, nil, nil
			case strings.Contains(q, "FROM mysql.user"):
				if denied {
					return nil, nil, &mysql.MySQLError{Number: 1142, Message: "SELECT command denied to user 'backup'@'10.0.0.1' for table 'user'"}
				}
				return []string{"User", "Host"}, [][]driver.Value{{"app", "%"}, {"r_read", "%"}}, nil
			case strings.Contains(q, "FROM mysql.role_edges"):
				return []string{"u", "h"}, [][]driver.Value{{"r_read", "%"}}, nil
			case q == "SHOW CREATE USER `app`@`%`":
				return one("c", "CREATE USER `app`@`%` IDENTIFIED WITH 'caching_sha2_password' AS 0x24 DEFAULT ROLE `r_read`@`%`")
			case q == "SHOW CREATE USER `r_read`@`%`":
				return one("c", "CREATE USER `r_read`@`%` ACCOUNT LOCK")
			case q == "SHOW GRANTS FOR `app`@`%`":
				return []string{"g"}, [][]driver.Value{{"GRANT USAGE ON *.* TO `app`@`%`"}, {"GRANT `r_read`@`%` TO `app`@`%`"}}, nil
			case q == "SHOW GRANTS FOR `r_read`@`%`":
				return one("g", "GRANT SELECT ON `app`.* TO `r_read`@`%`")
			}
			return nil, nil, fmt.Errorf("意外的查询 %q", q)
		}
	}

	t.Run("角色在前，先建账号再授权", func(t *testing.T) {
		e := newFakeEnv(t)
		e.tool("mysqldump", oracleVersion, mysqlOK)
		db := &fakeDB{answer: answer(false)}
		db.install(t)
		s, err := Start(context.Background(), e.base, mysqlSource(), Options{Databases: []string{"app"}, Accounts: true})
		require.NoError(t, err)
		got, err := readAll(t, s)
		require.NoError(t, err)
		require.NoError(t, s.Close())

		names := make([]string, 0, len(s.Files()))
		for _, f := range s.Files() {
			names = append(names, f.Name)
		}
		assert.Equal(t, []string{"databases.sql", "accounts.sql"}, names)
		acc := string(got["accounts.sql"])
		assert.Equal(t, int64(len(acc)), s.Files()[1].Bytes())
		order := []string{
			"CREATE USER IF NOT EXISTS `r_read`@`%` ACCOUNT LOCK;\n",
			"CREATE USER IF NOT EXISTS `app`@`%` IDENTIFIED WITH 'caching_sha2_password' AS 0x24 DEFAULT ROLE `r_read`@`%`;\n",
			"GRANT SELECT ON `app`.* TO `r_read`@`%`;\n",
			"GRANT USAGE ON *.* TO `app`@`%`;\n",
			"GRANT `r_read`@`%` TO `app`@`%`;\n",
		}
		last := -1
		for _, stmt := range order {
			i := strings.Index(acc, stmt)
			require.GreaterOrEqual(t, i, 0, "缺少 %q：\n%s", stmt, acc)
			assert.Greater(t, i, last, "顺序错误：%q", stmt)
			last = i
		}
		assert.Contains(t, db.seen(), "SET SESSION print_identified_with_as_hex = ON")
	})

	t.Run("没有读取权限时失败并说明需要的权限", func(t *testing.T) {
		e := newFakeEnv(t)
		e.tool("mysqldump", oracleVersion, mysqlOK)
		(&fakeDB{answer: answer(true)}).install(t)
		_, err := Start(context.Background(), e.base, mysqlSource(), Options{Databases: []string{"app"}, Accounts: true})
		require.ErrorIs(t, err, ErrPrivilege)
		assert.Contains(t, err.Error(), "GRANT SELECT ON mysql.* TO 'backup'@'%'")
		assert.Empty(t, e.runs("mysqldump"))
		assert.Empty(t, e.leftEntries())
	})
}

func pgExclusionAnswer(q string, args []driver.Value) ([]string, [][]driver.Value, error) {
	if !strings.Contains(q, "pg_class") {
		return nil, nil, fmt.Errorf("意外的查询 %q", q)
	}
	n := int64(0)
	if len(args) == 2 && args[0] == "public" && args[1] == "logs" {
		n = 1
	}
	return []string{"count"}, [][]driver.Value{{n}}, nil
}

func TestPostgresExport(t *testing.T) {
	e := newFakeEnv(t)
	e.tool("pg_dump", pgDumpVersion, pgDumpOK)
	e.tool("pg_dumpall", pgAllVersion, pgAllOK)
	db := &fakeDB{answer: pgExclusionAnswer}
	db.install(t)
	var lg logs

	s, err := Start(context.Background(), e.base, pgSource(), Options{
		Databases: []string{"app", "we/ird"}, Globals: true,
		ExcludeTables: []string{"app.public.logs", "app.public.nope", "other.public.x"}, Log: lg.add,
	})
	require.NoError(t, err)
	port := forwardPort(t, s)
	got, err := readAll(t, s)
	require.NoError(t, err)
	require.NoError(t, s.Close())
	assert.Empty(t, e.leftEntries())

	names := make([]string, 0, len(s.Files()))
	for _, f := range s.Files() {
		names = append(names, f.Name)
		assert.Equal(t, int64(len(got[f.Name])), f.Bytes(), f.Name)
	}
	assert.Equal(t, []string{"globals.sql", "app.dump", "we%2Fird.dump"}, names)
	assert.True(t, strings.HasPrefix(string(got["app.dump"]), "PGDMP"))
	assert.Contains(t, string(got["globals.sql"]), "CREATE ROLE app;")

	dumps := e.runs("pg_dump")
	require.Len(t, dumps, 2)
	byDB := map[string]fakeRun{}
	for _, r := range dumps {
		assertNoSecret(t, r)
		conn := r.arg("--dbname=")
		for _, want := range []string{"host='pg.example'", "hostaddr='127.0.0.1'", "port='" + port + "'", "user='backup'", "sslmode='prefer'"} {
			assert.Contains(t, conn, want)
		}
		for _, a := range []string{"--format=custom", "--compress=0", "--no-password"} {
			assert.Contains(t, r.argv, a)
		}
		pass, mode := r.copied(t, r.env["PGPASSFILE"])
		assert.Equal(t, os.FileMode(0o600), mode)
		assert.Equal(t, "*:*:*:*:s3cret#pw\"x\n", pass)
		switch {
		case strings.Contains(conn, "dbname='app'"):
			byDB["app"] = r
		case strings.Contains(conn, "dbname='we/ird'"):
			byDB["we/ird"] = r
		}
	}
	require.Len(t, byDB, 2)
	assert.Contains(t, byDB["app"].argv, `--exclude-table="public"."logs"`)
	assert.Contains(t, byDB["app"].argv, `--exclude-table="public"."nope"`)
	assert.NotContains(t, strings.Join(byDB["we/ird"].argv, " "), "--exclude-table")

	all := e.runs("pg_dumpall")
	require.Len(t, all, 1)
	assertNoSecret(t, all[0])
	assert.Contains(t, all[0].argv, "--globals-only")
	assert.Contains(t, all[0].argv, "--database=postgres")
	assert.Contains(t, all[0].arg("--dbname="), "hostaddr='127.0.0.1'")

	assert.Contains(t, lg.text(), "app.public.nope")
	assert.Contains(t, lg.text(), "other.public.x")
	assert.NotContains(t, lg.text(), "app.public.logs")
	assert.Equal(t, []string{"app"}, db.dbs, "只为有排除规则的库打开连接")
}

func TestPostgresTLS(t *testing.T) {
	ca, _ := testCert(t)
	cert, key := testCert(t)
	e := newFakeEnv(t)
	e.tool("pg_dump", pgDumpVersion, pgDumpOK)
	src := pgSource()
	src.Config.TLS = dsconn.TLSConfig{Mode: dsconn.TLSVerifyFull, CA: ca, ClientCert: cert, ClientKey: key}
	s, err := Start(context.Background(), e.base, src, Options{Databases: []string{"app"}})
	require.NoError(t, err)
	_, err = readAll(t, s)
	require.NoError(t, err)
	require.NoError(t, s.Close())

	r := e.runs("pg_dump")[0]
	conn := r.arg("--dbname=")
	assert.Contains(t, conn, "sslmode='verify-full'")
	assert.Contains(t, conn, "host='pg.example'", "verify-full 按数据源主机名校验，连接走 hostaddr")
	for k, want := range map[string][]byte{"sslrootcert": ca, "sslcert": cert, "sslkey": key} {
		path := connValue(t, conn, k)
		body, mode := r.copied(t, path)
		assert.Equal(t, string(want), body, k)
		assert.Equal(t, os.FileMode(0o600), mode, k)
	}
}

func connValue(t *testing.T, conn, key string) string {
	t.Helper()
	_, rest, ok := strings.Cut(conn, key+"='")
	require.True(t, ok, "连接串中没有 %s：%s", key, conn)
	v, _, _ := strings.Cut(rest, "'")
	return v
}

func TestPostgresIncomplete(t *testing.T) {
	cases := map[string][2]string{
		"归档头不对":     {"printf 'not an archive'", pgAllOK},
		"全局对象缺完成标记": {pgDumpOK, "printf '%s\\n' 'CREATE ROLE app;'"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e := newFakeEnv(t)
			e.tool("pg_dump", pgDumpVersion, c[0])
			e.tool("pg_dumpall", pgAllVersion, c[1])
			s, err := Start(context.Background(), e.base, pgSource(), Options{Databases: []string{"app"}, Globals: true})
			require.NoError(t, err)
			_, err = readAll(t, s)
			require.ErrorIs(t, err, ErrIncomplete)
			require.NoError(t, s.Close())
		})
	}
}

func TestToolChecks(t *testing.T) {
	t.Run("pg_dump 大版本低于服务端时失败", func(t *testing.T) {
		e := newFakeEnv(t)
		e.tool("pg_dump", "pg_dump (PostgreSQL) 15.2", pgDumpOK)
		_, err := Start(context.Background(), e.base, pgSource(), Options{Databases: []string{"app"}})
		require.ErrorIs(t, err, ErrToolVersion)
		assert.Contains(t, err.Error(), "15")
		assert.Contains(t, err.Error(), "16")
		assert.Empty(t, e.leftEntries())
	})

	t.Run("pg_dumpall 大版本低于服务端时失败", func(t *testing.T) {
		e := newFakeEnv(t)
		e.tool("pg_dump", pgDumpVersion, pgDumpOK)
		e.tool("pg_dumpall", "pg_dumpall (PostgreSQL) 14.1", pgAllOK)
		_, err := Start(context.Background(), e.base, pgSource(), Options{Databases: []string{"app"}, Globals: true})
		require.ErrorIs(t, err, ErrToolVersion)
		assert.Contains(t, err.Error(), "pg_dumpall")
	})

	t.Run("mysqldump 版本低于服务端时照常执行并提示", func(t *testing.T) {
		e := newFakeEnv(t)
		e.tool("mysqldump", "mysqldump  Ver 8.0.30 for Linux", mysqlOK)
		(&fakeDB{answer: tablesOnly()}).install(t)
		src := mysqlSource()
		src.ServerVersion = "8.4.2"
		var lg logs
		s, err := Start(context.Background(), e.base, src, Options{Databases: []string{"app"}, Log: lg.add})
		require.NoError(t, err)
		require.NoError(t, s.Close())
		assert.Contains(t, lg.text(), "8.0")
		assert.Contains(t, lg.text(), "8.4")
	})

	t.Run("MariaDB 的 mysqldump 低于 MariaDB 服务端时照常执行并提示", func(t *testing.T) {
		e := newFakeEnv(t)
		e.tool("mysqldump", mariadbVersion, mysqlOK)
		(&fakeDB{answer: tablesOnly()}).install(t)
		src := mysqlSource()
		src.ServerVersion = "11.4.2-MariaDB-ubu2404"
		var lg logs
		s, err := Start(context.Background(), e.base, src, Options{Databases: []string{"app"}, Log: lg.add})
		require.NoError(t, err)
		require.NoError(t, s.Close())
		assert.Contains(t, lg.text(), "10.11")
		assert.Contains(t, lg.text(), "11.4")
	})

	t.Run("找不到工具", func(t *testing.T) {
		e := newFakeEnv(t)
		e.tool("pg_dump", pgDumpVersion, pgDumpOK)
		_, err := Start(context.Background(), e.base, pgSource(), Options{Databases: []string{"app"}, Globals: true})
		require.ErrorIs(t, err, ErrToolNotFound)
		assert.Contains(t, err.Error(), "pg_dumpall")
		assert.Contains(t, err.Error(), "tools.dir")
		assert.Empty(t, e.leftEntries())
	})
}

func TestInvalidOptions(t *testing.T) {
	e := newFakeEnv(t)
	e.tool("mysqldump", oracleVersion, mysqlOK)
	e.tool("pg_dump", pgDumpVersion, pgDumpOK)
	cases := map[string]struct {
		src  Source
		opts Options
		want string
	}{
		"没有库":           {mysqlSource(), Options{}, ""},
		"MySQL 排除表格式":   {mysqlSource(), Options{Databases: []string{"app"}, ExcludeTables: []string{"app.public.t"}}, "app.public.t"},
		"PG 排除表格式":      {pgSource(), Options{Databases: []string{"app"}, ExcludeTables: []string{"app.t"}}, "app.t"},
		"不支持的类型":        {Source{Dialer: noDialer{}, Config: dsconn.Config{Type: dsconn.TypeServerFile, Host: "h", Port: 22, User: "u", Password: "p"}}, Options{Databases: []string{"app"}}, ""},
		"MySQL 不支持全局对象": {mysqlSource(), Options{Databases: []string{"app"}, Globals: true}, ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Start(context.Background(), e.base, c.src, c.opts)
			require.ErrorIs(t, err, ErrInvalidOptions)
			assert.Contains(t, err.Error(), c.want)
			assert.Empty(t, e.runs("mysqldump"))
			assert.Empty(t, e.leftEntries())
		})
	}
}

func TestValidateExcludeTable(t *testing.T) {
	ok := map[dsconn.Type][]string{
		dsconn.TypeMySQL:      {"app.logs", "a-b.t_1"},
		dsconn.TypePostgreSQL: {"app.public.logs", "app.Sales.Orders"},
	}
	bad := map[dsconn.Type][]string{
		dsconn.TypeMySQL:      {"", "app", "app.", ".t", "a.b.c", " app.t", "app.t "},
		dsconn.TypePostgreSQL: {"app.t", "app..t", "a.b.c.d", ".public.t"},
	}
	for typ, xs := range ok {
		for _, x := range xs {
			assert.NoError(t, ValidateExcludeTable(typ, x), "%s %q", typ, x)
		}
	}
	for typ, xs := range bad {
		for _, x := range xs {
			assert.ErrorIs(t, ValidateExcludeTable(typ, x), ErrInvalidOptions, "%s %q", typ, x)
		}
	}
}

func TestSweep(t *testing.T) {
	base := t.TempDir()
	for _, d := range []string{"run-1", "run-abc"} {
		require.NoError(t, os.MkdirAll(filepath.Join(base, d, "sub"), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(base, d, "my.cnf"), []byte("password=x"), 0o600))
	}
	require.NoError(t, os.WriteFile(filepath.Join(base, "keep.txt"), nil, 0o600))

	require.NoError(t, Sweep(base))
	ents, err := os.ReadDir(base)
	require.NoError(t, err)
	require.Len(t, ents, 1)
	assert.Equal(t, "keep.txt", ents[0].Name())

	assert.NoError(t, Sweep(filepath.Join(base, "missing")), "目录不存在不算错误")
}

func TestPrivilegeMessages(t *testing.T) {
	privilege := []string{
		"mysqldump: Error: 'Access denied; you need (at least one of) the PROCESS privilege(s) for this operation'",
		"mysqldump: Couldn't execute 'SHOW EVENTS': SELECT command denied to user 'b'@'%' for table 'event' (1142)",
		"mysqldump: Got error: 1044: \"Access denied for user 'b'@'%' to database 'app'\" when selecting the database",
		"mysqldump: Got error: 1044: Access denied for user 'b'@'%' to database 'app' when using LOCK TABLES",
		"pg_dump: error: query failed: ERROR:  permission denied for table secrets",
	}
	other := []string{
		"mysqldump: Got error: 1045: \"Access denied for user 'b'@'10.0.0.1' (using password: YES)\" when trying to connect",
		"pg_dump: error: connection to server failed: FATAL:  password authentication failed for user \"b\"",
		"mysqldump: Got error: 1049: \"Unknown database 'nope'\" when selecting the database",
	}
	for _, m := range privilege {
		assert.True(t, isPrivilegeMessage(m), m)
	}
	for _, m := range other {
		assert.False(t, isPrivilegeMessage(m), m)
	}
}

// 导出工具的错误输出并入运行日志：超过保留上限时注明省略了前面的内容，不留下被截断的半行
func TestToolStderrMergedIntoLog(t *testing.T) {
	e := newFakeEnv(t)
	e.tool("mysqldump", oracleVersion, "i=0; while [ $i -lt 3000 ]; do echo warning-line-$i >&2; i=$((i+1)); done; "+mysqlOK)
	(&fakeDB{answer: tablesOnly()}).install(t)
	var lg logs
	s, err := Start(context.Background(), e.base, mysqlSource(), Options{Databases: []string{"app"}, Log: lg.add})
	require.NoError(t, err)
	_, err = readAll(t, s)
	require.NoError(t, err)
	require.NoError(t, s.Close())

	text := lg.text()
	assert.Contains(t, text, "mysqldump: warning-line-2999", "保留最后的错误输出")
	assert.Contains(t, text, "省略", "注明省略了前面的错误输出")
	for _, line := range strings.Split(text, "\n") {
		msg, ok := strings.CutPrefix(line, "mysqldump: ")
		if !ok || strings.Contains(msg, "省略") {
			continue
		}
		assert.Regexp(t, `^warning-line-\d+$`, msg, "没有被截断的半行")
	}
}

// 库名以 - 开头时不能被 mysqldump 当成选项：“整个实例”的库名来自服务端，能建库的账号可以建
// 名为 --host=... 或 --result-file=... 的库，让导出连到别处（带着选项文件中的密码）或覆盖本机文件
func TestMySQLDatabaseNamesAreNotOptions(t *testing.T) {
	e := newFakeEnv(t)
	e.tool("mysqldump", oracleVersion, mysqlOK)
	(&fakeDB{answer: tablesOnly()}).install(t)
	s, err := Start(context.Background(), e.base, mysqlSource(), Options{Databases: []string{"app", "--host=evil.example"}})
	require.NoError(t, err)
	_, err = readAll(t, s)
	require.NoError(t, err)
	require.NoError(t, s.Close())
	runs := e.runs("mysqldump")
	require.Len(t, runs, 1)
	argv := runs[0].argv
	assert.Equal(t, []string{"--databases", "--", "app", "--host=evil.example"}, argv[len(argv)-4:],
		"-- 之后的参数只作为库名")
}

// 任何密码都能写进 mysqldump 的选项文件：同时含单引号、双引号、# 与反斜杠的密码也不例外
// （MySQL / MariaDB 的选项文件在双引号内识别 \" 与 \\ 转义，# 在引号内不是注释）
func TestMySQLOptionFileQuotesAnyPassword(t *testing.T) {
	e := newFakeEnv(t)
	e.tool("mysqldump", oracleVersion, mysqlOK)
	(&fakeDB{answer: tablesOnly()}).install(t)
	src := mysqlSource()
	src.Config.Password = `s3cret'"#\pw`
	s, err := Start(context.Background(), e.base, src, Options{Databases: []string{"app"}})
	require.NoError(t, err, "密码可以写进选项文件")
	_, err = readAll(t, s)
	require.NoError(t, err)
	require.NoError(t, s.Close())
	runs := e.runs("mysqldump")
	require.Len(t, runs, 1)
	cnf, _ := runs[0].copied(t, runs[0].arg("--defaults-file="))
	assert.Contains(t, cnf, `password="s3cret'\"#\\pw"`+"\n")
}

// pg 密码文件一行一条，无法表示含换行的密码：明确拒绝，而不是写出一个认证必然失败的密码文件
func TestPostgresPasswordWithNewline(t *testing.T) {
	e := newFakeEnv(t)
	e.tool("pg_dump", pgDumpVersion, pgDumpOK)
	src := pgSource()
	src.Config.Password = "s3cret\npw"
	_, err := Start(context.Background(), e.base, src, Options{Databases: []string{"app"}})
	require.ErrorIs(t, err, ErrInvalidOptions)
	assert.NotContains(t, err.Error(), "s3cret")
	assert.Empty(t, e.leftEntries(), "失败时不留下临时文件")
	assert.Empty(t, e.runs("pg_dump"))
}

// 每个工具的输出读完后即关闭管道：“整个实例”有很多库时，不会在整次导出期间一库占一个文件描述符
func TestToolOutputClosedAfterEOF(t *testing.T) {
	e := newFakeEnv(t)
	e.tool("pg_dump", pgDumpVersion, pgDumpOK)
	const n = 40
	dbs := make([]string, n)
	for i := range dbs {
		dbs[i] = "db" + strconv.Itoa(i)
	}
	before := openFDs(t)
	s, err := Start(context.Background(), e.base, pgSource(), Options{Databases: dbs})
	require.NoError(t, err)
	defer func() { _ = s.Close() }()
	_, err = readAll(t, s)
	require.NoError(t, err)
	assert.Less(t, openFDs(t)-before, n/2, "读完的工具输出已关闭")
}

func openFDs(t *testing.T) int {
	t.Helper()
	ents, err := os.ReadDir("/dev/fd")
	if err != nil {
		t.Skip("没有 /dev/fd")
	}
	return len(ents)
}

// 导出工具的环境不继承 OpsNap 的数据库变量，但保留动态库搜索路径：放在 tools.dir 中、靠
// LD_LIBRARY_PATH 找到自带库的客户端，在探测时能运行，导出时也要能运行
func TestToolEnvKeepsLibraryPath(t *testing.T) {
	e := newFakeEnv(t)
	e.tool("pg_dump", pgDumpVersion, pgDumpOK)
	t.Setenv("LD_LIBRARY_PATH", "/opt/pgclient/lib")
	t.Setenv("PGPASSWORD", "from-env")
	s, err := Start(context.Background(), e.base, pgSource(), Options{Databases: []string{"app"}})
	require.NoError(t, err)
	_, err = readAll(t, s)
	require.NoError(t, err)
	require.NoError(t, s.Close())
	runs := e.runs("pg_dump")
	require.Len(t, runs, 1)
	assert.Equal(t, "/opt/pgclient/lib", runs[0].env["LD_LIBRARY_PATH"])
	assert.NotContains(t, runs[0].env, "PGPASSWORD")
}
