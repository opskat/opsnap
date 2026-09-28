package job_svc

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cago-frame/cago/pkg/gogo"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opsnap/internal/model/entity/job_entity"
	"github.com/opskat/opsnap/internal/pkg/kopiarepo"
	"github.com/opskat/opsnap/internal/repository/job_repo"
	"github.com/opskat/opsnap/internal/repository/setting_repo"
	"github.com/opskat/opsnap/internal/service/storage_svc"
)

const (
	// maxConcurrentRuns 全局最多同时执行的运行数，其余排队（等待中）
	maxConcurrentRuns = 3
	// maxSleep 调度循环最长的休眠：兜住系统休眠、时钟调整与没有唤醒的任务变更
	maxSleep = time.Minute
	minSleep = 10 * time.Millisecond
	// maintenanceEvery 每个被任务使用的存储做完整维护的间隔
	maintenanceEvery = 24 * time.Hour
	// maintenanceTimeout 一次完整维护的上限；维护期间写同一存储的运行在等待
	maintenanceTimeout = time.Hour
	// maintenanceKey settings 表中记录存储最近一次完整维护时间（秒）的键前缀
	maintenanceKey = "storage_full_maintenance_"
)

// pendingRetry 失败后等待中的重试：到 due 时以“重试 attempt/total”入队
type pendingRetry struct {
	due            time.Time
	attempt, total int
}

// queuedRun 调度队列中的运行；storageID 为它写入的存储
type queuedRun struct {
	runID, storageID int64
}

// Scheduler 调度组件（docs/specs/2026-09-27-backup-jobs.md「调度」，「执行」中的排队、重试与 OpsNap 重启，
// 「保留」中每天的完整维护）：
//   - 队列：全局最多同时执行 3 个运行，其余按派发顺序排队（记录为等待中）；
//   - 计划：每个启用的任务按自己的频率与时区触发，任务已在运行或排队时记一条“跳过”；
//   - 重试：失败（含超时）后按任务的间隔以“重试 i/N”再次入队，下一次计划时间先到时作废；
//     取消、跳过与因 OpsNap 停止而中断的运行不重试；
//   - 启动：处理上次退出时未结束的运行（Runs().Recover），为每个启用的任务补跑一次错过的上一次计划，
//     再执行“立即执行一次”；
//   - 维护：每个被任务使用的存储每天一次完整维护，与写同一存储的运行不同时进行；
//   - 关闭：不再触发与开始新的运行，中断进行中的运行（失败“OpsNap 重启，运行中断”），排队的留给下次启动。
type Scheduler struct {
	now      func() time.Time
	execute  func(ctx context.Context, runID int64) (*job_entity.Run, error)
	maintain func(ctx context.Context, storageID int64) error

	// ctx 运行与维护所用，Close 时以 errShutdown 取消
	ctx    context.Context
	cancel context.CancelCauseFunc
	wakeCh chan struct{}
	// wg 进行中的运行与维护协程
	wg sync.WaitGroup

	mu       sync.Mutex
	closed   bool
	lastTick time.Time
	queue    []queuedRun
	// running 执行中的运行 → 存储
	running map[int64]int64
	// retries 任务 → 等待中的重试
	retries map[int64]pendingRetry
	// maintLast 存储最近一次完整维护（或开始计时）的时间；maintWanted 已到期、等写入该存储的运行结束；
	// maintActive 正在维护
	maintLast   map[int64]time.Time
	maintWanted map[int64]bool
	maintActive map[int64]bool
}

// current 已启动的调度器，供任务变更后唤醒
var current atomic.Pointer[Scheduler]

// NewScheduler 新建调度组件，Start 后开始工作
func NewScheduler() *Scheduler {
	ctx, cancel := context.WithCancelCause(context.Background())
	return &Scheduler{now: time.Now, execute: defaultRunner.Execute, maintain: fullMaintenance,
		ctx: ctx, cancel: cancel, wakeCh: make(chan struct{}, 1),
		running: map[int64]int64{}, retries: map[int64]pendingRetry{},
		maintLast: map[int64]time.Time{}, maintWanted: map[int64]bool{}, maintActive: map[int64]bool{}}
}

// Start 按顺序处理上次退出时未结束的运行、补跑、“立即执行一次”，然后在后台按计划触发。
// 导出临时目录由 Recover 清扫；kopia 写入会话的临时配置已由 storage_svc.SetDataDir 清扫
func (s *Scheduler) Start(ctx context.Context) error {
	if err := s.startup(ctx); err != nil {
		return err
	}
	current.Store(s)
	gogo.Go(s.loop)
	return nil
}

