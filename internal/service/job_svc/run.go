package job_svc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cago-frame/cago/pkg/gogo"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	dsapi "github.com/opskat/opsnap/internal/api/datasource"
	"github.com/opskat/opsnap/internal/model/entity/datasource_entity"
	"github.com/opskat/opsnap/internal/model/entity/job_entity"
	"github.com/opskat/opsnap/internal/model/entity/storage_entity"
	"github.com/opskat/opsnap/internal/pkg/dsconn"
	"github.com/opskat/opsnap/internal/pkg/dump"
	"github.com/opskat/opsnap/internal/pkg/kopiarepo"
	"github.com/opskat/opsnap/internal/pkg/netchain"
	"github.com/opskat/opsnap/internal/pkg/probe"
	"github.com/opskat/opsnap/internal/pkg/retention"
	"github.com/opskat/opsnap/internal/repository/datasource_repo"
	"github.com/opskat/opsnap/internal/repository/job_repo"
	"github.com/opskat/opsnap/internal/repository/storage_repo"
	"github.com/opskat/opsnap/internal/service/datasource_svc"
	"github.com/opskat/opsnap/internal/service/storage_svc"
)

// Trigger 一次运行的触发方式
type Trigger struct {
	// Kind job_entity.TriggerSchedule / TriggerManual / TriggerCatchUp / TriggerRetry
	Kind string
	// ScheduledAt 计划与补跑对应的计划时间（秒），其余为 0
	ScheduledAt int64
	// Attempt、Total 重试 i/N，其余为 0
	Attempt, Total int
}

var (
	// ErrRunActive 任务已有等待中或运行中的运行
	ErrRunActive = errors.New("任务已在运行或排队")
	// ErrRunNotFound 运行记录不存在
	ErrRunNotFound = errors.New("运行记录不存在")
	// ErrRunFinished 运行已结束，不能取消
	ErrRunFinished = errors.New("运行已结束")
)

// RunSvc 单次运行（docs/specs/2026-09-27-backup-jobs.md「执行」「运行记录」）。
// 一次运行先由 Enqueue 记为“等待中”，再由 Execute 按 准备 → 连接数据源 → 导出并写入仓库 → 校验 →
// 应用保留策略 执行到结束。两者之间可以放一个队列（全局并发上限、排队顺序）：Enqueue 之后调用派发函数
// （SetDispatcher，默认立即在后台 Execute），队列在轮到它时调用 Execute。
type RunSvc interface {
	// Enqueue 为任务新建一条等待中的运行记录（不执行）。任务已有等待中或运行中的运行时返回 ErrRunActive，
	// 任务不存在时返回 job_repo.ErrNotFound
	Enqueue(ctx context.Context, jobID int64, t Trigger) (*job_entity.Run, error)
	// Execute 执行一条等待中的运行直到结束（同步），返回最终的记录；运行已不是等待中（如已被取消）时
	// 什么也不做并返回当前记录。成功与失败都体现在记录的状态中，error 只表示无法开始（记录或任务不存在、读库失败）
	Execute(ctx context.Context, runID int64) (*job_entity.Run, error)
	// Cancel 取消运行：等待中的直接记为已取消；运行中的终止导出工具、关闭端口转发、删除临时文件，
	// 不形成快照，等它结束（最多 30 秒）后返回记录。已结束的返回 ErrRunFinished，不存在的返回 ErrRunNotFound
	Cancel(ctx context.Context, runID int64) (*job_entity.Run, error)
	// HasActive 任务是否有等待中或运行中的运行
	HasActive(ctx context.Context, jobID int64) (bool, error)
	// Recover 启动时调用：清理上次异常退出留下的运行临时目录（其中可能有凭据），把仍为运行中的记为失败
	// （“OpsNap 重启，运行中断”），仍在排队的记为已取消（“OpsNap 重启”）
	Recover(ctx context.Context) error
	// RunPending 处理带“立即执行一次”标记的任务：清除标记、以手动方式入队并派发，每个任务只执行一次
	RunPending(ctx context.Context) error
}

