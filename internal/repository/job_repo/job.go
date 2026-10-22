// Package job_repo 备份任务的数据访问
package job_repo

import (
	"context"

	"github.com/cago-frame/cago/database/db"

	"github.com/opskat/opsnap/internal/model/entity/job_entity"
)

// JobRepo 备份任务仓储
//
//go:generate mockgen -source job.go -destination mock_job_repo/job.go -package mock_job_repo
type JobRepo interface {
	// Find 按 ID 查找，不存在返回 nil
	Find(ctx context.Context, id int64) (*job_entity.Job, error)
	// List 列出全部任务，按 ID 升序
	List(ctx context.Context) ([]*job_entity.Job, error)
	Create(ctx context.Context, j *job_entity.Job) error
	// Update 整体保存配置，不改 paused
	Update(ctx context.Context, j *job_entity.Job) error
	// SetPaused 只改暂停标记
	SetPaused(ctx context.Context, id int64, paused bool, updatetime int64) error
	// ListForgetPending 列出还欠着一次旧快照清理的任务（forget_pending_at > 0），按 ID 升序
	ListForgetPending(ctx context.Context) ([]*job_entity.Job, error)
	// SetForgetPending 只改待清理锚点与原因，不碰其他配置与 updatetime；at 为 0 表示清除
	SetForgetPending(ctx context.Context, id int64, at int64, reason string) error
	// Delete 只删除任务这一行，不触及存储位置里的快照
	Delete(ctx context.Context, id int64) error
	// CountByServer 每台服务器被多少个任务使用；经 SSH 的数据库和文件任务才计入
	CountByServer(ctx context.Context) (map[int64]int, error)
	// CountByRepo 每个存储位置被多少个任务使用
	CountByRepo(ctx context.Context) (map[int64]int, error)
}

var defaultJob JobRepo

// Job 取得已注册的仓储
func Job() JobRepo { return defaultJob }

// RegisterJob 注册仓储实现
func RegisterJob(i JobRepo) { defaultJob = i }

type jobRepo struct{}

// NewJob 创建基于 GORM 的实现
func NewJob() JobRepo { return &jobRepo{} }

func (r *jobRepo) Find(ctx context.Context, id int64) (*job_entity.Job, error) {
	ret := &job_entity.Job{}
	if err := db.Ctx(ctx).Where("id = ?", id).First(ret).Error; err != nil {
		if db.RecordNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return ret, nil
}

func (r *jobRepo) List(ctx context.Context) ([]*job_entity.Job, error) {
	var list []*job_entity.Job
	if err := db.Ctx(ctx).Order("id ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (r *jobRepo) Create(ctx context.Context, j *job_entity.Job) error {
	return db.Ctx(ctx).Create(j).Error
}

func (r *jobRepo) Update(ctx context.Context, j *job_entity.Job) error {
	return db.Ctx(ctx).Select("*").Omit("id", "createtime", "paused").Where("id = ?", j.ID).Updates(j).Error
}

func (r *jobRepo) SetPaused(ctx context.Context, id int64, paused bool, updatetime int64) error {
	return db.Ctx(ctx).Model(&job_entity.Job{}).Where("id = ?", id).
		Updates(map[string]any{"paused": paused, "updatetime": updatetime}).Error
}

func (r *jobRepo) ListForgetPending(ctx context.Context) ([]*job_entity.Job, error) {
	var list []*job_entity.Job
	if err := db.Ctx(ctx).Where("forget_pending_at > 0").Order("id ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (r *jobRepo) SetForgetPending(ctx context.Context, id int64, at int64, reason string) error {
	return db.Ctx(ctx).Model(&job_entity.Job{}).Where("id = ?", id).
		Updates(map[string]any{"forget_pending_at": at, "forget_pending_error": reason}).Error
}

func (r *jobRepo) Delete(ctx context.Context, id int64) error {
	return db.Ctx(ctx).Where("id = ?", id).Delete(&job_entity.Job{}).Error
}

func (r *jobRepo) CountByServer(ctx context.Context) (map[int64]int, error) {
	return r.countBy(ctx, "server_id")
}

func (r *jobRepo) CountByRepo(ctx context.Context) (map[int64]int, error) {
	return r.countBy(ctx, "repo_id")
}

// countBy column 只能是内部传入的固定列名
func (r *jobRepo) countBy(ctx context.Context, column string) (map[int64]int, error) {
	var rows []struct {
		ID    int64
		Count int
	}
	err := db.Ctx(ctx).Model(&job_entity.Job{}).
		Select(column + " AS id, COUNT(*) AS count").Where(column + " > 0").Group(column).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[int64]int, len(rows))
	for _, row := range rows {
		out[row.ID] = row.Count
	}
	return out, nil
}
