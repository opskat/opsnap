// Package job_svc 实现备份任务的增删改、暂停与启用、计划预览（docs/specs/2026-09-27-backup-jobs.md
// 「任务与新建向导」「编辑、暂停、删除」）。数据源、存储与路径前缀决定快照归属，创建后不能修改；
// 删除任务时可选同时删除仓库中本任务的快照（只删除本任务的，见 kopiarepo.JobRef）。
package job_svc

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	api "github.com/opskat/opsnap/internal/api/job"
	"github.com/opskat/opsnap/internal/model/entity/datasource_entity"
	"github.com/opskat/opsnap/internal/model/entity/job_entity"
	"github.com/opskat/opsnap/internal/model/entity/storage_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/schedule"
	"github.com/opskat/opsnap/internal/repository/datasource_repo"
	"github.com/opskat/opsnap/internal/repository/job_repo"
	"github.com/opskat/opsnap/internal/repository/storage_repo"
	"github.com/opskat/opsnap/internal/service/storage_svc"
)

const (
	// previewRuns 计划预览给出的执行次数
	previewRuns = 3
	// snapshotDeleteTimeout 删除任务时删除它的快照的最长时间
	snapshotDeleteTimeout = 2 * time.Minute
)

type JobSvc interface {
	List(ctx context.Context, req *api.ListRequest) (*api.ListResponse, error)
	Get(ctx context.Context, req *api.GetRequest) (*api.GetResponse, error)
	Create(ctx context.Context, req *api.CreateRequest) (*api.CreateResponse, error)
	Update(ctx context.Context, req *api.UpdateRequest) (*api.UpdateResponse, error)
	Pause(ctx context.Context, req *api.PauseRequest) (*api.PauseResponse, error)
	Enable(ctx context.Context, req *api.EnableRequest) (*api.EnableResponse, error)
	// Delete 删除任务；有进行中的运行（见 SetActiveRunChecker）时拒绝。删除快照失败不影响删除任务，份数与提示在响应中
	Delete(ctx context.Context, req *api.DeleteRequest) (*api.DeleteResponse, error)
	// SchedulePreview 按所选时区计算接下来三次执行时间，并估算最多保留的快照份数
	SchedulePreview(ctx context.Context, req *api.SchedulePreviewRequest) (*api.SchedulePreviewResponse, error)
	// ByDataSource 使用该数据源的任务（按 ID 正序），供数据源的引用保护
	ByDataSource(ctx context.Context, dataSourceID int64) ([]*api.Ref, error)
	// ByStorage 使用该存储的任务（按 ID 正序），供存储的引用保护
	ByStorage(ctx context.Context, storageID int64) ([]*api.Ref, error)

	// RunNow 立即执行一次（手动触发）；任务已在运行或排队时拒绝
	RunNow(ctx context.Context, req *api.RunNowRequest) (*api.RunNowResponse, error)
	// CancelRun 取消等待中或运行中的运行
	CancelRun(ctx context.Context, req *api.CancelRunRequest) (*api.CancelRunResponse, error)
	// Runs 运行记录，按触发顺序倒序，每页 20 条
	Runs(ctx context.Context, req *api.RunsRequest) (*api.RunsResponse, error)
	// RunLog 一次运行的执行日志
	RunLog(ctx context.Context, req *api.RunLogRequest) (*api.RunLogResponse, error)
	// Stats 任务统计：快照数与最早时间、去重占用、导出总量与节省比例、最近一次成功、最近 30 次运行
	Stats(ctx context.Context, req *api.StatsRequest) (*api.StatsResponse, error)
}

// ActiveRunChecker 查询任务是否有正在运行或排队的运行
type ActiveRunChecker func(ctx context.Context, jobID int64) (bool, error)

type jobSvc struct {
	now func() time.Time
	// mu 串行化写操作：重名与前缀冲突检查到写入之间不被其他请求插入，整行保存也不互相覆盖
	mu sync.Mutex

	hookMu    sync.RWMutex
	activeRun ActiveRunChecker
}

var defaultJob = &jobSvc{now: time.Now}

func Job() JobSvc {
	return defaultJob
}

// SetActiveRunChecker 替换删除任务前“是否有正在运行或排队的运行”的查询；nil 恢复为默认：
// 查询运行记录中等待中或运行中的运行（Runs().HasActive）
func SetActiveRunChecker(fn ActiveRunChecker) {
	defaultJob.hookMu.Lock()
	defer defaultJob.hookMu.Unlock()
	defaultJob.activeRun = fn
}

