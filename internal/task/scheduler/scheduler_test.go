package scheduler

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/opskat/opsnap/internal/model/entity/job_entity"
	"github.com/opskat/opsnap/internal/model/entity/repo_entity"
	"github.com/opskat/opsnap/internal/model/entity/run_entity"
	"github.com/opskat/opsnap/internal/model/entity/setting_entity"
	"github.com/opskat/opsnap/internal/repository/job_repo"
	"github.com/opskat/opsnap/internal/repository/repo_repo"
	"github.com/opskat/opsnap/internal/repository/run_repo"
	"github.com/opskat/opsnap/internal/repository/setting_repo"
	"github.com/opskat/opsnap/internal/service/run_svc"
	"github.com/opskat/opsnap/migrations"
)

func loc(t *testing.T, name string) *time.Location {
	t.Helper()
	l, err := time.LoadLocation(name)
	require.NoError(t, err)
	return l
}

// fakeClock 手动推进的时钟：After 注册的定时器只在 Advance 越过它时才触发
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

type fakeTimer struct {
	at time.Time
	ch chan time.Time
}

func newClock(now time.Time) *fakeClock { return &fakeClock{now: now} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	tm := &fakeTimer{at: c.now.Add(d), ch: make(chan time.Time, 1)}
	c.timers = append(c.timers, tm)
	return tm.ch
}

func (c *fakeClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
	rest := c.timers[:0]
	for _, tm := range c.timers {
		if !tm.at.After(t) {
			tm.ch <- t
		} else {
			rest = append(rest, tm)
		}
	}
	c.timers = rest
}

func (c *fakeClock) pending() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.timers)
}

type call struct {
	job  int64
	trig run_entity.Trigger
	at   time.Time
}

type fixture struct {
	ctx      context.Context
	clock    *fakeClock
	mu       sync.Mutex
	triggers []call
	busy     map[int64]bool
	maintain []int64
	stale    int
}

func (f *fixture) trigger(_ context.Context, jobID int64, trig run_entity.Trigger) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.busy[jobID] {
		return 0, run_svc.ErrAlreadyRunning
	}
	f.triggers = append(f.triggers, call{jobID, trig, f.clock.Now()})
	return int64(len(f.triggers)), nil
}

func (f *fixture) calls() []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]call(nil), f.triggers...)
}

func (f *fixture) deps() Deps {
	return Deps{
		Clock:   f.clock,
		Trigger: f.trigger,
		Maintain: func(_ context.Context, id int64) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.maintain = append(f.maintain, id)
			return nil
		},
		CheckStale: func(context.Context) error { f.mu.Lock(); f.stale++; f.mu.Unlock(); return nil },
		Spawn:      func(fn func()) { fn() },
	}
}

// newScheduler 新建调度器实例（相当于一次进程启动）
func (f *fixture) newScheduler() *Scheduler { return New(f.deps()) }

func setup(t *testing.T, tz string, now time.Time) *fixture {
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
	repo_repo.RegisterRepo(repo_repo.NewRepo())
	run_repo.RegisterRun(run_repo.NewRun())
	setting_repo.RegisterSetting(setting_repo.NewSetting())
	f := &fixture{ctx: db.WithContextDB(context.Background(), gdb), clock: newClock(now), busy: map[int64]bool{}}
	f.setting(t, func(s *setting_entity.Setting) { s.Timezone = tz })
	return f
}

func (f *fixture) setting(t *testing.T, mutate func(*setting_entity.Setting)) {
	t.Helper()
	st := setting_entity.Default()
	mutate(st)
	require.NoError(t, setting_repo.Setting().Save(f.ctx, st))
}

func (f *fixture) job(t *testing.T, name, kind, clock string, paused bool) *job_entity.Job {
	t.Helper()
	j := &job_entity.Job{
		Name: name, SourceType: job_entity.SourceRedis, ConnMode: job_entity.ConnDirect,
		ScheduleKind: kind, ScheduleTime: clock, RetentionPreset: "30d", Compression: "auto",
		Databases: []string{}, Paths: []string{}, Excludes: []string{}, ExtraArgs: []string{},
		Paused: paused, Createtime: f.clock.Now().Unix(),
	}
	require.NoError(t, job_repo.Job().Create(f.ctx, j))
	return j
}

