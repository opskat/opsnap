// Package run_repo 运行记录的数据访问
package run_repo

import (
	"context"

	"github.com/cago-frame/cago/database/db"
	"gorm.io/gorm"

	"github.com/opskat/opsnap/internal/model/entity/run_entity"
)

// ListFilter 运行列表的筛选；零值表示不筛选
type ListFilter struct {
	JobID  int64
	Status string
	Offset int
	Limit  int
}

// RunRepo 运行记录仓储
//
//go:generate mockgen -source run.go -destination mock_run_repo/run.go -package mock_run_repo
type RunRepo interface {
	// Find 按 ID 查找（含日志），不存在返回 nil
	Find(ctx context.Context, id int64) (*run_entity.Run, error)
	// List 按 ID 倒序分页列出，不含日志
	List(ctx context.Context, f ListFilter) ([]*run_entity.Run, int64, error)
	Create(ctx context.Context, r *run_entity.Run) error
	// Save 整体保存
	Save(ctx context.Context, r *run_entity.Run) error
	// PreviousFinished 任务在 beforeID 之前最近一次有结果的运行，没有返回 nil
	PreviousFinished(ctx context.Context, jobID, beforeID int64) (*run_entity.Run, error)
	// LastSucceeded 任务最近一次生成了快照的运行，没有返回 nil
	LastSucceeded(ctx context.Context, jobID int64) (*run_entity.Run, error)
	// LatestByJob 每个任务最近一次运行（不含跳过，含运行中），不含日志，键是任务 ID
	LatestByJob(ctx context.Context) (map[int64]*run_entity.Run, error)
	// LatestFinishedStatus 每个任务最近一次有结果的运行的状态，键是任务 ID
	LatestFinishedStatus(ctx context.Context) (map[int64]string, error)
	// ListStartedSince 开始时间不早于 since（Unix 秒）的运行，不含日志和跳过的记录，按开始时间升序；概览时间线用
	ListStartedSince(ctx context.Context, since int64) ([]*run_entity.Run, error)
	// StreakStart 任务当前连续处于 status 的这一段里最早一次运行的开始时间（只看有结果的运行），没有返回 0
	StreakStart(ctx context.Context, jobID int64, status string) (int64, error)
	// DeleteStartedBefore 删除开始时间早于 cutoff 的记录（含日志），仍在运行的不删；返回删除的条数
	DeleteStartedBefore(ctx context.Context, cutoff int64) (int64, error)
}

var defaultRun RunRepo

// Run 取得已注册的仓储
func Run() RunRepo { return defaultRun }

// RegisterRun 注册仓储实现
func RegisterRun(i RunRepo) { defaultRun = i }

type runRepo struct{}

// NewRun 创建基于 GORM 的实现
func NewRun() RunRepo { return &runRepo{} }

var finishedStatuses = []string{run_entity.StatusSuccess, run_entity.StatusWarning, run_entity.StatusFailed}

func (r *runRepo) Find(ctx context.Context, id int64) (*run_entity.Run, error) {
	ret := &run_entity.Run{}
	if err := db.Ctx(ctx).Where("id = ?", id).First(ret).Error; err != nil {
		if db.RecordNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return ret, nil
}

func (r *runRepo) List(ctx context.Context, f ListFilter) ([]*run_entity.Run, int64, error) {
	q := db.Ctx(ctx).Model(&run_entity.Run{})
	if f.JobID != 0 {
		q = q.Where("job_id = ?", f.JobID)
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []*run_entity.Run
	q = q.Omit("log").Order("id DESC")
	if f.Limit > 0 {
		q = q.Offset(f.Offset).Limit(f.Limit)
	}
	if err := q.Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

func (r *runRepo) Create(ctx context.Context, run *run_entity.Run) error {
	return db.Ctx(ctx).Create(run).Error
}

func (r *runRepo) Save(ctx context.Context, run *run_entity.Run) error {
	return db.Ctx(ctx).Select("*").Omit("id", "createtime").Where("id = ?", run.ID).Updates(run).Error
}

func (r *runRepo) PreviousFinished(ctx context.Context, jobID, beforeID int64) (*run_entity.Run, error) {
	return first(db.Ctx(ctx).Omit("log").Where("job_id = ? AND id < ? AND status IN ?", jobID, beforeID, finishedStatuses))
}

func (r *runRepo) LastSucceeded(ctx context.Context, jobID int64) (*run_entity.Run, error) {
	return first(db.Ctx(ctx).Omit("log").
		Where("job_id = ? AND status IN ?", jobID, []string{run_entity.StatusSuccess, run_entity.StatusWarning}))
}

// first 按 ID 倒序取第一条，没有返回 nil（用 Find 而不是 First，避免把「没有记录」当错误写日志）
func first(q *gorm.DB) (*run_entity.Run, error) {
	var list []*run_entity.Run
	if err := q.Order("id DESC").Limit(1).Find(&list).Error; err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, nil
	}
	return list[0], nil
}

func (r *runRepo) LatestByJob(ctx context.Context) (map[int64]*run_entity.Run, error) {
	var list []*run_entity.Run
	err := db.Ctx(ctx).Select("id", "job_id", "status", "started_at", "bytes_added").
		Where("id IN (?)", db.Ctx(ctx).Model(&run_entity.Run{}).Select("MAX(id)").
			Where("status <> ?", run_entity.StatusSkipped).Group("job_id")).
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	out := make(map[int64]*run_entity.Run, len(list))
	for _, run := range list {
		out[run.JobID] = run
	}
	return out, nil
}

func (r *runRepo) LatestFinishedStatus(ctx context.Context) (map[int64]string, error) {
	var rows []struct {
		JobID  int64
		Status string
	}
	err := db.Ctx(ctx).Raw(`SELECT r.job_id AS job_id, r.status AS status FROM runs r
		JOIN (SELECT job_id, MAX(id) AS id FROM runs WHERE status IN ? GROUP BY job_id) m ON m.id = r.id`,
		finishedStatuses).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[int64]string, len(rows))
	for _, row := range rows {
		out[row.JobID] = row.Status
	}
	return out, nil
}

func (r *runRepo) DeleteStartedBefore(ctx context.Context, cutoff int64) (int64, error) {
	res := db.Ctx(ctx).Where("started_at < ? AND status <> ?", cutoff, run_entity.StatusRunning).Delete(&run_entity.Run{})
	return res.RowsAffected, res.Error
}

func (r *runRepo) ListStartedSince(ctx context.Context, since int64) ([]*run_entity.Run, error) {
	var list []*run_entity.Run
	err := db.Ctx(ctx).Select("id", "job_id", "status", "started_at").
		Where("started_at >= ? AND status <> ?", since, run_entity.StatusSkipped).
		Order("started_at ASC, id ASC").Find(&list).Error
	return list, err
}

func (r *runRepo) StreakStart(ctx context.Context, jobID int64, status string) (int64, error) {
	var breakID int64
	if err := db.Ctx(ctx).Model(&run_entity.Run{}).Select("COALESCE(MAX(id), 0)").
		Where("job_id = ? AND status IN ? AND status <> ?", jobID, finishedStatuses, status).
		Scan(&breakID).Error; err != nil {
		return 0, err
	}
	var start int64
	err := db.Ctx(ctx).Model(&run_entity.Run{}).Select("COALESCE(MIN(started_at), 0)").
		Where("job_id = ? AND status = ? AND id > ?", jobID, status, breakID).
		Scan(&start).Error
	return start, err
}
