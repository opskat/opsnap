// Package scheduler 内置调度器：按各任务的计划触发运行，并做定期维护、记录保留与「太久没有成功」检查。
//
// cago 自带的 cron 没有时区也不能注入时钟，所以这里自己写循环：每轮（Tick）从数据库读取任务和设置，
// 按设置的时区计算下次运行时间。任务的新建、修改、暂停、恢复、删除和时区变化都不需要通知调度器——
// 下一轮读到的状态不同，就从「现在」重新计算，所以对这些改动的响应延迟不超过一个轮询间隔。
//
// 重启后不补跑：调度器启动时所有任务的下次运行时间都从当前时刻算起；
// 进程运行期间若被挂起超过 missedGrace，错过的那次也直接放弃。
package scheduler

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cago-frame/cago/pkg/gogo"
	"github.com/cago-frame/cago/pkg/logger"

	"github.com/opskat/opsnap/internal/model/entity/job_entity"
	"github.com/opskat/opsnap/internal/model/entity/run_entity"
	"github.com/opskat/opsnap/internal/pkg/schedule"
	"github.com/opskat/opsnap/internal/repository/job_repo"
	"github.com/opskat/opsnap/internal/repository/repo_repo"
	"github.com/opskat/opsnap/internal/repository/run_repo"
	"github.com/opskat/opsnap/internal/service/alert_svc"
	"github.com/opskat/opsnap/internal/service/repo_svc"
	"github.com/opskat/opsnap/internal/service/run_svc"
	"github.com/opskat/opsnap/internal/service/setting_svc"
)

const (
	// pollInterval 轮询间隔：任务改动最迟这么久后被调度器看到，也是告警检查的频率
	pollInterval = 30 * time.Second
	// minWait 两轮之间最短的等待
	minWait = 100 * time.Millisecond
	// missedGrace 到点后超过这么久才被发现（进程被挂起）就当作错过，不再触发
	missedGrace = 5 * time.Minute

	// SkippedReason 按计划触发时上一次仍在运行的跳过原因
	SkippedReason = "上一次运行尚未结束，本次跳过"
)

// 定期维护：每周日 06:00；记录保留清理：每天 03:30。时间都按设置的时区解释。
var (
	maintenanceSpec = schedule.Spec{Kind: schedule.Weekly, Time: "06:00", Weekday: 0}
	retentionSpec   = schedule.Spec{Kind: schedule.Daily, Time: "03:30"}
)

// Clock 时钟。测试里用手动推进的实现，不需要真的等待。
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Deps 外部依赖，测试里替换；为空的项使用真实实现
type Deps struct {
	Clock Clock
	// Trigger 触发一次运行；同一任务仍在运行时返回 run_svc.ErrAlreadyRunning
	Trigger func(ctx context.Context, jobID int64, trig run_entity.Trigger) (int64, error)
	// Maintain 对一个存储位置做清理空间与抽样校验，独占该存储位置，结果由它记在存储位置上
	Maintain func(ctx context.Context, repoID int64) error
	// CheckStale 「太久没有成功」检查
	CheckStale func(ctx context.Context) error
	// CleanupForgetPending 补做因存储位置被其他备份占用而延后的旧快照清理
	CleanupForgetPending func(ctx context.Context) error
	// Spawn 在后台执行耗时的工作（维护、告警通知），不阻塞触发。默认 gogo.Go
	Spawn func(fn func())
}

// Scheduler 调度器
type Scheduler struct {
	d Deps

	mu       sync.Mutex
	lastTick time.Time
	tz       string
	jobs     map[int64]*jobState
	maintAt  time.Time
	retAt    time.Time

	busyMu sync.Mutex // 单独的锁：测试里 Spawn 同步执行，不能和 mu 共用
	busy   map[string]bool
}

type jobState struct {
	sig  string
	next time.Time
}

// New 创建调度器
func New(d Deps) *Scheduler {
	if d.Clock == nil {
		d.Clock = realClock{}
	}
	if d.Trigger == nil {
		d.Trigger = run_svc.Trigger
	}
	if d.Maintain == nil {
		d.Maintain = repo_svc.Maintain
	}
	if d.CheckStale == nil {
		d.CheckStale = alert_svc.Check
	}
	if d.CleanupForgetPending == nil {
		d.CleanupForgetPending = run_svc.CleanupForgetPending
	}
	if d.Spawn == nil {
		d.Spawn = func(fn func()) { gogo.Go(func() error { fn(); return nil }) }
	}
	return &Scheduler{d: d, jobs: map[int64]*jobState{}, busy: map[string]bool{}}
}