func (f *fixture) at(t *testing.T, s *Scheduler, now time.Time) {
	t.Helper()
	f.clock.Set(now)
	s.Tick(f.ctx)
}

func TestDueJobTriggersOnceInShanghai(t *testing.T) {
	sh := loc(t, "Asia/Shanghai")
	f := setup(t, "Asia/Shanghai", time.Date(2026, 10, 15, 2, 59, 0, 0, sh))
	j := f.job(t, "nightly", "daily", "03:00", false)
	s := f.newScheduler()

	f.at(t, s, time.Date(2026, 10, 15, 2, 59, 30, 0, sh))
	assert.Empty(t, f.calls(), "还没到点")
	// 03:00 +08:00 是 19:00 UTC；按 UTC 解释的实现会在 03:00 UTC 触发
	f.at(t, s, time.Date(2026, 10, 14, 19, 0, 5, 0, time.UTC))
	require.Len(t, f.calls(), 1)
	assert.Equal(t, j.ID, f.calls()[0].job)
	assert.Equal(t, run_entity.TriggerSchedule, f.calls()[0].trig)

	f.at(t, s, time.Date(2026, 10, 15, 3, 0, 40, 0, sh))
	f.at(t, s, time.Date(2026, 10, 15, 12, 0, 0, 0, sh))
	assert.Len(t, f.calls(), 1, "同一次到点只触发一次")
	f.at(t, s, time.Date(2026, 10, 16, 3, 0, 1, 0, sh))
	assert.Len(t, f.calls(), 2, "第二天再触发")
}

func TestDueJobFollowsDSTWallClock(t *testing.T) {
	ny := loc(t, "America/New_York")
	f := setup(t, "America/New_York", time.Date(2026, 3, 7, 10, 0, 0, 0, ny))
	f.job(t, "nine", "daily", "09:00", false)
	s := f.newScheduler()
	s.Tick(f.ctx)

	// 3 月 8 日 02:00 起夏令时：09:00 EDT = 13:00 UTC（冬令时是 14:00 UTC）
	f.at(t, s, time.Date(2026, 3, 8, 12, 59, 0, 0, time.UTC))
	assert.Empty(t, f.calls())
	f.at(t, s, time.Date(2026, 3, 8, 13, 0, 10, 0, time.UTC))
	assert.Len(t, f.calls(), 1)
}

func TestAlreadyRunningRecordsSkippedRun(t *testing.T) {
	sh := loc(t, "Asia/Shanghai")
	f := setup(t, "Asia/Shanghai", time.Date(2026, 10, 15, 2, 59, 0, 0, sh))
	j := f.job(t, "slow", "daily", "03:00", false)
	f.busy[j.ID] = true
	s := f.newScheduler()
	s.Tick(f.ctx)

	f.at(t, s, time.Date(2026, 10, 15, 3, 0, 5, 0, sh))
	assert.Empty(t, f.calls())
	runs, total, err := run_repo.Run().List(f.ctx, run_repo.ListFilter{JobID: j.ID})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	r := runs[0]
	assert.Equal(t, run_entity.StatusSkipped, r.Status)
	assert.Equal(t, "上一次运行尚未结束，本次跳过", r.Reason)
	assert.Equal(t, string(run_entity.TriggerSchedule), r.Trigger)
	assert.Equal(t, "slow", r.JobName)
	assert.False(t, r.Running())

	f.at(t, s, time.Date(2026, 10, 15, 3, 0, 35, 0, sh))
	_, total, err = run_repo.Run().List(f.ctx, run_repo.ListFilter{JobID: j.ID})
	require.NoError(t, err)
	assert.EqualValues(t, 1, total, "同一次到点只记一条")
}

func TestRestartDoesNotCatchUp(t *testing.T) {
	sh := loc(t, "Asia/Shanghai")
	f := setup(t, "Asia/Shanghai", time.Date(2026, 10, 15, 2, 0, 0, 0, sh))
	f.job(t, "nightly", "daily", "03:00", false)
	old := f.newScheduler()
	old.Tick(f.ctx)

	// 进程停到 10 月 15 日 08:00，错过了 03:00
	f.clock.Set(time.Date(2026, 10, 15, 8, 0, 0, 0, sh))
	fresh := f.newScheduler()
	fresh.Tick(f.ctx)
	assert.Empty(t, f.calls(), "重启后不补跑")
	f.at(t, fresh, time.Date(2026, 10, 15, 8, 0, 30, 0, sh))
	assert.Empty(t, f.calls())
	f.at(t, fresh, time.Date(2026, 10, 16, 3, 0, 1, 0, sh))
	assert.Len(t, f.calls(), 1, "按下一次计划时间继续")
}

