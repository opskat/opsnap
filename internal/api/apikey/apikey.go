// Package apikey API 密钥管理接口
package apikey

import "github.com/cago-frame/cago/server/mux"

// 过期时间选项
const (
	Expires30Days = "30d"
	Expires90Days = "90d"
	ExpiresNever  = "never"
)

// Item API 密钥列表项；不含完整密钥和哈希
type Item struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Prefix     string `json:"prefix"`       // 密钥前几位，用于识别
	LastUsedAt int64  `json:"last_used_at"` // Unix 秒，0 表示从未使用
	CreatedAt  int64  `json:"created_at"`
	ExpiresAt  int64  `json:"expires_at"` // Unix 秒，0 表示永不过期
}

// CreateRequest 创建 API 密钥。密钥不区分权限，等同管理员。
type CreateRequest struct {
	mux.Meta  `path:"/apikeys" method:"POST"`
	Name      string `json:"name" form:"name" binding:"required,max=64" label:"名称"`
	ExpiresIn string `json:"expires_in" form:"expires_in" binding:"required,oneof=30d 90d never" label:"过期时间"`
}

type CreateResponse struct {
	Item
	Key string `json:"key"` // 完整密钥，仅此一次返回
}

// ListRequest 列出未撤销的 API 密钥
type ListRequest struct {
	mux.Meta `path:"/apikeys" method:"GET"`
}

type ListResponse struct {
	List []*Item `json:"list"`
}

// RevokeRequest 撤销 API 密钥，立即生效
type RevokeRequest struct {
	mux.Meta `path:"/apikeys/:id" method:"DELETE"`
	ID       int64 `uri:"id" binding:"required"`
}

type RevokeResponse struct{}
