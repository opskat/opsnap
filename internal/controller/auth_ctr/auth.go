// Package auth_ctr 认证接口控制器：只做转发，Cookie 的读写放在这一层
package auth_ctr

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	authapi "github.com/opskat/opsnap/internal/api/auth"
	"github.com/opskat/opsnap/internal/service/auth_svc"
	"github.com/opskat/opsnap/internal/service/sso_svc"
)

type Auth struct{}

func NewAuth() *Auth { return &Auth{} }

func (a *Auth) Status(ctx context.Context, req *authapi.StatusRequest) (*authapi.StatusResponse, error) {
	return auth_svc.Auth().Status(ctx)
}

func (a *Auth) Setup(ctx context.Context, req *authapi.SetupRequest) (*authapi.SetupResponse, error) {
	return auth_svc.Auth().Setup(ctx, req)
}

// Login 登录成功后把会话令牌写入 HttpOnly Cookie，响应体里不含令牌
func (a *Auth) Login(c *gin.Context, req *authapi.LoginRequest) (*authapi.LoginResponse, error) {
	res, err := auth_svc.Auth().Login(c.Request.Context(), req)
	if err != nil {
		return nil, err
	}
	setSessionCookie(c, res.Token, int(time.Until(res.ExpiresAt).Seconds()))
	return &authapi.LoginResponse{Username: res.Username}, nil
}

func (a *Auth) Logout(c *gin.Context, req *authapi.LogoutRequest) (*authapi.LogoutResponse, error) {
	token, _ := c.Cookie(auth_svc.SessionCookie)
	if err := auth_svc.Auth().Logout(c.Request.Context(), token); err != nil {
		return nil, err
	}
	setSessionCookie(c, "", -1)
	return &authapi.LogoutResponse{}, nil
}

func (a *Auth) Me(ctx context.Context, req *authapi.MeRequest) (*authapi.MeResponse, error) {
	return auth_svc.Auth().Me(ctx)
}

func (a *Auth) ChangePassword(c *gin.Context, req *authapi.ChangePasswordRequest) (*authapi.ChangePasswordResponse, error) {
	token, _ := c.Cookie(auth_svc.SessionCookie)
	return auth_svc.Auth().ChangePassword(c.Request.Context(), req, token)
}

// setSessionCookie 写会话 Cookie：HttpOnly、SameSite=Lax；经 HTTPS（含反向代理转发）访问时加 Secure
func setSessionCookie(c *gin.Context, value string, maxAge int) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(auth_svc.SessionCookie, value, maxAge, "/", "", viaHTTPS(c), true)
}

// viaHTTPS 浏览器是否经 HTTPS 访问（含反向代理转发）
func viaHTTPS(c *gin.Context) bool {
	return c.Request.TLS != nil || strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https")
}

// OIDCCookie 单点登录进行中时写给浏览器的 Cookie：回调时据此确认是同一个浏览器发起的
const OIDCCookie = "opsnap_oidc"

// oidcCookiePath 只在回调时带上
const oidcCookiePath = "/api/v1/auth/oidc"

// oidcCookieMaxAge 与服务端保留进行中请求的时长一致
const oidcCookieMaxAge = int(sso_svc.PendingTTL / time.Second)

// OIDCLogin 开始单点登录：记下浏览器，返回提供方授权页地址
func (a *Auth) OIDCLogin(c *gin.Context, req *authapi.OIDCLoginRequest) (*authapi.OIDCLoginResponse, error) {
	st, err := sso_svc.SSO().StartLogin(c.Request.Context(), req.Next)
	if err != nil {
		return nil, err
	}
	setOIDCCookie(c, st.Browser, oidcCookieMaxAge)
	return &authapi.OIDCLoginResponse{URL: st.URL}, nil
}

// OIDCBind 开始绑定提供方账号（需要面板会话）
func (a *Auth) OIDCBind(c *gin.Context, req *authapi.OIDCBindRequest) (*authapi.OIDCBindResponse, error) {
	st, err := sso_svc.SSO().StartBind(c.Request.Context())
	if err != nil {
		return nil, err
	}
	setOIDCCookie(c, st.Browser, oidcCookieMaxAge)
	return &authapi.OIDCBindResponse{URL: st.URL}, nil
}

// OIDCCallback 提供方授权后回到这里：交给服务校验，成功时写会话 Cookie，然后跳转
func (a *Auth) OIDCCallback(c *gin.Context, req *authapi.OIDCCallbackRequest) error {
	browser, _ := c.Cookie(OIDCCookie)
	session, _ := c.Cookie(auth_svc.SessionCookie)
	res := sso_svc.SSO().Callback(c.Request.Context(), browser, session, req)
	setOIDCCookie(c, "", -1)
	if res.Session != nil {
		setSessionCookie(c, res.Session.Token, int(time.Until(res.Session.ExpiresAt).Seconds()))
	}
	c.Header("Cache-Control", "no-store")
	c.Redirect(http.StatusFound, res.Redirect)
	return nil
}

// OIDCResult 读取单点登录失败的原因
func (a *Auth) OIDCResult(ctx context.Context, req *authapi.OIDCResultRequest) (*authapi.OIDCResultResponse, error) {
	return &authapi.OIDCResultResponse{Message: sso_svc.SSO().Result(req.ID)}, nil
}

func setOIDCCookie(c *gin.Context, value string, maxAge int) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(OIDCCookie, value, maxAge, oidcCookiePath, "", viaHTTPS(c), true)
}