func TestLongStallInsideProcessRunsAtMostOnceAndOnlyWhenRecent(t *testing.T) {
	sh := loc(t, "Asia/Shanghai")
	f := setup(t, "Asia/Shanghai", time.Date(2026, 10, 15, 2, 0, 0, 0, sh))
	f.job(t, "nightly", "daily", "03:00", false)
	s := f.newScheduler()
	s.Tick(f.ctx)
	// 进程被挂起 5 小时后醒来：错过超过宽限，当作错过处理，不补跑
	f.at(t, s, time.Date(2026, 10, 15, 8, 0, 0, 0, sh))
	assert.Empty(t, f.calls())
}

func TestPausedJobNeverTriggersUntilResumed(t *testing.T) {
	sh := loc(t, "Asia/Shanghai")
	f := setup(t, "Asia/Shanghai", time.Date(2026, 10, 15, 2, 59, 0, 0, sh))
	j := f.job(t, "p", "daily", "03:00", true)
	s := f.newScheduler()
	s.Tick(f.ctx)
	f.at(t, s, time.Date(2026, 10, 15, 3, 0, 5, 0, sh))
	assert.Empty(t, f.calls())

	require.NoError(t, job_repo.Job().SetPaused(f.ctx, j.ID, false, 0))
	f.at(t, s, time.Date(2026, 10, 15, 3, 0, 20, 0, sh))
	assert.Empty(t, f.calls(), "恢复后从现在起算，不补刚才错过的")
	f.at(t, s, time.Date(2026, 10, 16, 3, 0, 5, 0, sh))
	assert.Len(t, f.calls(), 1)
}

func TestJobChangesAreFollowed(t *testing.T) {
	sh := loc(t, "Asia/Shanghai")
	f := setup(t, "Asia/Shanghai", time.Date(2026, 10, 15, 9, 0, 0, 0, sh))
	a := f.job(t, "a", "daily", "03:00", false)
	s := f.newScheduler()
	s.Tick(f.ctx)

	// 新建的任务：下一次 10:00 触发
	b := f.job(t, "b", "daily", "10:00", false)
	f.at(t, s, time.Date(2026, 10, 15, 9, 59, 50, 0, sh))
	f.at(t, s, time.Date(2026, 10, 15, 10, 0, 5, 0, sh))
	require.Len(t, f.calls(), 1)
	assert.Equal(t, b.ID, f.calls()[0].job)

	// 修改计划：改到 11:00
	a.ScheduleTime = "11:00"
	require.NoError(t, job_repo.Job().Update(f.ctx, a))
	f.at(t, s, time.Date(2026, 10, 15, 11, 0, 5, 0, sh))
	require.Len(t, f.calls(), 2)
	assert.Equal(t, a.ID, f.calls()[1].job)

	// 删除后不再触发
	require.NoError(t, job_repo.Job().Delete(f.ctx, b.ID))
	f.at(t, s, time.Date(2026, 10, 16, 10, 0, 5, 0, sh))
	assert.Len(t, f.calls(), 2)
}

func TestTimezoneChangeReinterpretsSchedules(t *testing.T) {
	sh := loc(t, "Asia/Shanghai")
	f := setup(t, "Asia/Shanghai", time.Date(2026, 10, 15, 0, 0, 0, 0, sh))
	f.job(t, "n", "daily", "03:00", false)
	s := f.newScheduler()
	s.Tick(f.ctx)
	f.setting(t, func(st *setting_entity.Setting) { st.Timezone = "UTC" })
	f.at(t, s, time.Date(2026, 10, 15, 0, 0, 10, 0, sh)) // 前一天 16:00 UTC

	f.at(t, s, time.Date(2026, 10, 15, 3, 0, 5, 0, sh)) // 03:00 上海 = 前一天 19:00 UTC
	assert.Empty(t, f.calls(), "按新时区 03:00 UTC 才触发")
	f.at(t, s, time.Date(2026, 10, 15, 3, 0, 5, 0, time.UTC))
	assert.Len(t, f.calls(), 1)
}

