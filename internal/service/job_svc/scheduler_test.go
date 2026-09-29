package job_svc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/cago-frame/cago/pkg/gogo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opsnap/internal/model/entity/job_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/testdb"
	"github.com/opskat/opsnap/internal/repository/job_repo"
	"github.com/opskat/opsnap/internal/repository/setting_repo"
)

// fakeClock 注入调度器与运行模块的时钟
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}

type outcome struct{ status, reason string }

// stubExec 代替真实的单次运行：把运行记为运行中，等测试给出结果后记为该结果
type stubExec struct {
	clk     *fakeClock
	started chan int64
	mu      sync.Mutex
	results map[int64]chan outcome
}

func (x *stubExec) result(id int64) chan outcome {
	x.mu.Lock()
	defer x.mu.Unlock()
	ch, ok := x.results[id]
	if !ok {
		ch = make(chan outcome, 1)
		x.results[id] = ch
	}
	return ch
}

func (x *stubExec) exec(ctx context.Context, id int64) (*job_entity.Run, error) {
	sctx := context.WithoutCancel(ctx)
	run, err := job_repo.Run().Find(sctx, id)
	if err != nil || run == nil || run.Status != job_entity.RunQueued {
		return run, err
	}
	run.Status, run.StartedAt = job_entity.RunRunning, x.clk.Now().UnixMilli()
	if ok, err := job_repo.Run().SaveIf(sctx, run, job_entity.RunQueued); err != nil || !ok {
		cur, ferr := job_repo.Run().Find(sctx, id)
		if err == nil {
			err = ferr
		}
		return cur, err
	}
	x.started <- id
	select {
	case o := <-x.result(id):
		run.Status, run.Reason = o.status, o.reason
	case <-ctx.Done():
		run.Status, run.Reason = job_entity.RunFailed, context.Cause(ctx).Error()
	}
	run.FinishedAt = x.clk.Now().UnixMilli()
	_, err = job_repo.Run().SaveIf(sctx, run, job_entity.RunRunning)
	return run, err
}

type schedEnv struct {
	t     *testing.T
	ctx   context.Context
	clk   *fakeClock
	x     *stubExec
	work  string
	maint chan int64
	// release 每个令牌让一次完整维护结束
	release chan struct{}
}

func newSchedEnv(t *testing.T, now time.Time) *schedEnv {
	t.Helper()
	ctx := testdb.New(t)
	setting_repo.RegisterSetting(setting_repo.NewSetting())
	job_repo.RegisterJob(job_repo.NewJob())
	job_repo.RegisterRun(job_repo.NewRun())
	e := &schedEnv{t: t, ctx: ctx, clk: &fakeClock{t: now}, work: t.TempDir(),
		maint: make(chan int64, 16), release: make(chan struct{}, 16)}
	e.x = &stubExec{clk: e.clk, started: make(chan int64, 64), results: map[int64]chan outcome{}}
	SetWorkDir(e.work)
	defaultRunner.setClock(e.clk.Now)
	// 先登记的后执行：调度器关闭（见 scheduler）之后再等后台协程结束
	t.Cleanup(gogo.Wait)
	t.Cleanup(func() {
		SetDispatcher(nil)
		defaultRunner.setClock(nil)
	})
	return e
}

// scheduler 新建一个调度器（相当于一次启动），不启动后台循环：测试直接调用 startup 与 tick
func (e *schedEnv) scheduler() *Scheduler {
	s := NewScheduler()
	s.now = e.clk.Now
	s.execute = e.x.exec
	s.maintain = func(ctx context.Context, storageID int64) error {
		e.maint <- storageID
		select {
		case <-e.release:
		case <-ctx.Done():
		}
		return nil
	}
	e.t.Cleanup(s.Close)
	return s
}

func (e *schedEnv) start(at time.Time) *Scheduler {
	e.t.Helper()
	e.clk.Set(at)
	s := e.scheduler()
	require.NoError(e.t, s.startup(e.ctx))
	return s
}

func (e *schedEnv) tick(s *Scheduler, at time.Time) {
	e.clk.Set(at)
	s.tick(e.ctx)
}

