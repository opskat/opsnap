package probe

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeFakeTool 在 dir 下写一个可执行脚本，执行 --version 时打印 output 并以 0 退出
func writeFakeTool(t *testing.T, dir, name, output string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("主控端工具查找假设类 Unix 环境，与部署目标一致")
	}
	path := filepath.Join(dir, name)
	script := "#!/bin/sh\necho '" + output + "'\n"
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755)) //nolint:gosec // 测试用假可执行文件
	return path
}

func TestToolPathFindsInPATH(t *testing.T) {
	dir := t.TempDir()
	writeFakeTool(t, dir, "mysqldump", "mysqldump  Ver 8.0.40 for Linux")
	t.Setenv("PATH", dir)
	SetToolsDir("")

	path, ok := ToolPath("mysqldump")
	require.True(t, ok)
	assert.Equal(t, filepath.Join(dir, "mysqldump"), path)
}

func TestToolPathFallsBackToToolsDir(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // PATH 中什么都没有
	toolsDirPath := t.TempDir()
	writeFakeTool(t, toolsDirPath, "pg_dump", "pg_dump (PostgreSQL) 16.4")
	SetToolsDir(toolsDirPath)
	t.Cleanup(func() { SetToolsDir("") })

	path, ok := ToolPath("pg_dump")
	require.True(t, ok)
	assert.Equal(t, filepath.Join(toolsDirPath, "pg_dump"), path)
}

func TestToolPathPATHTakesPrecedenceOverToolsDir(t *testing.T) {
	pathDir := t.TempDir()
	writeFakeTool(t, pathDir, "mysqldump", "mysqldump  Ver 8.0.40")
	toolsDirPath := t.TempDir()
	writeFakeTool(t, toolsDirPath, "mysqldump", "mysqldump  Ver 8.0.1")
	t.Setenv("PATH", pathDir)
	SetToolsDir(toolsDirPath)
	t.Cleanup(func() { SetToolsDir("") })

	path, ok := ToolPath("mysqldump")
	require.True(t, ok)
	assert.Equal(t, filepath.Join(pathDir, "mysqldump"), path)
}

func TestToolPathNotFound(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	SetToolsDir("")

	_, ok := ToolPath("mysqldump")
	assert.False(t, ok)
}

func TestToolPathToolsDirUnconfigured(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	SetToolsDir("")

	_, ok := ToolPath("pg_dump")
	assert.False(t, ok)
}

func TestToolVersionParsesLeadingVersion(t *testing.T) {
	dir := t.TempDir()
	path := writeFakeTool(t, dir, "mysqldump", "mysqldump  Ver 8.0.40 for Linux on x86_64")

	major, minor, raw, err := ToolVersion(context.Background(), path)
	require.NoError(t, err)
	assert.Equal(t, 8, major)
	assert.Equal(t, 0, minor)
	assert.Contains(t, raw, "8.0.40")
}

// MySQL 5.7 及更早的 mysqldump 先打印工具自身的版本号（Ver 10.13），客户端版本在 Distrib 之后
func TestToolVersionPrefersDistrib(t *testing.T) {
	dir := t.TempDir()
	path := writeFakeTool(t, dir, "mysqldump", "mysqldump  Ver 10.13 Distrib 5.7.44, for Linux (x86_64)")

	major, minor, _, err := ToolVersion(context.Background(), path)
	require.NoError(t, err)
	assert.Equal(t, 5, major)
	assert.Equal(t, 7, minor)
}

func TestToolVersionUnparseable(t *testing.T) {
	dir := t.TempDir()
	path := writeFakeTool(t, dir, "mysqldump", "not a version string")

	_, _, _, err := ToolVersion(context.Background(), path)
	assert.Error(t, err)
}

// writeVersionedTool 在 tools.dir 的版本子目录 sub 的 bin/ 下写一个假工具
func writeVersionedTool(t *testing.T, toolsDir, sub, name, output string) string {
	t.Helper()
	bin := filepath.Join(toolsDir, sub, "bin")
	require.NoError(t, os.MkdirAll(bin, 0o755)) //nolint:gosec // 测试临时目录
	return writeFakeTool(t, bin, name, output)
}

// useToolsDir 设置测试用的 PATH 与 tools.dir，测试结束后恢复
func useToolsDir(t *testing.T, pathDir, toolsDir string) {
	t.Helper()
	t.Setenv("PATH", pathDir)
	SetToolsDir(toolsDir)
	t.Cleanup(func() { SetToolsDir("") })
}

