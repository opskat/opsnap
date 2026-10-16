package overview_svc

import (
	"context"
	"testing"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	jobapi "github.com/opskat/opsnap/internal/api/job"
	"github.com/opskat/opsnap/internal/api/overview"
	"github.com/opskat/opsnap/internal/model/entity/job_entity"
	"github.com/opskat/opsnap/internal/model/entity/repo_entity"
	"github.com/opskat/opsnap/internal/model/entity/run_entity"
	"github.com/opskat/opsnap/internal/model/entity/server_entity"
	"github.com/opskat/opsnap/internal/model/entity/setting_entity"
	"github.com/opskat/opsnap/internal/repository/alert_repo"
	"github.com/opskat/opsnap/internal/repository/job_repo"
	"github.com/opskat/opsnap/internal/repository/notify_repo"
	"github.com/opskat/opsnap/internal/repository/repo_repo"
	"github.com/opskat/opsnap/internal/repository/run_repo"
	"github.com/opskat/opsnap/internal/repository/server_repo"
	"github.com/opskat/opsnap/internal/repository/setting_repo"
	"github.com/opskat/opsnap/internal/service/alert_svc"
	"github.com/opskat/opsnap/migrations"
)

// 固定的「现在」；设置的时区缺省为 UTC，所以日界线和这里的 UTC 一致
var t0 = time.Date(2026, 10, 15, 16, 0, 0, 0, time.UTC)

type fixture struct {
	ctx     context.Context
	planned []PlannedRun
	// plannedArgs 最近一次向计划来源传入的窗口
	plannedFrom, plannedTo time.Time
	repoID                 int64
}

func (f *fixture) svc() OverviewSvc {
	return NewOverview(Deps{
		Now:   func() time.Time { return t0 },
		Stale: alert_svc.NewAlert(alert_svc.Deps{Now: func() time.Time { return t0 }}).Stale,
		Planned: func(from, to time.Time) ([]PlannedRun, error) {
			f.plannedFrom, f.plannedTo = from, to
			var out []PlannedRun
			for _, p := range f.planned {
				if p.At.After(from) && !p.At.After(to) {
					out = append(out, PlannedRun{JobID: p.JobID, At: p.At})
				}
			}
			return out, nil
		},
	})
}

func setup(t *testing.T) *fixture {
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
	f := &fixture{ctx: db.WithContextDB(context.Background(), gdb)}
	st := setting_entity.Default()
	st.Timezone = "UTC"
	require.NoError(t, setting_repo.Setting().Save(f.ctx, st))
	f.repoID = f.repo(t, "nas", repo_entity.StatusOK, 100, 400, 7).ID
	return f
}

func (f *fixture) repo(t *testing.T, name, status string, stored, raw int64, snaps int) *repo_entity.Repo {
	t.Helper()
	r := &repo_entity.Repo{
		Name: name, Type: repo_entity.TypeLocal, Status: status,
		StoredSize: stored, RawSize: raw, SnapshotCount: snaps, FreeSpace: repo_entity.FreeSpaceUnknown,
	}
	require.NoError(t, repo_repo.Repo().Create(f.ctx, r))
	return r
}

func (f *fixture) job(t *testing.T, name string, created time.Time, paused bool) *job_entity.Job {
	t.Helper()
	j := &job_entity.Job{
		Name: name, SourceType: job_entity.SourceRedis, ConnMode: job_entity.ConnDirect, RepoID: f.repoID,
		ScheduleKind: "daily", ScheduleTime: "03:00", RetentionPreset: "30d", Compression: "auto",
		Databases: []string{}, Paths: []string{}, Excludes: []string{}, ExtraArgs: []string{},
		Paused: paused, Createtime: created.Unix(),
	}
	require.NoError(t, job_repo.Job().Create(f.ctx, j))
	return j
}

func (f *fixture) run(t *testing.T, j *job_entity.Job, status, reason string, at time.Time) *run_entity.Run {
	t.Helper()
	r := run_entity.New(j.ID, j.Name, run_entity.TriggerSchedule, at)
	if status != run_entity.StatusRunning {
		r.Finish(status, reason, at.Add(time.Minute), time.Minute)
	}
	require.NoError(t, run_repo.Run().Create(f.ctx, r))
	return r
}

func (f *fixture) get(t *testing.T, window string) *overview.GetResponse {
	t.Helper()
	resp, err := f.svc().Get(f.ctx, &overview.GetRequest{Window: window})
	require.NoError(t, err)
	return resp
}