func (s *jobSvc) hasActiveRun(ctx context.Context, jobID int64) (bool, error) {
	s.hookMu.RLock()
	fn := s.activeRun
	s.hookMu.RUnlock()
	if fn == nil {
		return defaultRunner.HasActive(ctx, jobID)
	}
	return fn(ctx, jobID)
}

// settings 新建与编辑共用的可修改字段
type settings struct {
	name          string
	scope         string
	databases     []string
	method        string
	options       api.Options
	excludeTables []string
	compression   string
	schedule      api.Schedule
	retention     api.Retention
	failure       api.Failure
}

func (st settings) apply(j *job_entity.Job) {
	j.Name, j.Scope, j.Method, j.Compression = st.name, st.scope, st.method, st.compression
	j.SetDatabases(st.databases)
	j.OptRoutines, j.OptTriggers, j.OptEvents = st.options.Routines, st.options.Triggers, st.options.Events
	j.OptUsers, j.OptGlobals = st.options.Users, st.options.Globals
	j.SetExcludeTables(st.excludeTables)
	j.ScheduleKind, j.ScheduleMinute, j.ScheduleHour = st.schedule.Kind, st.schedule.Minute, st.schedule.Hour
	j.SetWeekdays(st.schedule.Weekdays)
	j.ScheduleCron, j.Timezone = st.schedule.Cron, st.schedule.Timezone
	j.RetainDays, j.RetainWeeks, j.RetainMonths = st.retention.Days, st.retention.Weeks, st.retention.Months
	j.Retries, j.RetryInterval, j.Timeout = st.failure.Retries, st.failure.RetryInterval, st.failure.Timeout
}

func (s *jobSvc) toItem(j *job_entity.Job, ds *datasource_entity.DataSource, st *storage_entity.Storage, last *job_entity.Run) *api.Item {
	item := &api.Item{
		ID: j.ID, Name: j.Name, Type: j.Type,
		DataSourceID: j.DataSourceID, StorageID: j.StorageID, Prefix: j.Prefix,
		Scope: j.Scope, Databases: j.Databases(), Method: j.Method,
		Options: api.Options{Routines: j.OptRoutines, Triggers: j.OptTriggers, Events: j.OptEvents,
			Users: j.OptUsers, Globals: j.OptGlobals},
		ExcludeTables: j.ExcludeTableList(),
		Compression:   j.Compression,
		Schedule: api.Schedule{Kind: j.ScheduleKind, Minute: j.ScheduleMinute, Hour: j.ScheduleHour,
			Weekdays: j.Weekdays(), Cron: j.ScheduleCron, Timezone: j.Timezone},
		Retention:     api.Retention{Days: j.RetainDays, Weeks: j.RetainWeeks, Months: j.RetainMonths},
		Failure:       api.Failure{Retries: j.Retries, RetryInterval: j.RetryInterval, Timeout: j.Timeout},
		Enabled:       j.Enabled,
		CreatedAt:     j.Createtime,
		UpdatedAt:     j.Updatetime,
		LastRun:       defaultRunner.toRun(last),
		SnapshotCount: j.SnapshotCount,
	}
	if ds != nil {
		item.DataSourceName, item.DataSourceKind = ds.Name, ds.Kind
	}
	if st != nil {
		item.StorageName = st.Name
		item.Location = j.Location(st.Name)
	}
	if next, ok := j.NextRun(s.now()); ok {
		item.NextRunAt = next.Unix()
	}
	return item
}

// item 读取任务引用的数据源、存储与最近一次运行并转为响应
func (s *jobSvc) item(ctx context.Context, j *job_entity.Job) (*api.Item, error) {
	ds, err := datasource_repo.DataSource().Find(ctx, j.DataSourceID)
	if err != nil {
		return nil, err
	}
	st, err := storage_repo.Storage().Find(ctx, j.StorageID)
	if err != nil {
		return nil, err
	}
	last, err := lastRun(ctx, j.ID)
	if err != nil {
		return nil, err
	}
	return s.toItem(j, ds, st, last), nil
}

// lastRun 任务最近一次运行，没有时为 nil
func lastRun(ctx context.Context, jobID int64) (*job_entity.Run, error) {
	rows, _, err := job_repo.Run().Page(ctx, jobID, 0, 1)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return rows[0], nil
}

