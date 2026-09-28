package kopiarepo

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	kfs "github.com/kopia/kopia/fs"
	"github.com/kopia/kopia/repo"
	"github.com/kopia/kopia/repo/compression"
	"github.com/kopia/kopia/repo/manifest"
	"github.com/kopia/kopia/snapshot"
	"github.com/kopia/kopia/snapshot/snapshotfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestRepo 在临时目录建一个真实的 kopia 仓库
func newTestRepo(t *testing.T) Location {
	t.Helper()
	loc := localAt(filepath.Join(t.TempDir(), "repo"))
	require.NoError(t, Create(context.Background(), loc, testKey))
	return loc
}

func openTestWriter(t *testing.T, m *Manager, loc Location) *Writer {
	t.Helper()
	w, err := m.OpenWriter(context.Background(), loc, testKey)
	require.NoError(t, err)
	t.Cleanup(func() { _ = w.Close(context.Background()) })
	return w
}

// inspectRepo 用独立的只读连接打开仓库，从存储读取写入结果
func inspectRepo(t *testing.T, loc Location) repo.Repository {
	t.Helper()
	ctx := context.Background()
	cfg := filepath.Join(t.TempDir(), "inspect.config")
	st, err := openStorage(ctx, loc, false)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close(ctx) })
	require.NoError(t, repo.Connect(ctx, cfg, st, testKey, &repo.ConnectOptions{
		ClientOptions: repo.ClientOptions{ReadOnly: true},
	}))
	r, err := repo.Open(ctx, cfg, testKey, &repo.Options{DisableRepositoryLog: true, OnFatalError: func(error) {}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close(ctx) })
	return r
}

func snapshotCount(t *testing.T, loc Location) int {
	t.Helper()
	ids, err := snapshot.ListSnapshotManifests(context.Background(), inspectRepo(t, loc), nil, nil)
	require.NoError(t, err)
	return len(ids)
}

// snapshotFiles 读回快照中每个文件的条目
func snapshotFiles(t *testing.T, r repo.Repository, id string) (*snapshot.Manifest, map[string]kfs.Entry) {
	t.Helper()
	ctx := context.Background()
	man, err := snapshot.LoadSnapshot(ctx, r, manifest.ID(id))
	require.NoError(t, err)
	root, err := snapshotfs.SnapshotRoot(r, man)
	require.NoError(t, err)
	dir, ok := root.(kfs.Directory)
	require.True(t, ok)
	entries, err := kfs.GetAllEntries(ctx, dir)
	require.NoError(t, err)
	out := map[string]kfs.Entry{}
	for _, e := range entries {
		out[e.Name()] = e
	}
	return man, out
}

func readEntry(t *testing.T, r repo.Repository, e kfs.Entry) []byte {
	t.Helper()
	de, ok := e.(snapshot.HasDirEntry)
	require.True(t, ok)
	rd, err := r.OpenObject(context.Background(), de.DirEntry().ObjectID)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	b, err := io.ReadAll(rd)
	require.NoError(t, err)
	return b
}

func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	_, err := rand.Read(b)
	require.NoError(t, err)
	return b
}

