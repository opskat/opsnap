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
}

// UpdateRequest 保存提供方配置：先读取 <Issuer>/.well-known/openid-configuration 检查，通过才保存。
// client_secret 为空或为「已设置」时保留已保存的值。
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

// DisableRequest 停用单点登录：清空提供方配置
type DisableRequest struct {
	mux.Meta `path:"/settings/sso" method:"DELETE"`
}

type DisableResponse struct {
	GetResponse
}
