// Package apikey_ctr API 密钥接口控制器
package apikey_ctr

import (
	"context"

	apikeyapi "github.com/opskat/opsnap/internal/api/apikey"
	"github.com/opskat/opsnap/internal/service/apikey_svc"
)

type APIKey struct{}

func NewAPIKey() *APIKey { return &APIKey{} }

func (a *APIKey) Create(ctx context.Context, req *apikeyapi.CreateRequest) (*apikeyapi.CreateResponse, error) {
	return apikey_svc.APIKey().Create(ctx, req)
}

func (a *APIKey) List(ctx context.Context, req *apikeyapi.ListRequest) (*apikeyapi.ListResponse, error) {
	return apikey_svc.APIKey().List(ctx)
}

func (a *APIKey) Revoke(ctx context.Context, req *apikeyapi.RevokeRequest) (*apikeyapi.RevokeResponse, error) {
	return apikey_svc.APIKey().Revoke(ctx, req)
}
