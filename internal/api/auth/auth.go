// Package auth 定义首次设置、登录与会话相关接口的请求与响应。
package auth

import "github.com/cago-frame/cago/server/mux"

// StatusRequest 公开接口：前端据此决定显示首次设置页还是登录页
type StatusRequest struct {
	mux.Meta `path:"/auth/status" method:"GET"`
}

type StatusResponse struct {
	// Initialized 是否已创建管理员
	Initialized bool `json:"initialized"`
	// PasswordLogin 是否开启密码登录
	PasswordLogin bool `json:"password_login"`
	// OIDCLogin 已配置并绑定 OIDC 时可用的登录方式；否则为空
	OIDCLogin *OIDCLogin `json:"oidc_login"`
}

type OIDCLogin struct {
	DisplayName string `json:"display_name"`
}

// SetupRequest 公开接口：尚未创建管理员时，凭启动日志中的设置码创建唯一管理员，成功后直接登录
type SetupRequest struct {
	mux.Meta  `path:"/auth/setup" method:"POST"`
	SetupCode string `json:"setup_code" binding:"required"`
	Username  string `json:"username" binding:"required"`
	Password  string `json:"password" binding:"required"`
}

type SetupResponse struct {
	Username string `json:"username"`
}

// LoginRequest 公开接口：密码登录，成功后写入会话 Cookie
type LoginRequest struct {
	mux.Meta `path:"/auth/login" method:"POST"`
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type LoginResponse struct {
	Username string `json:"username"`
}

// LogoutRequest 退出登录，当前会话立即失效
type LogoutRequest struct {
	mux.Meta `path:"/auth/logout" method:"POST"`
}

type LogoutResponse struct{}

// MeRequest 当前登录的管理员与会话
type MeRequest struct {
	mux.Meta `path:"/auth/me" method:"GET"`
}

type MeResponse struct {
	Username string `json:"username"`
	// PasswordUpdatedAt 密码最后修改时间（Unix 秒）
	PasswordUpdatedAt int64 `json:"password_updated_at"`
	// Session 当前浏览器会话；使用 API 令牌访问时为空
	Session *SessionInfo `json:"session,omitempty"`
}

type SessionInfo struct {
	UserAgent string `json:"user_agent"`
	IP        string `json:"ip"`
	// ExpiresAt 过期时间（Unix 秒）；每次使用会顺延
	ExpiresAt int64 `json:"expires_at"`
}

// ChangePasswordRequest 修改密码：成功后除当前浏览器外的会话全部失效。只能通过浏览器会话调用
type ChangePasswordRequest struct {
	mux.Meta        `path:"/auth/password" method:"POST"`
	CurrentPassword string `json:"current_password" binding:"required"`
	NewPassword     string `json:"new_password" binding:"required"`
}

type ChangePasswordResponse struct{}
