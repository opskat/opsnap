// Package alert_repo 告警状态的数据访问
package alert_repo

import (
	"context"
	"time"

	"github.com/cago-frame/cago/database/db"
	"gorm.io/gorm/clause"

	"github.com/opskat/opsnap/internal/model/entity/alert_entity"
)

// AlertRepo 「太久没有成功」告警状态仓储
//
//go:generate mockgen -source alert.go -destination mock_alert_repo/alert.go -package mock_alert_repo
type AlertRepo interface {
	// Create 记录任务进入告警；已经记录过时返回 false，不改动原记录
	Create(ctx context.Context, jobID int64, since time.Time) (created bool, err error)
	// Clear 清除任务的告警记录；之前确有记录时返回 true
	Clear(ctx context.Context, jobID int64) (was bool, err error)
	// List 全部告警记录
	List(ctx context.Context) ([]*alert_entity.StaleAlert, error)
}

var defaultAlert AlertRepo

// Alert 取得已注册的仓储
func Alert() AlertRepo { return defaultAlert }

// RegisterAlert 注册仓储实现
func RegisterAlert(i AlertRepo) { defaultAlert = i }

type alertRepo struct{}

// NewAlert 创建基于 GORM 的实现
func NewAlert() AlertRepo { return &alertRepo{} }

func (r *alertRepo) Create(ctx context.Context, jobID int64, since time.Time) (bool, error) {
	row := &alert_entity.StaleAlert{JobID: jobID, Since: since.Unix(), Createtime: since.Unix()}
	res := db.Ctx(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(row)
	return res.RowsAffected > 0, res.Error
}

func (r *alertRepo) Clear(ctx context.Context, jobID int64) (bool, error) {
	res := db.Ctx(ctx).Where("job_id = ?", jobID).Delete(&alert_entity.StaleAlert{})
	return res.RowsAffected > 0, res.Error
}

func (r *alertRepo) List(ctx context.Context) ([]*alert_entity.StaleAlert, error) {
	var list []*alert_entity.StaleAlert
	if err := db.Ctx(ctx).Order("job_id ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}
