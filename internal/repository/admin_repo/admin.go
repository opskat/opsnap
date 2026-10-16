// Package admin_repo 管理员账号的数据访问
package admin_repo

import (
	"context"

	"github.com/cago-frame/cago/database/db"

	"github.com/opskat/opsnap/internal/model/entity/admin_entity"
)

// AdminRepo 管理员仓储；系统里只有一个管理员
//
//go:generate mockgen -source admin.go -destination mock_admin_repo/admin.go -package mock_admin_repo
type AdminRepo interface {
	// Find 返回管理员，尚未初始化时返回 nil
	Find(ctx context.Context) (*admin_entity.Admin, error)
	Create(ctx context.Context, a *admin_entity.Admin) error
	Update(ctx context.Context, a *admin_entity.Admin) error
}

var defaultAdmin AdminRepo

// Admin 取得已注册的仓储
func Admin() AdminRepo { return defaultAdmin }

// RegisterAdmin 注册仓储实现
func RegisterAdmin(i AdminRepo) { defaultAdmin = i }

type adminRepo struct{}

// NewAdmin 创建基于 GORM 的实现
func NewAdmin() AdminRepo { return &adminRepo{} }

func (r *adminRepo) Find(ctx context.Context) (*admin_entity.Admin, error) {
	ret := &admin_entity.Admin{}
	if err := db.Ctx(ctx).Where("id = ?", admin_entity.SingletonID).First(ret).Error; err != nil {
		if db.RecordNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return ret, nil
}

func (r *adminRepo) Create(ctx context.Context, a *admin_entity.Admin) error {
	a.ID = admin_entity.SingletonID
	return db.Ctx(ctx).Create(a).Error
}

func (r *adminRepo) Update(ctx context.Context, a *admin_entity.Admin) error {
	return db.Ctx(ctx).Save(a).Error
}