// MySQL 的版本子目录：选不低于服务端（按主次版本）的最低版本；版本子目录优先于 PATH；
// 名称不合约定、bin/ 下没有该工具的子目录不参与选择
func TestResolveToolMySQLVersionedDirs(t *testing.T) {
	pathDir := t.TempDir()
	writeFakeTool(t, pathDir, "mysqldump", "mysqldump  Ver 9.9.9 for Linux")
	toolsDir := t.TempDir()
	writeVersionedTool(t, toolsDir, "mysql-8.0", "mysqldump", "mysqldump  Ver 8.0.43 for Linux on x86_64")
	writeVersionedTool(t, toolsDir, "mysql-8.4", "mysqldump", "mysqldump  Ver 8.4.6 for Linux on x86_64")
	writeVersionedTool(t, toolsDir, "mysql-9.4", "mysqldump", "mysqldump  Ver 9.4.0 for Linux on x86_64")
	writeVersionedTool(t, toolsDir, "mysql-8", "mysqldump", "mysqldump  Ver 8.1.0")
	writeVersionedTool(t, toolsDir, "mysql-8.1-old", "mysqldump", "mysqldump  Ver 8.1.0")
	require.NoError(t, os.MkdirAll(filepath.Join(toolsDir, "mysql-8.2", "bin"), 0o755)) //nolint:gosec // 测试临时目录
	useToolsDir(t, pathDir, toolsDir)

	cases := []struct {
		server, dir, version string
	}{
		{"8.0.36", "mysql-8.0", "8.0.43"},
		{"8.0.50-log", "mysql-8.0", "8.0.43"}, // 只按主次版本比较
		{"8.1.0", "mysql-8.4", "8.4.6"},
		{"8.4.3", "mysql-8.4", "8.4.6"},
		{"9.0.1", "mysql-9.4", "9.4.0"},
		{"5.7.44-log", "mysql-8.0", "8.0.43"},
	}
	for _, c := range cases {
		tool, found, err := ResolveTool(context.Background(), "mysqldump", c.server)
		require.NoError(t, err, c.server)
		require.True(t, found, c.server)
		assert.Equal(t, filepath.Join(toolsDir, c.dir, "bin", "mysqldump"), tool.Path, c.server)
		assert.Equal(t, "mysqldump", tool.Name)
		assert.Equal(t, c.version, tool.Version, c.server)
	}
}

// 没有不低于服务端的版本时选已装的最高版本，版本比较交给调用方按现有规则处理
func TestResolveToolFallsBackToHighest(t *testing.T) {
	toolsDir := t.TempDir()
	writeVersionedTool(t, toolsDir, "mysql-8.0", "mysqldump", "mysqldump  Ver 8.0.43")
	writeVersionedTool(t, toolsDir, "mysql-8.4", "mysqldump", "mysqldump  Ver 8.4.6")
	writeVersionedTool(t, toolsDir, "postgresql-14", "pg_dump", "pg_dump (PostgreSQL) 14.19")
	writeVersionedTool(t, toolsDir, "postgresql-16", "pg_dump", "pg_dump (PostgreSQL) 16.10")
	useToolsDir(t, t.TempDir(), toolsDir)

	tool, found, err := ResolveTool(context.Background(), "mysqldump", "9.4.0")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, filepath.Join(toolsDir, "mysql-8.4", "bin", "mysqldump"), tool.Path)
	assert.Equal(t, 8, tool.Major)
	assert.Equal(t, 4, tool.Minor)

	tool, found, err = ResolveTool(context.Background(), "pg_dump", "18.0 (Debian 18.0-1)")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, filepath.Join(toolsDir, "postgresql-16", "bin", "pg_dump"), tool.Path)
	assert.Equal(t, "16.10", tool.Version)

	// 服务端版本未知时同样选最高版本
	tool, found, err = ResolveTool(context.Background(), "pg_dump", "")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, filepath.Join(toolsDir, "postgresql-16", "bin", "pg_dump"), tool.Path)
}