const (
	// logHead 日志超出上限时保留的开头行数；其余为结尾与一行省略标记
	logHead = 500
	logTail = job_entity.MaxLogLines - logHead - 1
	// cancelWait 取消运行中的运行后等待其结束的上限
	cancelWait = 30 * time.Second
	// retentionTimeout 应用保留策略（含快速维护）的上限；它在快照已确认之后执行，不受取消与超时影响
	retentionTimeout = 30 * time.Minute
	// reasonHead、reasonTail 失败原因过长时保留的开头与结尾字符数，完整内容在日志中
	reasonHead = 600
	reasonTail = 1200
)

var (
	// timeoutUnit 任务超时时长的单位，测试可缩短
	timeoutUnit = time.Minute
	// listDatabases 每次运行时列出数据源中的库（“整个实例”的范围、“指定数据库”是否仍存在），测试可替换
	listDatabases = datasource_svc.ListOpenDatabases
)

var (
	errCanceled = errors.New("运行已取消")
	errTimeout  = errors.New("运行超时")
	// errShutdown OpsNap 停止时中断进行中的运行（调度组件关闭）
	errShutdown = errors.New("OpsNap 停止")
)

type runner struct {
	// mu 串行化入队检查与写入、开始与取消的状态转换，以及删除任务时的“无进行中运行”检查
	mu     sync.Mutex
	active map[int64]*activeRun

	cfgMu sync.RWMutex
	now   func() time.Time
	// dispatch 入队后的派发，nil 为 backgroundDispatch
	dispatch func(runID int64)
	workDir  string
}

var defaultRunner = &runner{active: map[int64]*activeRun{}, now: time.Now,
	workDir: filepath.Join(os.TempDir(), "opsnap-runs")}

// Runs 单次运行，供调度模块调用
func Runs() RunSvc { return defaultRunner }

// backgroundDispatch 默认的派发：立即在后台执行
func backgroundDispatch(runID int64) {
	gogo.Go(func() error {
		ctx := context.Background()
		if _, err := defaultRunner.Execute(ctx, runID); err != nil {
			logger.Ctx(ctx).Error("执行运行失败", zap.Int64("run_id", runID), zap.Error(err))
		}
		return nil
	})
}

// SetDispatcher 替换入队后的派发函数（如调度模块的全局队列）；nil 恢复为立即在后台执行
func SetDispatcher(fn func(runID int64)) {
	defaultRunner.cfgMu.Lock()
	defer defaultRunner.cfgMu.Unlock()
	defaultRunner.dispatch = fn
}

// SetWorkDir 设置运行临时目录（导出工具的凭据文件）的父目录，启动时由 Recover 清理其中的残留
func SetWorkDir(dir string) {
	defaultRunner.cfgMu.Lock()
	defer defaultRunner.cfgMu.Unlock()
	defaultRunner.workDir = dir
}

func (r *runner) setClock(fn func() time.Time) {
	if fn == nil {
		fn = time.Now
	}
	r.cfgMu.Lock()
	defer r.cfgMu.Unlock()
	r.now = fn
}

func (r *runner) clock() time.Time {
	r.cfgMu.RLock()
	defer r.cfgMu.RUnlock()
	return r.now()
}

func (r *runner) dir() string {
	r.cfgMu.RLock()
	defer r.cfgMu.RUnlock()
	return r.workDir
}

func (r *runner) dispatchRun(runID int64) {
	r.cfgMu.RLock()
	fn := r.dispatch
	r.cfgMu.RUnlock()
	if fn == nil {
		fn = backgroundDispatch
	}
	fn(runID)
}

// activeRun 本进程中正在执行的一次运行；ctx 与 cancel 在登记之前建好，登记后随时可以取消
type activeRun struct {
	ctx    context.Context //nolint:containedctx // 运行的生命周期，Cancel 经 cancel 结束它
	cancel context.CancelCauseFunc
	log    *runLog
	sess   atomic.Pointer[dump.Session]
	done   chan struct{}
}

func (r *runner) lookup(runID int64) *activeRun {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.active[runID]
}

// liveBytes 运行中的已导出字节数；不在本进程中执行时返回 -1
func (r *runner) liveBytes(runID int64) int64 {
	ar := r.lookup(runID)
	if ar == nil {
		return -1
	}
	if s := ar.sess.Load(); s != nil {
		return s.Bytes()
	}
	return 0
}