// job 新建任务：默认每天 02:00（UTC）、不重试、此刻创建并启用
func (e *schedEnv) job(name string, storageID int64, mut func(j *job_entity.Job)) *job_entity.Job {
	e.t.Helper()
	now := e.clk.Now().Unix()
	j := &job_entity.Job{Name: name, Type: job_entity.TypeBackup, DataSourceID: 1, StorageID: storageID,
		Prefix: "pg/" + name, Scope: job_entity.ScopeDatabases, DatabaseNames: `["app"]`, Method: job_entity.MethodFull,
		ExcludeTables: "[]", Compression: "zstd", ScheduleKind: "daily", ScheduleHour: 2, ScheduleWeekdays: "[]",
		Timezone: "UTC", RetainDays: 7, RetryInterval: 5, Timeout: 60, Enabled: true,
		EnabledAt: now, Createtime: now, Updatetime: now}
	if mut != nil {
		mut(j)
	}
	require.NoError(e.t, job_repo.Job().Create(e.ctx, j))
	return j
}

// manual 手动触发一次并交给调度器派发
func (e *schedEnv) manual(jobID int64) int64 {
	e.t.Helper()
	run, err := Runs().Enqueue(e.ctx, jobID, Trigger{Kind: job_entity.TriggerManual})
	require.NoError(e.t, err)
	defaultRunner.dispatchRun(run.ID)
	return run.ID
}

func (e *schedEnv) finish(id int64, status string, reason ...string) {
	o := outcome{status: status}
	if len(reason) > 0 {
		o.reason = reason[0]
	}
	e.x.result(id) <- o
}

func (e *schedEnv) waitStarted() int64 {
	e.t.Helper()
	select {
	case id := <-e.x.started:
		return id
	case <-time.After(5 * time.Second):
		e.t.Fatal("没有运行开始")
		return 0
	}
}

func (e *schedEnv) assertNoStart() {
	e.t.Helper()
	select {
	case id := <-e.x.started:
		e.t.Fatalf("运行 %d 不应开始", id)
	case <-time.After(50 * time.Millisecond):
	}
}

func (e *schedEnv) waitMaint() int64 {
	e.t.Helper()
	select {
	case id := <-e.maint:
		return id
	case <-time.After(5 * time.Second):
		e.t.Fatal("没有开始完整维护")
		return 0
	}
}

func (e *schedEnv) assertNoMaint() {
	e.t.Helper()
	select {
	case id := <-e.maint:
		e.t.Fatalf("存储 %d 不应开始完整维护", id)
	case <-time.After(50 * time.Millisecond):
	}
}

// settle 等后台运行与维护全部结束（含结束后的重试安排与派发）
func (e *schedEnv) settle(s *Scheduler) {
	e.t.Helper()
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		e.t.Fatal("后台运行没有结束")
	}
}

func (e *schedEnv) run(id int64) *job_entity.Run {
	e.t.Helper()
	r, err := job_repo.Run().Find(e.ctx, id)
	require.NoError(e.t, err)
	require.NotNil(e.t, r)
	return r
}

// runs 任务的运行记录，按触发顺序
func (e *schedEnv) runs(jobID int64) []*job_entity.Run {
	e.t.Helper()
	rows, _, err := job_repo.Run().Page(e.ctx, jobID, 0, 100)
	require.NoError(e.t, err)
	slices.Reverse(rows)
	return rows
}

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestSchedulerQueue(t *testing.T) {
	e := newSchedEnv(t, at("2026-09-28T10:00:00Z"))
	s := e.start(at("2026-09-28T10:00:00Z"))
	ids := make([]int64, 0, 5)
	for i := range 5 {
		ids = append(ids, e.manual(e.job(fmt.Sprintf("j%d", i), 1, nil).ID))
	}
	first := []int64{e.waitStarted(), e.waitStarted(), e.waitStarted()}
	assert.ElementsMatch(t, ids[:3], first, "全局最多同时运行 3 个")
	e.assertNoStart()
	assert.Equal(t, job_entity.RunQueued, e.run(ids[3]).Status, "其余排队，显示等待中")
	assert.Equal(t, job_entity.RunQueued, e.run(ids[4]).Status)

	e.finish(ids[1], job_entity.RunSuccess)
	assert.Equal(t, ids[3], e.waitStarted(), "按触发顺序")
	e.assertNoStart()

	e.finish(ids[0], job_entity.RunSuccess)
	assert.Equal(t, ids[4], e.waitStarted())
	for _, id := range []int64{ids[2], ids[3], ids[4]} {
		e.finish(id, job_entity.RunSuccess)
	}
	e.settle(s)

	j := e.job("late", 1, nil)
	queued, err := Runs().Enqueue(e.ctx, j.ID, Trigger{Kind: job_entity.TriggerManual})
	require.NoError(t, err)
	blockers := []int64{e.manual(e.job("b1", 1, nil).ID), e.manual(e.job("b2", 1, nil).ID), e.manual(e.job("b3", 1, nil).ID)}
	for range blockers {
		e.waitStarted()
	}
	defaultRunner.dispatchRun(queued.ID)
	next := e.manual(e.job("next", 1, nil).ID)
	_, err = Runs().Cancel(e.ctx, queued.ID)
	require.NoError(t, err)
	e.finish(blockers[0], job_entity.RunSuccess)
	assert.Equal(t, next, e.waitStarted(), "已取消的排队运行不占用名额")
	e.finish(blockers[1], job_entity.RunSuccess)
	e.finish(blockers[2], job_entity.RunSuccess)
	e.finish(next, job_entity.RunSuccess)
	e.settle(s)
}

