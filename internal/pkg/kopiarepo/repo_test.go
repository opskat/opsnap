package kopiarepo

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testKey = "abcd-efgh-ijkl-mnop-qrst-uvwx"

func localAt(path string) Location {
	return Location{Kind: KindLocal, Path: path}
}

func TestProbeLocal(t *testing.T) {
	ctx := context.Background()

	t.Run("不存在的目录算作空位置", func(t *testing.T) {
		res, err := Probe(ctx, localAt(filepath.Join(t.TempDir(), "a", "b")))
		require.NoError(t, err)
		assert.Equal(t, StateEmpty, res.State)
	})

	t.Run("空目录为空位置，测试写入不留下文件", func(t *testing.T) {
		dir := t.TempDir()
		res, err := Probe(ctx, localAt(dir))
		require.NoError(t, err)
		assert.Equal(t, StateEmpty, res.State)
		entries, _ := os.ReadDir(dir)
		assert.Empty(t, entries)
	})

	t.Run("有其他文件的目录为非空", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o600))
		res, err := Probe(ctx, localAt(dir))
		require.NoError(t, err)
		assert.Equal(t, StateNotEmpty, res.State)
		entries, _ := os.ReadDir(dir)
		assert.Len(t, entries, 1, "探测不在目录里留下任何文件")
	})

	t.Run("kopia 仓库返回仓库状态与创建时间", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, Create(ctx, localAt(dir), testKey))
		res, err := Probe(ctx, localAt(dir))
		require.NoError(t, err)
		assert.Equal(t, StateRepository, res.State)
		assert.WithinDuration(t, time.Now(), res.CreatedAt, time.Minute)
	})

	t.Run("路径是文件时无法使用", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), "file")
		require.NoError(t, os.WriteFile(f, []byte("x"), 0o600))
		_, err := Probe(ctx, localAt(f))
		assertReason(t, err, ReasonNotDirectory)
	})

	t.Run("不可写的目录无法使用", func(t *testing.T) {
		skipIfRoot(t)
		dir := t.TempDir()
		chmod(t, dir, 0o500)
		t.Cleanup(func() { chmod(t, dir, 0o700) })
		_, err := Probe(ctx, localAt(dir))
		assertReason(t, err, ReasonNotWritable)

		_, err = Probe(ctx, localAt(filepath.Join(dir, "sub")))
		assertReason(t, err, ReasonNotWritable)
	})
}

func TestCreateAndVerifyLocal(t *testing.T) {
	ctx := context.Background()
	m := NewManager(t.TempDir())

	t.Run("不存在的目录以 0700 创建并建库，正确密钥可打开", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "repo")
		require.NoError(t, Create(ctx, localAt(dir), testKey))
		fi, err := os.Stat(dir)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o700), fi.Mode().Perm())

		n, err := m.Verify(ctx, 1, localAt(dir), testKey)
		require.NoError(t, err)
		assert.Equal(t, 0, n)
		assert.DirExists(t, filepath.Join(m.root, "1"))
	})

	t.Run("错误密钥返回 ErrInvalidPassword", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, Create(ctx, localAt(dir), testKey))
		_, err := m.Verify(ctx, 2, localAt(dir), "wrong-key-wrong-key")
		assert.ErrorIs(t, err, ErrInvalidPassword)
	})

	t.Run("不是仓库的位置无法打开", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o600))
		_, err := m.Verify(ctx, 3, localAt(dir), testKey)
		assert.ErrorIs(t, err, ErrNotRepository)
		entries, _ := os.ReadDir(dir)
		assert.Len(t, entries, 1, "连接失败也不在目录里留下文件")
	})

	t.Run("临时校验（id 为 0）不留下配置", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, Create(ctx, localAt(dir), testKey))
		_, err := m.Verify(ctx, 0, localAt(dir), testKey)
		require.NoError(t, err)
		entries, _ := os.ReadDir(m.root)
		for _, e := range entries {
			assert.NotContains(t, e.Name(), "tmp")
		}
	})

	t.Run("建库前位置已不为空时放弃，不写入任何数据", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o600))
		err := Create(ctx, localAt(dir), testKey)
		assert.ErrorIs(t, err, ErrNotEmpty)
		entries, _ := os.ReadDir(dir)
		assert.Len(t, entries, 1)
	})

	t.Run("建库前位置已是仓库时放弃，原仓库仍能用原密钥打开", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, Create(ctx, localAt(dir), testKey))
		err := Create(ctx, localAt(dir), "another-key-another-key")
		assert.ErrorIs(t, err, ErrAlreadyRepository)
		_, err = m.Verify(ctx, 4, localAt(dir), testKey)
		assert.NoError(t, err)
	})

	t.Run("Remove 清理该存储的配置与缓存", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, Create(ctx, localAt(dir), testKey))
		_, err := m.Verify(ctx, 5, localAt(dir), testKey)
		require.NoError(t, err)
		require.NoError(t, m.Remove(5))
		assert.NoDirExists(t, filepath.Join(m.root, "5"))
		assert.NoError(t, m.Remove(5), "重复清理不报错")
	})
}

func assertReason(t *testing.T, err error, want Reason) {
	t.Helper()
	var le *LocationError
	require.ErrorAs(t, err, &le)
	assert.Equal(t, want, le.Reason)
}

func skipIfRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root 不受目录权限限制")
	}
}

// chmod 修改测试目录的权限（目录需要执行位才能进入）
func chmod(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	require.NoError(t, os.Chmod(path, mode))
}