// Run 循环到 ctx 结束
func (s *Scheduler) Run(ctx context.Context) {
	for {
		s.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-s.d.Clock.After(s.wait()):
		}
	}
}

// wait 距离下一个要做的事的时间，不超过 pollInterval
func (s *Scheduler) wait() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.d.Clock.Now()
	wait := pollInterval
	consider := func(t time.Time) {
		if !t.IsZero() && t.Sub(now) < wait {
			wait = t.Sub(now)
		}
	}
	for _, st := range s.jobs {
		consider(st.next)
	}
	consider(s.maintAt)
	consider(s.retAt)
	return max(wait, minWait)
}

// Tick 处理一轮：触发到点的任务，必要时启动维护与保留清理，并做告警检查
func (s *Scheduler) Tick(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.d.Clock.Now()
	// 新出现或刚改动的任务从上一轮的时刻算起（此前的改动不会被错过），第一轮从现在算起（重启不补跑）
	base := s.lastTick
	if base.IsZero() || base.After(now) {
		base = now
	}
	s.lastTick = now

	st, err := setting_svc.Get(ctx)
	if err != nil {
		s.logf(ctx, "读取设置失败：%v", err)
		return
	}
	loc := st.Location()
	tzChanged := s.tz != "" && s.tz != st.Timezone
	s.tz = st.Timezone

	s.tickJobs(ctx, now, base, loc, tzChanged)
	if s.fire(&s.maintAt, maintenanceSpec, now, base, loc, tzChanged) {
		s.background(ctx, "maintenance", func(ctx context.Context) { s.maintainAll(ctx) })
	}
	if s.fire(&s.retAt, retentionSpec, now, base, loc, tzChanged) {
		days := st.RetentionDays
		s.background(ctx, "retention", func(ctx context.Context) { s.cleanup(ctx, now, days) })
	}
	// 撞锁延后的清理：存储位置空闲时补做，拿不到独占锁时它自己什么都不做
	s.background(ctx, "forget-pending", func(ctx context.Context) {
		if err := s.d.CleanupForgetPending(ctx); err != nil {
			s.logf(ctx, "补做延后的旧快照清理失败：%v", err)
		}
	})
	s.background(ctx, "stale", func(ctx context.Context) {
		if err := s.d.CheckStale(ctx); err != nil {
			s.logf(ctx, "太久没有成功检查失败：%v", err)
		}
	})
}

// fire 固定计划（维护、保留清理）：到点返回 true 并排好下一次。at 为零或时区变化时从 base 重新计算。
func (s *Scheduler) fire(at *time.Time, spec schedule.Spec, now, base time.Time, loc *time.Location, tzChanged bool) bool {
	if at.IsZero() || tzChanged {
		next, err := schedule.Next(spec, base, loc)
		if err != nil {
			return false
		}
		*at = next
	}
	if at.After(now) {
		return false
	}
	missed := now.Sub(*at) > missedGrace
	next, err := schedule.Next(spec, now, loc)
	if err != nil {
		return false
	}
	*at = next
	return !missed
}

func (s *Scheduler) tickJobs(ctx context.Context, now, base time.Time, loc *time.Location, tzChanged bool) {
	jobs, err := job_repo.Job().List(ctx)
	if err != nil {
		s.logf(ctx, "读取任务失败：%v", err)
		return
	}
	alive := make(map[int64]bool, len(jobs))
	for _, j := range jobs {
		alive[j.ID] = true
		if j.Paused {
			delete(s.jobs, j.ID)
			continue
		}
		sig := fmt.Sprintf("%+v|%s", j.Schedule(), loc)
		cur := s.jobs[j.ID]
		if cur == nil || cur.sig != sig || tzChanged {
			next, err := schedule.Next(j.Schedule(), base, loc)
			if err != nil {
				delete(s.jobs, j.ID)
				continue
			}
			cur = &jobState{sig: sig, next: next}
			s.jobs[j.ID] = cur
		}
		if cur.next.After(now) {
			continue
		}
		missed := now.Sub(cur.next) > missedGrace
		if next, err := schedule.Next(j.Schedule(), now, loc); err == nil {
			cur.next = next
		}
		if !missed {
			s.trigger(ctx, j, now)
		}
	}
	for id := range s.jobs {
		if !alive[id] {
			delete(s.jobs, id)
		}
	}
}

