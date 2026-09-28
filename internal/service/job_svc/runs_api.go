package job_svc

import (
	"context"
	"errors"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	api "github.com/opskat/opsnap/internal/api/job"
	"github.com/opskat/opsnap/internal/model/entity/job_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/repository/job_repo"
	"github.com/opskat/opsnap/internal/service/storage_svc"
)

const (
	// runsPageSize 运行记录每页条数
	runsPageSize = 20
	// recentRuns 统计成功率的最近运行次数
	recentRuns = 30
)

// toRun 转为响应；运行中的记录带实时的已导出量与已运行时间
func (r *runner) toRun(run *job_entity.Run) *api.Run {
	if run == nil {
		return nil
	}
	out := &api.Run{ID: run.ID, JobID: run.JobID, Status: run.Status, Trigger: run.Trigger,
		RetryAttempt: run.RetryAttempt, RetryTotal: run.RetryTotal, ScheduledAt: run.ScheduledAt,
		CreatedAt: run.Createtime, StartedAt: run.StartedAt / 1000, FinishedAt: run.FinishedAt / 1000,
		ExportedBytes: run.ExportedBytes, UploadedBytes: run.UploadedBytes, SnapshotID: run.SnapshotID,
		FailedStep: run.FailedStep, Reason: run.Reason}
	switch {
	case run.FinishedAt > 0 && run.StartedAt > 0:
		out.DurationMs = run.FinishedAt - run.StartedAt
	case run.Status == job_entity.RunRunning:
		out.DurationMs = max(r.clock().UnixMilli()-run.StartedAt, 0)
		if n := r.liveBytes(run.ID); n >= 0 {
			out.ExportedBytes = n
		}
	}
	return out
}

// runOf 任务的一次运行；不存在或不属于该任务时返回“运行记录不存在”
func (s *jobSvc) runOf(ctx context.Context, jobID, runID int64) (*job_entity.Run, error) {
	run, err := job_repo.Run().Find(ctx, runID)
	if err != nil {
		return nil, err
	}
	if run == nil || run.JobID != jobID {
		return nil, i18n.NewNotFoundError(ctx, code.JobRunNotFound)
	}
	return run, nil
}

func (s *jobSvc) RunNow(ctx context.Context, req *api.RunNowRequest) (*api.RunNowResponse, error) {
	if _, err := s.find(ctx, req.ID); err != nil {
		return nil, err
	}
	run, err := defaultRunner.Enqueue(ctx, req.ID, Trigger{Kind: job_entity.TriggerManual})
	switch {
	case errors.Is(err, ErrRunActive):
		return nil, i18n.NewError(ctx, code.JobRunAlreadyActive)
	case errors.Is(err, job_repo.ErrNotFound):
		return nil, i18n.NewNotFoundError(ctx, code.JobNotFound)
	case err != nil:
		return nil, err
	}
	defaultRunner.dispatchRun(run.ID)
	return &api.RunNowResponse{Run: defaultRunner.toRun(run)}, nil
}

func (s *jobSvc) CancelRun(ctx context.Context, req *api.CancelRunRequest) (*api.CancelRunResponse, error) {
	if _, err := s.find(ctx, req.ID); err != nil {
		return nil, err
	}
	if _, err := s.runOf(ctx, req.ID, req.RunID); err != nil {
		return nil, err
	}
	run, err := defaultRunner.Cancel(ctx, req.RunID)
	switch {
	case errors.Is(err, ErrRunFinished):
		return nil, i18n.NewError(ctx, code.JobRunFinished)
	case errors.Is(err, ErrRunNotFound):
		return nil, i18n.NewNotFoundError(ctx, code.JobRunNotFound)
	case err != nil:
		return nil, err
	}
	return &api.CancelRunResponse{Run: defaultRunner.toRun(run)}, nil
}