func TestMaintenanceSundayOnePerRepoExclusive(t *testing.T) {
	sh := loc(t, "Asia/Shanghai")
	// 2026-10-18 是周日
	f := setup(t, "Asia/Shanghai", time.Date(2026, 10, 18, 5, 0, 0, 0, sh))
	ids := make([]int64, 0, 2)
	for _, n := range []string{"nas-a", "nas-b"} {
		r := &repo_entity.Repo{Name: n, Type: repo_entity.TypeLocal, Params: repo_entity.Params{Path: "/tmp/" + n}, FreeSpace: repo_entity.FreeSpaceUnknown}
		require.NoError(t, repo_repo.Repo().Create(f.ctx, r))
		ids = append(ids, r.ID)
	}
	s := f.newScheduler()
	s.Tick(f.ctx)

	f.at(t, s, time.Date(2026, 10, 18, 5, 59, 0, 0, sh))
	assert.Empty(t, f.maintain)
	f.at(t, s, time.Date(2026, 10, 18, 6, 0, 5, 0, sh))
	assert.Equal(t, ids, f.maintain, "周日 06:00 对每个存储位置各调用一次")
	f.at(t, s, time.Date(2026, 10, 18, 6, 1, 0, 0, sh))
	assert.Len(t, f.maintain, 2)
	f.at(t, s, time.Date(2026, 10, 19, 6, 0, 5, 0, sh))
	assert.Len(t, f.maintain, 2, "周一不维护")
	f.at(t, s, time.Date(2026, 10, 25, 6, 0, 5, 0, sh))
	assert.Len(t, f.maintain, 4, "下一个周日")
}

func TestBackgroundWorkDoesNotStack(t *testing.T) {
	f := setup(t, "Asia/Shanghai", time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC))
	d := f.deps()
	var pending []func()
	d.Spawn = func(fn func()) { pending = append(pending, fn) }
	s := New(d)
	// 每一轮启动两个后台工作：补做延后的旧快照清理、很久没有成功检查
	s.Tick(f.ctx)
	require.Len(t, pending, 2)
	s.Tick(f.ctx)
	require.Len(t, pending, 2, "两个工作都还没结束时都不再启动第二个")
	for _, fn := range pending {
		fn()
	}
	pending = nil
	s.Tick(f.ctx)
	assert.Len(t, pending, 2, "结束后下一轮再启动")
}

func TestRetentionDeletesOnlyOlderThanSetting(t *testing.T) {
	sh := loc(t, "Asia/Shanghai")
	now := time.Date(2026, 10, 15, 2, 59, 0, 0, sh)
	f := setup(t, "Asia/Shanghai", now)
	f.setting(t, func(st *setting_entity.Setting) { st.RetentionDays = 30 })
	mk := func(age time.Duration, status string) int64 {
		r := run_entity.New(1, "j", run_entity.TriggerSchedule, now.Add(-age))
		if status != run_entity.StatusRunning {
			r.Finish(status, "", now.Add(-age), time.Second)
		}
		require.NoError(t, run_repo.Run().Create(f.ctx, r))
		return r.ID
	}
	day := 24 * time.Hour
	fresh := mk(29*day, run_entity.StatusSuccess)
	old := mk(31*day, run_entity.StatusFailed)
	older := mk(100*day, run_entity.StatusSuccess)
	stuck := mk(40*day, run_entity.StatusRunning)

	s := f.newScheduler()
	s.Tick(f.ctx)
	f.at(t, s, time.Date(2026, 10, 15, 3, 29, 0, 0, sh))
	assert.True(t, runExists(t, f, old), "每天 03:30 清理，还没到点")
	f.at(t, s, time.Date(2026, 10, 15, 3, 30, 5, 0, sh))

	exists := func(id int64) bool { return runExists(t, f, id) }
	assert.True(t, exists(fresh))
	assert.False(t, exists(old))
	assert.False(t, exists(older))
	assert.True(t, exists(stuck), "仍在运行的记录不删")
}

