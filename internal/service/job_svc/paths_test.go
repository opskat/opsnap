package job_svc

import (
	"context"
	"errors"
	"io/fs"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	jobapi "github.com/opskat/opsnap/internal/api/job"
	"github.com/opskat/opsnap/internal/pkg/remotefs"
	"github.com/opskat/opsnap/internal/pkg/sshconn"
)

// fakeFS 目录列举替身，按路径给结果
type fakeFS struct {
	dirs  map[string][]remotefs.Entry
	errs  map[string]error
	calls []string
}

func (f *fakeFS) List(_ context.Context, path string) ([]remotefs.Entry, error) {
	f.calls = append(f.calls, path)
	if err, ok := f.errs[path]; ok {
		return nil, err
	}
	return f.dirs[path], nil
}

func (f *fakeFS) Stat(ctx context.Context, path string) error {
	_, err := f.List(ctx, path)
	return err
}

// 让 fakeFS 满足 Browser
var _ Browser = (*fakeFS)(nil)

// withPaths 换一个注入目录浏览替身的服务实例
func (f *fixture) withPaths(open OpenFS) JobSvc {
	return NewJob(Deps{
		Now:    func() time.Time { return f.now },
		Tunnel: f.tunnel.open,
		Probes: func(string) (Probe, error) { return f.probe, nil },
		OpenFS: open,
	})
}

func (f *fixture) usePaths(dirs map[string][]remotefs.Entry, errs map[string]error) *fakeFS {
	fs := &fakeFS{dirs: dirs, errs: errs}
	f.svc = f.withPaths(func(context.Context, int64) (Browser, func(), error) {
		return fs, func() {}, nil
	})
	return fs
}

// Paths 列一层目录；失败不是接口错误，返回 OK=false 与原因。
func TestPaths(t *testing.T) {
	f := setupJobTest(t)

	t.Run("列出子目录", func(t *testing.T) {
		f.usePaths(map[string][]remotefs.Entry{
			"/srv/www": {
				{Name: "uploads", IsDir: true},
				{Name: "index.html"},
				{Name: "static", IsDir: true},
			},
		}, nil)
		req := &jobapi.PathsRequest{Paths: []string{"/srv/www"}, Path: "/srv/www"}
		req.ServerID = f.srvID
		req.SourceType = jobapi.SourceFiles
		resp, err := f.svc.Paths(f.ctx, req)
		require.NoError(t, err)
		assert.True(t, resp.OK)
		assert.Equal(t, "/srv/www", resp.Path)
		require.Len(t, resp.List, 2, "只列目录")
		assert.Equal(t, "static", resp.List[0].Name)
		assert.Equal(t, "uploads", resp.List[1].Name)
	})

	t.Run("目录不存在不是接口错误", func(t *testing.T) {
		f.usePaths(nil, map[string]error{"/nope": fs.ErrNotExist})
		req := &jobapi.PathsRequest{Paths: []string{"/var/www"}}
		req.ServerID = f.srvID
		req.SourceType = jobapi.SourceFiles
		req.Path = "/nope"
		resp, err := f.svc.Paths(f.ctx, req)
		require.NoError(t, err)
		assert.False(t, resp.OK)
		assert.Equal(t, remotefs.ReasonNotFound, resp.Reason)
		assert.NotEmpty(t, resp.ReasonLabel)
		assert.Contains(t, resp.Message, "/nope")
	})

	t.Run("没有权限", func(t *testing.T) {
		f.usePaths(nil, map[string]error{"/root": fs.ErrPermission})
		req := &jobapi.PathsRequest{Paths: []string{"/var/www"}}
		req.ServerID = f.srvID
		req.SourceType = jobapi.SourceFiles
		req.Path = "/root"
		resp, err := f.svc.Paths(f.ctx, req)
		require.NoError(t, err)
		assert.False(t, resp.OK)
		assert.Equal(t, remotefs.ReasonPermission, resp.Reason)
	})

	t.Run("SSH 链路失败沿用服务器原因", func(t *testing.T) {
		f.svc = f.withPaths(func(context.Context, int64) (Browser, func(), error) {
			return nil, nil, &sshconn.HopError{Index: 1, Kind: sshconn.KindSSH, Addr: "10.0.0.1:22", Reason: sshconn.ReasonTimeout, Message: "超时"}
		})
		req := &jobapi.PathsRequest{Paths: []string{"/var/www"}}
		req.ServerID = f.srvID
		req.SourceType = jobapi.SourceFiles
		resp, err := f.svc.Paths(f.ctx, req)
		require.NoError(t, err)
		assert.False(t, resp.OK)
		assert.Equal(t, string(sshconn.ReasonTimeout), resp.Reason)
		assert.Contains(t, resp.Message, "第 1 跳")
	})

	// 决策 14：路径框空着也要能浏览，否则第一次根本拿不到结果
	t.Run("路径为空也能浏览", func(t *testing.T) {
		f.usePaths(map[string][]remotefs.Entry{"/": {{Name: "srv", IsDir: true}}}, nil)
		req := &jobapi.PathsRequest{Paths: []string{""}}
		req.ServerID = f.srvID
		req.SourceType = jobapi.SourceFiles
		resp, err := f.svc.Paths(f.ctx, req)
		require.NoError(t, err)
		assert.True(t, resp.OK)
		assert.Equal(t, "/", resp.Path)
	})

	t.Run("非法路径按字段错误", func(t *testing.T) {
		fs := f.usePaths(nil, nil)
		req := &jobapi.PathsRequest{Paths: []string{"/var/www"}}
		req.ServerID = f.srvID
		req.SourceType = jobapi.SourceFiles
		req.Path = "/srv/../../etc"
		_, err := f.svc.Paths(f.ctx, req)
		assert.Contains(t, fieldErrors(t, err), "path")
		assert.Empty(t, fs.calls, "非法路径不进入 SFTP")
	})

	t.Run("数据库任务不能浏览目录", func(t *testing.T) {
		req := f.mysqlInput()
		preq := &jobapi.PathsRequest{SourceType: jobapi.SourceMySQL, ServerID: f.srvID, DBHost: "172.18.0.5", DBPort: 3306, DBUser: "backup", DBPassword: "pw", Databases: []string{"orders"}, RepoID: f.repoID}
		_ = req
		_, err := f.svc.Paths(f.ctx, preq)
		assert.Error(t, err)
	})
}

