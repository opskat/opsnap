package alert_svc

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/opskat/opsnap/internal/model/entity/job_entity"
	"github.com/opskat/opsnap/internal/model/entity/notify_entity"
	"github.com/opskat/opsnap/internal/model/entity/run_entity"
	"github.com/opskat/opsnap/internal/repository/alert_repo"
	"github.com/opskat/opsnap/internal/repository/job_repo"
	"github.com/opskat/opsnap/internal/repository/notify_repo"
	"github.com/opskat/opsnap/internal/repository/run_repo"
	"github.com/opskat/opsnap/internal/service/notify_svc"
	"github.com/opskat/opsnap/migrations"
)

var t0 = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

type sent struct {
	mu     sync.Mutex
	events []notify_svc.Event
}

func (s *sent) send(_ context.Context, e notify_svc.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
	return nil
}

func (s *sent) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.events)
}

type fixture struct {
	ctx  context.Context
	sent *sent
	now  time.Time
}

func (f *fixture) svc() AlertSvc {
	return NewAlert(Deps{Now: func() time.Time { return f.now }, Notify: f.sent.send})
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
	notify_repo.RegisterNotify(notify_repo.NewNotify())
	alert_repo.RegisterAlert(alert_repo.NewAlert())
	return &fixture{ctx: db.WithContextDB(context.Background(), gdb), sent: &sent{}, now: t0}
}

func (f *fixture) job(t *testing.T, name string, created time.Time, paused bool) *job_entity.Job {
	t.Helper()
	j := &job_entity.Job{
		Name: name, SourceType: job_entity.SourceRedis, ConnMode: job_entity.ConnDirect,
		ScheduleKind: "daily", ScheduleTime: "03:00", RetentionPreset: "30d", Compression: "auto",
		Databases: []string{}, Paths: []string{}, Excludes: []string{}, ExtraArgs: []string{},
		Paused: paused, Createtime: created.Unix(),
	}
	require.NoError(t, job_repo.Job().Create(f.ctx, j))
	return j
}

func (f *fixture) runAt(t *testing.T, j *job_entity.Job, status string, at time.Time) {
	t.Helper()
	r := run_entity.New(j.ID, j.Name, run_entity.TriggerSchedule, at)
	r.Finish(status, "", at, time.Second)
	require.NoError(t, run_repo.Run().Create(f.ctx, r))
}

func TestStaleFiresOncePerStaleStretch(t *testing.T) {
	f := setup(t)
	j := f.job(t, "orders", t0.Add(-49*time.Hour), false)

	require.NoError(t, f.svc().Check(f.ctx))
	require.Equal(t, 1, f.sent.count(), "超过 48 小时没有成功：发一条")
	ev := f.sent.events[0]
	assert.Equal(t, notify_entity.EventStale, ev.Type)
	assert.Equal(t, j.ID, ev.JobID)
	assert.Equal(t, "orders", ev.JobName)
	assert.True(t, ev.LastSuccessAt.IsZero(), "从未成功")
	assert.Equal(t, fmt.Sprintf("/jobs/%d/edit", j.ID), ev.Link, "链接指向面板里这个任务的页面")

	f.now = f.now.Add(time.Hour)
	require.NoError(t, f.svc().Check(f.ctx))
	require.NoError(t, f.svc().Check(f.ctx), "新实例（重启后）也不重复")
	assert.Equal(t, 1, f.sent.count())
}

func TestStaleBaselines(t *testing.T) {
	f := setup(t)
	young := f.job(t, "young", t0.Add(-47*time.Hour), false)
	oldOK := f.job(t, "old-ok", t0.Add(-200*time.Hour), false)
	f.runAt(t, oldOK, run_entity.StatusSuccess, t0.Add(-47*time.Hour))
	oldWarn := f.job(t, "old-warn", t0.Add(-200*time.Hour), false)
	f.runAt(t, oldWarn, run_entity.StatusWarning, t0.Add(-10*time.Hour))
	failing := f.job(t, "failing", t0.Add(-200*time.Hour), false)
	f.runAt(t, failing, run_entity.StatusSuccess, t0.Add(-60*time.Hour))
	f.runAt(t, failing, run_entity.StatusFailed, t0.Add(-1*time.Hour))
	f.runAt(t, failing, run_entity.StatusSkipped, t0.Add(-30*time.Minute))
	paused := f.job(t, "paused", t0.Add(-500*time.Hour), true)

	got, err := f.svc().Stale(f.ctx)
	require.NoError(t, err)
	require.Len(t, got, 1, "只有 failing 超时：成功过且在 48 小时内的、新建不足 48 小时的、已暂停的都不算")
	assert.Equal(t, failing.ID, got[0].JobID)
	assert.Equal(t, "failing", got[0].JobName)
	assert.Equal(t, t0.Add(-60*time.Hour).Unix(), got[0].LastSuccessAt.Unix())
	assert.Equal(t, t0.Add(-60*time.Hour+48*time.Hour).Unix(), got[0].Since.Unix(), "从超过阈值的那一刻起算")
	_, _, _ = young, paused, oldWarn

	require.NoError(t, f.svc().Check(f.ctx))
	require.Equal(t, 1, f.sent.count())
	assert.Equal(t, failing.ID, f.sent.events[0].JobID)
	assert.Equal(t, t0.Add(-60*time.Hour).Unix(), f.sent.events[0].LastSuccessAt.Unix())
}

func TestStaleThresholdFromNotifySettings(t *testing.T) {
	f := setup(t)
	f.job(t, "a", t0.Add(-25*time.Hour), false)
	n := notify_entity.Default()
	n.StaleHours = 24
	require.NoError(t, notify_repo.Notify().Save(f.ctx, n))

	got, err := f.svc().Stale(f.ctx)
	require.NoError(t, err)
	assert.Len(t, got, 1)
}

func TestStaleAlertRemembersEvenWhenSendFails(t *testing.T) {
	f := setup(t)
	f.job(t, "a", t0.Add(-100*time.Hour), false)
	calls := 0
	failing := NewAlert(Deps{Now: func() time.Time { return f.now }, Notify: func(context.Context, notify_svc.Event) error {
		calls++
		return assert.AnError
	}})
	require.Error(t, failing.Check(f.ctx))
	require.NoError(t, failing.Check(f.ctx))
	assert.Equal(t, 1, calls, "通知服务已重试后放弃，不在每次检查时重发")
}

func TestClearReportsWhetherAlerted(t *testing.T) {
	f := setup(t)
	j := f.job(t, "a", t0.Add(-100*time.Hour), false)
	require.NoError(t, f.svc().Check(f.ctx))

	was, err := alert_repo.Alert().Clear(f.ctx, j.ID)
	require.NoError(t, err)
	assert.True(t, was)
	was, err = alert_repo.Alert().Clear(f.ctx, j.ID)
	require.NoError(t, err)
	assert.False(t, was, "只返回一次，恢复正常据此只发一条")
}

func TestDeletedJobAlertStateIsDropped(t *testing.T) {
	f := setup(t)
	j := f.job(t, "a", t0.Add(-100*time.Hour), false)
	require.NoError(t, f.svc().Check(f.ctx))
	require.NoError(t, job_repo.Job().Delete(f.ctx, j.ID))
	require.NoError(t, f.svc().Check(f.ctx))
	was, err := alert_repo.Alert().Clear(f.ctx, j.ID)
	require.NoError(t, err)
	assert.False(t, was)
}