// leftFiles 列出目录下剩余的所有文件与子目录
func leftFiles(t *testing.T, root string) []string {
	t.Helper()
	var left []string
	err := filepath.WalkDir(root, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != root {
			left = append(left, p)
		}
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	require.NoError(t, err)
	return left
}

func TestWriteSnapshot(t *testing.T) {
	ctx := context.Background()

	t.Run("多个流式文件写成一份带来源与标签的快照，读回内容一致", func(t *testing.T) {
		loc := newTestRepo(t)
		w := openTestWriter(t, NewManager(t.TempDir()), loc)
		dump := randomBytes(t, 3<<20)
		grants := []byte("CREATE USER 'app'@'%';\n")
		res, err := w.WriteSnapshot(ctx, SnapshotRequest{
			Prefix:      "mysql/db-1",
			Tags:        SnapshotTags{JobID: 12, RunID: 34, Type: "full", Kind: "mysql"},
			Compression: CompressionZstd,
			Files: []SnapshotFile{
				{Name: "all.sql", Reader: bytes.NewReader(dump)},
				{Name: "grants.sql", Reader: bytes.NewReader(grants)},
			},
		})
		require.NoError(t, err)
		require.NotEmpty(t, res.ID)
		assert.Equal(t, []FileResult{{Name: "all.sql", Size: int64(len(dump))}, {Name: "grants.sql", Size: int64(len(grants))}}, res.Files)
		assert.Greater(t, res.UploadedBytes, int64(len(dump)), "随机数据压不小，新增上传至少是文件本身")

		r := inspectRepo(t, loc)
		man, files := snapshotFiles(t, r, res.ID)
		assert.Equal(t, snapshot.SourceInfo{Host: "opsnap", UserName: "opsnap", Path: "/mysql/db-1"}, man.Source)
		assert.Empty(t, man.IncompleteReason)
		assert.Equal(t, map[string]string{"tag:job": "12", "tag:run": "34", "tag:type": "full", "tag:kind": "mysql"}, man.Tags)
		require.Len(t, files, 2)
		assert.Equal(t, dump, readEntry(t, r, files["all.sql"]))
		assert.Equal(t, grants, readEntry(t, r, files["grants.sql"]))

		t.Run("相同内容再写一份，只新增清单与索引的上传", func(t *testing.T) {
			res2, err := w.WriteSnapshot(ctx, SnapshotRequest{
				Prefix: "mysql/db-1",
				Tags:   SnapshotTags{JobID: 12, RunID: 35, Type: "full", Kind: "mysql"},
				Files:  []SnapshotFile{{Name: "all.sql", Reader: bytes.NewReader(dump)}},
			})
			require.NoError(t, err)
			assert.NotEqual(t, res.ID, res2.ID)
			assert.Positive(t, res2.UploadedBytes)
			assert.Less(t, res2.UploadedBytes, int64(len(dump))/10)
			assert.Equal(t, 2, snapshotCount(t, loc))
		})
	})

	t.Run("按给定顺序逐个读取，前一个读到 EOF 后才开始读下一个", func(t *testing.T) {
		loc := newTestRepo(t)
		w := openTestWriter(t, NewManager(t.TempDir()), loc)
		var order []string
		files := make([]SnapshotFile, 0, 3)
		// 名字倒序给出，排除按名字排序读取的实现
		for _, name := range []string{"c.dump", "b.dump", "a.dump"} {
			files = append(files, SnapshotFile{Name: name, Reader: &orderReader{
				name: name, order: &order, r: bytes.NewReader(randomBytes(t, 256<<10)),
			}})
		}
		_, err := w.WriteSnapshot(ctx, SnapshotRequest{Prefix: "pg/a", Files: files})
		require.NoError(t, err)
		assert.Equal(t, []string{"c.dump", "c.dump EOF", "b.dump", "b.dump EOF", "a.dump", "a.dump EOF"}, order)
	})

	t.Run("按任务压缩设置写入文件内容，空值为 zstd", func(t *testing.T) {
		loc := newTestRepo(t)
		w := openTestWriter(t, NewManager(t.TempDir()), loc)
		cases := map[Compression]compression.Name{
			CompressionNone: "",
			CompressionGzip: "gzip",
			CompressionZstd: "zstd",
			"":              "zstd",
		}
		for c, want := range cases {
			// 每种设置用不同内容，否则去重会沿用先写入的那份
			data := bytes.Repeat([]byte("INSERT INTO t VALUES (1,'"+string(c)+"');\n"), 20000)
			res, err := w.WriteSnapshot(ctx, SnapshotRequest{
				Prefix:      "pg/" + string(c) + "x",
				Tags:        SnapshotTags{JobID: 1, RunID: 1, Type: "full", Kind: "postgresql"},
				Compression: c,
				Files:       []SnapshotFile{{Name: "db.dump", Reader: bytes.NewReader(data)}},
			})
			require.NoError(t, err, c)
			r := inspectRepo(t, loc)
			_, files := snapshotFiles(t, r, res.ID)
			de, ok := files["db.dump"].(snapshot.HasDirEntry)
			require.True(t, ok)
			cids, err := r.VerifyObject(ctx, de.DirEntry().ObjectID)
			require.NoError(t, err)
			require.NotEmpty(t, cids)
			for _, cid := range cids {
				info, err := r.ContentInfo(ctx, cid)
				require.NoError(t, err)
				assert.Equal(t, want, compression.HeaderIDToName[info.CompressionHeaderID], "压缩设置 %q", c)
			}
		}
	})

	t.Run("不认识的压缩方式在写入前拒绝", func(t *testing.T) {
		loc := newTestRepo(t)
		w := openTestWriter(t, NewManager(t.TempDir()), loc)
		_, err := w.WriteSnapshot(ctx, SnapshotRequest{
			Prefix: "a", Compression: "lz4",
			Files: []SnapshotFile{{Name: "a.sql", Reader: strings.NewReader("x")}},
		})
		require.Error(t, err)
		assert.Equal(t, 0, snapshotCount(t, loc))
	})

	t.Run("请求不完整时拒绝，不写入", func(t *testing.T) {
		loc := newTestRepo(t)
		w := openTestWriter(t, NewManager(t.TempDir()), loc)
		f := func(name string) SnapshotFile { return SnapshotFile{Name: name, Reader: strings.NewReader("x")} }
		for name, req := range map[string]SnapshotRequest{
			"没有前缀":   {Files: []SnapshotFile{f("a.sql")}},
			"没有文件":   {Prefix: "a"},
			"文件名为空":  {Prefix: "a", Files: []SnapshotFile{f("")}},
			"文件名含 /": {Prefix: "a", Files: []SnapshotFile{f("x/a.sql")}},
			"文件名重复":  {Prefix: "a", Files: []SnapshotFile{f("a.sql"), f("a.sql")}},
			"没有内容":   {Prefix: "a", Files: []SnapshotFile{{Name: "a.sql"}}},
		} {
			_, err := w.WriteSnapshot(ctx, req)
			assert.Error(t, err, name)
		}
		assert.Equal(t, 0, snapshotCount(t, loc))
	})

	t.Run("读取失败时返回该错误，不留下清单", func(t *testing.T) {
		loc := newTestRepo(t)
		w := openTestWriter(t, NewManager(t.TempDir()), loc)
		boom := errors.New("导出工具异常退出")
		_, err := w.WriteSnapshot(ctx, SnapshotRequest{
			Prefix: "mysql/a",
			Files: []SnapshotFile{
				{Name: "a.sql", Reader: strings.NewReader("ok")},
				{Name: "b.sql", Reader: io.MultiReader(bytes.NewReader(randomBytes(t, 1<<20)), &errReader{err: boom})},
			},
		})
		require.ErrorIs(t, err, boom)
		assert.Equal(t, 0, snapshotCount(t, loc))
	})

	t.Run("写入途中取消，不留下清单", func(t *testing.T) {
		loc := newTestRepo(t)
		w := openTestWriter(t, NewManager(t.TempDir()), loc)
		cctx, cancel := context.WithCancel(ctx)
		defer cancel()
		// 读过一段数据后取消，之后的读取不再返回 EOF，只能靠取消结束
		rd := &cancelAfterReader{data: randomBytes(t, 1<<20), cancel: cancel}
		_, err := w.WriteSnapshot(cctx, SnapshotRequest{
			Prefix: "mysql/a",
			Files:  []SnapshotFile{{Name: "a.sql", Reader: rd}},
		})
		require.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, 0, snapshotCount(t, loc))
	})

	t.Run("读回校验失败时删除已保存的清单", func(t *testing.T) {
		loc := newTestRepo(t)
		w := openTestWriter(t, NewManager(t.TempDir()), loc)
		orig := readBack
		t.Cleanup(func() { readBack = orig })
		bad := errors.New("读回大小不符")
		readBack = func(context.Context, repo.Repository, manifest.ID, []FileResult) error { return bad }
		_, err := w.WriteSnapshot(ctx, SnapshotRequest{
			Prefix: "mysql/a",
			Files:  []SnapshotFile{{Name: "a.sql", Reader: strings.NewReader("data")}},
		})
		require.ErrorIs(t, err, bad)
		assert.ErrorIs(t, err, ErrVerify, "调用方据此把失败归入“校验”一步")
		assert.Equal(t, 0, snapshotCount(t, loc))
	})
}