func (s *jobSvc) find(ctx context.Context, id int64) (*job_entity.Job, error) {
	j, err := job_repo.Job().Find(ctx, id)
	if err != nil {
		return nil, err
	}
	if j == nil {
		return nil, i18n.NewNotFoundError(ctx, code.JobNotFound)
	}
	return j, nil
}

// save 保存任务；它在此期间已被删除时返回“不存在”
func (s *jobSvc) save(ctx context.Context, j *job_entity.Job) error {
	if err := job_repo.Job().Save(ctx, j); err != nil {
		if errors.Is(err, job_repo.ErrNotFound) {
			return i18n.NewNotFoundError(ctx, code.JobNotFound)
		}
		return err
	}
	return nil
}

// dataSource 所选数据源须存在；newJob 时还须是 MySQL / PostgreSQL 且状态正常
func (s *jobSvc) dataSource(ctx context.Context, id int64, newJob bool) (*datasource_entity.DataSource, error) {
	ds, err := datasource_repo.DataSource().Find(ctx, id)
	if err != nil {
		return nil, err
	}
	if ds == nil {
		return nil, i18n.NewError(ctx, code.JobDataSourceNotFound)
	}
	if !newJob {
		return ds, nil
	}
	if ds.Kind != datasource_entity.KindMySQL && ds.Kind != datasource_entity.KindPostgreSQL {
		return nil, i18n.NewError(ctx, code.JobDataSourceUnsupported)
	}
	if ds.Status != datasource_entity.StatusOK {
		return nil, i18n.NewError(ctx, code.JobDataSourceNotReady)
	}
	return ds, nil
}

// checkUnique 名称在任务之间唯一；同一存储中路径前缀不能与其他任务相同或互为上下级
func (s *jobSvc) checkUnique(ctx context.Context, j *job_entity.Job) error {
	same, err := job_repo.Job().FindByName(ctx, j.Name)
	if err != nil {
		return err
	}
	if same != nil && same.ID != j.ID {
		return i18n.NewError(ctx, code.JobNameDuplicate)
	}
	others, err := job_repo.Job().ListByStorage(ctx, j.StorageID)
	if err != nil {
		return err
	}
	for _, o := range others {
		if o.ID != j.ID && j.Overlaps(o) {
			return i18n.NewError(ctx, code.JobPrefixConflict, o.Name)
		}
	}
	return nil
}

func (s *jobSvc) List(ctx context.Context, _ *api.ListRequest) (*api.ListResponse, error) {
	jobs, err := job_repo.Job().List(ctx)
	if err != nil {
		return nil, err
	}
	dsList, err := datasource_repo.DataSource().List(ctx)
	if err != nil {
		return nil, err
	}
	stList, err := storage_repo.Storage().List(ctx)
	if err != nil {
		return nil, err
	}
	dss := make(map[int64]*datasource_entity.DataSource, len(dsList))
	for _, ds := range dsList {
		dss[ds.ID] = ds
	}
	sts := make(map[int64]*storage_entity.Storage, len(stList))
	for _, st := range stList {
		sts[st.ID] = st
	}
	last, err := job_repo.Run().Latest(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]*api.Item, 0, len(jobs))
	for _, j := range jobs {
		items = append(items, s.toItem(j, dss[j.DataSourceID], sts[j.StorageID], last[j.ID]))
	}
	return &api.ListResponse{Items: items}, nil
}

