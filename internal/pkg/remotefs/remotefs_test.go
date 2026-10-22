package remotefs

import (
	"context"
	"errors"
	"io/fs"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeFS 是 DirLister 的替身：按路径给出条目或错误
type fakeFS struct {
	dirs  map[string][]Entry
	errs  map[string]error
	calls []string
}

func (f *fakeFS) List(_ context.Context, path string) ([]Entry, error) {
	f.calls = append(f.calls, path)
	if err, ok := f.errs[path]; ok {
		return nil, err
	}
	return f.dirs[path], nil
}

// Stat 让 fakeFS 满足 Lister；本文件的用例都不走这条路径
func (f *fakeFS) Stat(ctx context.Context, path string) error {
	_, err := f.List(ctx, path)
	return err
}

func entries(names ...string) []Entry {
	out := make([]Entry, 0, len(names))
	for _, n := range names {
		out = append(out, Entry{Name: n, ModTime: time.Unix(1700000000, 0)})
	}
	return out
}

func TestNormalize(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "空串是根", in: "", want: "/"},
		{name: "根", in: "/", want: "/"},
		{name: "结尾斜杠去掉", in: "/srv/www/", want: "/srv/www"},
		{name: "重复斜杠合并", in: "//srv//www", want: "/srv/www"},
		{name: "多点斜杠", in: "/srv///www///", want: "/srv/www"},
		{name: "根的多斜杠", in: "///", want: "/"},
		{name: "保留普通路径", in: "/srv/www/uploads", want: "/srv/www/uploads"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Normalize(c.in)
			require.NoError(t, err)
			assert.Equal(t, c.want, got)
		})
	}
}

func TestNormalizeRejects(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{name: "相对路径", in: "srv/www"},
		{name: "含上级引用", in: "/srv/../etc"},
		{name: "单独的上级引用", in: "/.."},
		{name: "含 NUL", in: "/srv/\x00www"},
		{name: "含换行", in: "/srv/\nwww"},
		{name: "含回车", in: "/srv/\rwww"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Normalize(c.in)
			var ie *InvalidPathError
			require.ErrorAs(t, err, &ie)
		})
	}
}

// 只列目录；符号链接按是否指向目录决定去留
func TestDirsOnly(t *testing.T) {
	got := DirsOnly([]Entry{
		{Name: "www", IsDir: true},
		{Name: "notes.txt"},
		{Name: "uploads", IsDir: true},
		{Name: "link-to-dir", IsDir: true, Symlink: true},
	})
	names := make([]string, 0, len(got))
	for _, e := range got {
		names = append(names, e.Name)
	}
	assert.Equal(t, []string{"link-to-dir", "uploads", "www"}, names, "按名称排序，文件被丢掉")
}

func TestDirsOnlySortsByNameNotLocale(t *testing.T) {
	got := DirsOnly([]Entry{
		{Name: "b", IsDir: true},
		{Name: "B", IsDir: true},
		{Name: "a", IsDir: true},
	})
	assert.Equal(t, []string{"B", "a", "b"}, []string{got[0].Name, got[1].Name, got[2].Name})
}

func TestApplyLimit(t *testing.T) {
	many := make([]Entry, 2500)
	for i := range many {
		many[i] = Entry{Name: string(rune('a' + i%26)), IsDir: true}
	}
	got, truncated := ApplyLimit(many, MaxEntries)
	assert.Len(t, got, MaxEntries)
	assert.True(t, truncated)

	exact := make([]Entry, MaxEntries)
	got, truncated = ApplyLimit(exact, MaxEntries)
	assert.Len(t, got, MaxEntries)
	assert.False(t, truncated, "正好等于上限不算截断")

	few, truncated := ApplyLimit(entries("a", "b"), MaxEntries)
	assert.Len(t, few, 2)
	assert.False(t, truncated)
}

// 中途失败时整层作废，不能把已收到的半份当完整列表
func TestListDropsPartialOnError(t *testing.T) {
	f := &fakeFS{
		errs: map[string]error{"/srv": errors.New("connection lost")},
	}
	_, err := List(context.Background(), f, "/srv")
	require.Error(t, err)

	var ce *Error
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, ReasonFailed, ce.Reason)
}

func TestListClassifies(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		reason string
	}{
		{name: "不存在", err: fs.ErrNotExist, reason: ReasonNotFound},
		{name: "无权限", err: fs.ErrPermission, reason: ReasonPermission},
		{name: "超时", err: context.DeadlineExceeded, reason: ReasonTimeout},
		{name: "其他", err: errors.New("boom"), reason: ReasonFailed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeFS{errs: map[string]error{"/srv": c.err}}
			_, err := List(context.Background(), f, "/srv")
			var ce *Error
			require.ErrorAs(t, err, &ce)
			assert.Equal(t, c.reason, ce.Reason)
		})
	}
}

// 不是目录必须由 Stat 得出，不能依赖 ReadDir 的错误码
func TestListDetectsNotDir(t *testing.T) {
	f := &fakeFS{
		errs: map[string]error{"/srv/notes.txt": &ErrNotDirectory{Path: "/srv/notes.txt"}},
	}
	_, err := List(context.Background(), f, "/srv/notes.txt")
	var ce *Error
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, ReasonNotDir, ce.Reason)
	assert.Contains(t, ce.Message, "/srv/notes.txt")
}

func TestListNormalizesPath(t *testing.T) {
	f := &fakeFS{dirs: map[string][]Entry{"/srv/www": entries("a")}}
	res, err := List(context.Background(), f, "/srv//www/")
	require.NoError(t, err)
	assert.Equal(t, "/srv/www", res.Path)
	assert.Equal(t, []string{"/srv/www"}, f.calls, "交给列举器的是规范化后的路径")
}

func TestListInvalidPathDoesNotReachLister(t *testing.T) {
	f := &fakeFS{}
	_, err := List(context.Background(), f, "/srv/../etc")
	var ie *InvalidPathError
	require.ErrorAs(t, err, &ie)
	assert.Empty(t, f.calls, "非法路径不进入列举器")
}

func TestListSymlinkFlagged(t *testing.T) {
	f := &fakeFS{dirs: map[string][]Entry{"/": {
		{Name: "www", IsDir: true},
		{Name: "current", IsDir: true, Symlink: true},
	}}}
	res, err := List(context.Background(), f, "/")
	require.NoError(t, err)
	require.Len(t, res.List, 2)
	assert.Equal(t, "current", res.List[0].Name, "按名称排序，current 在前")
	assert.True(t, res.List[0].Symlink)
	assert.Equal(t, "www", res.List[1].Name)
	assert.False(t, res.List[1].Symlink)
}

func TestListUnselectableNames(t *testing.T) {
	f := &fakeFS{dirs: map[string][]Entry{"/": {
		{Name: "ok", IsDir: true},
		{Name: "bad\x1bname", IsDir: true},
		{Name: string([]byte{0xff, 0xfe}), IsDir: true},
	}}}
	res, err := List(context.Background(), f, "/")
	require.NoError(t, err)
	require.Len(t, res.List, 3, "不静默隐藏")
	byName := map[string]bool{}
	for _, e := range res.List {
		byName[e.Name] = e.Selectable
	}
	assert.True(t, byName["ok"])
	assert.False(t, byName["bad\x1bname"], "控制字符不可选")
	assert.False(t, byName[string([]byte{0xff, 0xfe})], "非法 UTF-8 不可选")
}
