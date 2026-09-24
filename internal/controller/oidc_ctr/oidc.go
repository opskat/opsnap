// Package oidc_ctr 暴露 OIDC 登录方式接口。发起登录、绑定与回调以浏览器跳转响应。
package oidc_ctr

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/gin-gonic/gin"

	api "github.com/opskat/opsnap/internal/api/oidc"
	"github.com/opskat/opsnap/internal/middleware"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/service/auth_svc"
	"github.com/opskat/opsnap/internal/service/oidc_svc"
)

type OIDC struct{}

func NewOIDC() *OIDC {
	return &OIDC{}
}

// GetConfig 读取配置与绑定状态
func (o *OIDC) GetConfig(ctx context.Context, req *api.GetConfigRequest) (*api.ConfigResponse, error) {
	return oidc_svc.OIDC().GetConfig(ctx, req)
}

// SaveConfig 保存配置
func (o *OIDC) SaveConfig(ctx context.Context, req *api.SaveConfigRequest) (*api.ConfigResponse, error) {
	return oidc_svc.OIDC().SaveConfig(ctx, req)
}

// Unbind 解除绑定
func (o *OIDC) Unbind(ctx context.Context, req *api.UnbindRequest) (*api.UnbindResponse, error) {
	return oidc_svc.OIDC().Unbind(ctx, req)
}

// SetPasswordLogin 开启或关闭密码登录
func (o *OIDC) SetPasswordLogin(ctx context.Context, req *api.SetPasswordLoginRequest) (*api.SetPasswordLoginResponse, error) {
	return oidc_svc.OIDC().SetPasswordLogin(ctx, req)
}

// beginErrorKind 发起阶段的错误归到前端能显示的类别
func beginErrorKind(err error) string {
	var e *httputils.Error
	if !errors.As(err, &e) {
		return oidc_svc.ErrInvalid
	}
	switch e.Code {
	case code.OIDCIssuerUnreachable, code.OIDCDiscoveryInvalid:
		return oidc_svc.ErrUnreachable
	case code.OIDCAlreadyBound:
		return oidc_svc.ErrAlreadyBound
	}
	return oidc_svc.ErrInvalid
}

func redirectWithError(c *gin.Context, page, kind, desc string) {
	q := url.Values{"oidc_error": {kind}}
	if desc != "" {
		q.Set("oidc_error_description", desc)
	}
	c.Redirect(http.StatusFound, page+"?"+q.Encode())
}

// Bind 发起绑定（浏览器跳转）
func (o *OIDC) Bind(c *gin.Context, _ *api.BindRequest) error {
	authURL, err := oidc_svc.OIDC().BeginBind(c.Request.Context())
	if err != nil {
		redirectWithError(c, "/settings", beginErrorKind(err), "")
		return nil
	}
	c.Redirect(http.StatusFound, authURL)
	return nil
}

// Login 发起 OIDC 登录（公开，浏览器跳转）
func (o *OIDC) Login(c *gin.Context, req *api.LoginRequest) error {
	authURL, err := oidc_svc.OIDC().BeginLogin(c.Request.Context(), req.Next)
	if err != nil {
		redirectWithError(c, "/login", beginErrorKind(err), "")
		return nil
	}
	c.Redirect(http.StatusFound, authURL)
	return nil
}

// Callback IdP 回调（公开，浏览器跳转）：登录成功写入会话 Cookie 并回到原页面；绑定结果回到设置页
func (o *OIDC) Callback(c *gin.Context, req *api.CallbackRequest) error {
	res := oidc_svc.OIDC().Callback(c.Request.Context(), req,
		auth_svc.ClientMeta{IP: c.ClientIP(), UserAgent: c.Request.UserAgent()})
	page := "/login"
	if res.Mode == oidc_svc.ModeBind {
		page = "/settings"
	}
	if res.ErrorKind != "" {
		redirectWithError(c, page, res.ErrorKind, res.ErrorDescription)
		return nil
	}
	if res.Mode == oidc_svc.ModeBind {
		c.Redirect(http.StatusFound, "/settings?oidc=bound")
		return nil
	}
	middleware.SetSessionCookie(c, res.Session.Token, res.Session.Expires)
	c.Redirect(http.StatusFound, res.Next)
	return nil
}