func (r *runner) Enqueue(ctx context.Context, jobID int64, t Trigger) (*job_entity.Run, error) {
	switch t.Kind {
	case job_entity.TriggerSchedule, job_entity.TriggerManual, job_entity.TriggerCatchUp, job_entity.TriggerRetry:
	default:
		return nil, fmt.Errorf("未知的触发方式 %q", t.Kind)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	j, err := job_repo.Job().Find(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if j == nil {
		return nil, job_repo.ErrNotFound
	}
	act, err := job_repo.Run().Active(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if act != nil {
		return nil, ErrRunActive
	}
	now := r.clock().Unix()
	run := &job_entity.Run{JobID: jobID, Status: job_entity.RunQueued, Trigger: t.Kind,
		RetryAttempt: t.Attempt, RetryTotal: t.Total, ScheduledAt: t.ScheduledAt, Log: "[]",
		Createtime: now, Updatetime: now}
	if err := job_repo.Run().Create(ctx, run); err != nil {
		return nil, err
	}
	return run, nil
}

func (r *runner) HasActive(ctx context.Context, jobID int64) (bool, error) {
	act, err := job_repo.Run().Active(ctx, jobID)
	return act != nil, err
}

// begin 把等待中的运行转为运行中并登记；运行已不是等待中时返回当前记录与 nil
func (r *runner) begin(ctx context.Context, runID int64) (*job_entity.Run, *job_entity.Job, *activeRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, err := job_repo.Run().Find(ctx, runID)
	if err != nil {
		return nil, nil, nil, err
	}
	if run == nil {
		return nil, nil, nil, ErrRunNotFound
	}
	if run.Status != job_entity.RunQueued {
		return run, nil, nil, nil
	}
	j, err := job_repo.Job().Find(ctx, run.JobID)
	if err != nil {
		return nil, nil, nil, err
	}
	if j == nil {
		return nil, nil, nil, job_repo.ErrNotFound
	}
	now := r.clock()
	run.Status, run.StartedAt, run.Updatetime = job_entity.RunRunning, now.UnixMilli(), now.Unix()
	ok, err := job_repo.Run().SaveIf(ctx, run, job_entity.RunQueued)
	if err != nil {
		return nil, nil, nil, err
	}
	if !ok {
		cur, err := job_repo.Run().Find(ctx, runID)
		return cur, nil, nil, err
	}
	runCtx, cancel := context.WithCancelCause(ctx)
	ar := &activeRun{ctx: runCtx, cancel: cancel, log: newRunLog(r.clock), done: make(chan struct{})}
	r.active[runID] = ar
	return run, j, ar, nil
}

func (r *runner) Execute(ctx context.Context, runID int64) (*job_entity.Run, error) {
	run, j, ar, err := r.begin(ctx, runID)
	if err != nil || ar == nil {
		return run, err
	}
	tctx, tcancel := context.WithTimeoutCause(ar.ctx, time.Duration(j.Timeout)*timeoutUnit, errTimeout)
	defer tcancel()
	defer ar.cancel(nil)

	x := &execution{r: r, run: run, job: j, log: ar.log, ar: ar, started: time.UnixMilli(run.StartedAt)}
	res, stepErr := x.do(tctx)
	x.finish(tctx, res, stepErr)

	// 记录用不受取消影响的 ctx 保存
	sctx := context.WithoutCancel(ctx)
	run.FinishedAt = r.clock().UnixMilli()
	run.Updatetime = run.FinishedAt / 1000
	run.SetLog(ar.log.lines())
	if _, err := job_repo.Run().SaveIf(sctx, run, job_entity.RunRunning); err != nil {
		logger.Ctx(sctx).Error("保存运行记录失败", zap.Int64("run_id", run.ID), zap.Error(err))
	}
	r.mu.Lock()
	delete(r.active, runID)
	r.mu.Unlock()
	close(ar.done)
	if err := job_repo.Run().Trim(sctx, j.ID, job_entity.MaxRunsPerJob); err != nil {
		logger.Ctx(sctx).Warn("清理更早的运行记录失败", zap.Int64("job_id", j.ID), zap.Error(err))
	}
	return run, nil
}

func (r *runner) Cancel(ctx context.Context, runID int64) (*job_entity.Run, error) {
	r.mu.Lock()
	run, err := job_repo.Run().Find(ctx, runID)
	if err != nil || run == nil {
		r.mu.Unlock()
		if err == nil {
			err = ErrRunNotFound
		}
		return nil, err
	}
	if ar := r.active[runID]; ar != nil {
		r.mu.Unlock()
		ar.cancel(errCanceled)
		select {
		case <-ar.done:
		case <-time.After(cancelWait):
		case <-ctx.Done():
		}
		return job_repo.Run().Find(ctx, runID)
	}
	defer r.mu.Unlock()
	if run.Status != job_entity.RunQueued {
		return nil, ErrRunFinished
	}
	now := r.clock()
	run.Status, run.FinishedAt, run.Updatetime = job_entity.RunCanceled, now.UnixMilli(), now.Unix()
	ok, err := job_repo.Run().SaveIf(ctx, run, job_entity.RunQueued)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrRunFinished
	}
	return run, nil
}

func (r *runner) Recover(ctx context.Context) error {
	sweepErr := dump.Sweep(r.dir())
	if sweepErr != nil {
		logger.Ctx(ctx).Error("清理运行临时目录失败", zap.Error(sweepErr))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	runs, err := job_repo.Run().ListActive(ctx)
	if err != nil {
		return err
	}
	now := r.clock()
	for _, run := range runs {
		if r.active[run.ID] != nil {
			continue
		}
		from := run.Status
		if from == job_entity.RunRunning {
			run.Status = job_entity.RunFailed
			run.SetFixedReason(job_entity.ReasonInterrupted)
		} else {
			run.Status = job_entity.RunCanceled
			run.SetFixedReason(job_entity.ReasonRestart)
		}
		run.FinishedAt, run.Updatetime = now.UnixMilli(), now.Unix()
		if _, err := job_repo.Run().SaveIf(ctx, run, from); err != nil {
			return err
		}
	}
	return sweepErr
}

func (r *runner) RunPending(ctx context.Context) error {
	jobs, err := job_repo.Job().ListRunNow(ctx)
	if err != nil {
		return err
	}
	for _, j := range jobs {
		if err := r.startPending(ctx, j.ID); err != nil {
			return err
		}
	}
	return nil
}

// skip 记一条“跳过”的运行：计划到点时任务已在运行或排队；reasonCode 为固定原因（job_entity.Reason*）
func (r *runner) skip(ctx context.Context, jobID int64, t Trigger, reasonCode string) (*job_entity.Run, error) {
	now := r.clock()
	run := &job_entity.Run{JobID: jobID, Status: job_entity.RunSkipped, Trigger: t.Kind, RetryAttempt: t.Attempt,
		RetryTotal: t.Total, ScheduledAt: t.ScheduledAt, FinishedAt: now.UnixMilli(), Log: "[]",
		Createtime: now.Unix(), Updatetime: now.Unix()}
	run.SetFixedReason(reasonCode)
	if err := job_repo.Run().Create(ctx, run); err != nil {
		return nil, err
	}
	if err := job_repo.Run().Trim(ctx, jobID, job_entity.MaxRunsPerJob); err != nil {
		logger.Ctx(ctx).Warn("清理更早的运行记录失败", zap.Int64("job_id", jobID), zap.Error(err))
	}
	return run, nil
}

// void 把仍在等待中的运行记为已取消并写明固定原因（如作废的重试）；运行已开始或已结束时返回 false
func (r *runner) void(ctx context.Context, runID int64, reasonCode string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, err := job_repo.Run().Find(ctx, runID)
	if err != nil || run == nil || run.Status != job_entity.RunQueued {
		return false, err
	}
	now := r.clock()
	run.Status, run.FinishedAt, run.Updatetime = job_entity.RunCanceled, now.UnixMilli(), now.Unix()
	run.SetFixedReason(reasonCode)
	return job_repo.Run().SaveIf(ctx, run, job_entity.RunQueued)
}

// startPending 任务带“立即执行一次”标记时清除标记并以手动方式入队、派发；标记只会被一个调用方清除
func (r *runner) startPending(ctx context.Context, jobID int64) error {
	cleared, err := job_repo.Job().ClearRunNow(ctx, jobID)
	if err != nil || !cleared {
		return err
	}
	run, err := r.Enqueue(ctx, jobID, Trigger{Kind: job_entity.TriggerManual})
	if errors.Is(err, ErrRunActive) || errors.Is(err, job_repo.ErrNotFound) {
		logger.Ctx(ctx).Info("立即执行一次未执行", zap.Int64("job_id", jobID), zap.Error(err))
		return nil
	}
	if err != nil {
		return err
	}
	r.dispatchRun(run.ID)
	return nil
}

// stepError 某一步失败
type stepError struct {
	step string
	err  error
}

func (e *stepError) Error() string { return e.err.Error() }
func (e *stepError) Unwrap() error { return e.err }

func failAt(step string, format string, args ...any) error {
	return &stepError{step: step, err: fmt.Errorf(format, args...)}
}

// execution 一次运行的执行过程；设置取自开始时的任务（运行中修改任务不影响本次）
type execution struct {
	r       *runner
	run     *job_entity.Run
	job     *job_entity.Job
	log     *runLog
	ar      *activeRun
	started time.Time

	st     *storage_entity.Storage
	ds     *datasource_entity.DataSource
	writer *kopiarepo.Writer
	tunnel *netchain.Tunnel
	sess   *dump.Session
}

func (x *execution) do(ctx context.Context) (res *kopiarepo.SnapshotResult, err error) {
	defer x.close(ctx)
	if err := x.prepare(ctx); err != nil {
		return nil, err
	}
	if err := x.connect(ctx); err != nil {
		return nil, err
	}
	res, err = x.export(ctx)
	if err != nil {
		return nil, err
	}
	x.retention(ctx, res)
	return res, nil
}

// close 终止导出工具、关闭本机端口与链路、删除运行临时目录，最后关闭存储写入会话
func (x *execution) close(ctx context.Context) {
	cctx := context.WithoutCancel(ctx)
	if x.sess != nil {
		if err := x.sess.Close(); err != nil {
			logger.Ctx(cctx).Warn("关闭导出会话失败", zap.Int64("run_id", x.run.ID), zap.Error(err))
		}
	}
	if x.tunnel != nil {
		_ = x.tunnel.Close()
	}
	if x.writer != nil {
		if err := x.writer.Close(cctx); err != nil {
			logger.Ctx(cctx).Warn("关闭存储写入会话失败", zap.Int64("run_id", x.run.ID), zap.Error(err))
		}
	}
}

// requiredTools 任务需要的导出工具
func requiredTools(j *job_entity.Job, kind string) []string {
	if kind == datasource_entity.KindMySQL {
		return []string{"mysqldump"}
	}
	tools := []string{"pg_dump"}
	if j.OptGlobals {
		tools = append(tools, "pg_dumpall")
	}
	return tools
}

// prepare 检查存储状态为正常、数据源状态不是主机密钥已变化、导出工具可用，并打开存储；不连接数据源
func (x *execution) prepare(ctx context.Context) error {
	const step = job_entity.StepPrepare
	x.log.setStep(step)
	x.log.add("检查存储、数据源与导出工具")
	var err error
	if x.st, err = storage_repo.Storage().Find(ctx, x.job.StorageID); err != nil {
		return &stepError{step: step, err: err}
	}
	if x.st == nil {
		return failAt(step, "存储不存在")
	}
	if x.st.Status != storage_entity.StatusOK {
		return failAt(step, "存储“%s”的状态不是“正常”（%s），请先测试存储连接", x.st.Name, x.st.Status)
	}
	if x.ds, err = datasource_repo.DataSource().Find(ctx, x.job.DataSourceID); err != nil {
		return &stepError{step: step, err: err}
	}
	if x.ds == nil {
		return failAt(step, "数据源不存在")
	}
	if x.ds.Status == datasource_entity.StatusHostKeyChanged {
		return failAt(step, "数据源“%s”链路上的主机密钥已变化，请先在数据源页面确认新的主机密钥", x.ds.Name)
	}
	// 导出工具可用：找得到、读得出版本；PostgreSQL 工具的大版本不低于数据源最近一次测试时的版本；
	// MariaDB 的 mysqldump 能满足数据源的 TLS 模式。都在连接之前检查（连接后导出包按实际版本再查一次）
	if err := dump.CheckTools(ctx, dsconn.Type(x.ds.Kind), dsconn.TLSMode(x.ds.TLSMode), x.ds.Version,
		dump.Options{Globals: x.job.OptGlobals && x.ds.Kind == datasource_entity.KindPostgreSQL}); err != nil {
		return &stepError{step: step, err: err}
	}
	for _, name := range requiredTools(x.job, x.ds.Kind) {
		if path, ok := probe.ToolPath(name); ok {
			x.log.add(fmt.Sprintf("导出工具 %s：%s", name, path))
		}
	}
	if x.writer, err = storage_svc.Storage().OpenWriter(ctx, x.st.ID); err != nil {
		return failAt(step, "打开存储“%s”失败: %w", x.st.Name, err)
	}
	x.log.add(fmt.Sprintf("已打开存储“%s”", x.st.Name))
	return nil
}

// connect 沿网络通道连接数据源，“整个实例”时列出库，然后在本机 127.0.0.1 开临时端口转发给导出工具
func (x *execution) connect(ctx context.Context) error {
	const step = job_entity.StepConnect
	x.log.setStep(step)
	x.log.add(fmt.Sprintf("沿网络通道连接数据源“%s”（%s:%d）", x.ds.Name, x.ds.Host, x.ds.Port))
	tun, conn, cfg, secrets, err := datasource_svc.DataSource().OpenSaved(ctx, x.ds.ID)
	x.log.addSecrets(secrets...)
	if err != nil {
		return failAt(step, "连接数据源失败: %w", err)
	}
	x.tunnel = tun
	defer func() { _ = conn.Close() }()
	x.log.add("已连接，服务端版本 " + conn.Info.Version)

	// “整个实例”每次运行重新列出库；“指定数据库”确认每个库仍然存在，不存在时指出是哪个库
	list, err := listDatabases(ctx, cfg.Type, conn)
	if err != nil {
		return failAt(step, "列出实例中的库失败: %w", err)
	}
	dbs := x.job.Databases()
	if x.job.Scope == job_entity.ScopeInstance {
		dbs = dbs[:0]
		for _, d := range list {
			dbs = append(dbs, d.Name)
		}
		if len(dbs) == 0 {
			return failAt(step, "实例中没有可导出的库")
		}
	} else if missing := missingDatabases(cfg.Type, dbs, list); len(missing) > 0 {
		return failAt(step, "指定的库在数据源中不存在（或不允许连接）：%s", strings.Join(missing, ", "))
	}
	x.log.add("导出的库：" + strings.Join(dbs, ", "))

	opts := dump.Options{Databases: dbs, ExcludeTables: x.job.ExcludeTableList(), Log: x.log.add}
	if cfg.Type == dsconn.TypeMySQL {
		opts.Routines, opts.Triggers, opts.Events, opts.Accounts = x.job.OptRoutines, x.job.OptTriggers, x.job.OptEvents, x.job.OptUsers
	} else {
		opts.Globals = x.job.OptGlobals
	}
	sess, err := dump.Start(ctx, x.r.dir(), dump.Source{Dialer: tun, Config: cfg, ServerVersion: conn.Info.Version}, opts)
	if err != nil {
		return failAt(step, "准备导出失败: %w", err)
	}
	x.sess = sess
	x.ar.sess.Store(sess)
	x.log.add("已在本机 127.0.0.1 开临时端口，导出工具的连接经链路转发到数据源")
	return nil
}

// mysqlSystemSchemas MySQL 的系统库：不在库列表中（“整个实例”不包含它们），但可以被指定
var mysqlSystemSchemas = []string{"information_schema", "performance_schema", "sys", "mysql"}

// missingDatabases 指定的库中不在数据源库列表里的，按指定顺序
func missingDatabases(typ dsconn.Type, want []string, list []dsapi.Database) []string {
	have := make(map[string]bool, len(list))
	for _, d := range list {
		have[d.Name] = true
	}
	var missing []string
	for _, name := range want {
		if have[name] || typ == dsconn.TypeMySQL && slices.Contains(mysqlSystemSchemas, name) {
			continue
		}
		missing = append(missing, name)
	}
	return missing
}

// export 把导出工具的输出流式写入一份新快照；写入后 kopiarepo 已读回校验每个文件的大小与内容
func (x *execution) export(ctx context.Context) (*kopiarepo.SnapshotResult, error) {
	x.log.setStep(job_entity.StepExport)
	x.log.add(fmt.Sprintf("导出并流式写入仓库，路径前缀 %s，压缩 %s", x.job.Prefix, x.job.Compression))
	files := make([]kopiarepo.SnapshotFile, 0, len(x.sess.Files()))
	for _, f := range x.sess.Files() {
		files = append(files, kopiarepo.SnapshotFile{Name: f.Name, Reader: f})
	}
	res, err := x.writer.WriteSnapshot(ctx, kopiarepo.SnapshotRequest{
		Prefix:      x.job.Prefix,
		Tags:        kopiarepo.SnapshotTags{JobID: x.job.ID, RunID: x.run.ID, Type: job_entity.MethodFull, Kind: x.ds.Kind},
		Compression: kopiarepo.Compression(x.job.Compression),
		Files:       files,
	})
	if err != nil {
		step := job_entity.StepExport
		// 完整性检查（完成标记、归档头）与读回校验属于“校验”
		if errors.Is(err, dump.ErrIncomplete) || strings.Contains(err.Error(), "读回校验快照") {
			step = job_entity.StepVerify
		}
		return nil, &stepError{step: step, err: err}
	}
	x.log.setStep(job_entity.StepVerify)
	x.log.add("导出工具均正常退出，导出内容通过完整性检查")
	var total int64
	for _, f := range res.Files {
		total += f.Size
		x.log.add(fmt.Sprintf("%s：%d 字节", f.Name, f.Size))
	}
	x.log.add(fmt.Sprintf("快照 %s 已写入并读回校验：%d 个文件，共 %d 字节，新增上传 %d 字节", res.ID, len(res.Files), total, res.UploadedBytes))
	return res, nil
}

// retention 成功之后按任务的保留策略删除本任务更早的快照，删除后做一次快速维护。
// 快照已经确认，这一步的失败只写进日志，本次运行仍为成功
func (x *execution) retention(ctx context.Context, res *kopiarepo.SnapshotResult) {
	x.log.setStep(job_entity.StepRetention)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), retentionTimeout)
	defer cancel()
	ref := x.job.Ref()
	snaps, err := x.writer.ListJobSnapshots(ctx, ref)
	if err != nil {
		x.log.add(fmt.Sprintf("读取本任务的快照失败，本次不应用保留策略: %v", err))
		return
	}
	loc, err := time.LoadLocation(x.job.Timezone)
	if err != nil {
		loc = time.UTC
	}
	in := make([]retention.Snapshot, len(snaps))
	for i, s := range snaps {
		in[i] = retention.Snapshot{ID: s.ID, Time: s.StartTime}
	}
	_, remove := retention.Select(in, retention.Policy{Days: x.job.RetainDays, Weeks: x.job.RetainWeeks, Months: x.job.RetainMonths},
		loc, x.started)
	// 本次成功的快照无论如何不删除
	remove = slicesDelete(remove, res.ID)
	x.log.add(fmt.Sprintf("保留策略：最近 %d 天、%d 周、%d 个月；本任务共 %d 份快照，保留 %d 份，删除 %d 份",
		x.job.RetainDays, x.job.RetainWeeks, x.job.RetainMonths, len(snaps), len(snaps)-len(remove), len(remove)))
	count := len(snaps)
	if len(remove) > 0 {
		n, err := x.writer.DeleteSnapshots(ctx, ref, remove)
		if err != nil {
			x.log.add(fmt.Sprintf("删除过期快照失败（本次运行仍为成功）: %v", err))
		} else {
			count -= n
			x.log.add(fmt.Sprintf("已删除 %d 份过期快照，开始快速维护", n))
			if err := x.writer.Maintain(ctx, kopiarepo.MaintenanceQuick); err != nil {
				x.log.add(fmt.Sprintf("快速维护失败（本次运行仍为成功）: %v", err))
			} else {
				x.log.add("快速维护完成；删除的数据在之后的完整维护中释放")
			}
		}
	}
	if err := job_repo.Job().SetSnapshotCount(ctx, x.job.ID, count); err != nil {
		logger.Ctx(ctx).Warn("记录快照数失败", zap.Int64("job_id", x.job.ID), zap.Error(err))
	}
}

func slicesDelete(ids []string, id string) []string {
	out := ids[:0]
	for _, v := range ids {
		if v != id {
			out = append(out, v)
		}
	}
	return out
}

// finish 按结果填写运行记录：确认了快照即成功；否则取消为已取消，超时为失败“超时（超过 X）”，其余为失败并指出步骤
func (x *execution) finish(ctx context.Context, res *kopiarepo.SnapshotResult, err error) {
	run := x.run
	if res != nil {
		run.Status, run.SnapshotID, run.UploadedBytes = job_entity.RunSuccess, res.ID, res.UploadedBytes
		run.ExportedBytes = 0
		for _, f := range res.Files {
			run.ExportedBytes += f.Size
		}
		return
	}
	if x.sess != nil {
		run.ExportedBytes = x.sess.Bytes()
	}
	step := x.log.currentStep()
	var se *stepError
	if errors.As(err, &se) {
		step = se.step
	}
	cause := context.Cause(ctx)
	switch {
	case errors.Is(cause, errCanceled):
		run.Status = job_entity.RunCanceled
		x.log.add("运行已取消：已终止导出工具、关闭端口转发并删除临时文件，未形成快照")
	case errors.Is(cause, errShutdown):
		run.Status, run.FailedStep = job_entity.RunFailed, step
		run.SetFixedReason(job_entity.ReasonInterrupted)
		x.log.setStep(step)
		x.log.add(run.Reason + "：已终止导出工具，未形成快照")
	case errors.Is(cause, errTimeout):
		run.Status, run.FailedStep = job_entity.RunFailed, step
		run.SetFixedReason(job_entity.TimeoutReason(x.job.Timeout))
		x.log.setStep(step)
		x.log.add(run.Reason + "：已终止导出工具，未形成快照")
	default:
		run.Status, run.FailedStep = job_entity.RunFailed, step
		msg := x.log.scrub(err.Error())
		run.Reason = limitReason(msg)
		x.log.setStep(step)
		x.log.add(msg)
	}
}

// limitReason 过长的失败原因只保留开头与结尾（完整内容在日志中）
func limitReason(s string) string {
	r := []rune(s)
	if len(r) <= reasonHead+reasonTail {
		return s
	}
	return string(r[:reasonHead]) + "\n……\n" + string(r[len(r)-reasonTail:])
}

// runLog 一次运行的执行日志：每行带时间和步骤名，写入前去掉秘密；超过 MaxLogLines 行时保留开头与结尾，
// 中间用一行省略标记注明省略了多少行
type runLog struct {
	mu      sync.Mutex
	now     func() time.Time
	step    string
	secrets []string
	head    []job_entity.LogLine
	tail    []job_entity.LogLine
	omitted int
}

func newRunLog(now func() time.Time) *runLog { return &runLog{now: now} }

func (l *runLog) setStep(step string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.step = step
}

func (l *runLog) currentStep() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.step
}