func TestStaleCheckRunsEachTick(t *testing.T) {
	f := setup(t, "Asia/Shanghai", time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC))
	s := f.newScheduler()
	s.Tick(f.ctx)
	s.Tick(f.ctx)
	assert.Equal(t, 2, f.stale)
}

func TestRunLoopWakesOnClockTimers(t *testing.T) {
	sh := loc(t, "Asia/Shanghai")
	f := setup(t, "Asia/Shanghai", time.Date(2026, 10, 15, 2, 59, 0, 0, sh))
	f.job(t, "nightly", "daily", "03:00", false)
	s := f.newScheduler()
	ctx, cancel := context.WithCancel(f.ctx)
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	require.Eventually(t, func() bool { return f.clock.pending() == 1 }, 2*time.Second, time.Millisecond)
	f.clock.Set(time.Date(2026, 10, 15, 3, 0, 1, 0, sh))
	require.Eventually(t, func() bool { return len(f.calls()) == 1 }, 2*time.Second, time.Millisecond)
	require.Eventually(t, func() bool { return f.clock.pending() == 1 }, 2*time.Second, time.Millisecond, "触发后重新等待")
}

func TestPlannedListsRunsInWindow(t *testing.T) {
	sh := loc(t, "Asia/Shanghai")
	f := setup(t, "Asia/Shanghai", time.Date(2026, 10, 15, 0, 0, 0, 0, sh))
	d := f.job(t, "daily", "daily", "03:00", false)
	h := f.job(t, "hourly", "hourly", "", false)
	f.job(t, "paused", "daily", "04:00", true)

	from := time.Date(2026, 10, 15, 0, 0, 0, 0, sh)
	got, err := Planned(from, from.Add(24*time.Hour))
	require.NoError(t, err)
	byJob := map[int64][]time.Time{}
	for _, p := range got {
		byJob[p.JobID] = append(byJob[p.JobID], p.At)
	}
	require.Len(t, byJob, 2, "暂停的任务没有计划")
	assert.Equal(t, []time.Time{time.Date(2026, 10, 15, 3, 0, 0, 0, sh)}, byJob[d.ID])
	assert.Len(t, byJob[h.ID], 24, "半开窗口 (from, to]：01:00 到次日 00:00")
	assert.Equal(t, "hourly", got[len(got)-1].JobName)
}

func runExists(t *testing.T, f *fixture, id int64) bool {
	t.Helper()
	r, err := run_repo.Run().Find(f.ctx, id)
	require.NoError(t, err)
	return r != nil
}

// TestCleanupForgetPendingEachTick 撞锁延后的清理由每一轮补做：进行中不叠加，每轮结束就开始下一轮
func TestCleanupForgetPendingEachTick(t *testing.T) {
	f := setup(t, "Asia/Shanghai", time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC))
	d := f.deps()
	var pending []func()
	cleanups := 0
	d.CleanupForgetPending = func(context.Context) error { cleanups++; return nil }
	d.Spawn = func(fn func()) { pending = append(pending, fn) }
	s := New(d)

	// 每一轮会启动两个后台工作：补做清理与「太久没有成功」检查
	runAll := func() {
		for _, fn := range pending {
			fn()
		}
		pending = nil
	}

	s.Tick(f.ctx)
	require.Len(t, pending, 2, "每一轮都补做一次")
	// 还没跑完就再来两轮：不叠加第二个
	s.Tick(f.ctx)
	s.Tick(f.ctx)
	require.Len(t, pending, 2, "上一轮还没结束时不启动第二个")
	runAll()
	assert.Equal(t, 1, cleanups)

	s.Tick(f.ctx)
	require.Len(t, pending, 2, "结束后下一轮再启动")
	runAll()
	assert.Equal(t, 2, cleanups)
}

// TestCleanupForgetPendingDefaultWired Deps 没注入时用真实实现，不 panic（真实实现读任务表，这里没有待清理）
func TestCleanupForgetPendingDefaultWired(t *testing.T) {
	f := setup(t, "Asia/Shanghai", time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC))
	d := f.deps()
	d.CleanupForgetPending = nil
	s := New(d)
	assert.NotNil(t, s.d.CleanupForgetPending, "为空的项用真实实现")
}