func TestSchedulerFires(t *testing.T) {
	e := newSchedEnv(t, at("2026-09-28T01:00:00Z"))
	utc := e.job("utc", 1, nil)
	// 上海 09:30 即 UTC 01:30
	sh := e.job("shanghai", 1, func(j *job_entity.Job) { j.ScheduleHour, j.ScheduleMinute, j.Timezone = 9, 30, "Asia/Shanghai" })
	paused := e.job("paused", 1, func(j *job_entity.Job) { j.Enabled = false })
	s := e.start(at("2026-09-28T01:00:00Z"))

	e.tick(s, at("2026-09-28T01:29:59Z"))
	e.assertNoStart()
	e.tick(s, at("2026-09-28T01:30:00Z"))
	shRun := e.waitStarted()
	r := e.run(shRun)
	assert.Equal(t, sh.ID, r.JobID, "按任务自己的时区触发")
	assert.Equal(t, job_entity.TriggerSchedule, r.Trigger)
	assert.Equal(t, at("2026-09-28T01:30:00Z").Unix(), r.ScheduledAt)

	e.tick(s, at("2026-09-28T02:00:01Z"))
	utcRun := e.waitStarted()
	assert.Equal(t, utc.ID, e.run(utcRun).JobID)
	e.assertNoStart()
	assert.Empty(t, e.runs(paused.ID), "暂停的任务不触发")
	e.tick(s, at("2026-09-28T02:00:30Z"))
	assert.Len(t, e.runs(utc.ID), 1, "同一计划时间只触发一次")

	// 暂停期间可以手动执行
	manual := e.manual(paused.ID)
	assert.Equal(t, manual, e.waitStarted())

	// 第二天到点时上一次仍在运行：记为跳过
	e.tick(s, at("2026-09-29T02:00:00Z"))
	e.assertNoStart()
	for _, j := range []*job_entity.Job{utc, sh} {
		runs := e.runs(j.ID)
		require.Len(t, runs, 2)
		skip := runs[1]
		assert.Equal(t, job_entity.RunSkipped, skip.Status)
		assert.Equal(t, "上一次仍在运行", skip.Reason)
		assert.Equal(t, "The previous run is still running", apiReasons(t, e.ctx, j.ID, code.LangEn)[skip.ID], "英文界面的原因为英文")
		assert.Equal(t, job_entity.TriggerSchedule, skip.Trigger)
		assert.Positive(t, skip.ScheduledAt)
		assert.Equal(t, job_entity.RunRunning, runs[0].Status, "正在运行的不受影响")
	}
	assert.Equal(t, at("2026-09-29T02:00:00Z").Unix(), e.runs(utc.ID)[1].ScheduledAt)

	// 当天到点后存储 1 的完整维护在等运行结束
	e.release <- struct{}{}
	for _, id := range []int64{shRun, utcRun, manual} {
		e.finish(id, job_entity.RunSuccess)
	}
	e.settle(s)
}

