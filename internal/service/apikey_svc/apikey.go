// Package apikey_svc API 密钥的创建、列表与撤销
package apikey_svc

import (
	"context"
	"time"

	"github.com/cago-frame/cago/pkg/i18n"

	apikeyapi "github.com/opskat/opsnap/internal/api/apikey"
	"github.com/opskat/opsnap/internal/model/entity/apikey_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/repository/apikey_repo"
)

// APIKeySvc API 密钥服务
type APIKeySvc interface {
	// Create 创建密钥；完整密钥只在返回值里出现一次，库里只存哈希
	Create(ctx context.Context, req *apikeyapi.CreateRequest) (*apikeyapi.CreateResponse, error)
	List(ctx context.Context) (*apikeyapi.ListResponse, error)
	// Revoke 撤销密钥，立即生效
	Revoke(ctx context.Context, req *apikeyapi.RevokeRequest) (*apikeyapi.RevokeResponse, error)
}

type apiKeySvc struct{ now func() time.Time }

func newAPIKey(now func() time.Time) *apiKeySvc { return &apiKeySvc{now: now} }

var defaultAPIKey APIKeySvc = newAPIKey(time.Now)

// APIKey 取得 API 密钥服务
func APIKey() APIKeySvc { return defaultAPIKey }

// RegisterAPIKey 替换服务实现
func RegisterAPIKey(i APIKeySvc) { defaultAPIKey = i }

// NewAPIKey 创建使用指定时钟的服务
func NewAPIKey(now func() time.Time) APIKeySvc { return newAPIKey(now) }

func item(k *apikey_entity.APIKey) apikeyapi.Item {
	return apikeyapi.Item{
		ID: k.ID, Name: k.Name, Prefix: k.Prefix,
		LastUsedAt: k.LastUsedAt, CreatedAt: k.Createtime, ExpiresAt: k.ExpiresAt,
	}
}

func (s *apiKeySvc) Create(ctx context.Context, req *apikeyapi.CreateRequest) (*apikeyapi.CreateResponse, error) {
	now := s.now()
	var expiresAt int64
	switch req.ExpiresIn {
	case apikeyapi.Expires30Days:
		expiresAt = now.Add(30 * 24 * time.Hour).Unix()
	case apikeyapi.Expires90Days:
		expiresAt = now.Add(90 * 24 * time.Hour).Unix()
	}
	full, prefix, hash, err := apikey_entity.Generate()
	if err != nil {
		return nil, err
	}
	k := &apikey_entity.APIKey{
		Name: req.Name, Prefix: prefix, KeyHash: hash, ExpiresAt: expiresAt, Createtime: now.Unix(),
	}
	if err := apikey_repo.APIKey().Create(ctx, k); err != nil {
		return nil, err
	}
	return &apikeyapi.CreateResponse{Item: item(k), Key: full}, nil
}

func (s *apiKeySvc) List(ctx context.Context) (*apikeyapi.ListResponse, error) {
	keys, err := apikey_repo.APIKey().List(ctx)
	if err != nil {
		return nil, err
	}
	resp := &apikeyapi.ListResponse{List: make([]*apikeyapi.Item, 0, len(keys))}
	for _, k := range keys {
		it := item(k)
		resp.List = append(resp.List, &it)
	}
	return resp, nil
}

func (s *apiKeySvc) Revoke(ctx context.Context, req *apikeyapi.RevokeRequest) (*apikeyapi.RevokeResponse, error) {
	ok, err := apikey_repo.APIKey().Revoke(ctx, req.ID, s.now().Unix())
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, i18n.NewNotFoundError(ctx, code.APIKeyNotFound)
	}
	return &apikeyapi.RevokeResponse{}, nil
}