func TestVerifySnapshotReadBack(t *testing.T) {
	ctx := context.Background()
	loc := newTestRepo(t)
	w := openTestWriter(t, NewManager(t.TempDir()), loc)
	res, err := w.WriteSnapshot(ctx, SnapshotRequest{
		Prefix: "mysql/a",
		Files:  []SnapshotFile{{Name: "a.sql", Reader: strings.NewReader("data")}},
	})
	require.NoError(t, err)
	r := inspectRepo(t, loc)
	id := manifest.ID(res.ID)

	assert.NoError(t, readBack(ctx, r, id, []FileResult{{Name: "a.sql", Size: 4}}))
	assert.Error(t, readBack(ctx, r, id, []FileResult{{Name: "a.sql", Size: 5}}), "大小不符")
	assert.Error(t, readBack(ctx, r, id, []FileResult{{Name: "a.sql", Size: 4}, {Name: "b.sql", Size: 0}}), "缺少文件")
	assert.Error(t, readBack(ctx, r, id, nil), "多出文件")
	assert.Error(t, readBack(ctx, r, "0123456789abcdef0123456789abcdef", nil), "清单不存在")
}

func TestWriterSessionConfig(t *testing.T) {
	ctx := context.Background()

	t.Run("会话期间连接配置在 0700 临时目录中，关闭后本机不留任何文件", func(t *testing.T) {
		loc := newTestRepo(t)
		root := filepath.Join(t.TempDir(), "kopia")
		m := NewManager(root)
		w, err := m.OpenWriter(ctx, loc, testKey)
		require.NoError(t, err)
		dirs, _ := filepath.Glob(filepath.Join(root, "tmp-write-*"))
		require.Len(t, dirs, 1)
		fi, err := os.Stat(dirs[0])
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o700), fi.Mode().Perm())
		assert.FileExists(t, filepath.Join(dirs[0], configName))

		_, err = w.WriteSnapshot(ctx, SnapshotRequest{
			Prefix: "a", Files: []SnapshotFile{{Name: "a.sql", Reader: strings.NewReader("x")}},
		})
		require.NoError(t, err)
		require.NoError(t, w.Close(ctx))
		assert.Empty(t, leftFiles(t, root))
	})

	t.Run("错误密钥返回 ErrInvalidPassword，不留下配置", func(t *testing.T) {
		loc := newTestRepo(t)
		root := filepath.Join(t.TempDir(), "kopia")
		m := NewManager(root)
		_, err := m.OpenWriter(ctx, loc, "wrong-key-wrong-key")
		require.ErrorIs(t, err, ErrInvalidPassword)
		assert.Empty(t, leftFiles(t, root))
	})

	t.Run("启动时清理上次写入中途退出留下的会话目录", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "kopia")
		stale := filepath.Join(root, "tmp-write-123", configName)
		require.NoError(t, os.MkdirAll(filepath.Dir(stale), 0o700))
		require.NoError(t, os.WriteFile(stale, []byte(`{"storage":{"config":{"secretAccessKey":"x"}}}`), 0o600))
		NewManager(root)
		assert.NoDirExists(t, filepath.Dir(stale))
	})
}

// orderReader 记录第一次读取与读到 EOF 的先后
type orderReader struct {
	name    string
	order   *[]string
	r       io.Reader
	started bool
}

func (o *orderReader) Read(p []byte) (int, error) {
	if !o.started {
		o.started = true
		*o.order = append(*o.order, o.name)
	}
	n, err := o.r.Read(p)
	if errors.Is(err, io.EOF) {
		*o.order = append(*o.order, o.name+" EOF")
	}
	return n, err
}

type errReader struct{ err error }

func (r *errReader) Read([]byte) (int, error) { return 0, r.err }

// cancelAfterReader 读完 data 后取消 context，然后一直阻塞到被取消的读取不再发生
type cancelAfterReader struct {
	data   []byte
	off    int
	cancel context.CancelFunc
}

func (r *cancelAfterReader) Read(p []byte) (int, error) {
	if r.off < len(r.data) {
		n := copy(p, r.data[r.off:])
		r.off += n
		return n, nil
	}
	r.cancel()
	// 模拟仍在运行的导出：从不返回 EOF
	return 0, nil
}
