package run_ctr

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/cago-frame/cago/server/mux/muxtest"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	runapi "github.com/opskat/opsnap/internal/api/run"
	"github.com/opskat/opsnap/internal/model/entity/job_entity"
	"github.com/opskat/opsnap/internal/model/entity/repo_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/executor"
	"github.com/opskat/opsnap/internal/pkg/secret"
	"github.com/opskat/opsnap/internal/repository/alert_repo"
	"github.com/opskat/opsnap/internal/repository/job_repo"
	"github.com/opskat/opsnap/internal/repository/repo_repo"
	"github.com/opskat/opsnap/internal/repository/run_repo"
	"github.com/opskat/opsnap/internal/repository/server_repo"
	"github.com/opskat/opsnap/internal/repository/setting_repo"
	"github.com/opskat/opsnap/internal/service/notify_svc"
	"github.com/opskat/opsnap/internal/service/repo_svc"
	"github.com/opskat/opsnap/internal/service/run_svc"
	"github.com/opskat/opsnap/migrations"
)

const dbPassword = "SECRET-DB-PW" //nolint:gosec // 测试用的假凭据

func setupRunTest(t *testing.T) (context.Context, *muxtest.TestMux, *executor.Fake, int64) {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, migrations.RunMigrations(gdb))
	s, err := secret.New("run-ctr-test-key")
	require.NoError(t, err)
	secret.SetDefault(s)
	t.Cleanup(func() { secret.SetDefault(nil) })

	db.SetDefault(gdb)
	job_repo.RegisterJob(job_repo.NewJob())
	repo_repo.RegisterRepo(repo_repo.NewRepo())
	server_repo.RegisterServer(server_repo.NewServer())
	setting_repo.RegisterSetting(setting_repo.NewSetting())
	run_repo.RegisterRun(run_repo.NewRun())
	alert_repo.RegisterAlert(alert_repo.NewAlert())
	fake := executor.NewFake()
	repo_svc.RegisterRepo(repo_svc.NewRepo(fake, time.Now))
	run_svc.RegisterRun(run_svc.NewRun(run_svc.Deps{
		Runner: fake, DataDir: t.TempDir(), Self: "/opt/opsnap",
		Notify: func(context.Context, notify_svc.Event) error { return nil },
	}))
	ctx := db.WithContextDB(context.Background(), gdb)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, repo_entity.MarkerFile), nil, 0o600))
	key, err := secret.Default().Encrypt("SECRET-REPO-KEY")
	require.NoError(t, err)
	rp := &repo_entity.Repo{Name: "nas", Type: repo_entity.TypeLocal, Params: repo_entity.Params{Path: dir}, OpsnapKey: key, Status: repo_entity.StatusOK, FreeSpace: -1}
	require.NoError(t, repo_repo.Repo().Create(ctx, rp))
	pw, err := secret.Default().Encrypt(dbPassword)
	require.NoError(t, err)
	j := &job_entity.Job{
		Name: "cache", SourceType: job_entity.SourceRedis, ConnMode: job_entity.ConnDirect, DBHost: "10.0.0.1", DBPort: 6379,
		DBPassword: pw, RepoID: rp.ID, ScheduleKind: "daily", ScheduleTime: "03:00", RetentionPreset: "7d",
		TimeoutSec: 3600, Compression: "auto", Databases: []string{}, Paths: []string{}, Excludes: []string{}, ExtraArgs: []string{},
	}
	require.NoError(t, job_repo.Job().Create(ctx, j))

	testMux := muxtest.NewTestMux()
	ctr := NewRun()
	testMux.Group("/api/v1").Bind(ctr.RunJob, ctr.List, ctr.Get, ctr.Stop, ctr.Log, ctr.StreamLog)
	return ctx, testMux, fake, j.ID
}

func codeOf(t *testing.T, err error) int {
	t.Helper()
	var he *httputils.Error
	require.ErrorAs(t, err, &he, "%v", err)
	return he.Code
}

