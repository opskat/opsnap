// Package session_repo 读写登录会话。会话标识只以 SHA-256 形式保存。
package session_repo

import (
	"context"

	"github.com/cago-frame/cago/database/db"

	"github.com/opskat/opsnap/internal/model/entity/admin_entity"
)

//go:generate mockgen -source session.go -destination mock/session.go

type SessionRepo interface {
	Create(ctx context.Context, s *admin_entity.Session) error
	// FindByTokenHash 不存在时返回 nil, nil
	FindByTokenHash(ctx context.Context, hash string) (*admin_entity.Session, error)
	// Touch 顺延过期时间
	Touch(ctx context.Context, id int64, expiretime, now int64) error
	Delete(ctx context.Context, id int64) error
	// DeleteAllExcept 删除管理员的全部会话，keepID 非 0 时保留该会话
	DeleteAllExcept(ctx context.Context, adminID, keepID int64) error
	// Find 按 ID 读取；不存在时返回 nil, nil
	Find(ctx context.Context, id int64) (*admin_entity.Session, error)
}

var defaultSession SessionRepo

func Session() SessionRepo {
	return defaultSession
}

func RegisterSession(i SessionRepo) {
	defaultSession = i
}

type sessionRepo struct{}

func NewSession() SessionRepo {
	return &sessionRepo{}
}

func (r *sessionRepo) Create(ctx context.Context, s *admin_entity.Session) error {
	return db.Ctx(ctx).Create(s).Error
}

func (r *sessionRepo) FindByTokenHash(ctx context.Context, hash string) (*admin_entity.Session, error) {
	var rows []*admin_entity.Session
	if err := db.Ctx(ctx).Where("token_hash = ?", hash).Limit(1).Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

func (r *sessionRepo) Touch(ctx context.Context, id int64, expiretime, now int64) error {
	return db.Ctx(ctx).Model(&admin_entity.Session{}).Where("id = ?", id).
		Updates(map[string]any{"expiretime": expiretime, "updatetime": now}).Error
}

func (r *sessionRepo) DeleteAllExcept(ctx context.Context, adminID, keepID int64) error {
	return db.Ctx(ctx).Where("admin_id = ? AND id <> ?", adminID, keepID).Delete(&admin_entity.Session{}).Error
}

func (r *sessionRepo) Find(ctx context.Context, id int64) (*admin_entity.Session, error) {
	var rows []*admin_entity.Session
	if err := db.Ctx(ctx).Where("id = ?", id).Limit(1).Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

func (r *sessionRepo) Delete(ctx context.Context, id int64) error {
	return db.Ctx(ctx).Where("id = ?", id).Delete(&admin_entity.Session{}).Error
}
