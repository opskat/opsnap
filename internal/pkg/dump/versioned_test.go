package dump

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opsnap/internal/pkg/probe"
)

// useToolsDir 设置 tools.dir，测试结束后恢复
func useToolsDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	probe.SetToolsDir(dir)
	t.Cleanup(func() { probe.SetToolsDir("") })
	return dir
}

// 导出按服务端版本从 tools.dir 的版本子目录中选用工具（优先于 PATH），选不低于服务端的最低版本
func TestExportUsesVersionedTools(t *testing.T) {
	t.Run("MySQL", func(t *testing.T) {
		e := newFakeEnv(t)
		e.tool("mysqldump", oracleVersion, mysqlOK)
		dir := useToolsDir(t)
		e.versionedTool(dir, "mysql-8.0", "mysqldump", "mysqldump  Ver 8.0.43 for Linux", mysqlOK)
		e.versionedTool(dir, "mysql-8.4", "mysqldump", "mysqldump  Ver 8.4.6 for Linux", mysqlOK)
		e.versionedTool(dir, "mysql-9.4", "mysqldump", "mysqldump  Ver 9.4.0 for Linux", mysqlOK)
		(&fakeDB{answer: tablesOnly()}).install(t)
		src := mysqlSource()
		src.ServerVersion = "8.4.2"
		var lg logs
		s, err := Start(context.Background(), e.base, src, Options{Databases: []string{"app"}, Log: lg.add})
		require.NoError(t, err)
		_, err = readAll(t, s)
		require.NoError(t, err)
		require.NoError(t, s.Close())

		assert.Len(t, e.runs("mysql-8.4-mysqldump"), 1)
		assert.Empty(t, e.runs("mysqldump"), "版本子目录优先于 PATH")
		assert.Empty(t, e.runs("mysql-8.0-mysqldump"))
		assert.Empty(t, e.runs("mysql-9.4-mysqldump"))
		assert.NotContains(t, lg.text(), "低于服务端")
	})

	t.Run("PostgreSQL", func(t *testing.T) {
		e := newFakeEnv(t)
		dir := useToolsDir(t)
		for _, v := range []string{"14", "16", "17"} {
			e.versionedTool(dir, "postgresql-"+v, "pg_dump", "pg_dump (PostgreSQL) "+v+".5", pgDumpOK)
			e.versionedTool(dir, "postgresql-"+v, "pg_dumpall", "pg_dumpall (PostgreSQL) "+v+".5", pgAllOK)
		}
		s, err := Start(context.Background(), e.base, pgSource(), Options{Databases: []string{"app"}, Globals: true})
		require.NoError(t, err)
		_, err = readAll(t, s)
		require.NoError(t, err)
		require.NoError(t, s.Close())

		assert.Len(t, e.runs("postgresql-16-pg_dump"), 1)
		assert.Len(t, e.runs("postgresql-16-pg_dumpall"), 1)
		assert.Empty(t, e.runs("postgresql-17-pg_dump"))
		assert.Empty(t, e.runs("postgresql-14-pg_dump"))
	})
}

// 没有不低于服务端的版本时选最高版本，并按现有规则处理：MySQL 照常导出并提示，PostgreSQL 失败
func TestExportVersionedToolsAllOlder(t *testing.T) {
	t.Run("MySQL 照常导出并提示", func(t *testing.T) {
		e := newFakeEnv(t)
		dir := useToolsDir(t)
		e.versionedTool(dir, "mysql-8.0", "mysqldump", "mysqldump  Ver 8.0.43 for Linux", mysqlOK)
		e.versionedTool(dir, "mysql-8.4", "mysqldump", "mysqldump  Ver 8.4.6 for Linux", mysqlOK)
		(&fakeDB{answer: tablesOnly()}).install(t)
		src := mysqlSource()
		src.ServerVersion = "9.4.0"
		var lg logs
		s, err := Start(context.Background(), e.base, src, Options{Databases: []string{"app"}, Log: lg.add})
		require.NoError(t, err)
		_, err = readAll(t, s)
		require.NoError(t, err)
		require.NoError(t, s.Close())
		assert.Len(t, e.runs("mysql-8.4-mysqldump"), 1)
		assert.Contains(t, lg.text(), "mysqldump 版本 8.4 低于服务端 9.4")
	})

	t.Run("PostgreSQL 失败", func(t *testing.T) {
		e := newFakeEnv(t)
		dir := useToolsDir(t)
		e.versionedTool(dir, "postgresql-16", "pg_dump", "pg_dump (PostgreSQL) 16.10", pgDumpOK)
		e.versionedTool(dir, "postgresql-17", "pg_dump", "pg_dump (PostgreSQL) 17.6", pgDumpOK)
		src := pgSource()
		src.ServerVersion = "18.0"
		_, err := Start(context.Background(), e.base, src, Options{Databases: []string{"app"}})
		require.ErrorIs(t, err, ErrToolVersion)
		assert.Contains(t, err.Error(), "大版本 17 低于服务端 18")
		assert.Empty(t, e.leftEntries())
	})
}

