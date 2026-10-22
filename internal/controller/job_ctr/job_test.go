package job_ctr

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/cago-frame/cago/server/mux/muxtest"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	jobapi "github.com/opskat/opsnap/internal/api/job"
	"github.com/opskat/opsnap/internal/model/entity/credential_entity"
	"github.com/opskat/opsnap/internal/model/entity/repo_entity"
	"github.com/opskat/opsnap/internal/model/entity/server_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/executor"
	"github.com/opskat/opsnap/internal/pkg/remotefs"
	"github.com/opskat/opsnap/internal/pkg/secret"
	"github.com/opskat/opsnap/internal/pkg/sshconn"
	"github.com/opskat/opsnap/internal/repository/credential_repo"
	"github.com/opskat/opsnap/internal/repository/job_repo"
	"github.com/opskat/opsnap/internal/repository/repo_repo"
	"github.com/opskat/opsnap/internal/repository/run_repo"
	"github.com/opskat/opsnap/internal/repository/server_repo"
	"github.com/opskat/opsnap/internal/repository/setting_repo"
	"github.com/opskat/opsnap/internal/service/job_svc"
	"github.com/opskat/opsnap/internal/service/repo_svc"
	"github.com/opskat/opsnap/migrations"
)

func setupJobTest(t *testing.T) (context.Context, *muxtest.TestMux, jobapi.JobInput) {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, migrations.RunMigrations(gdb))
	s, err := secret.New("job-ctr-test-key")
	require.NoError(t, err)
	secret.SetDefault(s)
	t.Cleanup(func() { secret.SetDefault(nil) })

	db.SetDefault(gdb)
	job_repo.RegisterJob(job_repo.NewJob())
	repo_repo.RegisterRepo(repo_repo.NewRepo())
	server_repo.RegisterServer(server_repo.NewServer())
	credential_repo.RegisterCredential(credential_repo.NewCredential())
	setting_repo.RegisterSetting(setting_repo.NewSetting())
	run_repo.RegisterRun(run_repo.NewRun())
	repo_svc.RegisterRepo(repo_svc.NewRepo(executor.NewFake(), time.Now))
	// 不做真实的 SSH 连接与目录读取
	job_svc.RegisterJob(job_svc.NewJob(job_svc.Deps{
		Tunnel: func(context.Context, int64, string) (string, func(), error) { return "", func() {}, nil },
		OpenFS: func(context.Context, int64) (job_svc.Browser, func(), error) {
			return fakeBrowser{}, func() {}, nil
		},
	}))
	ctx := db.WithContextDB(context.Background(), gdb)

	cred := &credential_entity.Credential{Kind: credential_entity.KindPassword, Password: "x"}
	require.NoError(t, credential_repo.Credential().Create(ctx, cred))
	srv := &server_entity.Server{Name: "web", Host: "10.0.0.1", Port: 22, Username: "root", CredentialID: cred.ID, Hops: []server_entity.Hop{}}
	require.NoError(t, server_repo.Server().Create(ctx, srv))
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, repo_entity.MarkerFile), nil, 0o600))
	rp := &repo_entity.Repo{Name: "nas", Type: repo_entity.TypeLocal, Params: repo_entity.Params{Path: dir}, OpsnapKey: "x", Status: repo_entity.StatusOK, FreeSpace: -1}
	require.NoError(t, repo_repo.Repo().Create(ctx, rp))

	testMux := muxtest.NewTestMux()
	ctr := NewJob()
	testMux.Group("/api/v1").Bind(ctr.List, ctr.Get, ctr.Create, ctr.Update, ctr.Delete, ctr.Duplicate,
		ctr.Pause, ctr.Resume, ctr.NextRuns, ctr.Databases, ctr.Paths, ctr.Test)
	in := jobapi.JobInput{
		Name: "site", SourceType: jobapi.SourceFiles, ServerID: srv.ID, Paths: []string{"/var/www"},
		RepoID:    rp.ID,
		Schedule:  jobapi.ScheduleInput{Kind: "daily", Time: "03:00"},
		Retention: jobapi.RetentionInput{Preset: "7d"},
	}
	return ctx, testMux, in
}

// fakeBrowser 目录浏览替身：所有路径都存在，/var/www 下有一个子目录
type fakeBrowser struct{}

func (fakeBrowser) List(_ context.Context, path string) ([]remotefs.Entry, error) {
	if path == "/var/www" {
		return []remotefs.Entry{{Name: "uploads", IsDir: true}}, nil
	}
	return nil, nil
}

func (fakeBrowser) Stat(context.Context, string) error { return nil }

// ListRequest 供 TestJobControllerPaths 断言返回形状
func TestJobControllerPaths(t *testing.T) {
	ctx, testMux, in := setupJobTest(t)
	resp := &jobapi.PathsResponse{}
	require.NoError(t, testMux.Do(ctx, &jobapi.PathsRequest{
		SourceType: in.SourceType, ServerID: in.ServerID, Paths: in.Paths,
		RepoID: in.RepoID, Schedule: in.Schedule, Retention: in.Retention,
		Path: "/var/www",
	}, resp))
	assert.True(t, resp.OK)
	assert.Equal(t, "/var/www", resp.Path)
	require.Len(t, resp.List, 1)
	assert.Equal(t, "uploads", resp.List[0].Name)

	t.Run("数据库任务不能浏览目录", func(t *testing.T) {
		err := testMux.Do(ctx, &jobapi.PathsRequest{SourceType: jobapi.SourceMySQL, ServerID: in.ServerID, Path: "/"}, &jobapi.PathsResponse{})
		var he *httputils.Error
		require.ErrorAs(t, err, &he)
	})

	t.Run("非法路径按字段错误返回 11401", func(t *testing.T) {
		err := testMux.Do(ctx, &jobapi.PathsRequest{
			SourceType: in.SourceType, ServerID: in.ServerID, Path: "/srv/../etc",
		}, &jobapi.PathsResponse{})
		var he *httputils.Error
		require.ErrorAs(t, err, &he)
		assert.Equal(t, code.JobFieldInvalid, he.Code)
		assert.Contains(t, he.Msg, "[path] ")
	})
}

