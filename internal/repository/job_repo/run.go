package job_repo

import (
	"context"

	"github.com/cago-frame/cago/database/db"
	"gorm.io/gorm"

	"github.com/opskat/opsnap/internal/model/entity/job_entity"
)

//go:generate mockgen -source run.go -destination mock/run.go

// RunRepo 读写任务的运行记录。列表类方法不读取执行日志（Log 为空），日志用 Find 读取
type RunRepo interface {
	Create(ctx context.Context, r *job_entity.Run) error
	// SaveIf 状态仍为 from 时按 ID 更新全部字段，返回是否更新（状态已被其他操作改变时为 false）
	SaveIf(ctx context.Context, r *job_entity.Run, from string) (bool, error)
	// Find 不存在时返回 nil, nil
	Find(ctx context.Context, id int64) (*job_entity.Run, error)
	// Active 任务等待中或运行中的运行，没有时返回 nil, nil
	Active(ctx context.Context, jobID int64) (*job_entity.Run, error)
	// ListActive 全部等待中或运行中的运行，按 ID 正序
	ListActive(ctx context.Context) ([]*job_entity.Run, error)
	// Page 任务的运行记录，按 ID 倒序（即触发顺序倒序），以及总数
	Page(ctx context.Context, jobID int64, offset, limit int) ([]*job_entity.Run, int64, error)
	// Latest 每个任务最近一次运行（ID 最大），任务 ID → 运行
	Latest(ctx context.Context) (map[int64]*job_entity.Run, error)
	// RecentFinished 任务最近 limit 次已结束（不含等待中、运行中）的运行，按 ID 倒序
	RecentFinished(ctx context.Context, jobID int64, limit int) ([]*job_entity.Run, error)
	// LastSuccess 任务最近一次成功的运行，没有时返回 nil, nil
	LastSuccess(ctx context.Context, jobID int64) (*job_entity.Run, error)
	// HasScheduled 任务是否已有对应该计划时间（秒）的运行记录（计划或补跑，含跳过）
	HasScheduled(ctx context.Context, jobID int64, scheduledAt int64) (bool, error)
	// Trim 只保留任务最近 keep 条运行记录（按 ID），删除更早的；等待中与运行中的记录无论多早都不删除
	Trim(ctx context.Context, jobID int64, keep int) error
	// DeleteByJob 删除任务的全部运行记录
	DeleteByJob(ctx context.Context, jobID int64) error

	// ListRecent 所有任务中最近 limit 次运行，按 ID 倒序（即触发顺序倒序）；status 非空时只列出该状态的
	ListRecent(ctx context.Context, status string, limit int) ([]*job_entity.Run, error)
	// ListStartedSince 所有任务中开始时间（毫秒）不早于 since、状态为 statuses 之一的运行，
	// 只读取 ID、任务、状态与开始时间
	ListStartedSince(ctx context.Context, since int64, statuses []string) ([]*job_entity.Run, error)
	// SucceededJobs 至少有一次成功运行的任务，任务 ID → true
	SucceededJobs(ctx context.Context) (map[int64]bool, error)
}

// defaultRun 运行记录与任务共用元数据库，没有需要替换的依赖；测试可用 RegisterRun 替换为 mock
var defaultRun RunRepo = &runRepo{}

func Run() RunRepo {
	return defaultRun
}

func RegisterRun(i RunRepo) {
	defaultRun = i
}

type runRepo struct{}

func NewRun() RunRepo {
	return &runRepo{}
}

// summary 不读取执行日志的查询
func summary(ctx context.Context) *gorm.DB {
	return db.Ctx(ctx).Model(&job_entity.Run{}).Omit("log")
}

func (r *runRepo) Create(ctx context.Context, run *job_entity.Run) error {
	return db.Ctx(ctx).Create(run).Error
}

func (r *runRepo) SaveIf(ctx context.Context, run *job_entity.Run, from string) (bool, error) {
	res := db.Ctx(ctx).Model(run).Where("status = ?", from).Select("*").Updates(run)
	return res.RowsAffected > 0, res.Error
}

func (r *runRepo) first(q *gorm.DB) (*job_entity.Run, error) {
	var rows []*job_entity.Run
	if err := q.Limit(1).Find(&rows).Error; err != nil || len(rows) == 0 {
		return nil, err
	}
	return rows[0], nil
}

