package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/cago-frame/cago/server/mux"
	"github.com/gin-gonic/gin"
	"github.com/swaggo/swag"

	_ "github.com/opskat/opsnap/docs" // 注册 cago gen 生成的 OpenAPI 文档
	"github.com/opskat/opsnap/internal/controller/apikey_ctr"
	"github.com/opskat/opsnap/internal/controller/auth_ctr"
	"github.com/opskat/opsnap/internal/controller/job_ctr"
	"github.com/opskat/opsnap/internal/controller/notify_ctr"
	"github.com/opskat/opsnap/internal/controller/overview_ctr"
	"github.com/opskat/opsnap/internal/controller/repo_ctr"
	"github.com/opskat/opsnap/internal/controller/run_ctr"
	"github.com/opskat/opsnap/internal/controller/server_ctr"
	"github.com/opskat/opsnap/internal/controller/setting_ctr"
	"github.com/opskat/opsnap/internal/controller/snapshot_ctr"
	"github.com/opskat/opsnap/internal/controller/sso_ctr"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/service/auth_svc"
	"github.com/opskat/opsnap/web"
)

// Router 注册全部路由
// @title    opsnap API
// @version  1.0
// @BasePath /api/v1
func Router(ctx context.Context, root *mux.Router) error {
	engine, ok := root.IRouter.(*gin.Engine)
	if !ok {
		return errors.New("根路由必须是 *gin.Engine，才能挂载面板回退路由")
	}

	r := root.Group("/api/v1")
	r.GET("/openapi.json", openAPI)

	authCtr := auth_ctr.NewAuth()
	apiKeyCtr := apikey_ctr.NewAPIKey()
	settingCtr := setting_ctr.NewSetting()
	notifyCtr := notify_ctr.NewNotify()
	serverCtr := server_ctr.NewServer()
	repoCtr := repo_ctr.NewRepo()
	jobCtr := job_ctr.NewJob()
	runCtr := run_ctr.NewRun()
	snapshotCtr := snapshot_ctr.NewSnapshot()
	overviewCtr := overview_ctr.NewOverview()
	ssoCtr := sso_ctr.NewSSO()
	// 未登录可访问：是否已初始化、首次创建管理员、登录，以及单点登录的开始、回调和失败原因。
	// /health 由 cago 在 /api/v1 之外提供。
	r.Group("/").Bind(
		authCtr.Status,
		authCtr.Setup,
		authCtr.Login,
		authCtr.OIDCLogin,
		authCtr.OIDCCallback,
		authCtr.OIDCResult,
	)
	// 其余接口需要会话 Cookie 或 Bearer API 密钥
	r.Group("/", auth_svc.Middleware()).Bind(
		authCtr.Logout,
		authCtr.Me,
		authCtr.ChangePassword,
		apiKeyCtr.Create,
		apiKeyCtr.List,
		apiKeyCtr.Revoke,
		settingCtr.Get,
		settingCtr.Update,
		settingCtr.RotateMasterKey,
		ssoCtr.Get,
		ssoCtr.Update,
		ssoCtr.Disable,
		ssoCtr.Unbind,
		authCtr.OIDCBind,
		notifyCtr.Get,
		notifyCtr.Update,
		notifyCtr.Test,
		serverCtr.List,
		serverCtr.Get,
		serverCtr.Create,
		serverCtr.Update,
		serverCtr.Delete,
		serverCtr.Test,
		serverCtr.TestSaved,
		repoCtr.List,
		repoCtr.Get,
		repoCtr.Create,
		repoCtr.Update,
		repoCtr.Delete,
		repoCtr.SFTPHostKey,
		repoCtr.NewRecoveryKey,
		repoCtr.Keys,
		repoCtr.RotateKey,
		repoCtr.GenerateRecoveryKey,
		repoCtr.RemoveRecoveryKey,
		repoCtr.RecoveryBundle,
		repoCtr.Recheck,
		repoCtr.Check,
		repoCtr.Prune,
		repoCtr.Unlock,
		jobCtr.List,
		jobCtr.Get,
		jobCtr.Create,
		jobCtr.Update,
		jobCtr.Delete,
		jobCtr.Duplicate,
		jobCtr.Pause,
		jobCtr.Resume,
		jobCtr.NextRuns,
		jobCtr.Databases,
		jobCtr.Test,
		runCtr.RunJob,
		runCtr.List,
		runCtr.Get,
		runCtr.Stop,
		runCtr.Log,
		runCtr.StreamLog,
		snapshotCtr.List,
		snapshotCtr.Files,
		snapshotCtr.Exports,
		snapshotCtr.Download,
		snapshotCtr.CheckDownload,
		overviewCtr.Get,
	)

	engine.NoRoute(noRoute(web.Handler(web.Dist())))
	return nil
}

// openAPI 返回 OpenAPI（Swagger 2.0）文档
func openAPI(c *gin.Context) {
	doc, err := swag.ReadDoc()
	if err != nil {
		httputils.HandleResp(c, err)
		return
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", []byte(doc))
}

// noRoute 未匹配路由的处理：/api 下的路径和非 GET/HEAD 请求返回 404 JSON，
// 其余交给内嵌面板，由前端路由处理。
func noRoute(spa gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		p := c.Request.URL.Path
		isAPI := p == "/api" || strings.HasPrefix(p, "/api/")
		isRead := c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead
		if isAPI || !isRead {
			httputils.HandleResp(c, i18n.NewNotFoundError(c, code.RouteNotFound))
			return
		}
		spa(c)
	}
}