// PostgreSQL 按主版本比较；pg_dump 与 pg_dumpall 各自只在有该工具的子目录中选
func TestResolveToolPostgresVersionedDirs(t *testing.T) {
	toolsDir := t.TempDir()
	writeVersionedTool(t, toolsDir, "postgresql-14", "pg_dump", "pg_dump (PostgreSQL) 14.19")
	writeVersionedTool(t, toolsDir, "postgresql-16", "pg_dump", "pg_dump (PostgreSQL) 16.10")
	writeVersionedTool(t, toolsDir, "postgresql-16", "pg_dumpall", "pg_dumpall (PostgreSQL) 16.10")
	writeVersionedTool(t, toolsDir, "postgresql-18", "pg_dump", "pg_dump (PostgreSQL) 18.0")
	useToolsDir(t, t.TempDir(), toolsDir)

	for server, dir := range map[string]string{"14.9": "postgresql-14", "15.4": "postgresql-16", "16.4 (Debian 16.4-1)": "postgresql-16", "17.2": "postgresql-18"} {
		tool, found, err := ResolveTool(context.Background(), "pg_dump", server)
		require.NoError(t, err, server)
		require.True(t, found, server)
		assert.Equal(t, filepath.Join(toolsDir, dir, "bin", "pg_dump"), tool.Path, server)
	}
	tool, found, err := ResolveTool(context.Background(), "pg_dumpall", "14.9")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, filepath.Join(toolsDir, "postgresql-16", "bin", "pg_dumpall"), tool.Path)
}

// MariaDB 服务端用 mariadb/ 子目录（mariadb-dump 优先，其次 mysqldump）；没有该子目录时按 MySQL 的规则选；
// MySQL 服务端不用 mariadb/
func TestResolveToolMariaDB(t *testing.T) {
	const server = "10.11.14-MariaDB-ubu2204"
	t.Run("mariadb-dump", func(t *testing.T) {
		toolsDir := t.TempDir()
		writeVersionedTool(t, toolsDir, "mysql-8.4", "mysqldump", "mysqldump  Ver 8.4.6")
		writeVersionedTool(t, toolsDir, "mariadb", "mariadb-dump", "mariadb-dump from 11.8.3-MariaDB, client 10.19 for debian-linux-gnu (x86_64)")
		writeVersionedTool(t, toolsDir, "mariadb", "mysqldump", "mysqldump from 11.8.3-MariaDB, client 10.19 for debian-linux-gnu (x86_64)")
		useToolsDir(t, t.TempDir(), toolsDir)

		tool, found, err := ResolveTool(context.Background(), "mysqldump", server)
		require.NoError(t, err)
		require.True(t, found)
		assert.Equal(t, filepath.Join(toolsDir, "mariadb", "bin", "mariadb-dump"), tool.Path)
		assert.Equal(t, "mariadb-dump", tool.Name)
		assert.Equal(t, "11.8.3", tool.Version)
		assert.Contains(t, tool.Raw, "MariaDB")

		tool, _, err = ResolveTool(context.Background(), "mysqldump", "8.4.2")
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(toolsDir, "mysql-8.4", "bin", "mysqldump"), tool.Path, "MySQL 服务端不用 mariadb/")
	})
	t.Run("mariadb 子目录中只有 mysqldump", func(t *testing.T) {
		toolsDir := t.TempDir()
		writeVersionedTool(t, toolsDir, "mariadb", "mysqldump", "mysqldump  Ver 10.19 Distrib 10.11.14-MariaDB, for debian-linux-gnu (x86_64)")
		useToolsDir(t, t.TempDir(), toolsDir)

		tool, found, err := ResolveTool(context.Background(), "mysqldump", server)
		require.NoError(t, err)
		require.True(t, found)
		assert.Equal(t, filepath.Join(toolsDir, "mariadb", "bin", "mysqldump"), tool.Path)
		assert.Equal(t, "mysqldump", tool.Name)
		assert.Equal(t, "10.11.14", tool.Version)
	})
	t.Run("没有 mariadb 子目录时按 MySQL 规则", func(t *testing.T) {
		toolsDir := t.TempDir()
		writeVersionedTool(t, toolsDir, "mysql-8.0", "mysqldump", "mysqldump  Ver 8.0.43")
		writeVersionedTool(t, toolsDir, "mysql-8.4", "mysqldump", "mysqldump  Ver 8.4.6")
		useToolsDir(t, t.TempDir(), toolsDir)

		tool, found, err := ResolveTool(context.Background(), "mysqldump", server)
		require.NoError(t, err)
		require.True(t, found)
		assert.Equal(t, filepath.Join(toolsDir, "mysql-8.4", "bin", "mysqldump"), tool.Path)
	})
}

