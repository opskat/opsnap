// Package sso_ctr 单点登录设置接口控制器
package sso_ctr

import (
	"context"

	ssoapi "github.com/opskat/opsnap/internal/api/sso"
	"github.com/opskat/opsnap/internal/service/sso_svc"
)

type SSO struct{}

func NewSSO() *SSO { return &SSO{} }

func (s *SSO) Get(ctx context.Context, req *ssoapi.GetRequest) (*ssoapi.GetResponse, error) {
	return sso_svc.SSO().View(ctx)
}

func (s *SSO) Update(ctx context.Context, req *ssoapi.UpdateRequest) (*ssoapi.UpdateResponse, error) {
	return sso_svc.SSO().Update(ctx, req)
}

// Disable 停用单点登录
func (s *SSO) Disable(ctx context.Context, req *ssoapi.DisableRequest) (*ssoapi.DisableResponse, error) {
	return sso_svc.SSO().Disable(ctx)
}

// Unbind 解除绑定
func (s *SSO) Unbind(ctx context.Context, req *ssoapi.UnbindRequest) (*ssoapi.UnbindResponse, error) {
	return sso_svc.SSO().Unbind(ctx)
}

// PasswordLogin 打开或关闭「允许用户名密码登录」
func (s *SSO) PasswordLogin(ctx context.Context, req *ssoapi.PasswordLoginRequest) (*ssoapi.PasswordLoginResponse, error) {
	return sso_svc.SSO().SetPasswordLogin(ctx, req)
}
