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
	// Save 按 ID 更新全部字段；记录已被删除时返回 ErrNotFound，不会重新插入
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
	res := db.Ctx(ctx).Model(j).Select("*").Updates(j)
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