// Close 停止触发与开始新的运行，并中断进行中的运行与维护；不等待它们结束（由 cago 的 gogo.Wait 等待）
func (s *Scheduler) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	current.CompareAndSwap(s, nil)
	s.cancel(errShutdown)
}

func (s *Scheduler) startup(ctx context.Context) error {
	if err := defaultRunner.Recover(ctx); err != nil {
		return fmt.Errorf("处理上次退出时未结束的运行: %w", err)
	}
	now := s.now()
	s.mu.Lock()
	s.lastTick = now
	s.mu.Unlock()
	SetDispatcher(s.dispatch)

	jobs, err := job_repo.Job().List(ctx)
	if err != nil {
		return fmt.Errorf("读取任务: %w", err)
	}
	for _, j := range jobs {
		if !j.Enabled {
			continue
		}
		// 错过多次也只补跑上一次计划
		if p, err := j.Schedule().Prev(now); err == nil && eligible(j, p) {
			s.fire(ctx, j, p, job_entity.TriggerCatchUp)
		}
	}
	if err := defaultRunner.RunPending(ctx); err != nil {
		logger.Ctx(ctx).Error("处理“立即执行一次”失败", zap.Error(err))
	}
	return nil
}

// eligible 计划时间在任务创建与最近一次启用之后：此前与暂停期间的计划不算
func eligible(j *job_entity.Job, p time.Time) bool {
	return p.Unix() > j.Createtime && p.Unix() > j.EnabledAt
}

func (s *Scheduler) wake() {
	select {
	case s.wakeCh <- struct{}{}:
	default:
	}
}

// wakeScheduler 任务新建、修改、启用后立即重新计算下一次触发
func wakeScheduler() {
	if s := current.Load(); s != nil {
		s.wake()
	}
}

func (s *Scheduler) loop() error {
	for {
		next := s.tick(s.ctx)
		d := min(max(next.Sub(s.now()), minSleep), maxSleep)
		timer := time.NewTimer(d)
		select {
		case <-s.ctx.Done():
			timer.Stop()
			return nil
		case <-s.wakeCh:
			timer.Stop()
		case <-timer.C:
		}
	}
}

// tick 处理到 now 为止到期的计划、重试与维护，返回下一次需要处理的时间
func (s *Scheduler) tick(ctx context.Context) time.Time {
	now := s.now()
	next := now.Add(maxSleep)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return next
	}
	last := s.lastTick
	if now.After(last) {
		s.lastTick = now
	}
	s.mu.Unlock()

	jobs, err := job_repo.Job().List(ctx)
	if err != nil {
		logger.Ctx(ctx).Error("读取任务失败", zap.Error(err))
		return next
	}
	// 计划先于重试处理：同时到期时计划运行执行，重试作废
	for _, j := range jobs {
		if !j.Enabled {
			continue
		}
		spec := j.Schedule()
		if p, err := spec.Prev(now); err == nil && p.After(last) && eligible(j, p) {
			s.fire(ctx, j, p, job_entity.TriggerSchedule)
		}
		if n, err := spec.Next(now, 1); err == nil && len(n) > 0 && n[0].Before(next) {
			next = n[0]
		}
	}
	for _, due := range []time.Time{s.runRetries(ctx, now), s.checkMaintenance(ctx, now, jobs)} {
		if !due.IsZero() && due.Before(next) {
			next = due
		}
	}
	s.pump()
	return next
}

// fire 触发任务在计划时间 p 的运行（计划或补跑）：已有该时间的记录时不再触发；作废等待中的重试；
// 任务已在运行或排队时记为跳过
func (s *Scheduler) fire(ctx context.Context, j *job_entity.Job, p time.Time, kind string) {
	log := logger.Ctx(ctx).With(zap.Int64("job_id", j.ID), zap.Time("scheduled_at", p), zap.String("trigger", kind))
	done, err := job_repo.Run().HasScheduled(ctx, j.ID, p.Unix())
	if err != nil {
		log.Error("读取运行记录失败", zap.Error(err))
		return
	}
	if done {
		return
	}
	s.voidRetry(ctx, j.ID)
	t := Trigger{Kind: kind, ScheduledAt: p.Unix()}
	run, err := defaultRunner.Enqueue(ctx, j.ID, t)
	switch {
	case errors.Is(err, ErrRunActive):
		if _, err := defaultRunner.skip(ctx, j.ID, t, job_entity.ReasonStillRunning); err != nil && !errors.Is(err, job_repo.ErrNotFound) {
			log.Error("记录跳过失败", zap.Error(err))
		}
	case err != nil:
		log.Error("触发运行失败", zap.Error(err))
	default:
		s.dispatch(run.ID)
	}
}

