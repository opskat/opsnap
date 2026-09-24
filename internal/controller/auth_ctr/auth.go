// Package auth_ctr 暴露首次设置、会话相关的 HTTP 接口。
package auth_ctr

import (
	"context"

	"github.com/gin-gonic/gin"

	api "github.com/opskat/opsnap/internal/api/auth"
	"github.com/opskat/opsnap/internal/middleware"
	"github.com/opskat/opsnap/internal/pkg/authctx"
	"github.com/opskat/opsnap/internal/service/auth_svc"
)

type Auth struct{}

func NewAuth() *Auth {
	return &Auth{}
}

func clientMeta(c *gin.Context) auth_svc.ClientMeta {
	return auth_svc.ClientMeta{IP: c.ClientIP(), UserAgent: c.Request.UserAgent()}
}

// Status 初始化状态（公开）
func (a *Auth) Status(ctx context.Context, req *api.StatusRequest) (*api.StatusResponse, error) {
	return auth_svc.Auth().Status(ctx, req)
}

// Setup 首次设置（公开），成功后写入会话 Cookie
func (a *Auth) Setup(c *gin.Context, req *api.SetupRequest) (*api.SetupResponse, error) {
	resp, issued, err := auth_svc.Auth().Setup(c.Request.Context(), req, clientMeta(c))
	if err != nil {
		return nil, err
	}
	middleware.SetSessionCookie(c, issued.Token, issued.Expires)
	return resp, nil
}

// Login 密码登录（公开），成功后写入会话 Cookie
func (a *Auth) Login(c *gin.Context, req *api.LoginRequest) (*api.LoginResponse, error) {
	resp, issued, err := auth_svc.Auth().Login(c.Request.Context(), req, clientMeta(c))
	if err != nil {
		return nil, err
	}
	middleware.SetSessionCookie(c, issued.Token, issued.Expires)
	return resp, nil
}

// Logout 退出登录
func (a *Auth) Logout(c *gin.Context, _ *api.LogoutRequest) (*api.LogoutResponse, error) {
	ctx := c.Request.Context()
	if err := auth_svc.Auth().Logout(ctx, authctx.From(ctx)); err != nil {
		return nil, err
	}
	middleware.ClearSessionCookie(c)
	return &api.LogoutResponse{}, nil
}

// ChangePassword 修改密码
func (a *Auth) ChangePassword(c *gin.Context, req *api.ChangePasswordRequest) (*api.ChangePasswordResponse, error) {
	return auth_svc.Auth().ChangePassword(c.Request.Context(), req, clientMeta(c))
}

// Me 当前登录的管理员
func (a *Auth) Me(ctx context.Context, req *api.MeRequest) (*api.MeResponse, error) {
	return auth_svc.Auth().Me(ctx, req)
}
