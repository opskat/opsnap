// Package sso_entity 单点登录（OIDC）：提供方配置、绑定的提供方账号、密码登录开关
package sso_entity

import (
	"context"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/opskat/opsnap/internal/pkg/code"
)

const (
	// SingletonID 单点登录配置只有一行
	SingletonID = 1
	// DefaultButtonLabel 登录按钮文字的默认值
	DefaultButtonLabel = "Gitea"
	// MaxButtonLabel 按钮文字最多的字符数
	MaxButtonLabel = 32
	// CallbackPath 回调地址在面板对外地址之后的路径
	CallbackPath = "/api/v1/auth/oidc/callback"
)

// SSO 单点登录配置。Issuer 为空表示未配置；BoundSub 为空表示未绑定。
type SSO struct {
	ID           int64  `gorm:"column:id;primaryKey;autoIncrement:false"`
	Issuer       string `gorm:"column:issuer;not null"`
	ClientID     string `gorm:"column:client_id;not null"`
	ClientSecret string `gorm:"column:client_secret;not null"` // 主密钥加密后的密文
	ButtonLabel  string `gorm:"column:button_label;not null"`
	PanelURL     string `gorm:"column:panel_url;not null"`
	BoundIssuer  string `gorm:"column:bound_issuer;not null"`
	BoundSub     string `gorm:"column:bound_sub;not null"`
	BoundName    string `gorm:"column:bound_name;not null"` // 绑定时的显示名，只用于展示
	BoundAt      int64  `gorm:"column:bound_at;not null"`
	// PasswordLogin 设置里保存的「允许用户名密码登录」
	PasswordLogin bool  `gorm:"column:password_login;not null"`
	Updatetime    int64 `gorm:"column:updatetime"`
}

func (SSO) TableName() string { return "sso" }

// Default 尚未保存过时的状态：未配置、未绑定、允许密码登录
func Default() *SSO {
	return &SSO{ID: SingletonID, PasswordLogin: true}
}

// Configured 是否已保存提供方配置
func (s *SSO) Configured() bool { return s != nil && s.Issuer != "" }

// Bound 是否已绑定提供方账号
func (s *SSO) Bound() bool { return s != nil && s.BoundSub != "" }

// ClearBinding 解除绑定，同时打开密码登录：密码登录关闭时一定存在绑定
func (s *SSO) ClearBinding() {
	s.BoundIssuer, s.BoundSub, s.BoundName, s.BoundAt = "", "", "", 0
	s.PasswordLogin = true
}

// PasswordLoginAllowed 是否允许用户名密码登录；forced 表示环境变量强制打开
func (s *SSO) PasswordLoginAllowed(forced bool) bool {
	return forced || s == nil || s.PasswordLogin
}

// Locked 密码登录关闭期间（且没有被环境变量强制打开）：绑定不能失效，Issuer、Client ID 不能改
func (s *SSO) Locked(forced bool) bool {
	return !s.PasswordLoginAllowed(forced)
}

// CallbackURL 由面板对外地址拼出的回调地址；未填写面板地址时为空
func (s *SSO) CallbackURL() string {
	if s == nil || s.PanelURL == "" {
		return ""
	}
	return strings.TrimRight(s.PanelURL, "/") + CallbackPath
}

func httpURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// ValidateProvider 校验提供方配置的各字段（不含 Client Secret，它由服务层按是否已保存判断）
func (s *SSO) ValidateProvider(ctx context.Context) error {
	if !httpURL(s.Issuer) {
		return i18n.NewError(ctx, code.SSOIssuerInvalid)
	}
	if s.ClientID == "" {
		return i18n.NewError(ctx, code.SSOClientIDRequired)
	}
	if n := utf8.RuneCountInString(s.ButtonLabel); n == 0 || n > MaxButtonLabel {
		return i18n.NewError(ctx, code.SSOButtonLabelInvalid, MaxButtonLabel)
	}
	if !httpURL(s.PanelURL) {
		return i18n.NewError(ctx, code.SSOPanelURLInvalid)
	}
	return nil
}
