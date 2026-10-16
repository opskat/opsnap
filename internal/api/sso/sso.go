// Package sso 单点登录（OIDC）设置接口
package sso

import "github.com/cago-frame/cago/server/mux"

// GetRequest 读取单点登录设置
type GetRequest struct {
	mux.Meta `path:"/settings/sso" method:"GET"`
}

type GetResponse struct {
	// Configured 是否已保存提供方配置
	Configured bool   `json:"configured"`
	Issuer     string `json:"issuer"`
	ClientID   string `json:"client_id"`
	// ClientSecret 已保存时为「已设置」，否则为空；不返回内容
	ClientSecret string `json:"client_secret"`
	ButtonLabel  string `json:"button_label"`
	PanelURL     string `json:"panel_url"`
	// CallbackURL 要填到提供方 OAuth2 应用里的重定向 URI，由面板对外地址拼成
	CallbackURL string `json:"callback_url"`
	// Binding 绑定的提供方账号；未绑定时为 null
	Binding *Binding `json:"binding"`
	// PasswordLogin 设置里保存的「允许用户名密码登录」
	PasswordLogin bool `json:"password_login"`
	// PasswordLoginForced 环境变量 OPSNAP_PASSWORD_LOGIN 正在强制打开密码登录
	PasswordLoginForced bool `json:"password_login_forced"`
}

// Binding 绑定的提供方账号：只认 issuer + sub，Name 只用于展示
type Binding struct {
	Issuer  string `json:"issuer"`
	Sub     string `json:"sub"`
	Name    string `json:"name"`
	BoundAt int64  `json:"bound_at"` // Unix 秒
}

// UpdateRequest 保存提供方配置：先读取 <Issuer>/.well-known/openid-configuration 检查，通过才保存。
// client_secret 为空或为「已设置」时保留已保存的值。修改 issuer 或 client_id 会解除已有的绑定。
type UpdateRequest struct {
	mux.Meta     `path:"/settings/sso" method:"PUT"`
	Issuer       string `json:"issuer" form:"issuer" binding:"max=512" label:"Issuer 地址"`
	ClientID     string `json:"client_id" form:"client_id" binding:"max=512" label:"Client ID"`
	ClientSecret string `json:"client_secret" form:"client_secret" binding:"max=1024" label:"Client Secret"`
	ButtonLabel  string `json:"button_label" form:"button_label" label:"按钮文字"`
	PanelURL     string `json:"panel_url" form:"panel_url" binding:"max=512" label:"面板对外地址"`
}

type UpdateResponse struct {
	GetResponse
}

// PasswordLoginRequest 打开或关闭「允许用户名密码登录」。没有绑定账号时不能关闭。
// 关闭期间不能修改 Issuer、Client ID，也不能解除绑定或停用；任何解除绑定的操作都会把它改回打开。
type PasswordLoginRequest struct {
	mux.Meta `path:"/settings/sso/password-login" method:"PUT"`
	Enabled  bool `json:"enabled" form:"enabled"`
}

type PasswordLoginResponse struct {
	GetResponse
}

// UnbindRequest 解除绑定
type UnbindRequest struct {
	mux.Meta `path:"/settings/sso/binding" method:"DELETE"`
}

type UnbindResponse struct {
	GetResponse
}

// DisableRequest 停用单点登录：清空提供方配置并解除绑定
type DisableRequest struct {
	mux.Meta `path:"/settings/sso" method:"DELETE"`
}

type DisableResponse struct {
	GetResponse
}
