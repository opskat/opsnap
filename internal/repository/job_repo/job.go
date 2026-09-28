// Package job_repo 读写备份任务记录。
package job_repo

import (
	"context"
	"errors"

	"github.com/cago-frame/cago/database/db"

	"github.com/opskat/opsnap/internal/model/entity/job_entity"
)

//go:generate mockgen -source job.go -destination mock/job.go

type JobRepo interface {
	Create(ctx context.Context, j *job_entity.Job) error
	// Save 按 ID 更新全部字段（run_now 与 snapshot_count 除外，它们由运行模块单独维护）；
	// 记录已被删除时返回 ErrNotFound，不会重新插入
	Save(ctx context.Context, j *job_entity.Job) error
	// List 按 ID 正序
	List(ctx context.Context) ([]*job_entity.Job, error)
	// Find 不存在时返回 nil, nil
	Find(ctx context.Context, id int64) (*job_entity.Job, error)
	// FindByName 不存在时返回 nil, nil
	FindByName(ctx context.Context, name string) (*job_entity.Job, error)
	// ListByStorage 使用该存储的任务，按 ID 正序
	ListByStorage(ctx context.Context, storageID int64) ([]*job_entity.Job, error)
	// ListByDataSource 使用该数据源的任务，按 ID 正序
	ListByDataSource(ctx context.Context, dataSourceID int64) ([]*job_entity.Job, error)
	Delete(ctx context.Context, id int64) error
	// ClearRunNow 清除“立即执行一次”标记；返回是否由本次清除（标记原本为 true），并发调用只有一个得到 true
	ClearRunNow(ctx context.Context, id int64) (bool, error)
	// ListRunNow 带“立即执行一次”标记的任务，按 ID 正序
	ListRunNow(ctx context.Context) ([]*job_entity.Job, error)
	// SetSnapshotCount 记录本任务当前的快照数
	SetSnapshotCount(ctx context.Context, id int64, n int) error
}

// ErrNotFound 保存时记录已不存在（已被删除）
var ErrNotFound = errors.New("job not found")

var defaultJob JobRepo

func Job() JobRepo {
	return defaultJob
}

func RegisterJob(i JobRepo) {
	defaultJob = i
}

type jobRepo struct{}

func NewJob() JobRepo {
	return &jobRepo{}
}

func (r *jobRepo) Create(ctx context.Context, j *job_entity.Job) error {
	return db.Ctx(ctx).Create(j).Error
}

func (r *jobRepo) Save(ctx context.Context, j *job_entity.Job) error {
	// 不用 gorm 的 Save：它在没有匹配行时会改为插入，把刚删除的任务重新写回
	res := db.Ctx(ctx).Model(j).Select("*").Omit("run_now", "snapshot_count").Updates(j)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *jobRepo) list(ctx context.Context, query string, args ...any) ([]*job_entity.Job, error) {
	var rows []*job_entity.Job
	q := db.Ctx(ctx)
	if query != "" {
		q = q.Where(query, args...)
	}
	err := q.Order("id ASC").Find(&rows).Error
	return rows, err
}

func (r *jobRepo) List(ctx context.Context) ([]*job_entity.Job, error) {
	return r.list(ctx, "")
}

func (r *jobRepo) first(ctx context.Context, query string, arg any) (*job_entity.Job, error) {
	rows, err := r.list(ctx, query, arg)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return rows[0], nil
}

func (r *jobRepo) Find(ctx context.Context, id int64) (*job_entity.Job, error) {
	return r.first(ctx, "id = ?", id)
}

func (r *jobRepo) FindByName(ctx context.Context, name string) (*job_entity.Job, error) {
	return r.first(ctx, "name = ?", name)
}

func (r *jobRepo) ListByStorage(ctx context.Context, storageID int64) ([]*job_entity.Job, error) {
	return r.list(ctx, "storage_id = ?", storageID)
}

func (r *jobRepo) ListByDataSource(ctx context.Context, dataSourceID int64) ([]*job_entity.Job, error) {
	return r.list(ctx, "datasource_id = ?", dataSourceID)
}

func (r *jobRepo) Delete(ctx context.Context, id int64) error {
	return db.Ctx(ctx).Delete(&job_entity.Job{}, id).Error
}

func (r *jobRepo) ClearRunNow(ctx context.Context, id int64) (bool, error) {
	res := db.Ctx(ctx).Model(&job_entity.Job{}).Where("id = ? AND run_now = ?", id, true).Update("run_now", false)
	return res.RowsAffected > 0, res.Error
}

func (r *jobRepo) ListRunNow(ctx context.Context) ([]*job_entity.Job, error) {
	return r.list(ctx, "run_now = ?", true)
}

func (r *jobRepo) SetSnapshotCount(ctx context.Context, id int64, n int) error {
	return db.Ctx(ctx).Model(&job_entity.Job{}).Where("id = ?", id).Update("snapshot_count", n).Error
}