// trigger 按计划触发；上一次还在运行就记一条跳过
func (s *Scheduler) trigger(ctx context.Context, j *job_entity.Job, now time.Time) {
	_, err := s.d.Trigger(ctx, j.ID, run_entity.TriggerSchedule)
	switch {
	case err == nil:
	case isAlreadyRunning(err):
		r := run_entity.New(j.ID, j.Name, run_entity.TriggerSchedule, now)
		r.Finish(run_entity.StatusSkipped, SkippedReason, now, 0)
		if err := run_repo.Run().Create(ctx, r); err != nil {
			s.logf(ctx, "记录任务 %d 的跳过失败：%v", j.ID, err)
		}
	default:
		s.logf(ctx, "触发任务 %d 失败：%v", j.ID, err)
	}
}

// background 在后台执行；同名工作还没结束时不再启动第二个
func (s *Scheduler) background(ctx context.Context, name string, fn func(ctx context.Context)) {
	s.busyMu.Lock()
	if s.busy[name] {
		s.busyMu.Unlock()
		return
	}
	s.busy[name] = true
	s.busyMu.Unlock()
	s.d.Spawn(func() {
		defer func() {
			s.busyMu.Lock()
			s.busy[name] = false
			s.busyMu.Unlock()
		}()
		fn(ctx)
	})
}

// maintainAll 依次维护每个存储位置。Maintain 自己等待并独占存储位置，与写入它的备份互斥。
func (s *Scheduler) maintainAll(ctx context.Context) {
	repos, err := repo_repo.Repo().List(ctx)
	if err != nil {
		s.logf(ctx, "读取存储位置失败：%v", err)
		return
	}
	for _, r := range repos {
		if err := s.d.Maintain(ctx, r.ID); err != nil {
			// 结果（含失败原因）已由 Maintain 记在存储位置的校验与清理记录里
			s.logf(ctx, "存储位置 %d 定期维护失败：%v", r.ID, err)
		}
	}
}

// cleanup 删除开始时间早于保留天数的运行记录（日志在记录里一并删除）
func (s *Scheduler) cleanup(ctx context.Context, now time.Time, days int) {
	cutoff := now.Add(-time.Duration(days) * 24 * time.Hour)
	if _, err := run_repo.Run().DeleteStartedBefore(ctx, cutoff.Unix()); err != nil {
		s.logf(ctx, "清理过期运行记录失败：%v", err)
	}
}

func (s *Scheduler) logf(ctx context.Context, format string, args ...any) {
	logger.Ctx(ctx).Sugar().Errorf(format, args...)
}

// PlannedRun 窗口内某个任务的一次计划运行
type PlannedRun struct {
	JobID   int64
	JobName string
	At      time.Time
}

// maxPlannedPerJob 单个任务在一个窗口内最多列出的次数，防止「每分钟」之类的计划撑爆结果
const maxPlannedPerJob = 1000

// Planned 列出 (from, to] 内未暂停任务的计划运行，按时间升序，时间用设置的时区表示。
// 概览页的时间线用它；它只读计划，不依赖调度器是否在运行。
func Planned(from, to time.Time) ([]PlannedRun, error) {
	ctx := context.Background()
	st, err := setting_svc.Get(ctx)
	if err != nil {
		return nil, err
	}
	jobs, err := job_repo.Job().List(ctx)
	if err != nil {
		return nil, err
	}
	loc := st.Location()
	var out []PlannedRun
	for _, j := range jobs {
		if j.Paused {
			continue
		}
		at := from
		for range maxPlannedPerJob {
			next, ok := j.NextRun(at, loc)
			if !ok || next.After(to) {
				break
			}
			out = append(out, PlannedRun{JobID: j.ID, JobName: j.Name, At: next})
			at = next
		}
	}
	sortPlanned(out)
	return out, nil
}
