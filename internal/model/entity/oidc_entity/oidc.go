// Package oidc_entity 定义 OIDC 提供方配置与绑定身份。两者都以 JSON 保存在 settings 表中。
package oidc_entity

// Provider OIDC 提供方配置；ClientSecret 为主密钥加密后的密文
type Provider struct {
	DisplayName  string   `json:"display_name"`
	Issuer       string   `json:"issuer"`
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret"`
	Scopes       []string `json:"scopes"`
	RedirectURL  string   `json:"redirect_url"`
}

// Binding 绑定到管理员的 OIDC 身份（issuer + subject）
type Binding struct {
	Issuer            string `json:"issuer"`
	Subject           string `json:"subject"`
	Email             string `json:"email"`
	PreferredUsername string `json:"preferred_username"`
	BoundAt           int64  `json:"bound_at"`
	// LastLoginAt 最近一次通过 OIDC 登录成功的时间；0 表示从未登录过
	LastLoginAt int64 `json:"last_login_at"`
}

// Matches 回调中的身份是否就是绑定的身份
func (b *Binding) Matches(issuer, subject string) bool {
	return b != nil && b.Issuer == issuer && b.Subject == subject
}
