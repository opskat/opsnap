// Package job_ctr 备份任务接口控制器
package job_ctr

import (
	"context"

	jobapi "github.com/opskat/opsnap/internal/api/job"
	"github.com/opskat/opsnap/internal/service/job_svc"
)

type Job struct{}

func NewJob() *Job { return &Job{} }

// List 列出任务
func (c *Job) List(ctx context.Context, req *jobapi.ListRequest) (*jobapi.ListResponse, error) {
	return job_svc.Job().List(ctx, req.Filter, req.Q)
}

// Get 读取一个任务
func (c *Job) Get(ctx context.Context, req *jobapi.GetRequest) (*jobapi.GetResponse, error) {
	return job_svc.Job().Get(ctx, req.ID)
}

// Create 新建任务
func (c *Job) Create(ctx context.Context, req *jobapi.CreateRequest) (*jobapi.CreateResponse, error) {
	return job_svc.Job().Create(ctx, req)
}

// Update 修改任务
func (c *Job) Update(ctx context.Context, req *jobapi.UpdateRequest) (*jobapi.UpdateResponse, error) {
	return job_svc.Job().Update(ctx, req)
}

// Delete 删除任务，保留快照
func (c *Job) Delete(ctx context.Context, req *jobapi.DeleteRequest) (*jobapi.DeleteResponse, error) {
	if err := job_svc.Job().Delete(ctx, req.ID); err != nil {
		return nil, err
	}
	return &jobapi.DeleteResponse{}, nil
}

// Duplicate 复制为新任务
func (c *Job) Duplicate(ctx context.Context, req *jobapi.DuplicateRequest) (*jobapi.DuplicateResponse, error) {
	item, err := job_svc.Job().Duplicate(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	return &jobapi.DuplicateResponse{Item: *item}, nil
}

// Pause 暂停计划
func (c *Job) Pause(ctx context.Context, req *jobapi.PauseRequest) (*jobapi.PauseResponse, error) {
	item, err := job_svc.Job().SetPaused(ctx, req.ID, true)
	if err != nil {
		return nil, err
	}
	return &jobapi.PauseResponse{Item: *item}, nil
}

// Resume 恢复计划
func (c *Job) Resume(ctx context.Context, req *jobapi.ResumeRequest) (*jobapi.ResumeResponse, error) {
	item, err := job_svc.Job().SetPaused(ctx, req.ID, false)
	if err != nil {
		return nil, err
	}
	return &jobapi.ResumeResponse{Item: *item}, nil
}

// NextRuns 预览下次运行时间
func (c *Job) NextRuns(ctx context.Context, req *jobapi.NextRunsRequest) (*jobapi.NextRunsResponse, error) {
	return job_svc.Job().NextRuns(ctx, req)
}

// Databases 读取数据库列表及大小
func (c *Job) Databases(ctx context.Context, req *jobapi.DatabasesRequest) (*jobapi.DatabasesResponse, error) {
	return job_svc.Job().Databases(ctx, req)
}

// Test 测试连接
func (c *Job) Test(ctx context.Context, req *jobapi.TestRequest) (*jobapi.TestResponse, error) {
	return job_svc.Job().Test(ctx, req)
}