// 没有版本子目录时与原来相同：先 PATH，再 tools.dir 根目录
func TestResolveToolWithoutVersionedDirs(t *testing.T) {
	pathDir := t.TempDir()
	toolsDir := t.TempDir()
	writeFakeTool(t, toolsDir, "mysqldump", "mysqldump  Ver 8.0.1")
	writeFakeTool(t, toolsDir, "pg_dump", "pg_dump (PostgreSQL) 16.4")
	writeFakeTool(t, pathDir, "mysqldump", "mysqldump  Ver 8.0.40 for Linux")
	// 只有 PostgreSQL 的版本子目录：不影响 mysqldump 的查找
	writeVersionedTool(t, toolsDir, "postgresql-17", "pg_dumpall", "pg_dumpall (PostgreSQL) 17.6")
	useToolsDir(t, pathDir, toolsDir)

	tool, found, err := ResolveTool(context.Background(), "mysqldump", "8.4.2")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, filepath.Join(pathDir, "mysqldump"), tool.Path)
	assert.Equal(t, "8.0.40", tool.Version)

	// pg_dump 没有版本子目录（postgresql-17 中只有 pg_dumpall）：落到 tools.dir 根目录
	tool, found, err = ResolveTool(context.Background(), "pg_dump", "17.2")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, filepath.Join(toolsDir, "pg_dump"), tool.Path)

	_, found, err = ResolveTool(context.Background(), "pg_dumpall", "17.2")
	require.NoError(t, err)
	require.True(t, found)

	SetToolsDir("")
	_, found, err = ResolveTool(context.Background(), "pg_dump", "16.4")
	require.NoError(t, err)
	assert.False(t, found)
}

// 选中的工具读不出版本时返回错误，同时给出选中的路径
func TestResolveToolVersionUnknown(t *testing.T) {
	toolsDir := t.TempDir()
	writeVersionedTool(t, toolsDir, "mysql-8.4", "mysqldump", "garbage")
	useToolsDir(t, t.TempDir(), toolsDir)

	tool, found, err := ResolveTool(context.Background(), "mysqldump", "8.4.2")
	require.Error(t, err)
	assert.True(t, found)
	assert.Equal(t, filepath.Join(toolsDir, "mysql-8.4", "bin", "mysqldump"), tool.Path)
}

// 能力探测的“主控端工具”项按服务端版本选用工具，并写出选用的路径与版本
func TestProbeToolItemShowsChosenPathAndVersion(t *testing.T) {
	toolsDir := t.TempDir()
	writeVersionedTool(t, toolsDir, "mysql-8.0", "mysqldump", "mysqldump  Ver 8.0.43")
	writeVersionedTool(t, toolsDir, "mysql-8.4", "mysqldump", "mysqldump  Ver 8.4.6")
	writeVersionedTool(t, toolsDir, "postgresql-16", "pg_dump", "pg_dump (PostgreSQL) 16.10")
	writeVersionedTool(t, toolsDir, "postgresql-17", "pg_dump", "pg_dump (PostgreSQL) 17.6")
	useToolsDir(t, t.TempDir(), toolsDir)
	ctx := context.Background()

	my := decideMySQLDump("8.4.2", lookupTool(ctx, "mysqldump", "8.4.2"))
	assert.Equal(t, TierOK, my.Tier)
	want := filepath.Join(toolsDir, "mysql-8.4", "bin", "mysqldump")
	assert.Contains(t, my.Detail.ZhCN, want+"（8.4.6）")
	assert.Contains(t, my.Detail.En, want+" (8.4.6)")

	my = decideMySQLDump("9.4.0", lookupTool(ctx, "mysqldump", "9.4.0"))
	assert.Equal(t, TierWarn, my.Tier, "都低于服务端时选最高版本，按现有规则为风险")
	assert.Contains(t, my.Detail.ZhCN, want+"（8.4.6）")

	pg := decidePgDump("16.4", lookupTool(ctx, "pg_dump", "16.4"))
	assert.Equal(t, TierOK, pg.Tier)
	wantPG := filepath.Join(toolsDir, "postgresql-16", "bin", "pg_dump")
	assert.Contains(t, pg.Detail.ZhCN, wantPG+"（16.10）")
	assert.Contains(t, pg.Detail.En, wantPG+" (16.10)")

	pg = decidePgDump("18.0", lookupTool(ctx, "pg_dump", "18.0"))
	assert.Equal(t, TierFail, pg.Tier, "都低于服务端时选最高版本，按现有规则为不可用")
	assert.Contains(t, pg.Detail.ZhCN, filepath.Join(toolsDir, "postgresql-17", "bin", "pg_dump")+"（17.6）")
}
