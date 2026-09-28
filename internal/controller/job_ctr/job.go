// Package job_ctr 暴露备份任务管理接口。
package job_ctr

import (
	"context"

	api "github.com/opskat/opsnap/internal/api/job"
	"github.com/opskat/opsnap/internal/service/job_svc"
)

type Job struct{}

func NewJob() *Job {
	return &Job{}
}

// List 任务列表
func (c *Job) List(ctx context.Context, req *api.ListRequest) (*api.ListResponse, error) {
	return job_svc.Job().List(ctx, req)
}

// Get 按 ID 获取任务
func (c *Job) Get(ctx context.Context, req *api.GetRequest) (*api.GetResponse, error) {
	return job_svc.Job().Get(ctx, req)
}

// Create 新建并启用任务
func (c *Job) Create(ctx context.Context, req *api.CreateRequest) (*api.CreateResponse, error) {
	return job_svc.Job().Create(ctx, req)
}

// Update 编辑任务
func (c *Job) Update(ctx context.Context, req *api.UpdateRequest) (*api.UpdateResponse, error) {
	return job_svc.Job().Update(ctx, req)
}

// Pause 暂停任务
func (c *Job) Pause(ctx context.Context, req *api.PauseRequest) (*api.PauseResponse, error) {
	return job_svc.Job().Pause(ctx, req)
}

// Enable 启用任务
func (c *Job) Enable(ctx context.Context, req *api.EnableRequest) (*api.EnableResponse, error) {
	return job_svc.Job().Enable(ctx, req)
}

// Delete 删除任务，可选同时删除它的快照
func (c *Job) Delete(ctx context.Context, req *api.DeleteRequest) (*api.DeleteResponse, error) {
	return job_svc.Job().Delete(ctx, req)
}

// SchedulePreview 计划预览：接下来三次执行时间与最多保留的快照份数
func (c *Job) SchedulePreview(ctx context.Context, req *api.SchedulePreviewRequest) (*api.SchedulePreviewResponse, error) {
	return job_svc.Job().SchedulePreview(ctx, req)
}

// RunNow 立即执行一次
func (c *Job) RunNow(ctx context.Context, req *api.RunNowRequest) (*api.RunNowResponse, error) {
	return job_svc.Job().RunNow(ctx, req)
}

// CancelRun 取消运行
func (c *Job) CancelRun(ctx context.Context, req *api.CancelRunRequest) (*api.CancelRunResponse, error) {
	return job_svc.Job().CancelRun(ctx, req)
}

// Runs 运行记录
func (c *Job) Runs(ctx context.Context, req *api.RunsRequest) (*api.RunsResponse, error) {
	return job_svc.Job().Runs(ctx, req)
}

// RunLog 运行日志
func (c *Job) RunLog(ctx context.Context, req *api.RunLogRequest) (*api.RunLogResponse, error) {
	return job_svc.Job().RunLog(ctx, req)
}

// Stats 任务统计
func (c *Job) Stats(ctx context.Context, req *api.StatsRequest) (*api.StatsResponse, error) {
	return job_svc.Job().Stats(ctx, req)
}