// voidRetry 作废任务等待中的重试，包括已入队但还没开始的
func (s *Scheduler) voidRetry(ctx context.Context, jobID int64) {
	s.mu.Lock()
	delete(s.retries, jobID)
	s.mu.Unlock()
	act, err := job_repo.Run().Active(ctx, jobID)
	if err != nil {
		logger.Ctx(ctx).Error("读取任务的运行失败", zap.Int64("job_id", jobID), zap.Error(err))
		return
	}
	if act == nil || act.Status != job_entity.RunQueued || act.Trigger != job_entity.TriggerRetry {
		return
	}
	ok, err := defaultRunner.void(ctx, act.ID, job_entity.ReasonRetryVoided)
	if err != nil {
		logger.Ctx(ctx).Error("作废重试失败", zap.Int64("run_id", act.ID), zap.Error(err))
		return
	}
	if ok {
		s.mu.Lock()
		s.queue = slices.DeleteFunc(s.queue, func(q queuedRun) bool { return q.runID == act.ID })
		s.mu.Unlock()
	}
}

// runRetries 把到期的重试入队，返回最早一个未到期重试的时间（没有时为零值）
func (s *Scheduler) runRetries(ctx context.Context, now time.Time) time.Time {
	type dueRetry struct {
		jobID int64
		r     pendingRetry
	}
	var due []dueRetry
	var next time.Time
	s.mu.Lock()
	for id, r := range s.retries {
		switch {
		case !r.due.After(now):
			due = append(due, dueRetry{id, r})
			delete(s.retries, id)
		case next.IsZero() || r.due.Before(next):
			next = r.due
		}
	}
	s.mu.Unlock()
	slices.SortFunc(due, func(a, b dueRetry) int { return a.r.due.Compare(b.r.due) })
	for _, d := range due {
		run, err := defaultRunner.Enqueue(ctx, d.jobID, Trigger{Kind: job_entity.TriggerRetry, Attempt: d.r.attempt, Total: d.r.total})
		switch {
		case errors.Is(err, ErrRunActive), errors.Is(err, job_repo.ErrNotFound):
			logger.Ctx(ctx).Info("重试未执行", zap.Int64("job_id", d.jobID), zap.Error(err))
		case err != nil:
			logger.Ctx(ctx).Error("重试入队失败", zap.Int64("job_id", d.jobID), zap.Error(err))
		default:
			s.dispatch(run.ID)
		}
	}
	return next
}

// afterRun 运行失败（含超时）后安排重试；取消、跳过、因 OpsNap 停止而中断的不重试
func (s *Scheduler) afterRun(ctx context.Context, run *job_entity.Run) {
	if run == nil || run.Status != job_entity.RunFailed || run.ReasonCode == job_entity.ReasonInterrupted {
		return
	}
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return
	}
	j, err := job_repo.Job().Find(ctx, run.JobID)
	if err != nil || j == nil {
		if err != nil {
			logger.Ctx(ctx).Error("读取任务失败，不安排重试", zap.Int64("job_id", run.JobID), zap.Error(err))
		}
		return
	}
	attempt, total := 1, j.Retries
	if run.Trigger == job_entity.TriggerRetry {
		attempt, total = run.RetryAttempt+1, run.RetryTotal
	}
	if attempt > total {
		return
	}
	finished := s.now()
	if run.FinishedAt > 0 {
		finished = time.UnixMilli(run.FinishedAt)
	}
	s.mu.Lock()
	s.retries[j.ID] = pendingRetry{due: finished.Add(time.Duration(j.RetryInterval) * time.Minute), attempt: attempt, total: total}
	s.mu.Unlock()
	s.wake()
}

// dispatch 派发函数（SetDispatcher）：把入队的运行放进调度队列
func (s *Scheduler) dispatch(runID int64) {
	var storageID int64
	ctx := context.Background()
	if run, err := job_repo.Run().Find(ctx, runID); err == nil && run != nil {
		if j, err := job_repo.Job().Find(ctx, run.JobID); err == nil && j != nil {
			storageID = j.StorageID
		}
	}
	s.mu.Lock()
	s.queue = append(s.queue, queuedRun{runID: runID, storageID: storageID})
	s.mu.Unlock()
	s.pump()
}

// pump 开始可以开始的维护与运行：维护等写入同一存储的运行结束；运行在名额未满、
// 其存储没有到期或正在进行的维护时按排队顺序开始
func (s *Scheduler) pump() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	writing := map[int64]bool{}
	for _, st := range s.running {
		writing[st] = true
	}
	for st := range s.maintWanted {
		if !writing[st] {
			delete(s.maintWanted, st)
			s.maintActive[st] = true
			s.goMaintain(st)
		}
	}
	rest := s.queue[:0]
	for _, q := range s.queue {
		if len(s.running) >= maxConcurrentRuns || s.maintWanted[q.storageID] || s.maintActive[q.storageID] {
			rest = append(rest, q)
			continue
		}
		s.running[q.runID] = q.storageID
		s.goRun(q.runID)
	}
	s.queue = rest
}