// MariaDB 服务端用 mariadb/ 子目录中的 mariadb-dump，按 MariaDB 客户端的写法导出；运行日志与错误中的工具名为实际的可执行文件名
func TestExportMariaDBDir(t *testing.T) {
	e := newFakeEnv(t)
	dir := useToolsDir(t)
	e.versionedTool(dir, "mysql-8.4", "mysqldump", "mysqldump  Ver 8.4.6 for Linux", mysqlOK)
	e.versionedTool(dir, "mariadb", "mariadb-dump", "mariadb-dump from 11.8.3-MariaDB, client 10.19 for debian-linux-gnu (x86_64)", mysqlOK)
	(&fakeDB{answer: tablesOnly()}).install(t)
	src := mysqlSource()
	src.ServerVersion = "11.4.2-MariaDB-ubu2404"
	s, err := Start(context.Background(), e.base, src, Options{Databases: []string{"app"}})
	require.NoError(t, err)
	_, err = readAll(t, s)
	require.NoError(t, err)
	require.NoError(t, s.Close())

	runs := e.runs("mariadb-mariadb-dump")
	require.Len(t, runs, 1)
	assert.Empty(t, e.runs("mysql-8.4-mysqldump"))
	assert.NotContains(t, strings.Join(runs[0].argv, " "), "gtid", "MariaDB 客户端没有 --set-gtid-purged")
}

// Session.Tools 给出实际选用的工具（路径与版本），运行日志据此写出“导出工具”行
func TestSessionToolsReportsChosenTools(t *testing.T) {
	e := newFakeEnv(t)
	dir := useToolsDir(t)
	e.versionedTool(dir, "postgresql-16", "pg_dump", "pg_dump (PostgreSQL) 16.10", pgDumpOK)
	e.versionedTool(dir, "postgresql-16", "pg_dumpall", "pg_dumpall (PostgreSQL) 16.10", pgAllOK)
	e.versionedTool(dir, "postgresql-17", "pg_dump", "pg_dump (PostgreSQL) 17.6", pgDumpOK)

	s, err := Start(context.Background(), e.base, pgSource(), Options{Databases: []string{"app"}, Globals: true})
	require.NoError(t, err)
	defer func() { require.NoError(t, s.Close()) }()
	tools := s.Tools()
	require.Len(t, tools, 2)
	assert.Equal(t, "pg_dump", tools[0].Name)
	assert.Equal(t, filepath.Join(dir, "postgresql-16", "bin", "pg_dump"), tools[0].Path)
	assert.Equal(t, "16.10", tools[0].Version)
	assert.Equal(t, "pg_dumpall", tools[1].Name)
	assert.Equal(t, filepath.Join(dir, "postgresql-16", "bin", "pg_dumpall"), tools[1].Path)

	// 没有版本子目录时与原来相同：在 PATH 中找到
	probe.SetToolsDir("")
	e.tool("mysqldump", oracleVersion, mysqlOK)
	(&fakeDB{answer: tablesOnly()}).install(t)
	my, err := Start(context.Background(), e.base, mysqlSource(), Options{Databases: []string{"app"}})
	require.NoError(t, err)
	defer func() { require.NoError(t, my.Close()) }()
	require.Len(t, my.Tools(), 1)
	assert.Equal(t, filepath.Join(e.bin, "mysqldump"), my.Tools()[0].Path)
	assert.Equal(t, "8.0.40", my.Tools()[0].Version)
}