func TestSchedulerRetry(t *testing.T) {
	t.Run("失败（含超时）后按间隔重试 i/N，用完为止", func(t *testing.T) {
		e := newSchedEnv(t, at("2026-09-28T01:00:00Z"))
		j := e.job("retry", 1, func(j *job_entity.Job) { j.Retries, j.RetryInterval = 2, 5 })
		s := e.start(at("2026-09-28T01:00:00Z"))
		e.tick(s, at("2026-09-28T02:00:00Z"))
		first := e.waitStarted()
		e.clk.Set(at("2026-09-28T02:01:00Z"))
		e.finish(first, job_entity.RunFailed, "超时（超过 1 小时）")
		e.settle(s)

		e.tick(s, at("2026-09-28T02:05:59Z"))
		e.assertNoStart()
		e.tick(s, at("2026-09-28T02:06:00Z"))
		r1 := e.run(e.waitStarted())
		assert.Equal(t, job_entity.TriggerRetry, r1.Trigger)
		assert.Equal(t, []int{1, 2}, []int{r1.RetryAttempt, r1.RetryTotal}, "重试 1/2")
		e.finish(r1.ID, job_entity.RunFailed, "导出失败")
		e.settle(s)

		e.tick(s, at("2026-09-28T02:11:00Z"))
		r2 := e.run(e.waitStarted())
		assert.Equal(t, []int{2, 2}, []int{r2.RetryAttempt, r2.RetryTotal}, "重试 2/2")
		e.finish(r2.ID, job_entity.RunFailed, "导出失败")
		e.settle(s)

		e.tick(s, at("2026-09-28T02:40:00Z"))
		e.assertNoStart()
		assert.Len(t, e.runs(j.ID), 3, "次数用完后不再重试")
	})

	t.Run("取消与成功不重试", func(t *testing.T) {
		e := newSchedEnv(t, at("2026-09-28T01:00:00Z"))
		j := e.job("retry", 1, func(j *job_entity.Job) { j.Retries = 2 })
		s := e.start(at("2026-09-28T01:00:00Z"))
		e.tick(s, at("2026-09-28T02:00:00Z"))
		e.finish(e.waitStarted(), job_entity.RunCanceled)
		e.settle(s)
		e.tick(s, at("2026-09-28T02:30:00Z"))
		e.assertNoStart()
		e.finish(e.manual(j.ID), job_entity.RunSuccess)
		e.waitStarted()
		e.settle(s)
		e.tick(s, at("2026-09-28T03:00:00Z"))
		e.assertNoStart()
		assert.Len(t, e.runs(j.ID), 2)
	})

	t.Run("下一次计划时间到来时，等待中的重试作废", func(t *testing.T) {
		e := newSchedEnv(t, at("2026-09-28T01:30:00Z"))
		j := e.job("hourly", 1, func(j *job_entity.Job) {
			j.ScheduleKind, j.ScheduleMinute, j.Retries, j.RetryInterval = "hourly", 0, 1, 90
		})
		s := e.start(at("2026-09-28T01:30:00Z"))
		e.tick(s, at("2026-09-28T02:00:00Z"))
		e.finish(e.waitStarted(), job_entity.RunFailed, "导出失败")
		e.settle(s)

		e.tick(s, at("2026-09-28T03:00:00Z"))
		next := e.run(e.waitStarted())
		assert.Equal(t, job_entity.TriggerSchedule, next.Trigger, "改为执行计划运行")
		e.finish(next.ID, job_entity.RunSuccess)
		e.settle(s)
		e.tick(s, at("2026-09-28T03:31:00Z"))
		e.assertNoStart()
		runs := e.runs(j.ID)
		require.Len(t, runs, 2, "作废的重试不再执行")
		assert.Equal(t, job_entity.TriggerSchedule, runs[1].Trigger)
	})

	t.Run("已入队但未开始的重试同样作废", func(t *testing.T) {
		e := newSchedEnv(t, at("2026-09-28T01:30:00Z"))
		j := e.job("hourly", 1, func(j *job_entity.Job) {
			j.ScheduleKind, j.ScheduleMinute, j.Retries, j.RetryInterval = "hourly", 0, 1, 30
		})
		s := e.start(at("2026-09-28T01:30:00Z"))
		e.tick(s, at("2026-09-28T02:00:00Z"))
		first := e.waitStarted()
		blockers := make([]int64, 0, 3)
		blockers = append(blockers, e.manual(e.job("b1", 2, nil).ID), e.manual(e.job("b2", 2, nil).ID))
		e.waitStarted()
		e.waitStarted()
		e.finish(first, job_entity.RunFailed, "导出失败")
		require.Eventually(t, func() bool {
			s.mu.Lock()
			defer s.mu.Unlock()
			_, ok := s.retries[j.ID]
			return ok
		}, 5*time.Second, time.Millisecond)
		blockers = append(blockers, e.manual(e.job("b3", 2, nil).ID))
		e.waitStarted()

		e.tick(s, at("2026-09-28T02:30:00Z"))
		e.assertNoStart()
		runs := e.runs(j.ID)
		require.Len(t, runs, 2)
		retry := runs[1]
		assert.Equal(t, job_entity.TriggerRetry, retry.Trigger)
		assert.Equal(t, job_entity.RunQueued, retry.Status, "名额已满，重试在排队")

		e.tick(s, at("2026-09-28T03:00:00Z"))
		runs = e.runs(j.ID)
		require.Len(t, runs, 3)
		assert.Equal(t, job_entity.RunCanceled, runs[1].Status, "未开始的重试作废")
		assert.NotEmpty(t, runs[1].Reason)
		assert.Equal(t, "Retry voided: the next scheduled run is due", apiReasons(t, e.ctx, j.ID, code.LangEn)[runs[1].ID], "英文界面的原因为英文")
		assert.Equal(t, job_entity.TriggerSchedule, runs[2].Trigger, "不是记为跳过，而是执行计划运行")
		assert.Equal(t, job_entity.RunQueued, runs[2].Status)

		e.finish(blockers[0], job_entity.RunSuccess)
		assert.Equal(t, runs[2].ID, e.waitStarted())
		e.finish(runs[2].ID, job_entity.RunSuccess)
		e.finish(blockers[1], job_entity.RunSuccess)
		e.finish(blockers[2], job_entity.RunSuccess)
		e.settle(s)
	})
}

