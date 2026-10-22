// Package run_svc 运行引擎与运行记录。
//
// 一次运行：预检（存储位置可访问、已初始化、本地目录有挂载标记；需要时经代理链连上服务器并核对指纹）
// → 取存储位置的共享锁 → 导出并写入快照（数据库经管道直接进入 restic；文件先 rsync 到本机镜像再生成快照）
// → 按保留规则 forget → 记录 → 通知。任何失败、超时或手动停止都不会留下快照。
// 运行在 gogo.Go 启动的 goroutine 里进行，不依赖发起请求的 ctx。
package run_svc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/cago-frame/cago/pkg/gogo"
	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	runapi "github.com/opskat/opsnap/internal/api/run"
	"github.com/opskat/opsnap/internal/model/auth"
	"github.com/opskat/opsnap/internal/model/entity/job_entity"
	"github.com/opskat/opsnap/internal/model/entity/notify_entity"
	"github.com/opskat/opsnap/internal/model/entity/run_entity"
	"github.com/opskat/opsnap/internal/model/entity/setting_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/executor"
	"github.com/opskat/opsnap/internal/pkg/repolock"
	"github.com/opskat/opsnap/internal/repository/job_repo"
	"github.com/opskat/opsnap/internal/repository/run_repo"
	"github.com/opskat/opsnap/internal/service/alert_svc"
	"github.com/opskat/opsnap/internal/service/job_svc"
	"github.com/opskat/opsnap/internal/service/notify_svc"
	"github.com/opskat/opsnap/internal/service/repo_svc"
	"github.com/opskat/opsnap/internal/service/setting_svc"
)

var (
	// ErrAlreadyRunning 同一任务正在运行（含排队等待名额）。调度器据此记录一次跳过。
	ErrAlreadyRunning = errors.New("任务正在运行")

	errTimeout = errors.New("超时")
	errStopped = errors.New("手动停止")
)

const (
	// interruptedReason opsnap 重启时仍在运行的记录的失败原因
	interruptedReason = "opsnap 重启，运行被中断"
	// EventPing StreamLog 在长时间没有新日志时发出的保活事件，没有数据
	EventPing = "ping"
	// pingInterval 保活间隔，避免反向代理断开空闲连接
	pingInterval = 15 * time.Second
)

// RunSvc 运行引擎与运行记录
type RunSvc interface {
	// Trigger 开始一次运行并立即返回运行 ID；同一任务正在运行时返回 ErrAlreadyRunning，不产生记录。
	// 运行受全局「同时运行的任务数」限制，名额不足时排队，排队期间也算正在运行。
	Trigger(ctx context.Context, jobID int64, trig run_entity.Trigger) (int64, error)
	// RunJob 手动运行；发起者是 API 密钥时触发方式记为 api_key:<名称>
	RunJob(ctx context.Context, req *runapi.RunJobRequest) (*runapi.RunJobResponse, error)
	List(ctx context.Context, req *runapi.ListRequest) (*runapi.ListResponse, error)
	Get(ctx context.Context, id int64) (*runapi.GetResponse, error)
	// Stop 停止运行：效果同超时，原因写「手动停止」
	Stop(ctx context.Context, req *runapi.StopRequest) (*runapi.StopResponse, error)
	Log(ctx context.Context, req *runapi.LogRequest) (*runapi.LogResponse, error)
	// StreamLog 推送日志：先补发已有的行（event line），再推送新行，运行结束时发送 end，空闲时发送 EventPing。
	// emit 返回错误或 ctx 结束时停止并返回 nil；运行不存在时在发送任何事件前返回错误。
	StreamLog(ctx context.Context, id int64, warningsOnly bool, emit func(event string, data any) error) error
	// Attention 需要处理的任务：最近一次有结果的运行失败或有警告，或太久没有成功（与概览的「需要处理」一致）
	Attention(ctx context.Context) (map[int64]bool, error)
	// Recover 启动时把上次进程留下的「运行中」记录改为失败（进行中的步骤记为失败），并在后台发送「备份失败」通知
	Recover(ctx context.Context) error
	// CleanupForgetPending 补做因存储位置被其他备份占用而延后的旧快照清理。
	// 只处理当前空闲的存储位置，不写运行记录、不发通知。
	CleanupForgetPending(ctx context.Context) error
}

