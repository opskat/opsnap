package overview_ctr

import (
	"context"
	"testing"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/cago-frame/cago/server/mux/muxtest"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/opskat/opsnap/internal/api/overview"
	"github.com/opskat/opsnap/internal/model/entity/repo_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/repository/alert_repo"
	"github.com/opskat/opsnap/internal/repository/job_repo"
	"github.com/opskat/opsnap/internal/repository/notify_repo"
	"github.com/opskat/opsnap/internal/repository/repo_repo"
	"github.com/opskat/opsnap/internal/repository/run_repo"
	"github.com/opskat/opsnap/internal/repository/server_repo"
	"github.com/opskat/opsnap/internal/repository/setting_repo"
	"github.com/opskat/opsnap/internal/service/overview_svc"
	"github.com/opskat/opsnap/migrations"
)

func setup(t *testing.T) (context.Context, *muxtest.TestMux) {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, migrations.RunMigrations(gdb))
	db.SetDefault(gdb)
	job_repo.RegisterJob(job_repo.NewJob())
	run_repo.RegisterRun(run_repo.NewRun())
	repo_repo.RegisterRepo(repo_repo.NewRepo())
	server_repo.RegisterServer(server_repo.NewServer())
	setting_repo.RegisterSetting(setting_repo.NewSetting())
	notify_repo.RegisterNotify(notify_repo.NewNotify())
	alert_repo.RegisterAlert(alert_repo.NewAlert())
	overview_svc.RegisterOverview(overview_svc.NewOverview(overview_svc.Deps{
		Now: func() time.Time { return time.Date(2026, 10, 15, 16, 0, 0, 0, time.UTC) },
	}))
	ctx := db.WithContextDB(context.Background(), gdb)
	m := muxtest.NewTestMux()
	m.Group("/api/v1").Bind(NewOverview().Get)
	return ctx, m
}

func TestOverviewController(t *testing.T) {
	ctx, m := setup(t)
	require.NoError(t, repo_repo.Repo().Create(ctx, &repo_entity.Repo{
		Name: "nas", Type: repo_entity.TypeLocal, Status: repo_entity.StatusOK, StoredSize: 10, RawSize: 30, SnapshotCount: 2,
	}))

	resp := &overview.GetResponse{}
	require.NoError(t, m.Do(ctx, &overview.GetRequest{Window: "30d"}, resp))
	assert.Equal(t, "30d", resp.Timeline.Window)
	assert.Equal(t, overview.Totals{RawBytes: 30, StoredBytes: 10, Snapshots: 2}, resp.Totals)
	assert.Equal(t, 0, resp.Summary.Jobs)
	assert.Equal(t, 1, resp.Summary.Repos)

	err := m.Do(ctx, &overview.GetRequest{Window: "1y"}, &overview.GetResponse{})
	var he *httputils.Error
	require.ErrorAs(t, err, &he)
	assert.Equal(t, code.OverviewWindowInvalid, he.Code)
}
