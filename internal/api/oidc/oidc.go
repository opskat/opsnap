// Package oidc 定义 OIDC 登录方式相关接口。配置、绑定、解除绑定只能通过浏览器会话调用；
// 发起登录与回调是公开接口，以浏览器跳转而不是 JSON 响应。
package oidc

import "github.com/cago-frame/cago/server/mux"

// GetConfigRequest 读取 OIDC 配置与绑定状态；Client Secret 不会返回
type GetConfigRequest struct {
	mux.Meta `path:"/auth/oidc/config" method:"GET"`
}

type ConfigResponse struct {
	Configured  bool     `json:"configured"`
	DisplayName string   `json:"display_name"`
	Issuer      string   `json:"issuer"`
	ClientID    string   `json:"client_id"`
	HasSecret   bool     `json:"has_secret"`
	Scopes      []string `json:"scopes"`
	RedirectURL string   `json:"redirect_url"`
	Binding     *Binding `json:"binding"`
	// PasswordLogin 是否开启密码登录
	PasswordLogin bool `json:"password_login"`
	// CanDisablePasswordLogin 已绑定且通过 OIDC 登录过时才允许关闭密码登录
	CanDisablePasswordLogin bool `json:"can_disable_password_login"`
}

// SetPasswordLoginRequest 开启或关闭密码登录
type SetPasswordLoginRequest struct {
	mux.Meta `path:"/auth/password-login" method:"PUT"`
	Enabled  bool `json:"enabled"`
}

type SetPasswordLoginResponse struct {
	PasswordLogin bool `json:"password_login"`
}

type Binding struct {
	Subject string `json:"subject"`
	// Display 邮箱，没有时为 preferred_username，都没有时为空
	Display     string `json:"display"`
	BoundAt     int64  `json:"bound_at"`
	LastLoginAt int64  `json:"last_login_at"`
}

// SaveConfigRequest 保存 OIDC 配置；保存前拉取 discovery 文档校验
type SaveConfigRequest struct {
	mux.Meta    `path:"/auth/oidc/config" method:"PUT"`
	DisplayName string `json:"display_name" binding:"required"`
	Issuer      string `json:"issuer" binding:"required"`
	ClientID    string `json:"client_id" binding:"required"`
	// ClientSecret 留空表示不修改（首次配置时必填）
	ClientSecret string   `json:"client_secret"`
	Scopes       []string `json:"scopes"`
	// RedirectURL 由前端按当前访问地址生成
	RedirectURL string `json:"redirect_url" binding:"required"`
	// ConfirmReset 修改 Issuer 或 Client ID 会清除已有绑定，需要显式确认
	ConfirmReset bool `json:"confirm_reset"`
}

// UnbindRequest 解除绑定
type UnbindRequest struct {
	mux.Meta `path:"/auth/oidc/unbind" method:"POST"`
}

type UnbindResponse struct{}

// BindRequest 浏览器跳转：发起绑定，登录 IdP 后回到回调地址
type BindRequest struct {
	mux.Meta `path:"/auth/oidc/bind" method:"GET"`
}

// LoginRequest 浏览器跳转（公开）：发起 OIDC 登录
type LoginRequest struct {
	mux.Meta `path:"/auth/oidc/login" method:"GET"`
	Next     string `form:"next"`
}

// CallbackRequest 浏览器跳转（公开）：IdP 回调
type CallbackRequest struct {
	mux.Meta         `path:"/auth/oidc/callback" method:"GET"`
	State            string `form:"state"`
	Code             string `form:"code"`
	Error            string `form:"error"`
	ErrorDescription string `form:"error_description"`
}
