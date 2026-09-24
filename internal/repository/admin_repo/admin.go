// Package admin_repo 读写唯一管理员账号。
package admin_repo

import (
	"context"
	"errors"

	"github.com/cago-frame/cago/database/db"

	"github.com/opskat/opsnap/internal/model/entity/admin_entity"
)

//go:generate mockgen -source admin.go -destination mock/admin.go

// ErrAlreadyExists 管理员已存在（首次设置并发时只有一个能成功）
var ErrAlreadyExists = errors.New("管理员已存在")

type AdminRepo interface {
	// Get 读取管理员；尚未创建时返回 nil, nil
	Get(ctx context.Context) (*admin_entity.Admin, error)
	// Create 创建管理员；已存在时返回 ErrAlreadyExists
	Create(ctx context.Context, admin *admin_entity.Admin) error
	// UpdatePassword 更新密码哈希与密码修改时间
	UpdatePassword(ctx context.Context, hash string, now int64) error
}

var defaultAdmin AdminRepo

func Admin() AdminRepo {
	return defaultAdmin
}

func RegisterAdmin(i AdminRepo) {
	defaultAdmin = i
}

type adminRepo struct{}

func NewAdmin() AdminRepo {
	return &adminRepo{}
}

func (r *adminRepo) Get(ctx context.Context) (*admin_entity.Admin, error) {
	var rows []*admin_entity.Admin
	if err := db.Ctx(ctx).Where("id = ?", admin_entity.AdminID).Limit(1).Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

func (r *adminRepo) UpdatePassword(ctx context.Context, hash string, now int64) error {
	return db.Ctx(ctx).Model(&admin_entity.Admin{}).Where("id = ?", admin_entity.AdminID).
		Updates(map[string]any{"password_hash": hash, "password_updatetime": now, "updatetime": now}).Error
}

func (r *adminRepo) Create(ctx context.Context, admin *admin_entity.Admin) error {
	admin.ID = admin_entity.AdminID
	res := db.Ctx(ctx).Exec(`INSERT INTO admins (id, username, password_hash, password_updatetime, createtime, updatetime)
VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT (id) DO NOTHING`,
		admin.ID, admin.Username, admin.PasswordHash, admin.PasswordUpdatetime, admin.Createtime, admin.Updatetime)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrAlreadyExists
	}
	return nil
}
