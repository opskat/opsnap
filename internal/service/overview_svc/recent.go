package overview_svc

import (
	"context"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	jobapi "github.com/opskat/opsnap/internal/api/job"
	api "github.com/opskat/opsnap/internal/api/overview"
	"github.com/opskat/opsnap/internal/model/entity/datasource_entity"
	"github.com/opskat/opsnap/internal/model/entity/job_entity"
	"github.com/opskat/opsnap/internal/repository/job_repo"
	"github.com/opskat/opsnap/internal/service/job_svc"
)

// recent 所有任务中最近 recentRuns 次运行（status 非空时只列该状态的），按触发顺序倒序
func recent(ctx context.Context, status string, jobs map[int64]*job_entity.Job,
	dss map[int64]*datasource_entity.DataSource) ([]*api.RecentRun, error) {
	runs, err := job_repo.Run().ListRecent(ctx, status, recentRuns)
	if err != nil {
		return nil, err
	}
	out := make([]*api.RecentRun, 0, len(runs))
	for _, run := range runs {
		row := &api.RecentRun{Run: toRun(ctx, run)}
		if run.Status == job_entity.RunRunning {
			if live := liveRun(ctx, run); live != nil {
				row.Run = *live
			}
		}
		if j := jobs[run.JobID]; j != nil {
			row.JobName, row.JobType = j.Name, j.Type
			if ds := dss[j.DataSourceID]; ds != nil {
				row.DataSourceKind, row.DataSourceAddress = ds.Kind, ds.Address()
			}
		}
		out = append(out, row)
	}
	return out, nil
}

// toRun 已结束或等待中的运行，字段与任务页的运行记录相同（固定原因按 ctx 的界面语言显示）
func toRun(ctx context.Context, run *job_entity.Run) jobapi.Run {
	out := jobapi.Run{ID: run.ID, JobID: run.JobID, Status: run.Status, Trigger: run.Trigger,
		RetryAttempt: run.RetryAttempt, RetryTotal: run.RetryTotal, ScheduledAt: run.ScheduledAt,
		CreatedAt: run.Createtime, StartedAt: run.StartedAt / 1000, FinishedAt: run.FinishedAt / 1000,
		ExportedBytes: run.ExportedBytes, UploadedBytes: run.UploadedBytes, SnapshotID: run.SnapshotID,
		FailedStep: run.FailedStep, Reason: run.DisplayReason(ctx)}
	if run.FinishedAt > 0 && run.StartedAt > 0 {
		out.DurationMs = run.FinishedAt - run.StartedAt
	}
	return out
}

// liveRun 运行中的运行由任务模块给出实时的已导出量与已运行时长（与任务页相同）。它在最近 recentRuns 次运行之中，
// 比它新的运行不到 recentRuns 次，因此一定在本任务运行记录的第一页。读不到（如任务此间被删除）时返回 nil，
// 该行照常显示数据库中的记录
func liveRun(ctx context.Context, run *job_entity.Run) *jobapi.Run {
	page, err := job_svc.Job().Runs(ctx, &jobapi.RunsRequest{ID: run.JobID})
	if err != nil {
		logger.Ctx(ctx).Warn("读取运行中的实时进度失败", zap.Int64("run_id", run.ID), zap.Error(err))
		return nil
	}
	for _, r := range page.Items {
		if r.ID == run.ID {
			return r
		}
	}
	return nil
}