func (s *jobSvc) Runs(ctx context.Context, req *api.RunsRequest) (*api.RunsResponse, error) {
	if _, err := s.find(ctx, req.ID); err != nil {
		return nil, err
	}
	page := max(req.Page, 1)
	rows, total, err := job_repo.Run().Page(ctx, req.ID, (page-1)*runsPageSize, runsPageSize)
	if err != nil {
		return nil, err
	}
	items := make([]*api.Run, 0, len(rows))
	for _, run := range rows {
		items = append(items, defaultRunner.toRun(run))
	}
	return &api.RunsResponse{Items: items, Total: total}, nil
}

func (s *jobSvc) RunLog(ctx context.Context, req *api.RunLogRequest) (*api.RunLogResponse, error) {
	if _, err := s.find(ctx, req.ID); err != nil {
		return nil, err
	}
	run, err := s.runOf(ctx, req.ID, req.RunID)
	if err != nil {
		return nil, err
	}
	lines := run.LogLines()
	if ar := defaultRunner.lookup(run.ID); ar != nil {
		lines = ar.log.lines()
	}
	out := make([]*api.LogLine, 0, len(lines))
	for _, l := range lines {
		out = append(out, &api.LogLine{Time: l.Time, Step: l.Step, Message: l.Message, Omitted: l.Omitted})
	}
	return &api.RunLogResponse{Lines: out}, nil
}

func (s *jobSvc) Stats(ctx context.Context, req *api.StatsRequest) (*api.StatsResponse, error) {
	j, err := s.find(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	resp := &api.StatsResponse{}
	last, err := job_repo.Run().LastSuccess(ctx, j.ID)
	if err != nil {
		return nil, err
	}
	resp.LastSuccess = defaultRunner.toRun(last)
	recent, err := job_repo.Run().RecentFinished(ctx, j.ID, recentRuns)
	if err != nil {
		return nil, err
	}
	resp.Recent.Runs = len(recent)
	for _, run := range recent {
		switch run.Status {
		case job_entity.RunSuccess:
			resp.Recent.Success++
		case job_entity.RunFailed:
			resp.Recent.Failed++
		}
	}
	if n := resp.Recent.Success + resp.Recent.Failed; n > 0 {
		resp.Recent.SuccessRate = float64(resp.Recent.Success) / float64(n)
	}
	if err := s.snapshotStats(ctx, j, resp); err != nil {
		logger.Ctx(ctx).Warn("读取任务的快照统计失败", zap.Int64("job_id", j.ID), zap.Error(err))
		resp.StorageError = i18n.T(ctx, code.JobStatsStorageUnreadable)
	}
	return resp, nil
}

// snapshotStats 从仓库读取本任务现存快照的份数、最早时间与占用，并刷新任务上记录的快照数
func (s *jobSvc) snapshotStats(ctx context.Context, j *job_entity.Job, resp *api.StatsResponse) error {
	w, err := storage_svc.Storage().OpenWriter(ctx, j.StorageID)
	if err != nil {
		return err
	}
	defer func() {
		if err := w.Close(ctx); err != nil {
			logger.Ctx(ctx).Warn("关闭存储写入会话失败", zap.Int64("storage_id", j.StorageID), zap.Error(err))
		}
	}()
	snaps, err := w.ListJobSnapshots(ctx, j.Ref())
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(snaps))
	for _, sn := range snaps {
		ids = append(ids, sn.ID)
	}
	usage, err := w.Usage(ctx, ids)
	if err != nil {
		return err
	}
	resp.SnapshotCount = len(snaps)
	if len(snaps) > 0 {
		resp.EarliestSnapshotAt = snaps[0].StartTime.Unix()
	}
	resp.ExportBytes, resp.PackedBytes = usage.ExportBytes, usage.PackedBytes
	if usage.ExportBytes > 0 {
		resp.Savings = max(1-float64(usage.PackedBytes)/float64(usage.ExportBytes), 0)
	}
	if err := job_repo.Job().SetSnapshotCount(ctx, j.ID, len(snaps)); err != nil {
		logger.Ctx(ctx).Warn("记录快照数失败", zap.Int64("job_id", j.ID), zap.Error(err))
	}
	return nil
}
