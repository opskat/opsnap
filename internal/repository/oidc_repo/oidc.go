// Package oidc_repo 读写 OIDC 提供方配置与绑定身份（以 JSON 保存在 settings 表中）。
package oidc_repo

import (
	"context"
	"encoding/json"

	"github.com/opskat/opsnap/internal/model/entity/oidc_entity"
	"github.com/opskat/opsnap/internal/repository/setting_repo"
)

//go:generate mockgen -source oidc.go -destination mock/oidc.go

const (
	providerKey = "oidc_provider"
	bindingKey  = "oidc_binding"
)

type OIDCRepo interface {
	// GetProvider 未配置时返回 nil, nil
	GetProvider(ctx context.Context) (*oidc_entity.Provider, error)
	SaveProvider(ctx context.Context, p *oidc_entity.Provider) error
	// GetBinding 未绑定时返回 nil, nil
	GetBinding(ctx context.Context) (*oidc_entity.Binding, error)
	SaveBinding(ctx context.Context, b *oidc_entity.Binding) error
	DeleteBinding(ctx context.Context) error
}

var defaultOIDC OIDCRepo

func OIDC() OIDCRepo {
	return defaultOIDC
}

func RegisterOIDC(i OIDCRepo) {
	defaultOIDC = i
}

// oidcRepo 复用 settings 表的读写实现，只负责 JSON 编解码
type oidcRepo struct {
	settings setting_repo.SettingRepo
}

func NewOIDC() OIDCRepo {
	return &oidcRepo{settings: setting_repo.NewSetting()}
}

func get[T any](ctx context.Context, r *oidcRepo, key string) (*T, error) {
	raw, ok, err := r.settings.Get(ctx, key)
	if err != nil || !ok {
		return nil, err
	}
	v := new(T)
	if err := json.Unmarshal([]byte(raw), v); err != nil {
		return nil, err
	}
	return v, nil
}

func (r *oidcRepo) put(ctx context.Context, key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return r.settings.Set(ctx, key, string(b))
}

func (r *oidcRepo) GetProvider(ctx context.Context) (*oidc_entity.Provider, error) {
	return get[oidc_entity.Provider](ctx, r, providerKey)
}

func (r *oidcRepo) SaveProvider(ctx context.Context, p *oidc_entity.Provider) error {
	return r.put(ctx, providerKey, p)
}

func (r *oidcRepo) GetBinding(ctx context.Context) (*oidc_entity.Binding, error) {
	return get[oidc_entity.Binding](ctx, r, bindingKey)
}

func (r *oidcRepo) SaveBinding(ctx context.Context, b *oidc_entity.Binding) error {
	return r.put(ctx, bindingKey, b)
}

func (r *oidcRepo) DeleteBinding(ctx context.Context) error {
	return r.settings.Delete(ctx, bindingKey)
}