func (l *runLog) addSecrets(secrets ...string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, s := range secrets {
		if s != "" {
			l.secrets = append(l.secrets, s)
		}
	}
}

func (l *runLog) scrub(text string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.scrubLocked(text)
}

func (l *runLog) scrubLocked(text string) string {
	for _, s := range l.secrets {
		text = strings.ReplaceAll(text, s, "******")
	}
	return text
}

// add 按当前步骤追加日志，多行文本拆成多行
func (l *runLog) add(msg string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	msg = l.scrubLocked(msg)
	t := l.now().UnixMilli()
	for _, line := range strings.Split(strings.TrimRight(msg, "\n"), "\n") {
		entry := job_entity.LogLine{Time: t, Step: l.step, Message: line}
		switch {
		case len(l.head) < logHead:
			l.head = append(l.head, entry)
		case len(l.tail) < logTail:
			l.tail = append(l.tail, entry)
		default:
			l.tail = append(l.tail[1:], entry)
			l.omitted++
		}
	}
}

func (l *runLog) lines() []job_entity.LogLine {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]job_entity.LogLine, 0, len(l.head)+len(l.tail)+1)
	out = append(out, l.head...)
	if l.omitted > 0 {
		out = append(out, job_entity.LogLine{Time: l.tail[0].Time, Omitted: l.omitted})
	}
	return append(out, l.tail...)
}