func (s *Scheduler) goRun(runID int64) {
	s.wg.Add(1)
	gogo.Go(func() error {
		defer s.wg.Done()
		run, err := s.execute(s.ctx, runID)
		s.mu.Lock()
		delete(s.running, runID)
		s.mu.Unlock()
		ctx := context.WithoutCancel(s.ctx)
		if err != nil {
			logger.Ctx(ctx).Error("执行运行失败", zap.Int64("run_id", runID), zap.Error(err))
		} else {
			s.afterRun(ctx, run)
		}
		s.pump()
		return nil
	})
}

func (s *Scheduler) goMaintain(storageID int64) {
	s.wg.Add(1)
	gogo.Go(func() error {
		defer s.wg.Done()
		ctx, cancel := context.WithTimeout(s.ctx, maintenanceTimeout)
		err := s.maintain(ctx, storageID)
		cancel()
		log := logger.Ctx(s.ctx).With(zap.Int64("storage_id", storageID))
		switch {
		case errors.Is(err, storage_svc.ErrNotReady):
			log.Warn("存储状态不是“正常”，本次不做完整维护", zap.Error(err))
		case err != nil:
			log.Warn("存储完整维护失败", zap.Error(err))
		default:
			log.Info("存储完整维护完成")
		}
		s.mu.Lock()
		delete(s.maintActive, storageID)
		s.mu.Unlock()
		s.pump()
		return nil
	})
}

// checkMaintenance 被任务使用的存储距上次完整维护满一天时安排维护，返回最早的下一次到期时间
func (s *Scheduler) checkMaintenance(ctx context.Context, now time.Time, jobs []*job_entity.Job) time.Time {
	var used []int64
	for _, j := range jobs {
		if !slices.Contains(used, j.StorageID) {
			used = append(used, j.StorageID)
		}
	}
	var next time.Time
	for _, st := range used {
		last, ok := s.lastMaintenance(ctx, st, now)
		if !ok {
			continue
		}
		due := last.Add(maintenanceEvery)
		if due.After(now) {
			if next.IsZero() || due.Before(next) {
				next = due
			}
			continue
		}
		s.mu.Lock()
		s.maintWanted[st], s.maintLast[st] = true, now
		s.mu.Unlock()
		s.saveMaintenance(ctx, st, now)
	}
	return next
}

// lastMaintenance 存储最近一次完整维护的时间；第一次见到的存储从现在起算，一天后第一次维护
func (s *Scheduler) lastMaintenance(ctx context.Context, storageID int64, now time.Time) (time.Time, bool) {
	s.mu.Lock()
	t, ok := s.maintLast[storageID]
	s.mu.Unlock()
	if ok {
		return t, true
	}
	v, found, err := setting_repo.Setting().Get(ctx, maintenanceKey+strconv.FormatInt(storageID, 10))
	if err != nil {
		logger.Ctx(ctx).Error("读取存储的完整维护时间失败", zap.Int64("storage_id", storageID), zap.Error(err))
		return time.Time{}, false
	}
	sec, perr := strconv.ParseInt(v, 10, 64)
	if found && perr == nil {
		t = time.Unix(sec, 0)
	} else {
		t = now
		s.saveMaintenance(ctx, storageID, now)
	}
	s.mu.Lock()
	s.maintLast[storageID] = t
	s.mu.Unlock()
	return t, true
}

func (s *Scheduler) saveMaintenance(ctx context.Context, storageID int64, t time.Time) {
	key := maintenanceKey + strconv.FormatInt(storageID, 10)
	if err := setting_repo.Setting().Set(ctx, key, strconv.FormatInt(t.Unix(), 10)); err != nil {
		logger.Ctx(ctx).Error("记录存储的完整维护时间失败", zap.Int64("storage_id", storageID), zap.Error(err))
	}
}

// fullMaintenance 对存储做一次完整维护，回收已删除快照不再引用的数据
func fullMaintenance(ctx context.Context, storageID int64) error {
	w, err := storage_svc.Storage().OpenWriter(ctx, storageID)
	if err != nil {
		return err
	}
	defer func() {
		if err := w.Close(context.WithoutCancel(ctx)); err != nil {
			logger.Ctx(ctx).Warn("关闭存储写入会话失败", zap.Int64("storage_id", storageID), zap.Error(err))
		}
	}()
	return w.Maintain(ctx, kopiarepo.MaintenanceFull)
}
