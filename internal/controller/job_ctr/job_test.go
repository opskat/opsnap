package job_ctr

import (
	"context"
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
	"github.com/opskat/opsnap/internal/pkg/secret"
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
	// 不做真实的 SSH 连接
	job_svc.RegisterJob(job_svc.NewJob(job_svc.Deps{
		Tunnel: func(context.Context, int64, string) (string, func(), error) { return "", func() {}, nil },
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
		ctr.Pause, ctr.Resume, ctr.NextRuns, ctr.Databases, ctr.Test)
	in := jobapi.JobInput{
		Name: "site", SourceType: jobapi.SourceFiles, ServerID: srv.ID, Paths: []string{"/var/www"},
		RepoID:    rp.ID,
		Schedule:  jobapi.ScheduleInput{Kind: "daily", Time: "03:00"},
		Retention: jobapi.RetentionInput{Preset: "7d"},
	}
	return ctx, testMux, in
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
	// 文件任务只有 SSH 链路与存储位置两项
	require.Len(t, resp.Checks, 2)
	assert.Equal(t, jobapi.CheckSSH, resp.Checks[0].Key)
	assert.Equal(t, jobapi.CheckRepo, resp.Checks[1].Key)
	assert.True(t, resp.OK)
}