// Deps 外部依赖，测试里替换；为空的项使用真实实现
type Deps struct {
	// Runner 执行 restic、rsync 等子进程
	Runner executor.Runner
	Now    func() time.Time
	// DataDir 数据目录，文件镜像放在 <DataDir>/mirror/<任务 ID>
	DataDir string
	// Self opsnap 可执行文件的绝对路径，rsync 经它的 rsh 子命令访问服务器
	Self string
	// ListDatabases 运行时展开「全部，含以后新建」
	ListDatabases func(ctx context.Context, j *job_entity.Job, addr string) ([]string, error)
	// Notify 发送通知
	Notify func(ctx context.Context, e notify_svc.Event) error
	// Stale 太久没有成功的任务
	Stale func(ctx context.Context) ([]alert_svc.StaleJob, error)
}

// active 一次进行中的运行
type active struct {
	log    *liveLog
	cancel context.CancelCauseFunc
	done   chan struct{}
}

type runSvc struct {
	d       Deps
	limiter *limiter

	mu    sync.Mutex
	byJob map[int64]*active
	byRun map[int64]*active
	// bg 后台任务（重启后补发的通知），测试里等待它结束
	bg sync.WaitGroup
}

func newRun(d Deps) *runSvc {
	if d.Runner == nil {
		d.Runner = executor.New()
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.DataDir == "" {
		d.DataDir = "/data"
	}
	if d.Self == "" {
		if exe, err := os.Executable(); err == nil {
			d.Self = exe
		}
	}
	if d.ListDatabases == nil {
		d.ListDatabases = job_svc.ListDatabases
	}
	if d.Notify == nil {
		d.Notify = notify_svc.Send
	}
	if d.Stale == nil {
		d.Stale = alert_svc.Stale
	}
	return &runSvc{d: d, limiter: newLimiter(), byJob: map[int64]*active{}, byRun: map[int64]*active{}}
}

// NewRun 创建运行服务
func NewRun(d Deps) RunSvc { return newRun(d) }

var defaultRun RunSvc = newRun(Deps{})

// Run 取得运行服务
func Run() RunSvc { return defaultRun }

// RegisterRun 替换服务实现（main 按配置的数据目录注册）
func RegisterRun(i RunSvc) { defaultRun = i }

// CleanupForgetPending 补做撞锁时延后的旧快照清理，供调度器在每一轮调用。
//
// 只处理当前在本进程内空闲的存储位置：拿到它的独占锁就说明没有备份在写、也没有维护在进行。
// 补做不是一次备份运行，所以既不写运行记录，也不发通知；成功与否都只体现为任务上的待清理标记与一条服务日志。
func (s *runSvc) CleanupForgetPending(ctx context.Context) error {
	jobs, err := job_repo.Job().ListForgetPending(ctx)
	if err != nil {
		return err
	}
	// 按存储位置分组，保持任务 ID 升序；同一位置只试探一次独占锁
	order := make([]int64, 0, len(jobs))
	byRepo := make(map[int64][]*job_entity.Job, len(jobs))
	for _, j := range jobs {
		if _, ok := byRepo[j.RepoID]; !ok {
			order = append(order, j.RepoID)
		}
		byRepo[j.RepoID] = append(byRepo[j.RepoID], j)
	}
	for _, repoID := range order {
		s.cleanupRepo(ctx, repoID, byRepo[repoID])
	}
	return nil
}

// CleanupForgetPending 补做撞锁时延后的旧快照清理，供调度器使用
func CleanupForgetPending(ctx context.Context) error { return defaultRun.CleanupForgetPending(ctx) }

// Trigger 见 RunSvc.Trigger，供调度器使用
func Trigger(ctx context.Context, jobID int64, trig run_entity.Trigger) (int64, error) {
	return defaultRun.Trigger(ctx, jobID, trig)
}

// Attention 见 RunSvc.Attention，供 job_svc.RegisterAttention 注册
func Attention(ctx context.Context) (map[int64]bool, error) { return defaultRun.Attention(ctx) }

// maxConcurrent 读取设置里的同时运行上限
func (s *runSvc) maxConcurrent() int {
	st, err := setting_svc.Get(context.Background())
	if err != nil {
		return setting_entity.Default().MaxConcurrentRuns
	}
	return st.MaxConcurrentRuns
}

// wait 等到运行结束（测试用）
func (s *runSvc) wait(runID int64) {
	s.mu.Lock()
	a := s.byRun[runID]
	s.mu.Unlock()
	if a != nil {
		<-a.done
	}
}

// Trigger 见 RunSvc.Trigger
func (s *runSvc) Trigger(ctx context.Context, jobID int64, trig run_entity.Trigger) (int64, error) {
	j, err := job_repo.Job().Find(ctx, jobID)
	if err != nil {
		return 0, err
	}
	if err := j.Check(ctx); err != nil {
		return 0, err
	}
	a := &active{log: newLiveLog(s.d.Now), done: make(chan struct{})}
	s.mu.Lock()
	if _, busy := s.byJob[jobID]; busy {
		s.mu.Unlock()
		return 0, ErrAlreadyRunning
	}
	s.byJob[jobID] = a
	s.mu.Unlock()

	started := s.d.Now()
	r := run_entity.New(j.ID, j.Name, trig, started)
	if err := run_repo.Run().Create(context.WithoutCancel(ctx), r); err != nil {
		s.mu.Lock()
		delete(s.byJob, jobID)
		s.mu.Unlock()
		return 0, err
	}
	// 运行不依赖请求：用独立的 ctx，停止时以「手动停止」为原因取消
	runCtx, cancel := context.WithCancelCause(context.Background())
	a.cancel = cancel
	s.mu.Lock()
	s.byRun[r.ID] = a
	s.mu.Unlock()

	e := &execution{s: s, job: j, run: r, log: a.log, started: started}
	gogo.Go(func() error {
		defer func() {
			s.mu.Lock()
			delete(s.byJob, jobID)
			delete(s.byRun, r.ID)
			s.mu.Unlock()
			cancel(nil)
			close(a.done)
		}()
		e.perform(runCtx)
		return nil
	})
	return r.ID, nil
}

// RunJob 见 RunSvc.RunJob
func (s *runSvc) RunJob(ctx context.Context, req *runapi.RunJobRequest) (*runapi.RunJobResponse, error) {
	trig := run_entity.TriggerManual
	if actor, ok := auth.ActorFrom(ctx); ok && actor.Kind == auth.KindAPIKey {
		trig = run_entity.TriggerAPIKey(actor.Name, actor.Prefix)
	}
	id, err := s.Trigger(ctx, req.ID, trig)
	if errors.Is(err, ErrAlreadyRunning) {
		return nil, i18n.NewError(ctx, code.RunAlreadyRunning)
	}
	if err != nil {
		return nil, err
	}
	return &runapi.RunJobResponse{RunID: id}, nil
}

// List 见 RunSvc.List
func (s *runSvc) List(ctx context.Context, req *runapi.ListRequest) (*runapi.ListResponse, error) {
	list, total, err := run_repo.Run().List(ctx, run_repo.ListFilter{
		JobID: req.JobID, Status: req.Status, Offset: req.GetOffset(), Limit: req.GetLimit(),
	})
	if err != nil {
		return nil, err
	}
	resp := &runapi.ListResponse{List: make([]*runapi.Item, 0, len(list)), Total: total}
	for _, r := range list {
		resp.List = append(resp.List, item(r))
	}
	return resp, nil
}

// Get 见 RunSvc.Get
func (s *runSvc) Get(ctx context.Context, id int64) (*runapi.GetResponse, error) {
	r, err := s.find(ctx, id)
	if err != nil {
		return nil, err
	}
	return &runapi.GetResponse{Item: *item(r)}, nil
}

// Stop 见 RunSvc.Stop
func (s *runSvc) Stop(ctx context.Context, req *runapi.StopRequest) (*runapi.StopResponse, error) {
	if !req.Confirm {
		return nil, i18n.NewError(ctx, code.RunStopNotConfirmed)
	}
	s.mu.Lock()
	a := s.byRun[req.ID]
	s.mu.Unlock()
	if a == nil {
		if _, err := s.find(ctx, req.ID); err != nil {
			return nil, err
		}
		return nil, i18n.NewError(ctx, code.RunNotRunning)
	}
	a.log.warnf("管理员请求停止运行")
	a.cancel(errStopped)
	return &runapi.StopResponse{}, nil
}

// Log 见 RunSvc.Log
func (s *runSvc) Log(ctx context.Context, req *runapi.LogRequest) (*runapi.LogResponse, error) {
	s.mu.Lock()
	a := s.byRun[req.ID]
	s.mu.Unlock()
	if a != nil {
		return &runapi.LogResponse{Running: true, Lines: logLines(a.log.snapshot(), req.WarningsOnly)}, nil
	}
	r, err := s.find(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	return &runapi.LogResponse{Running: r.Running(), Lines: logLines(r.Log, req.WarningsOnly)}, nil
}

// StreamLog 见 RunSvc.StreamLog
func (s *runSvc) StreamLog(ctx context.Context, id int64, warningsOnly bool, emit func(event string, data any) error) error {
	s.mu.Lock()
	a := s.byRun[id]
	s.mu.Unlock()
	if a == nil {
		r, err := s.find(ctx, id)
		if err != nil {
			return err
		}
		for _, l := range logLines(r.Log, warningsOnly) {
			if err := emit("line", l); err != nil {
				return nil
			}
		}
		_ = emit("end", runapi.StreamEnd{Status: r.Status, Reason: r.Reason})
		return nil
	}
	ping := time.NewTicker(pingInterval)
	defer ping.Stop()
	cursor := 0
	for {
		lines, changed, done, status, reason := a.log.since(cursor)
		cursor += len(lines)
		for _, l := range logLines(lines, warningsOnly) {
			if err := emit("line", l); err != nil {
				return nil
			}
		}
		if done {
			_ = emit("end", runapi.StreamEnd{Status: status, Reason: reason})
			return nil
		}
		select {
		case <-changed:
		case <-ping.C:
			if err := emit(EventPing, nil); err != nil {
				return nil
			}
		case <-ctx.Done():
			return nil
		}
	}
}

// Attention 见 RunSvc.Attention
func (s *runSvc) Attention(ctx context.Context) (map[int64]bool, error) {
	latest, err := run_repo.Run().LatestFinishedStatus(ctx)
	if err != nil {
		return nil, err
	}
	out := map[int64]bool{}
	for jobID, status := range latest {
		if status == run_entity.StatusFailed || status == run_entity.StatusWarning {
			out[jobID] = true
		}
	}
	stale, err := s.d.Stale(ctx)
	if err != nil {
		return nil, err
	}
	for _, sj := range stale {
		out[sj.JobID] = true
	}
	return out, nil
}

// Recover 见 RunSvc.Recover
func (s *runSvc) Recover(ctx context.Context) error {
	list, _, err := run_repo.Run().List(ctx, run_repo.ListFilter{Status: run_entity.StatusRunning})
	if err != nil {
		return err
	}
	now := s.d.Now()
	var failed []*run_entity.Run
	for _, it := range list {
		// 列表不含日志，整体保存前取完整记录
		r, err := run_repo.Run().Find(ctx, it.ID)
		if err != nil {
			return err
		}
		if r == nil || !r.Running() {
			continue
		}
		r.Interrupt(interruptedReason, now)
		if err := run_repo.Run().Save(ctx, r); err != nil {
			return err
		}
		failed = append(failed, r)
	}
	if len(failed) == 0 {
		return nil
	}
	s.bg.Add(1)
	// 删除残留快照、通知都可能要很久，不阻塞启动
	gogo.Go(func() error {
		defer s.bg.Done()
		for _, r := range failed {
			s.discardInterrupted(r)
			s.notifyInterrupted(r)
		}
		return nil
	})
	return nil
}

// discardInterrupted 被中断的运行同样不能留下快照：按它的运行标签查出已写入的快照并删除，
// 过程写进运行日志，由随后的 notifyInterrupted 一并保存。
func (s *runSvc) discardInterrupted(r *run_entity.Run) {
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	log := newLiveLog(s.d.Now)
	defer func() { r.Log = append(r.Log, log.snapshot()...) }()
	j, err := job_repo.Job().Find(ctx, r.JobID)
	if err != nil || j == nil {
		log.errorf("找不到任务，没有删除本次运行可能留下的快照（标签 run:%d）", r.ID)
		return
	}
	repo, release, err := repo_svc.Env(ctx, j.RepoID)
	if err != nil {
		log.errorf("无法访问存储位置，没有删除本次运行可能留下的快照（标签 run:%d），请在存储位置手动删除：%v", r.ID, err)
		return
	}
	defer release()
	log.secret(repo.Password)
	unlock, err := repolock.Acquire(ctx, j.RepoID, repolock.Shared)
	if err != nil {
		log.errorf("等待存储位置空闲超时，没有删除本次运行可能留下的快照（标签 run:%d）", r.ID)
		return
	}
	defer unlock()
	e := &execution{s: s, job: j, run: r, log: log, repo: repo}
	e.rollback()
}

// notifyInterrupted 为被重启中断的运行发「备份失败」；发送失败时在记录里注明
func (s *runSvc) notifyInterrupted(r *run_entity.Run) {
	ctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
	defer cancel()
	ev := notify_svc.Event{
		Type: notify_entity.EventFailed, JobID: r.JobID, JobName: r.JobName, RunID: r.ID,
		StartedAt: time.Unix(r.StartedAt, 0), Duration: time.Duration(r.DurationMs) * time.Millisecond,
		ErrorSummary: r.Reason, Link: fmt.Sprintf("/runs/%d", r.ID),
	}
	// 上次成功时间只是附带信息，查不到也照常发送
	last, err := run_repo.Run().LastSucceeded(ctx, r.JobID)
	if err != nil {
		logger.Default().Warn("读取上次成功的时间失败，通知里不带这一项", zap.Int64("run_id", r.ID), zap.Error(err))
	}
	if last != nil {
		ev.LastSuccessAt = time.Unix(last.FinishedAt, 0)
	}
	err = s.d.Notify(ctx, ev)
	if err != nil {
		r.NotifyFailed = true
		r.SetStep(run_entity.StepNotify, run_entity.StepFailed, "通知发送失败："+err.Error(), s.d.Now())
	} else {
		r.SetStep(run_entity.StepNotify, run_entity.StepSuccess, "", s.d.Now())
	}
	if err := run_repo.Run().Save(context.Background(), r); err != nil {
		logger.Default().Error("保存被中断的运行记录失败", zap.Int64("run_id", r.ID), zap.Error(err))
	}
}

// waitBackground 等待后台任务结束（测试用）
func (s *runSvc) waitBackground() { s.bg.Wait() }

func (s *runSvc) find(ctx context.Context, id int64) (*run_entity.Run, error) {
	r, err := run_repo.Run().Find(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := r.Check(ctx); err != nil {
		return nil, err
	}
	return r, nil
}

var stepLabels = map[string]string{
	run_entity.StepPreflight: "预检",
	run_entity.StepExport:    "导出并写入快照",
	run_entity.StepForget:    "清理旧快照",
	run_entity.StepNotify:    "记录并通知",
}

func item(r *run_entity.Run) *runapi.Item {
	it := &runapi.Item{
		ID: r.ID, JobID: r.JobID, JobName: r.JobName,
		Trigger: r.Trigger, TriggerLabel: run_entity.TriggerLabel(r.Trigger),
		Status: r.Status, Reason: r.Reason, NotifyFailed: r.NotifyFailed,
		StartedAt: r.StartedAt, FinishedAt: r.FinishedAt, DurationMs: r.DurationMs,
		BytesProcessed: r.BytesProcessed, BytesAdded: r.BytesAdded,
		SnapshotIDs: r.SnapshotIDs, Steps: make([]*runapi.Step, 0, len(r.Steps)),
	}
	if it.SnapshotIDs == nil {
		it.SnapshotIDs = []string{}
	}
	for _, st := range r.Steps {
		it.Steps = append(it.Steps, &runapi.Step{
			Key: st.Key, Label: stepLabels[st.Key], Status: st.Status, Message: st.Message,
			StartedAt: st.StartedAt, FinishedAt: st.FinishedAt,
		})
	}
	return it
}

func logLines(lines []run_entity.LogLine, warningsOnly bool) []*runapi.LogLine {
	out := make([]*runapi.LogLine, 0, len(lines))
	for _, l := range lines {
		if warningsOnly && !l.IsWarning() {
			continue
		}
		out = append(out, &runapi.LogLine{Time: l.Time, Level: l.Level, Text: l.Text})
	}
	return out
}
