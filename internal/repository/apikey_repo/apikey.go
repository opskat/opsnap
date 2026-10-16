// Package apikey_repo API 密钥的数据访问
package apikey_repo

import (
	"context"

	"github.com/cago-frame/cago/database/db"

	"github.com/opskat/opsnap/internal/model/entity/apikey_entity"
)

// APIKeyRepo API 密钥仓储
//
//go:generate mockgen -source apikey.go -destination mock_apikey_repo/apikey.go -package mock_apikey_repo
type APIKeyRepo interface {
	Create(ctx context.Context, k *apikey_entity.APIKey) error
	// List 列出未撤销的密钥，新的在前
	List(ctx context.Context) ([]*apikey_entity.APIKey, error)
	// FindByHash 按密钥哈希查找（含已撤销），不存在返回 nil
	FindByHash(ctx context.Context, hash string) (*apikey_entity.APIKey, error)
	// Revoke 撤销尚未撤销的密钥；找不到或已撤销时返回 false
	Revoke(ctx context.Context, id, now int64) (bool, error)
	TouchLastUsed(ctx context.Context, id, now int64) error
}

var defaultAPIKey APIKeyRepo

// APIKey 取得已注册的仓储
func APIKey() APIKeyRepo { return defaultAPIKey }

// RegisterAPIKey 注册仓储实现
func RegisterAPIKey(i APIKeyRepo) { defaultAPIKey = i }

type apiKeyRepo struct{}

// NewAPIKey 创建基于 GORM 的实现
func NewAPIKey() APIKeyRepo { return &apiKeyRepo{} }

func (r *apiKeyRepo) Create(ctx context.Context, k *apikey_entity.APIKey) error {
	return db.Ctx(ctx).Create(k).Error
}

func (r *apiKeyRepo) List(ctx context.Context) ([]*apikey_entity.APIKey, error) {
	var list []*apikey_entity.APIKey
	if err := db.Ctx(ctx).Where("revoked_at = 0").Order("id DESC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (r *apiKeyRepo) FindByHash(ctx context.Context, hash string) (*apikey_entity.APIKey, error) {
	ret := &apikey_entity.APIKey{}
	if err := db.Ctx(ctx).Where("key_hash = ?", hash).First(ret).Error; err != nil {
		if db.RecordNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return ret, nil
}

func (r *apiKeyRepo) Revoke(ctx context.Context, id, now int64) (bool, error) {
	res := db.Ctx(ctx).Model(&apikey_entity.APIKey{}).
		Where("id = ? AND revoked_at = 0", id).Update("revoked_at", now)
	return res.RowsAffected > 0, res.Error
}

func (r *apiKeyRepo) TouchLastUsed(ctx context.Context, id, now int64) error {
	return db.Ctx(ctx).Model(&apikey_entity.APIKey{}).Where("id = ?", id).Update("last_used_at", now).Error
}