func TestAttentionListsFailedWarningAndStale(t *testing.T) {
	f := setup(t)
	old := t0.Add(-200 * time.Hour)
	ok := f.job(t, "ok", old, false)
	f.run(t, ok, run_entity.StatusSuccess, "", t0.Add(-3*time.Hour))
	bad := f.job(t, "bad", old, false)
	f.run(t, bad, run_entity.StatusSuccess, "", t0.Add(-30*time.Hour))
	f.run(t, bad, run_entity.StatusFailed, "连接超时", t0.Add(-26*time.Hour))
	f.run(t, bad, run_entity.StatusFailed, "连接超时 2", t0.Add(-2*time.Hour))
	warn := f.job(t, "warn", old, false)
	w := f.run(t, warn, run_entity.StatusWarning, "跳过了 3 个文件", t0.Add(-4*time.Hour))
	stale := f.job(t, "stale", old, false)
	f.run(t, stale, run_entity.StatusSuccess, "", t0.Add(-60*time.Hour))
	f.run(t, stale, run_entity.StatusSkipped, "", t0.Add(-1*time.Hour))
	paused := f.job(t, "paused", old, true)
	_ = paused

	got := f.get(t, "7d")
	byName := map[string]*overview.AttentionItem{}
	for _, it := range got.Attention {
		byName[it.JobName] = it
	}
	require.Len(t, got.Attention, 3, "ok 与 paused 不出现")

	b := byName["bad"]
	require.NotNil(t, b)
	assert.Equal(t, overview.KindFailed, b.Kind)
	assert.Equal(t, "连接超时 2", b.Reason)
	assert.Equal(t, t0.Add(-26*time.Hour).Unix(), b.Since, "从连续失败的第一次算起，而不是最近一次")
	assert.NotZero(t, b.RunID)
	assert.Equal(t, f.repoID, b.RepoID)
	assert.False(t, b.RepoNotMounted)

	wi := byName["warn"]
	require.NotNil(t, wi)
	assert.Equal(t, overview.KindWarning, wi.Kind)
	assert.Equal(t, "跳过了 3 个文件", wi.Reason)
	assert.Equal(t, w.ID, wi.RunID)
	assert.Equal(t, w.StartedAt, wi.Since)

	s := byName["stale"]
	require.NotNil(t, s)
	assert.Equal(t, overview.KindStale, s.Kind)
	assert.NotEmpty(t, s.Reason)
	assert.Equal(t, t0.Add(-60*time.Hour).Add(time.Minute).Unix(), s.LastSuccessAt)
	assert.Equal(t, s.LastSuccessAt+48*3600, s.Since, "越过阈值的时刻")
	assert.NotZero(t, s.RunID, "查看日志指向最近一次有结果的运行")

	assert.Equal(t, overview.Summary{Jobs: 5, Attention: 3, Paused: 1, OK: 1, Servers: 0, Repos: 1}, got.Summary)
}

func TestFailedAndStaleJobIsListedOnceAsFailed(t *testing.T) {
	f := setup(t)
	j := f.job(t, "both", t0.Add(-300*time.Hour), false)
	f.run(t, j, run_entity.StatusSuccess, "", t0.Add(-100*time.Hour))
	f.run(t, j, run_entity.StatusFailed, "磁盘满", t0.Add(-5*time.Hour))

	got := f.get(t, "7d").Attention
	require.Len(t, got, 1)
	assert.Equal(t, overview.KindFailed, got[0].Kind)
	assert.Equal(t, t0.Add(-100*time.Hour).Add(time.Minute).Unix(), got[0].LastSuccessAt, "带上次成功时间")
}

func TestAttentionFlagsUnmountedRepoAndNeverRun(t *testing.T) {
	f := setup(t)
	nas := f.repo(t, "feiniu", repo_entity.StatusNotMounted, 0, 0, 0)
	j := f.job(t, "files", t0.Add(-1*time.Hour), false)
	j.RepoID = nas.ID
	require.NoError(t, job_repo.Job().Update(f.ctx, j))
	f.run(t, j, run_entity.StatusFailed, "没有挂载", t0.Add(-30*time.Minute))

	got := f.get(t, "7d").Attention
	require.Len(t, got, 1)
	assert.True(t, got[0].RepoNotMounted)
	assert.Equal(t, nas.ID, got[0].RepoID)
}

func TestNoAttentionIsEmptyNotNull(t *testing.T) {
	f := setup(t)
	j := f.job(t, "ok", t0.Add(-100*time.Hour), false)
	f.run(t, j, run_entity.StatusSuccess, "", t0.Add(-2*time.Hour))
	running := f.job(t, "running", t0.Add(-100*time.Hour), false)
	f.run(t, running, run_entity.StatusSuccess, "", t0.Add(-5*time.Hour))
	f.run(t, running, run_entity.StatusRunning, "", t0.Add(-1*time.Minute))

	got := f.get(t, "7d")
	assert.NotNil(t, got.Attention)
	assert.Empty(t, got.Attention)
	assert.Equal(t, 2, got.Summary.OK, "运行中的任务算运行正常")
	assert.Equal(t, 0, got.Summary.Attention)
}

