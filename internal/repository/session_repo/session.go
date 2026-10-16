// Package session_repo 登录会话的数据访问
package session_repo

import (
	"context"

	"github.com/cago-frame/cago/database/db"

	"github.com/opskat/opsnap/internal/model/entity/session_entity"
)

// SessionRepo 会话仓储
//
//go:generate mockgen -source session.go -destination mock_session_repo/session.go -package mock_session_repo
type SessionRepo interface {
	Create(ctx context.Context, s *session_entity.Session) error
	// FindByHash 按令牌哈希查找，不存在返回 nil
	FindByHash(ctx context.Context, hash string) (*session_entity.Session, error)
	Delete(ctx context.Context, hash string) error
	// DeleteExcept 删除除指定令牌之外的全部会话
	DeleteExcept(ctx context.Context, keepHash string) error
	// DeleteExpired 删除 expires_at <= now（Unix 秒）的会话
	DeleteExpired(ctx context.Context, now int64) error
}

var defaultSession SessionRepo

// Session 取得已注册的仓储
func Session() SessionRepo { return defaultSession }

// RegisterSession 注册仓储实现
func RegisterSession(i SessionRepo) { defaultSession = i }

type sessionRepo struct{}

// NewSession 创建基于 GORM 的实现
func NewSession() SessionRepo { return &sessionRepo{} }

func (r *sessionRepo) Create(ctx context.Context, s *session_entity.Session) error {
	return db.Ctx(ctx).Create(s).Error
}

func (r *sessionRepo) FindByHash(ctx context.Context, hash string) (*session_entity.Session, error) {
	ret := &session_entity.Session{}
	if err := db.Ctx(ctx).Where("token_hash = ?", hash).First(ret).Error; err != nil {
		if db.RecordNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return ret, nil
}

func (r *sessionRepo) Delete(ctx context.Context, hash string) error {
	return db.Ctx(ctx).Where("token_hash = ?", hash).Delete(&session_entity.Session{}).Error
}

func (r *sessionRepo) DeleteExcept(ctx context.Context, keepHash string) error {
	return db.Ctx(ctx).Where("token_hash <> ?", keepHash).Delete(&session_entity.Session{}).Error
}

func (r *sessionRepo) DeleteExpired(ctx context.Context, now int64) error {
	return db.Ctx(ctx).Where("expires_at <= ?", now).Delete(&session_entity.Session{}).Error
}
