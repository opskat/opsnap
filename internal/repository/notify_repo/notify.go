// Package notify_repo 通知设置的数据访问
package notify_repo

import (
	"context"

	"github.com/cago-frame/cago/database/db"
	"gorm.io/gorm/clause"

	"github.com/opskat/opsnap/internal/model/entity/notify_entity"
)

// NotifyRepo 通知设置仓储；只有一行
//
//go:generate mockgen -source notify.go -destination mock_notify_repo/notify.go -package mock_notify_repo
type NotifyRepo interface {
	// Get 返回已保存的设置，从未保存过时返回 nil
	Get(ctx context.Context) (*notify_entity.Notify, error)
	// Save 覆盖保存
	Save(ctx context.Context, n *notify_entity.Notify) error
}

var defaultNotify NotifyRepo

// Notify 取得已注册的仓储
func Notify() NotifyRepo { return defaultNotify }

// RegisterNotify 注册仓储实现
func RegisterNotify(i NotifyRepo) { defaultNotify = i }

type notifyRepo struct{}

// NewNotify 创建基于 GORM 的实现
func NewNotify() NotifyRepo { return &notifyRepo{} }

func (r *notifyRepo) Get(ctx context.Context) (*notify_entity.Notify, error) {
	ret := &notify_entity.Notify{}
	if err := db.Ctx(ctx).Where("id = ?", notify_entity.SingletonID).First(ret).Error; err != nil {
		if db.RecordNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return ret, nil
}

func (r *notifyRepo) Save(ctx context.Context, n *notify_entity.Notify) error {
	n.ID = notify_entity.SingletonID
	return db.Ctx(ctx).Clauses(clause.OnConflict{UpdateAll: true}).Create(n).Error
}
