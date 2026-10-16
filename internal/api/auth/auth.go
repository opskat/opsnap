// Package auth 管理员认证相关接口
package auth

import "github.com/cago-frame/cago/server/mux"

// StatusRequest 查询是否已创建管理员（未登录可访问）
type StatusRequest struct {
	mux.Meta `path:"/auth/status" method:"GET"`
}

type StatusResponse struct {
	Initialized bool `json:"initialized"` // 是否已创建管理员
}

// SetupRequest 首次运行时创建管理员（未登录可访问，仅在尚无管理员时可用）
type SetupRequest struct {
	mux.Meta `path:"/auth/setup" method:"POST"`
	Username string `json:"username" form:"username" binding:"required,max=64" label:"用户名"`
	Password string `json:"password" form:"password" binding:"required,max=128" label:"密码"`
}

type SetupResponse struct{}

// LoginRequest 登录；成功后通过 HttpOnly Cookie 下发会话
type LoginRequest struct {
	mux.Meta `path:"/auth/login" method:"POST"`
	Username string `json:"username" form:"username" binding:"required,max=64" label:"用户名"`
	Password string `json:"password" form:"password" binding:"required,max=128" label:"密码"`
}

type LoginResponse struct {
	Username string `json:"username"`
}

// LogoutRequest 退出登录，使当前会话失效
type LogoutRequest struct {
	mux.Meta `path:"/auth/logout" method:"POST"`
}

type LogoutResponse struct{}

// MeRequest 当前身份
type MeRequest struct {
	mux.Meta `path:"/auth/me" method:"GET"`
}

type MeResponse struct {
	Kind     string `json:"kind"`               // admin 或 api_key
	Username string `json:"username,omitempty"` // 管理员用户名
	KeyID    int64  `json:"key_id,omitempty"`   // API 密钥 id
	KeyName  string `json:"key_name,omitempty"` // API 密钥名称
}

// ChangePasswordRequest 修改管理员密码；其他会话随之失效
type ChangePasswordRequest struct {
	mux.Meta    `path:"/auth/password" method:"PUT"`
	OldPassword string `json:"old_password" form:"old_password" binding:"required,max=128" label:"原密码"`
	NewPassword string `json:"new_password" form:"new_password" binding:"required,max=128" label:"新密码"`
}

type ChangePasswordResponse struct{}