func TestTotalsAndRepoCards(t *testing.T) {
	f := setup(t)
	f.repo(t, "feiniu", repo_entity.StatusNotMounted, 0, 0, 0)
	f.job(t, "a", t0, false)
	f.job(t, "b", t0, false)
	require.NoError(t, server_repo.Server().Create(f.ctx, &server_entity.Server{Name: "web-01"}))

	got := f.get(t, "7d")
	assert.Equal(t, overview.Totals{RawBytes: 400, StoredBytes: 100, Snapshots: 7}, got.Totals)
	assert.Equal(t, 1, got.Summary.Servers)
	require.Len(t, got.Repos, 2)
	nas := got.Repos[0]
	assert.Equal(t, "nas", nas.Name)
	assert.InDelta(t, 4.0, nas.Ratio, 1e-9)
	assert.Equal(t, 2, nas.Jobs)
	assert.Equal(t, 7, nas.Snapshots)
	assert.Equal(t, overview.RepoCard{
		ID: got.Repos[1].ID, Name: "feiniu", Type: repo_entity.TypeLocal, Status: repo_entity.StatusNotMounted,
		FreeSpace: repo_entity.FreeSpaceUnknown,
	}, *got.Repos[1], "没有数据时压缩比为 0，未挂载状态原样给出")
}

func TestTimelinePastAndPlanned(t *testing.T) {
	f := setup(t)
	j := f.job(t, "daily", t0.Add(-900*time.Hour), false)
	p := f.job(t, "paused", t0.Add(-900*time.Hour), true)
	f.run(t, j, run_entity.StatusSuccess, "", t0.Add(-8*24*time.Hour)) // 窗口之外
	inA := f.run(t, j, run_entity.StatusFailed, "x", t0.Add(-30*time.Hour))
	inB := f.run(t, j, run_entity.StatusSuccess, "", t0.Add(-6*time.Hour))
	f.run(t, j, run_entity.StatusSkipped, "", t0.Add(-5*time.Hour))
	f.run(t, p, run_entity.StatusSuccess, "", t0.Add(-7*time.Hour))
	f.planned = []PlannedRun{
		{JobID: j.ID, At: t0.Add(11 * time.Hour)},
		{JobID: j.ID, At: t0.Add(31 * time.Hour)}, // 超出 30 小时
	}

	got := f.get(t, "7d")
	assert.Equal(t, t0, f.plannedFrom)
	assert.Equal(t, t0.Add(30*time.Hour), f.plannedTo, "7 天窗口向后显示 30 小时")
	tl := got.Timeline
	assert.Equal(t, "7d", tl.Window)
	assert.Equal(t, 30, tl.AheadHours)
	assert.Equal(t, time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC).Unix(), tl.From, "含今天共 7 天")
	assert.Equal(t, t0.Add(30*time.Hour).Unix(), tl.To)
	require.Len(t, tl.Jobs, 2)
	tj := tl.Jobs[0]
	assert.Equal(t, "daily", tj.Name)
	require.Len(t, tj.Runs, 2, "窗口外和跳过的运行不出现")
	assert.Equal(t, inA.ID, tj.Runs[0].ID)
	assert.Equal(t, "failed", tj.Runs[0].Status)
	assert.Equal(t, inB.StartedAt, tj.Runs[1].StartedAt, "升序")
	assert.Equal(t, []int64{t0.Add(11 * time.Hour).Unix()}, tj.Planned)
	assert.Equal(t, "success", tj.LastStatus)
	assert.Equal(t, jobapi.ScheduleInput{Kind: "daily", Time: "03:00"}, tj.Schedule, "轨道标题下写计划")
	assert.Equal(t, time.Date(2026, 10, 16, 3, 0, 0, 0, time.UTC).Unix(), tj.NextRunAt)
	pj := tl.Jobs[1]
	assert.True(t, pj.Paused)
	assert.Zero(t, pj.NextRunAt)
	assert.Empty(t, pj.Planned)
	assert.NotNil(t, pj.Planned)

	got = f.get(t, "30d")
	assert.Equal(t, 72, got.Timeline.AheadHours)
	assert.Equal(t, t0.Add(72*time.Hour), f.plannedTo)
	assert.Equal(t, time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC).Unix(), got.Timeline.From)
	assert.Len(t, got.Timeline.Jobs[0].Runs, 3, "30 天窗口包含 8 天前那次")
	assert.Len(t, got.Timeline.Jobs[0].Planned, 2)
}

func TestWindowDefaultsAndRejectsUnknown(t *testing.T) {
	f := setup(t)
	assert.Equal(t, "7d", f.get(t, "").Timeline.Window)
	_, err := f.svc().Get(f.ctx, &overview.GetRequest{Window: "90d"})
	assert.Error(t, err)
}

// runGone 模拟两次查询之间运行记录被清理：最近状态还在，记录本身已经查不到
type runGone struct{ run_repo.RunRepo }

func (runGone) PreviousFinished(context.Context, int64, int64) (*run_entity.Run, error) {
	return nil, nil
}

func TestAttentionSurvivesRunDeletedMeanwhile(t *testing.T) {
	f := setup(t)
	j := f.job(t, "gone", t0.Add(-300*time.Hour), false)
	f.run(t, j, run_entity.StatusFailed, "磁盘满", t0.Add(-5*time.Hour))
	run_repo.RegisterRun(runGone{run_repo.NewRun()})
	t.Cleanup(func() { run_repo.RegisterRun(run_repo.NewRun()) })

	got := f.get(t, "7d").Attention
	require.Len(t, got, 1)
	assert.Equal(t, overview.KindFailed, got[0].Kind)
}
