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

	path, ok := toolPath("mysqldump")
	require.True(t, ok)
	assert.Equal(t, filepath.Join(dir, "mysqldump"), path)
}

func TestToolPathFallsBackToToolsDir(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // PATH 中什么都没有
	toolsDirPath := t.TempDir()
	writeFakeTool(t, toolsDirPath, "pg_dump", "pg_dump (PostgreSQL) 16.4")
	SetToolsDir(toolsDirPath)
	t.Cleanup(func() { SetToolsDir("") })

	path, ok := toolPath("pg_dump")
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

	path, ok := toolPath("mysqldump")
	require.True(t, ok)
	assert.Equal(t, filepath.Join(pathDir, "mysqldump"), path)
}

func TestToolPathNotFound(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	SetToolsDir("")

	_, ok := toolPath("mysqldump")
	assert.False(t, ok)
}

func TestToolPathToolsDirUnconfigured(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	SetToolsDir("")

	_, ok := toolPath("pg_dump")
	assert.False(t, ok)
}

func TestToolVersionParsesLeadingVersion(t *testing.T) {
	dir := t.TempDir()
	path := writeFakeTool(t, dir, "mysqldump", "mysqldump  Ver 8.0.40 for Linux on x86_64")

	major, minor, raw, err := toolVersion(context.Background(), path)
	require.NoError(t, err)
	assert.Equal(t, 8, major)
	assert.Equal(t, 0, minor)
	assert.Contains(t, raw, "8.0.40")
}

// MySQL 5.7 及更早的 mysqldump 先打印工具自身的版本号（Ver 10.13），客户端版本在 Distrib 之后
func TestToolVersionPrefersDistrib(t *testing.T) {
	dir := t.TempDir()
	path := writeFakeTool(t, dir, "mysqldump", "mysqldump  Ver 10.13 Distrib 5.7.44, for Linux (x86_64)")

	major, minor, _, err := toolVersion(context.Background(), path)
	require.NoError(t, err)
	assert.Equal(t, 5, major)
	assert.Equal(t, 7, minor)
}

func TestToolVersionUnparseable(t *testing.T) {
	dir := t.TempDir()
	path := writeFakeTool(t, dir, "mysqldump", "not a version string")

	_, _, _, err := toolVersion(context.Background(), path)
	assert.Error(t, err)
}
