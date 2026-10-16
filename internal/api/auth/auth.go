// Package auth 管理员认证相关接口
package auth

import "github.com/cago-frame/cago/server/mux"

// StatusRequest 查询是否已创建管理员（未登录可访问）
type StatusRequest struct {
	mux.Meta `path:"/auth/status" method:"GET"`
}

type StatusResponse struct {
	Initialized bool `json:"initialized"` // 是否已创建管理员
	// SSOEnabled 是否可以用单点登录（已配置并已绑定账号）
	SSOEnabled bool `json:"sso_enabled"`
	// SSOLabel 登录按钮文字，例如 Gitea
	SSOLabel string `json:"sso_label,omitempty"`
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

// OIDCLoginRequest 开始单点登录（未登录可访问）：返回提供方授权页地址。
// next 是登录后要回到的站内路径，不是站内路径时回到首页。
type OIDCLoginRequest struct {
	mux.Meta `path:"/auth/oidc/login" method:"POST"`
	Next     string `json:"next" form:"next" binding:"max=2048"`
}

type OIDCLoginResponse struct {
	URL string `json:"url"` // 提供方授权页
}

// OIDCBindRequest 开始绑定提供方账号：返回提供方授权页地址，浏览器跳过去授权，完成后回到「设置 → 单点登录」。
// 只能由已登录面板的管理员（会话）发起，API 密钥返回 403。
type OIDCBindRequest struct {
	mux.Meta `path:"/settings/sso/bind" method:"POST"`
}

type OIDCBindResponse struct {
	URL string `json:"url"` // 提供方授权页
}

// OIDCCallbackRequest 提供方授权后浏览器回到这里（未登录可访问）。
// 校验通过后建立会话并跳到登录前的页面（绑定时回到设置页）；不通过时跳回登录页，地址里带 sso_error。
type OIDCCallbackRequest struct {
	mux.Meta `path:"/auth/oidc/callback" method:"GET"`
	Code     string `form:"code"`
	State    string `form:"state"`
	Error    string `form:"error"`
}

// OIDCResultRequest 读取单点登录失败的原因（未登录可访问）；id 来自回调跳转地址里的 sso_error
type OIDCResultRequest struct {
	mux.Meta `path:"/auth/oidc/result" method:"GET"`
	ID       string `form:"id" binding:"max=64"`
}

type OIDCResultResponse struct {
	// Message 失败原因；id 不存在或已过期时为空
	Message string `json:"message"`
}
