// Package token_ctr 暴露 API 令牌管理接口。
package token_ctr

import (
	"context"

	api "github.com/opskat/opsnap/internal/api/token"
	"github.com/opskat/opsnap/internal/service/token_svc"
)

type Token struct{}

func NewToken() *Token {
	return &Token{}
}

// List 令牌列表
func (t *Token) List(ctx context.Context, req *api.ListRequest) (*api.ListResponse, error) {
	return token_svc.Token().List(ctx, req)
}

// Create 生成令牌
func (t *Token) Create(ctx context.Context, req *api.CreateRequest) (*api.CreateResponse, error) {
	return token_svc.Token().Create(ctx, req)
}

// Revoke 吊销令牌
func (t *Token) Revoke(ctx context.Context, req *api.RevokeRequest) (*api.RevokeResponse, error) {
	return token_svc.Token().Revoke(ctx, req)
}
