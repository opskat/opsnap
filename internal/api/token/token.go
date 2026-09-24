// Package token 定义 API 令牌管理接口的请求与响应。令牌管理只能通过浏览器会话调用。
package token

import "github.com/cago-frame/cago/server/mux"

// ListRequest 列出全部令牌（含已过期、已吊销）
type ListRequest struct {
	mux.Meta `path:"/tokens" method:"GET"`
}

type ListResponse struct {
	Items []*Item `json:"items"`
}

type Item struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Prefix string `json:"prefix"`
	// Status active / expired / revoked
	Status    string `json:"status"`
	CreatedAt int64  `json:"created_at"`
	// LastUsedAt 从未使用时为 0
	LastUsedAt int64 `json:"last_used_at"`
	// ExpiresAt 永不过期时为 0
	ExpiresAt int64 `json:"expires_at"`
	RevokedAt int64 `json:"revoked_at"`
}

// CreateRequest 生成令牌；完整令牌只在响应中出现这一次
type CreateRequest struct {
	mux.Meta `path:"/tokens" method:"POST"`
	Name     string `json:"name" binding:"required"`
	// ExpiresInDays 30 / 90 / 365，0 表示永不过期
	ExpiresInDays int `json:"expires_in_days"`
}

type CreateResponse struct {
	Item  *Item  `json:"item"`
	Token string `json:"token"`
}

// RevokeRequest 吊销令牌，立即生效
type RevokeRequest struct {
	mux.Meta `path:"/tokens/:id/revoke" method:"POST"`
	ID       int64 `uri:"id" binding:"required"`
}

type RevokeResponse struct{}
