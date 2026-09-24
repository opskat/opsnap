package kopiarepo

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListDirs(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	mk := func(name string) string {
		p := filepath.Join(root, name)
		require.NoError(t, os.Mkdir(p, 0o700))
		return p
	}
	mk("b-empty")
	full := mk("a-full")
	require.NoError(t, os.WriteFile(filepath.Join(full, "x"), []byte("x"), 0o600))
	repoDir := mk("c-repo")
	require.NoError(t, Create(ctx, localAt(repoDir), testKey))
	require.NoError(t, os.WriteFile(filepath.Join(root, "file.txt"), []byte("x"), 0o600))
	require.NoError(t, os.Symlink(full, filepath.Join(root, "d-link")))
	require.NoError(t, os.Symlink(filepath.Join(root, "file.txt"), filepath.Join(root, "e-filelink")))

	t.Run("浏览不改动被浏览的目录", func(t *testing.T) {
		_, err := ListDirs(root)
		require.NoError(t, err)
		entries, _ := os.ReadDir(full)
		assert.Len(t, entries, 1)
	})

	t.Run("只列目录（含指向目录的符号链接），按名称排序并标出状态", func(t *testing.T) {
		dirs, err := ListDirs(root)
		require.NoError(t, err)
		got := map[string]DirStatus{}
		names := make([]string, 0, len(dirs))
		for _, d := range dirs {
			got[d.Name] = d.Status
			names = append(names, d.Name)
			assert.Equal(t, filepath.Join(root, d.Name), d.Path)
		}
		assert.Equal(t, []string{"a-full", "b-empty", "c-repo", "d-link"}, names)
		assert.Equal(t, DirNotEmpty, got["a-full"])
		assert.Equal(t, DirEmpty, got["b-empty"])
		assert.Equal(t, DirRepository, got["c-repo"])
		assert.Equal(t, DirNotEmpty, got["d-link"])
	})

	t.Run("无权读取与不可写的目录", func(t *testing.T) {
		skipIfRoot(t)
		base := t.TempDir()
		noRead := filepath.Join(base, "no-read")
		noWrite := filepath.Join(base, "no-write")
		require.NoError(t, os.Mkdir(noRead, 0o300))
		require.NoError(t, os.Mkdir(noWrite, 0o500))
		t.Cleanup(func() { chmod(t, noRead, 0o700); chmod(t, noWrite, 0o700) })

		dirs, err := ListDirs(base)
		require.NoError(t, err)
		require.Len(t, dirs, 2)
		assert.Equal(t, DirNoAccess, dirs[0].Status)
		assert.Equal(t, DirNotWritable, dirs[1].Status)
	})

	t.Run("相对路径与不存在的路径报错", func(t *testing.T) {
		_, err := ListDirs("relative")
		assert.ErrorIs(t, err, ErrRelativePath)
		_, err = ListDirs(filepath.Join(root, "missing"))
		assert.ErrorIs(t, err, os.ErrNotExist)
	})
}

func TestMkdir(t *testing.T) {
	root := t.TempDir()

	t.Run("在当前目录下以 0700 新建文件夹", func(t *testing.T) {
		p, err := Mkdir(root, "new")
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(root, "new"), p)
		fi, err := os.Stat(p)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o700), fi.Mode().Perm())
	})

	t.Run("名称为空、含斜杠、为 . 或 .. 时拒绝", func(t *testing.T) {
		for _, name := range []string{"", "  ", "a/b", ".", ".."} {
			_, err := Mkdir(root, name)
			assert.ErrorIs(t, err, ErrInvalidDirName, name)
		}
	})

	t.Run("与已有目录重名时拒绝", func(t *testing.T) {
		_, err := Mkdir(root, "dup")
		require.NoError(t, err)
		_, err = Mkdir(root, "dup")
		assert.ErrorIs(t, err, os.ErrExist)
	})

	t.Run("没有写权限时返回权限错误", func(t *testing.T) {
		skipIfRoot(t)
		ro := filepath.Join(root, "ro")
		require.NoError(t, os.Mkdir(ro, 0o500))
		t.Cleanup(func() { chmod(t, ro, 0o700) })
		_, err := Mkdir(ro, "x")
		assert.ErrorIs(t, err, os.ErrPermission)
	})
}