// 保存时连不上服务器：放行，但要把「没能确认的路径」透到响应里，面板据此提示
func TestJobControllerSaveUnverifiedPaths(t *testing.T) {
	ctx, testMux, in := setupJobTest(t)
	job_svc.RegisterJob(job_svc.NewJob(job_svc.Deps{
		Tunnel: func(context.Context, int64, string) (string, func(), error) { return "", func() {}, nil },
		OpenFS: func(context.Context, int64) (job_svc.Browser, func(), error) {
			return nil, nil, fmt.Errorf("包装：%w", sshconn.ErrServerNotFound)
		},
	}))
	_ = in
	resp := &jobapi.CreateResponse{}
	err := testMux.Do(ctx, jobapi.NewCreateRequest(&in), resp)
	// 服务器不存在是永久错误，必须报出来而不是静默放行
	var he *httputils.Error
	require.ErrorAs(t, err, &he)
	assert.NotEqual(t, 0, he.Code)
}

func TestJobController(t *testing.T) {
	ctx, testMux, in := setupJobTest(t)

	created := &jobapi.CreateResponse{}
	require.NoError(t, testMux.Do(ctx, jobapi.NewCreateRequest(&in), created))
	assert.NotZero(t, created.ID)
	assert.NotZero(t, created.NextRunAt)

	got := &jobapi.GetResponse{}
	require.NoError(t, testMux.Do(ctx, &jobapi.GetRequest{ID: created.ID}, got))
	assert.Equal(t, "site", got.Name)
	assert.Equal(t, []string{"/var/www"}, got.Paths)

	in.Name = "site-2"
	upd := &jobapi.UpdateResponse{}
	require.NoError(t, testMux.Do(ctx, jobapi.NewUpdateRequest(created.ID, &in), upd))
	assert.Equal(t, "site-2", upd.Name)

	paused := &jobapi.PauseResponse{}
	require.NoError(t, testMux.Do(ctx, &jobapi.PauseRequest{ID: created.ID}, paused))
	assert.True(t, paused.Paused)
	assert.Zero(t, paused.NextRunAt)

	list := &jobapi.ListResponse{}
	require.NoError(t, testMux.Do(ctx, &jobapi.ListRequest{Filter: jobapi.FilterPaused, Q: "var/www"}, list))
	require.Len(t, list.List, 1)

	resumed := &jobapi.ResumeResponse{}
	require.NoError(t, testMux.Do(ctx, &jobapi.ResumeRequest{ID: created.ID}, resumed))
	assert.False(t, resumed.Paused)

	dup := &jobapi.DuplicateResponse{}
	require.NoError(t, testMux.Do(ctx, &jobapi.DuplicateRequest{ID: created.ID}, dup))
	assert.Equal(t, "site-2 副本", dup.Name)

	runs := &jobapi.NextRunsResponse{}
	require.NoError(t, testMux.Do(ctx, &jobapi.NextRunsRequest{Schedule: in.Schedule, Count: 3}, runs))
	assert.Len(t, runs.Times, 3)

	require.NoError(t, testMux.Do(ctx, &jobapi.DeleteRequest{ID: created.ID}, &jobapi.DeleteResponse{}))
	err := testMux.Do(ctx, &jobapi.GetRequest{ID: created.ID}, &jobapi.GetResponse{})
	var he *httputils.Error
	require.ErrorAs(t, err, &he)
	assert.Equal(t, code.JobNotFound, he.Code)
}

func TestJobControllerFieldErrorShape(t *testing.T) {
	ctx, testMux, in := setupJobTest(t)
	in.Name = ""
	in.Paths = nil
	err := testMux.Do(ctx, jobapi.NewCreateRequest(&in), &jobapi.CreateResponse{})
	var he *httputils.Error
	require.ErrorAs(t, err, &he)
	assert.Equal(t, code.JobFieldInvalid, he.Code)
	assert.Contains(t, he.Msg, "[name] ")
	assert.Contains(t, he.Msg, "[paths] ")
}

func TestJobControllerTest(t *testing.T) {
	ctx, testMux, in := setupJobTest(t)
	resp := &jobapi.TestResponse{}
	require.NoError(t, testMux.Do(ctx, jobapi.NewTestRequest(0, &in), resp))
	// 文件任务有 SSH 链路、目录可读与存储位置三项
	require.Len(t, resp.Checks, 3)
	assert.Equal(t, jobapi.CheckSSH, resp.Checks[0].Key)
	assert.Equal(t, jobapi.CheckPaths, resp.Checks[1].Key)
	assert.Equal(t, jobapi.CheckRepo, resp.Checks[2].Key)
	assert.True(t, resp.OK)
}
