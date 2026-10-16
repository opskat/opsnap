// Package sso_repo 单点登录配置的数据访问
package sso_repo

import (
	"context"

	"github.com/cago-frame/cago/database/db"
	"gorm.io/gorm/clause"

	"github.com/opskat/opsnap/internal/model/entity/sso_entity"
)

// SSORepo 单点登录配置仓储；只有一行
//
//go:generate mockgen -source sso.go -destination mock_sso_repo/sso.go -package mock_sso_repo
type SSORepo interface {
	// Get 返回已保存的配置，从未保存过（或已停用）时返回 nil
	Get(ctx context.Context) (*sso_entity.SSO, error)
	// Save 覆盖保存
	Save(ctx context.Context, s *sso_entity.SSO) error
	// Delete 删除整行：停用单点登录、解除绑定、恢复允许密码登录
	Delete(ctx context.Context) error
}

var defaultSSO SSORepo

// SSO 取得已注册的仓储
func SSO() SSORepo { return defaultSSO }

// RegisterSSO 注册仓储实现
func RegisterSSO(i SSORepo) { defaultSSO = i }

type ssoRepo struct{}

// NewSSO 创建基于 GORM 的实现
func NewSSO() SSORepo { return &ssoRepo{} }

func (r *ssoRepo) Get(ctx context.Context) (*sso_entity.SSO, error) {
	ret := &sso_entity.SSO{}
	if err := db.Ctx(ctx).Where("id = ?", sso_entity.SingletonID).First(ret).Error; err != nil {
		if db.RecordNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return ret, nil
}

func (r *ssoRepo) Save(ctx context.Context, s *sso_entity.SSO) error {
	s.ID = sso_entity.SingletonID
	return db.Ctx(ctx).Clauses(clause.OnConflict{UpdateAll: true}).Create(s).Error
}

func (r *ssoRepo) Delete(ctx context.Context) error {
	return db.Ctx(ctx).Where("id = ?", sso_entity.SingletonID).Delete(&sso_entity.SSO{}).Error
}
