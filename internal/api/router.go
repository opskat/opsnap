// Package api 注册所有 HTTP 路由。
package api

import (
	"context"

	"github.com/cago-frame/cago/server/mux"

	"github.com/opskat/opsnap/internal/controller/auth_ctr"
	"github.com/opskat/opsnap/internal/controller/channel_ctr"
	"github.com/opskat/opsnap/internal/controller/oidc_ctr"
	"github.com/opskat/opsnap/internal/controller/storage_ctr"
	"github.com/opskat/opsnap/internal/controller/system_ctr"
	"github.com/opskat/opsnap/internal/controller/token_ctr"
	"github.com/opskat/opsnap/internal/middleware"
)

// 认证中间件；守护测试把它们替换成哨兵，检查每个接口挂在正确的分组
var (
	requireAuth    = middleware.Auth
	requireSession = middleware.RequireSession
)

// Router 所有业务接口统一挂在 /api/v1 下；其余路径交给前端单页应用（见 internal/web）。分组：
//   - public：未登录可访问，只允许 docs/specs/2026-09-23-admin-auth.md「公开接口」列出的接口
//   - authed：浏览器会话或 API 令牌均可（业务接口默认放这里）
//   - account：只允许浏览器会话（修改密码、令牌管理、登录方式、查看仓库密钥、浏览主机目录），API 令牌返回 403
func Router(ctx context.Context, root *mux.Router) error {
	r := root.Group("/api/v1", middleware.SameOrigin())

	system := system_ctr.NewSystem()
	auth := auth_ctr.NewAuth()
	token := token_ctr.NewToken()
	oidc := oidc_ctr.NewOIDC()
	storage := storage_ctr.NewStorage()
	channel := channel_ctr.NewChannel()

	public := r.Group("/")
	public.Bind(system.Health, auth.Status, auth.Setup, auth.Login, oidc.Login, oidc.Callback)

	authed := r.Group("/", requireAuth())
	authed.Bind(auth.Logout, auth.Me,
		storage.List, storage.Probe, storage.Create, storage.Update, storage.Test, storage.Unlock, storage.Delete, storage.Key,
		channel.List, channel.Probe, channel.Create, channel.Update, channel.Test, channel.ConfirmHostKey, channel.Delete)

	account := authed.Group("/", requireSession())
	account.Bind(auth.ChangePassword, token.List, token.Create, token.Revoke,
		oidc.GetConfig, oidc.SaveConfig, oidc.Unbind, oidc.Bind, oidc.SetPasswordLogin, oidc.Reauth,
		storage.Reveal, storage.ListDirs, storage.MakeDir)
	return nil
}
