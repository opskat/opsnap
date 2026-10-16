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
	secure := c.Request.TLS != nil || strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https")
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(auth_svc.SessionCookie, value, maxAge, "/", "", secure, true)
}