func waitDone(t *testing.T, ctx context.Context, testMux *muxtest.TestMux, id int64) *runapi.GetResponse {
	t.Helper()
	var got *runapi.GetResponse
	require.Eventually(t, func() bool {
		got = &runapi.GetResponse{}
		require.NoError(t, testMux.Do(ctx, &runapi.GetRequest{ID: id}, got))
		return got.Status != runapi.StatusRunning
	}, 10*time.Second, 20*time.Millisecond)
	return got
}

func TestRunController(t *testing.T) {
	ctx, testMux, fake, jobID := setupRunTest(t)
	fake.On(executor.Match("restic", "backup"), executor.Step{
		Output: "warning: dump used password " + dbPassword + "\n" +
			`{"message_type":"summary","data_added":5,"total_bytes_processed":9,"snapshot_id":"abcdef12"}` + "\n",
	})

	resp := &runapi.RunJobResponse{}
	require.NoError(t, testMux.Do(ctx, &runapi.RunJobRequest{ID: jobID}, resp))
	require.NotZero(t, resp.RunID)
	got := waitDone(t, ctx, testMux, resp.RunID)
	assert.Equal(t, runapi.StatusSuccess, got.Status)
	assert.Equal(t, []string{"abcdef12"}, got.SnapshotIDs)
	assert.Equal(t, "manual", got.Trigger)

	list := &runapi.ListResponse{}
	require.NoError(t, testMux.Do(ctx, &runapi.ListRequest{JobID: jobID}, list))
	assert.Equal(t, int64(1), list.Total)

	logs := &runapi.LogResponse{}
	require.NoError(t, testMux.Do(ctx, &runapi.LogRequest{ID: resp.RunID, WarningsOnly: true}, logs))
	require.NotEmpty(t, logs.Lines)
	assert.False(t, logs.Running)
	for _, l := range logs.Lines {
		assert.NotContains(t, l.Text, dbPassword)
		assert.NotEqual(t, "info", l.Level)
	}

	err := testMux.Do(ctx, &runapi.StopRequest{ID: resp.RunID}, &runapi.StopResponse{})
	assert.Equal(t, code.RunStopNotConfirmed, codeOf(t, err))
	err = testMux.Do(ctx, &runapi.StopRequest{ID: resp.RunID, Confirm: true}, &runapi.StopResponse{})
	assert.Equal(t, code.RunNotRunning, codeOf(t, err))
	err = testMux.Do(ctx, &runapi.GetRequest{ID: 999}, &runapi.GetResponse{})
	assert.Equal(t, code.RunNotFound, codeOf(t, err))

	t.Run("实时日志：text/event-stream，逐行推送，结束事件带状态，不含凭据", func(t *testing.T) {
		engine, ok := testMux.IRouter.(*gin.Engine)
		require.True(t, ok)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/runs/%d/log/stream", resp.RunID), nil))
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "text/event-stream", w.Header().Get("Content-Type"))
		body := w.Body.String()
		assert.Contains(t, body, "event: line\ndata: {")
		assert.Contains(t, body, "event: end\ndata: {\"status\":\"success\"")
		assert.True(t, strings.HasSuffix(body, "\n\n"))
		assert.NotContains(t, body, dbPassword)
		assert.Contains(t, body, "******")

		w = httptest.NewRecorder()
		engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/runs/999/log/stream", nil))
		assert.Contains(t, w.Body.String(), fmt.Sprint(code.RunNotFound), "找不到时按普通错误返回 JSON")
	})

	t.Run("同一任务正在运行时拒绝", func(t *testing.T) {
		fake.On(executor.Match("restic"), executor.Step{Block: true})
		first := &runapi.RunJobResponse{}
		require.NoError(t, testMux.Do(ctx, &runapi.RunJobRequest{ID: jobID}, first))
		err := testMux.Do(ctx, &runapi.RunJobRequest{ID: jobID}, &runapi.RunJobResponse{})
		assert.Equal(t, code.RunAlreadyRunning, codeOf(t, err))
		require.NoError(t, testMux.Do(ctx, &runapi.StopRequest{ID: first.RunID, Confirm: true}, &runapi.StopResponse{}))
		got := waitDone(t, ctx, testMux, first.RunID)
		assert.Equal(t, runapi.StatusFailed, got.Status)
		assert.Equal(t, "手动停止", got.Reason)
	})
}