// 决策 14：paths 移出 connectionFields，路径空着时测试连接仍能跑
func TestConnectionAllowsEmptyPaths(t *testing.T) {
	f := setupJobTest(t)
	f.usePaths(map[string][]remotefs.Entry{"/": {}}, nil)
	in := f.filesInput()
	in.Paths = nil
	resp, err := f.svc.Test(f.ctx, jobapi.NewTestRequest(0, &in))
	require.NoError(t, err, "路径为空不该拦住测试连接")
	assert.Equal(t, []string{"ssh", "paths", "repo"}, keys(resp))
}

// 「目录可读」是独立检查项，排在 SSH 之后、存储位置之前
func TestConnectionChecksPaths(t *testing.T) {
	f := setupJobTest(t)

	t.Run("全部可读", func(t *testing.T) {
		f.usePaths(map[string][]remotefs.Entry{
			"/var/www":  {},
			"/srv/data": {},
		}, nil)
		in := f.filesInput()
		in.Paths = []string{"/var/www", "/srv/data"}
		resp, err := f.svc.Test(f.ctx, jobapi.NewTestRequest(0, &in))
		require.NoError(t, err)
		assert.True(t, resp.OK)
		assert.Equal(t, []string{"ssh", "paths", "repo"}, keys(resp))
		assert.Contains(t, checkOf(t, resp, "paths").Label, "目录")
	})

	t.Run("有不可读的路径", func(t *testing.T) {
		f.usePaths(nil, map[string]error{
			"/var/www":      fs.ErrNotExist,
			"/srv/secret":   fs.ErrPermission,
			"/srv/notes.md": &remotefs.ErrNotDirectory{Path: "/srv/notes.md"},
		})
		in := f.filesInput()
		in.Paths = []string{"/var/www", "/srv/secret", "/srv/notes.md"}
		resp, err := f.svc.Test(f.ctx, jobapi.NewTestRequest(0, &in))
		require.NoError(t, err)
		assert.False(t, resp.OK)
		item := checkOf(t, resp, "paths")
		assert.False(t, item.OK)
		assert.Contains(t, item.Reason, "/var/www")
		assert.Contains(t, item.Reason, "/srv/secret")
		assert.Contains(t, item.Reason, "/srv/notes.md")
	})

	t.Run("SSH 不通时跳过", func(t *testing.T) {
		f.tunnel.err = &sshconn.HopError{Index: 1, Kind: sshconn.KindSSH, Addr: "10.0.0.1:22", Reason: sshconn.ReasonAuthFailed, Message: "认证失败"}
		defer func() { f.tunnel.err = nil }()
		in := f.filesInput()
		resp, err := f.svc.Test(f.ctx, jobapi.NewTestRequest(0, &in))
		require.NoError(t, err)
		assert.True(t, checkOf(t, resp, "paths").Skipped)
	})

	t.Run("数据库任务没有这一项", func(t *testing.T) {
		resp, err := f.svc.Test(f.ctx, jobapi.NewTestRequest(0, ptr(f.mysqlInput())))
		require.NoError(t, err)
		assert.NotContains(t, keys(resp), "paths")
	})
}

// 保存时校验路径存在且是目录；服务器连不上时放行（决策 10）
func TestSaveChecksPaths(t *testing.T) {
	f := setupJobTest(t)

	t.Run("路径不存在不能保存", func(t *testing.T) {
		f.usePaths(nil, map[string]error{"/var/www": fs.ErrNotExist})
		_, err := f.svc.Create(f.ctx, jobapi.NewCreateRequest(&[]jobapi.JobInput{f.filesInput()}[0]))
		require.Error(t, err)
		assert.Contains(t, fieldErrors(t, err), "paths")
	})

	t.Run("不是目录不能保存", func(t *testing.T) {
		f.usePaths(nil, map[string]error{"/var/www": &remotefs.ErrNotDirectory{Path: "/var/www"}})
		_, err := f.svc.Create(f.ctx, jobapi.NewCreateRequest(&[]jobapi.JobInput{f.filesInput()}[0]))
		require.Error(t, err)
		assert.Contains(t, fieldErrors(t, err), "paths")
	})

	t.Run("服务器连不上放行保存", func(t *testing.T) {
		f.svc = f.withPaths(func(context.Context, int64) (Browser, func(), error) {
			return nil, nil, errors.New("dial tcp: i/o timeout")
		})
		resp, err := f.svc.Create(f.ctx, jobapi.NewCreateRequest(&[]jobapi.JobInput{f.filesInput()}[0]))
		require.NoError(t, err, "网络抖动不该挡住保存")
		assert.NotZero(t, resp.ID)
	})

	t.Run("数据库任务不校验路径", func(t *testing.T) {
		fs := f.usePaths(nil, map[string]error{"/x": fs.ErrNotExist})
		_, err := f.svc.Create(f.ctx, jobapi.NewCreateRequest(&[]jobapi.JobInput{f.mysqlInput()}[0]))
		require.NoError(t, err)
		assert.Empty(t, fs.calls)
	})
}