func TestSchedulerCatchUp(t *testing.T) {
	now := at("2026-09-28T10:00:00Z")
	e := newSchedEnv(t, now)
	old := func(j *job_entity.Job) {
		j.Createtime = at("2026-09-23T10:00:00Z").Unix()
		j.EnabledAt = j.Createtime
	}
	missed := e.job("missed", 1, old)
	skipped := e.job("skipped", 1, old)
	require.NoError(t, job_repo.Run().Create(e.ctx, &job_entity.Run{JobID: skipped.ID, Status: job_entity.RunSkipped,
		Trigger: job_entity.TriggerSchedule, ScheduledAt: at("2026-09-28T02:00:00Z").Unix(), Log: "[]"}))
	fresh := e.job("fresh", 1, func(j *job_entity.Job) {
		old(j)
		j.Createtime = at("2026-09-28T03:00:00Z").Unix()
	})
	reenabled := e.job("reenabled", 1, func(j *job_entity.Job) {
		old(j)
		j.EnabledAt = at("2026-09-28T05:00:00Z").Unix()
	})
	paused := e.job("paused", 1, func(j *job_entity.Job) {
		old(j)
		j.Enabled = false
	})

	s := e.start(now)
	id := e.waitStarted()
	e.assertNoStart()
	runs := e.runs(missed.ID)
	require.Len(t, runs, 1, "错过多次也只补一次")
	assert.Equal(t, id, runs[0].ID)
	assert.Equal(t, job_entity.TriggerCatchUp, runs[0].Trigger)
	assert.Equal(t, at("2026-09-28T02:00:00Z").Unix(), runs[0].ScheduledAt)
	assert.Len(t, e.runs(skipped.ID), 1, "有跳过记录算已执行")
	assert.Empty(t, e.runs(fresh.ID), "任务创建之前的时间不算错过")
	assert.Empty(t, e.runs(reenabled.ID), "暂停期间的计划不算错过")
	assert.Empty(t, e.runs(paused.ID))
	e.finish(id, job_entity.RunSuccess)
	e.settle(s)
	s.Close()

	// 再次启动：上一次计划时间已有记录，不再补跑
	s = e.start(at("2026-09-28T11:00:00Z"))
	e.assertNoStart()
	assert.Len(t, e.runs(missed.ID), 1)
	e.settle(s)
}