func (r *runRepo) Find(ctx context.Context, id int64) (*job_entity.Run, error) {
	return r.first(db.Ctx(ctx).Where("id = ?", id))
}

var activeStatuses = []string{job_entity.RunQueued, job_entity.RunRunning}

func (r *runRepo) Active(ctx context.Context, jobID int64) (*job_entity.Run, error) {
	return r.first(summary(ctx).Where("job_id = ? AND status IN ?", jobID, activeStatuses).Order("id DESC"))
}

func (r *runRepo) ListActive(ctx context.Context) ([]*job_entity.Run, error) {
	var rows []*job_entity.Run
	err := summary(ctx).Where("status IN ?", activeStatuses).Order("id ASC").Find(&rows).Error
	return rows, err
}

func (r *runRepo) Page(ctx context.Context, jobID int64, offset, limit int) ([]*job_entity.Run, int64, error) {
	var total int64
	if err := db.Ctx(ctx).Model(&job_entity.Run{}).Where("job_id = ?", jobID).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []*job_entity.Run
	err := summary(ctx).Where("job_id = ?", jobID).Order("id DESC").Offset(offset).Limit(limit).Find(&rows).Error
	return rows, total, err
}

func (r *runRepo) Latest(ctx context.Context) (map[int64]*job_entity.Run, error) {
	var rows []*job_entity.Run
	err := summary(ctx).Where("id IN (?)", db.Ctx(ctx).Model(&job_entity.Run{}).Select("MAX(id)").Group("job_id")).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[int64]*job_entity.Run, len(rows))
	for _, run := range rows {
		out[run.JobID] = run
	}
	return out, nil
}

func (r *runRepo) RecentFinished(ctx context.Context, jobID int64, limit int) ([]*job_entity.Run, error) {
	var rows []*job_entity.Run
	err := summary(ctx).Where("job_id = ? AND status NOT IN ?", jobID, activeStatuses).
		Order("id DESC").Limit(limit).Find(&rows).Error
	return rows, err
}

func (r *runRepo) LastSuccess(ctx context.Context, jobID int64) (*job_entity.Run, error) {
	return r.first(summary(ctx).Where("job_id = ? AND status = ?", jobID, job_entity.RunSuccess).Order("id DESC"))
}

func (r *runRepo) HasScheduled(ctx context.Context, jobID int64, scheduledAt int64) (bool, error) {
	var n int64
	err := db.Ctx(ctx).Model(&job_entity.Run{}).Where("job_id = ? AND scheduled_at = ?", jobID, scheduledAt).Count(&n).Error
	return n > 0, err
}

func (r *runRepo) Trim(ctx context.Context, jobID int64, keep int) error {
	keepIDs := db.Ctx(ctx).Model(&job_entity.Run{}).Select("id").Where("job_id = ?", jobID).Order("id DESC").Limit(keep)
	return db.Ctx(ctx).Where("job_id = ? AND id NOT IN (?) AND status NOT IN ?", jobID, keepIDs, activeStatuses).
		Delete(&job_entity.Run{}).Error
}

func (r *runRepo) DeleteByJob(ctx context.Context, jobID int64) error {
	return db.Ctx(ctx).Where("job_id = ?", jobID).Delete(&job_entity.Run{}).Error
}

func (r *runRepo) ListRecent(ctx context.Context, status string, limit int) ([]*job_entity.Run, error) {
	q := summary(ctx)
	if status != "" {
		q = q.Where("status = ?", status)
	}
	var rows []*job_entity.Run
	err := q.Order("id DESC").Limit(limit).Find(&rows).Error
	return rows, err
}

func (r *runRepo) ListStartedSince(ctx context.Context, since int64, statuses []string) ([]*job_entity.Run, error) {
	var rows []*job_entity.Run
	err := db.Ctx(ctx).Model(&job_entity.Run{}).Select("id", "job_id", "status", "started_at").
		Where("status IN ? AND started_at >= ?", statuses, since).Find(&rows).Error
	return rows, err
}

func (r *runRepo) SucceededJobs(ctx context.Context) (map[int64]bool, error) {
	var ids []int64
	err := db.Ctx(ctx).Model(&job_entity.Run{}).Distinct("job_id").Where("status = ?", job_entity.RunSuccess).
		Pluck("job_id", &ids).Error
	if err != nil {
		return nil, err
	}
	out := make(map[int64]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}