func (s *jobSvc) Get(ctx context.Context, req *api.GetRequest) (*api.GetResponse, error) {
	j, err := s.find(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	item, err := s.item(ctx, j)
	if err != nil {
		return nil, err
	}
	return &api.GetResponse{Item: item}, nil
}

func (s *jobSvc) Create(ctx context.Context, req *api.CreateRequest) (*api.CreateResponse, error) {
	j := &job_entity.Job{Type: req.Type, DataSourceID: req.DataSourceID, StorageID: req.StorageID, Prefix: req.Prefix}
	settings{name: req.Name, scope: req.Scope, databases: req.Databases, method: req.Method, options: req.Options,
		excludeTables: req.ExcludeTables, compression: req.Compression, schedule: req.Schedule,
		retention: req.Retention, failure: req.Failure}.apply(j)

	s.mu.Lock()
	defer s.mu.Unlock()
	ds, err := s.dataSource(ctx, j.DataSourceID, true)
	if err != nil {
		return nil, err
	}
	st, err := storage_repo.Storage().Find(ctx, j.StorageID)
	if err != nil {
		return nil, err
	}
	if st == nil {
		return nil, i18n.NewError(ctx, code.JobStorageNotFound)
	}
	if st.Status != storage_entity.StatusOK {
		return nil, i18n.NewError(ctx, code.JobStorageNotReady)
	}
	if err := j.Check(ctx, ds.Kind); err != nil {
		return nil, err
	}
	if err := s.checkUnique(ctx, j); err != nil {
		return nil, err
	}
	now := s.now().Unix()
	j.Enabled, j.EnabledAt, j.RunNow = true, now, req.RunNow
	j.Createtime, j.Updatetime = now, now
	if err := job_repo.Job().Create(ctx, j); err != nil {
		return nil, err
	}
	wakeScheduler()
	// 创建后“立即执行一次”：清除标记并以手动方式执行。任务已经创建，失败只记录日志；
	// 标记未清除时由 Runs().RunPending 在启动时补上
	if err := defaultRunner.startPending(ctx, j.ID); err != nil {
		logger.Ctx(ctx).Error("创建后立即执行一次失败", zap.Int64("job_id", j.ID), zap.Error(err))
	}
	last, err := lastRun(ctx, j.ID)
	if err != nil {
		return nil, err
	}
	return &api.CreateResponse{Item: s.toItem(j, ds, st, last)}, nil
}

func (s *jobSvc) Update(ctx context.Context, req *api.UpdateRequest) (*api.UpdateResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, err := s.find(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	// 数据源、存储、路径前缀决定快照归属，创建后不能修改；留空表示不变
	if req.DataSourceID != 0 && req.DataSourceID != j.DataSourceID ||
		req.StorageID != 0 && req.StorageID != j.StorageID ||
		req.Prefix != "" && req.Prefix != j.Prefix {
		return nil, i18n.NewError(ctx, code.JobImmutableField)
	}
	settings{name: req.Name, scope: req.Scope, databases: req.Databases, method: req.Method, options: req.Options,
		excludeTables: req.ExcludeTables, compression: req.Compression, schedule: req.Schedule,
		retention: req.Retention, failure: req.Failure}.apply(j)
	ds, err := s.dataSource(ctx, j.DataSourceID, false)
	if err != nil {
		return nil, err
	}
	if err := j.Check(ctx, ds.Kind); err != nil {
		return nil, err
	}
	if err := s.checkUnique(ctx, j); err != nil {
		return nil, err
	}
	j.Updatetime = s.now().Unix()
	if err := s.save(ctx, j); err != nil {
		return nil, err
	}
	wakeScheduler()
	item, err := s.item(ctx, j)
	if err != nil {
		return nil, err
	}
	return &api.UpdateResponse{Item: item}, nil
}

// setEnabled 暂停或启用；启用时记录启用时间，此前（暂停期间）的计划不算错过。状态不变时不写库
func (s *jobSvc) setEnabled(ctx context.Context, id int64, enabled bool) (*api.Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, err := s.find(ctx, id)
	if err != nil {
		return nil, err
	}
	if j.Enabled != enabled {
		now := s.now().Unix()
		j.Enabled, j.Updatetime = enabled, now
		if enabled {
			j.EnabledAt = now
		}
		if err := s.save(ctx, j); err != nil {
			return nil, err
		}
		wakeScheduler()
	}
	return s.item(ctx, j)
}

func (s *jobSvc) Pause(ctx context.Context, req *api.PauseRequest) (*api.PauseResponse, error) {
	item, err := s.setEnabled(ctx, req.ID, false)
	if err != nil {
		return nil, err
	}
	return &api.PauseResponse{Item: item}, nil
}

func (s *jobSvc) Enable(ctx context.Context, req *api.EnableRequest) (*api.EnableResponse, error) {
	item, err := s.setEnabled(ctx, req.ID, true)
	if err != nil {
		return nil, err
	}
	return &api.EnableResponse{Item: item}, nil
}

func (s *jobSvc) Delete(ctx context.Context, req *api.DeleteRequest) (*api.DeleteResponse, error) {
	j, err := s.remove(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	resp := &api.DeleteResponse{}
	if req.DeleteSnapshots {
		s.deleteSnapshots(ctx, j, resp)
	}
	return resp, nil
}

// remove 没有进行中的运行时删除任务及其运行记录，返回被删除的任务
func (s *jobSvc) remove(ctx context.Context, id int64) (*job_entity.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, err := s.find(ctx, id)
	if err != nil {
		return nil, err
	}
	// 检查与删除期间不能有新的运行入队
	defaultRunner.mu.Lock()
	defer defaultRunner.mu.Unlock()
	active, err := s.hasActiveRun(ctx, j.ID)
	if err != nil {
		return nil, err
	}
	if active {
		return nil, i18n.NewError(ctx, code.JobRunActive)
	}
	if err := job_repo.Job().Delete(ctx, j.ID); err != nil {
		return nil, err
	}
	// 任务的运行记录一并删除
	if err := job_repo.Run().DeleteByJob(ctx, j.ID); err != nil {
		return nil, err
	}
	return j, nil
}

// deleteSnapshots 删除仓库中本任务的快照（来源为本任务的前缀且带本任务标签），结果写入 resp；
// 任务已经删除，失败只记录日志并提示，可以用 kopia 命令行手动处理
func (s *jobSvc) deleteSnapshots(ctx context.Context, j *job_entity.Job, resp *api.DeleteResponse) {
	ctx, cancel := context.WithTimeout(ctx, snapshotDeleteTimeout)
	defer cancel()
	log := logger.Ctx(ctx).With(zap.Int64("job_id", j.ID), zap.Int64("storage_id", j.StorageID))
	w, err := storage_svc.Storage().OpenWriter(ctx, j.StorageID)
	if err != nil {
		log.Warn("删除任务时无法打开存储，快照未删除", zap.Error(err))
		resp.SnapshotsMessage = i18n.T(ctx, code.JobSnapshotsUnreachable)
		return
	}
	defer func() {
		if err := w.Close(ctx); err != nil {
			log.Warn("关闭存储写入会话失败", zap.Error(err))
		}
	}()
	res, err := w.DeleteJobSnapshots(ctx, j.Ref())
	resp.SnapshotsDeleted, resp.SnapshotsFailed = res.Deleted, res.Failed
	if err == nil {
		return
	}
	log.Warn("删除任务的快照失败", zap.Int("failed", res.Failed), zap.Error(err))
	if res.Failed > 0 {
		resp.SnapshotsMessage = i18n.T(ctx, code.JobSnapshotsNotDeleted, res.Failed)
	} else {
		// 读取快照列表就失败了，不知道份数
		resp.SnapshotsMessage = i18n.T(ctx, code.JobSnapshotsUnreachable)
	}
}

func (s *jobSvc) SchedulePreview(ctx context.Context, req *api.SchedulePreviewRequest) (*api.SchedulePreviewResponse, error) {
	spec := schedule.Spec{Kind: schedule.Kind(req.Schedule.Kind), Minute: req.Schedule.Minute, Hour: req.Schedule.Hour,
		Cron: req.Schedule.Cron, Timezone: req.Schedule.Timezone}
	for _, d := range req.Schedule.Weekdays {
		spec.Weekdays = append(spec.Weekdays, time.Weekday(d))
	}
	spec, err := job_entity.CheckSchedule(ctx, spec)
	if err != nil {
		return nil, err
	}
	r := req.Retention
	if err := job_entity.CheckRetention(ctx, r.Days, r.Weeks, r.Months); err != nil {
		return nil, err
	}
	loc, err := time.LoadLocation(spec.Timezone)
	if err != nil {
		return nil, err
	}
	runs, err := spec.Next(s.now(), previewRuns)
	if err != nil {
		return nil, err
	}
	resp := &api.SchedulePreviewResponse{
		NextRuns:     make([]string, 0, len(runs)),
		MaxSnapshots: schedule.EstimateMaxSnapshots(spec, r.Days, r.Weeks, r.Months),
	}
	for _, t := range runs {
		resp.NextRuns = append(resp.NextRuns, t.In(loc).Format(time.RFC3339))
	}
	return resp, nil
}

func refs(jobs []*job_entity.Job) []*api.Ref {
	out := make([]*api.Ref, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, &api.Ref{ID: j.ID, Name: j.Name})
	}
	return out
}

func (s *jobSvc) ByDataSource(ctx context.Context, dataSourceID int64) ([]*api.Ref, error) {
	jobs, err := job_repo.Job().ListByDataSource(ctx, dataSourceID)
	if err != nil {
		return nil, err
	}
	return refs(jobs), nil
}

func (s *jobSvc) ByStorage(ctx context.Context, storageID int64) ([]*api.Ref, error) {
	jobs, err := job_repo.Job().ListByStorage(ctx, storageID)
	if err != nil {
		return nil, err
	}
	return refs(jobs), nil
}