func TestSchedulerRestart(t *testing.T) {
	now := at("2026-09-28T10:00:00Z")
	e := newSchedEnv(t, now)
	// 重启前运行中：记为失败且不重试；随后补跑它错过的 02:00
	j := e.job("interrupted", 1, func(j *job_entity.Job) {
		j.Retries, j.Createtime, j.EnabledAt = 3, at("2026-09-27T00:00:00Z").Unix(), at("2026-09-27T00:00:00Z").Unix()
	})
	running := &job_entity.Run{JobID: j.ID, Status: job_entity.RunRunning, Trigger: job_entity.TriggerSchedule,
		ScheduledAt: at("2026-09-27T02:00:00Z").Unix(), StartedAt: at("2026-09-27T02:00:00Z").UnixMilli(), Log: "[]"}
	require.NoError(t, job_repo.Run().Create(e.ctx, running))
	other := e.job("queued", 1, nil)
	queued := &job_entity.Run{JobID: other.ID, Status: job_entity.RunQueued, Trigger: job_entity.TriggerManual, Log: "[]"}
	require.NoError(t, job_repo.Run().Create(e.ctx, queued))
	pending := e.job("pending", 1, func(j *job_entity.Job) { j.RunNow = true })
	require.NoError(t, os.MkdirAll(filepath.Join(e.work, "run-left"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(e.work, "run-left", "pgpass"), []byte("secret"), 0o600))

	s := e.start(now)
	got := e.run(running.ID)
	assert.Equal(t, job_entity.RunFailed, got.Status)
	assert.Equal(t, "OpsNap 重启，运行中断", got.Reason)
	got = e.run(queued.ID)
	assert.Equal(t, job_entity.RunCanceled, got.Status)
	assert.Equal(t, "OpsNap 重启", got.Reason)
	_, err := os.Stat(filepath.Join(e.work, "run-left"))
	assert.True(t, os.IsNotExist(err), "启动时清扫导出临时目录")

	started := []int64{e.waitStarted(), e.waitStarted()}
	e.assertNoStart()
	runs := e.runs(j.ID)
	require.Len(t, runs, 2)
	assert.Equal(t, job_entity.TriggerCatchUp, runs[1].Trigger, "先处理中断的运行，再补跑")
	pendingRuns := e.runs(pending.ID)
	require.Len(t, pendingRuns, 1)
	assert.Equal(t, job_entity.TriggerManual, pendingRuns[0].Trigger, "然后执行“立即执行一次”")
	assert.ElementsMatch(t, []int64{runs[1].ID, pendingRuns[0].ID}, started)
	for _, id := range started {
		e.finish(id, job_entity.RunSuccess)
	}
	e.settle(s)

	e.tick(s, at("2026-09-28T11:00:00Z"))
	e.assertNoStart()
	assert.Len(t, e.runs(j.ID), 2, "重启中断的运行不重试")
}

func TestSchedulerMaintenance(t *testing.T) {
	t.Run("每个被任务使用的存储每天一次完整维护", func(t *testing.T) {
		t0 := at("2026-09-28T10:00:00Z")
		e := newSchedEnv(t, t0)
		// 暂停的任务同样使用存储；暂停使到点的计划不触发，只观察维护
		off := func(j *job_entity.Job) { j.Enabled = false }
		e.job("a", 1, off)
		e.job("b", 1, off)
		e.job("c", 2, off)
		s := e.start(t0)
		e.tick(s, t0)
		e.assertNoMaint()
		e.tick(s, t0.Add(24*time.Hour-time.Second))
		e.assertNoMaint()
		e.tick(s, t0.Add(24*time.Hour))
		assert.ElementsMatch(t, []int64{1, 2}, []int64{e.waitMaint(), e.waitMaint()})
		e.release <- struct{}{}
		e.release <- struct{}{}
		e.settle(s)
		e.tick(s, t0.Add(25*time.Hour))
		e.assertNoMaint()
		s.Close()

		// 重启不重置每天一次
		s = e.start(t0.Add(26 * time.Hour))
		e.tick(s, t0.Add(26*time.Hour))
		e.assertNoMaint()
		e.tick(s, t0.Add(48*time.Hour))
		assert.ElementsMatch(t, []int64{1, 2}, []int64{e.waitMaint(), e.waitMaint()})
		e.release <- struct{}{}
		e.release <- struct{}{}
		e.settle(s)
	})

	t.Run("完整维护与写同一存储的运行不同时进行", func(t *testing.T) {
		t0 := at("2026-09-28T10:00:00Z")
		e := newSchedEnv(t, t0)
		off := func(j *job_entity.Job) { j.Enabled = false }
		a := e.job("a", 1, off)
		c := e.job("c", 2, off)
		s := e.start(t0)
		e.tick(s, t0)
		runA := e.manual(a.ID)
		require.Equal(t, runA, e.waitStarted())

		e.tick(s, t0.Add(24*time.Hour))
		assert.Equal(t, int64(2), e.waitMaint(), "存储 1 上有运行，只维护存储 2")
		e.assertNoMaint()
		runC := e.manual(c.ID)
		e.assertNoStart()
		assert.Equal(t, job_entity.RunQueued, e.run(runC).Status, "维护期间同一存储的运行等待")

		e.finish(runA, job_entity.RunSuccess)
		assert.Equal(t, int64(1), e.waitMaint(), "运行结束后维护存储 1")
		runA2 := e.manual(a.ID)
		e.assertNoStart()
		e.release <- struct{}{}
		e.release <- struct{}{}
		assert.ElementsMatch(t, []int64{runC, runA2}, []int64{e.waitStarted(), e.waitStarted()})
		e.finish(runC, job_entity.RunSuccess)
		e.finish(runA2, job_entity.RunSuccess)
		e.settle(s)
	})
}

// 后台循环：到点由唤醒或定时器触发；关闭后不再触发，也不再开始排队的运行
func TestSchedulerLoop(t *testing.T) {
	t0 := at("2026-09-28T01:59:00Z")
	e := newSchedEnv(t, t0)
	j := e.job("loop", 1, nil)
	s := e.scheduler()
	require.NoError(t, s.Start(e.ctx))
	e.clk.Set(at("2026-09-28T02:00:00Z"))
	wakeScheduler()
	id := e.waitStarted()
	assert.Equal(t, j.ID, e.run(id).JobID)

	s.Close()
	gogo.Wait()
	r := e.run(id)
	assert.Equal(t, job_entity.RunFailed, r.Status, "关闭时中断进行中的运行")
	late := e.manual(e.job("late", 1, nil).ID)
	e.assertNoStart()
	assert.Equal(t, job_entity.RunQueued, e.run(late).Status, "关闭后不再开始，下次启动时记为已取消")
	e.tick(s, at("2026-09-29T02:00:00Z"))
	assert.Len(t, e.runs(j.ID), 1, "关闭后不再触发")
}

// 停止 OpsNap 时进行中的真实运行：终止导出工具、清理临时文件，记为失败“OpsNap 重启，运行中断”，不重试
func TestSchedulerShutdownRealRun(t *testing.T) {
	e := newRunEnv(t)
	now := time.Now().Unix()
	e.job.Createtime, e.job.EnabledAt = now, now
	require.NoError(t, job_repo.Job().Save(e.ctx, e.job))
	e.tool("pg_dump", "pg_dump (PostgreSQL) 16.4", pgDumpHang)
	s := NewScheduler()
	require.NoError(t, s.Start(e.ctx))
	t.Cleanup(s.Close)
	run, err := Runs().Enqueue(e.ctx, e.job.ID, Trigger{Kind: job_entity.TriggerManual})
	require.NoError(t, err)
	defaultRunner.dispatchRun(run.ID)
	require.Eventually(t, func() bool { return len(e.argv("pg_dump")) > 0 }, 30*time.Second, 20*time.Millisecond)

	s.Close()
	done := make(chan struct{})
	go func() {
		gogo.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("关闭后运行没有结束")
	}
	final, err := job_repo.Run().Find(e.ctx, run.ID)
	require.NoError(t, err)
	assert.Equal(t, job_entity.RunFailed, final.Status)
	assert.Equal(t, "OpsNap 重启，运行中断", final.Reason)
	assert.Empty(t, final.SnapshotID)
	e.assertClean()
	s.mu.Lock()
	assert.Empty(t, s.retries, "中断的运行不重试")
	s.mu.Unlock()
}

// 计划到点时任务已在运行而记“跳过”，但此时任务已被删除：不留下没有任务的运行记录
func TestSkipAfterJobDeleted(t *testing.T) {
	e := newSchedEnv(t, at("2026-09-28T01:00:00Z"))
	j := e.job("gone", 1, nil)
	require.NoError(t, job_repo.Job().Delete(e.ctx, j.ID))
	_, err := defaultRunner.skip(e.ctx, j.ID, Trigger{Kind: job_entity.TriggerSchedule, ScheduledAt: 1}, job_entity.ReasonStillRunning)
	assert.ErrorIs(t, err, job_repo.ErrNotFound)
	assert.Empty(t, e.runs(j.ID))
}
