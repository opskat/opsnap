package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	"github.com/opskat/opsnap/internal/repository/admin_repo"
	"github.com/opskat/opsnap/internal/repository/admin_repo/mock_admin_repo"
	"github.com/opskat/opsnap/internal/repository/sso_repo"
	"github.com/opskat/opsnap/internal/repository/sso_repo/mock_sso_repo"
)

func TestAuthRoutes(t *testing.T) {
	engine := setupRouterTest(t)
	convey.Convey("认证路由", t, func() {
		convey.Convey("除登录、初始化、状态外的接口未登录一律 401", func() {
			protected := []struct{ method, path string }{
				{http.MethodPost, "/api/v1/auth/logout"},
				{http.MethodGet, "/api/v1/auth/me"},
				{http.MethodPut, "/api/v1/auth/password"},
				{http.MethodGet, "/api/v1/apikeys"},
				{http.MethodPost, "/api/v1/apikeys"},
				{http.MethodDelete, "/api/v1/apikeys/1"},
				{http.MethodGet, "/api/v1/settings"},
				{http.MethodPut, "/api/v1/settings"},
				{http.MethodGet, "/api/v1/settings/sso"},
				{http.MethodPut, "/api/v1/settings/sso"},
				{http.MethodDelete, "/api/v1/settings/sso"},
				{http.MethodPost, "/api/v1/settings/sso/bind"},
				{http.MethodDelete, "/api/v1/settings/sso/binding"},
				{http.MethodGet, "/api/v1/notify"},
				{http.MethodPut, "/api/v1/notify"},
				{http.MethodPost, "/api/v1/notify/test"},
				{http.MethodGet, "/api/v1/servers"},
				{http.MethodPost, "/api/v1/servers"},
				{http.MethodPost, "/api/v1/servers/test"},
				{http.MethodGet, "/api/v1/servers/1"},
				{http.MethodPut, "/api/v1/servers/1"},
				{http.MethodDelete, "/api/v1/servers/1"},
				{http.MethodPost, "/api/v1/servers/1/test"},
				{http.MethodGet, "/api/v1/repos"},
				{http.MethodPost, "/api/v1/repos"},
				{http.MethodPost, "/api/v1/repos/sftp-host-key"},
				{http.MethodPost, "/api/v1/repos/recovery-key"},
				{http.MethodGet, "/api/v1/repos/1"},
				{http.MethodPut, "/api/v1/repos/1"},
				{http.MethodDelete, "/api/v1/repos/1"},
				{http.MethodGet, "/api/v1/repos/1/keys"},
				{http.MethodPost, "/api/v1/repos/1/keys/rotate"},
				{http.MethodPost, "/api/v1/repos/1/keys/recovery"},
				{http.MethodDelete, "/api/v1/repos/1/keys/recovery"},
				{http.MethodGet, "/api/v1/repos/1/recovery-bundle"},
				{http.MethodPost, "/api/v1/repos/1/recheck"},
				{http.MethodPost, "/api/v1/repos/1/check"},
				{http.MethodPost, "/api/v1/repos/1/prune"},
				{http.MethodPost, "/api/v1/repos/1/unlock"},
				{http.MethodPost, "/api/v1/jobs/1/run"},
				{http.MethodGet, "/api/v1/runs"},
				{http.MethodGet, "/api/v1/runs/1"},
				{http.MethodPost, "/api/v1/runs/1/stop"},
				{http.MethodGet, "/api/v1/runs/1/log"},
				{http.MethodGet, "/api/v1/runs/1/log/stream"},
			}
			for _, p := range protected {
				w := httptest.NewRecorder()
				engine.ServeHTTP(w, httptest.NewRequest(p.method, p.path, strings.NewReader("{}")))
				assert.Equal(t, http.StatusUnauthorized, w.Code, p.method+" "+p.path)
			}
		})

		convey.Convey("路由表里除公开接口外的每一条 /api/v1 路由未登录都是 401", func() {
			public := map[string]bool{
				"GET /api/v1/openapi.json": true,
				"GET /api/v1/auth/status":  true,
				"POST /api/v1/auth/setup":  true,
				"POST /api/v1/auth/login":  true,
				// 单点登录：开始登录、提供方回调、读取失败原因
				"POST /api/v1/auth/oidc/login":   true,
				"GET /api/v1/auth/oidc/callback": true,
				"GET /api/v1/auth/oidc/result":   true,
			}
			checked := 0
			for _, rt := range engine.Routes() {
				if !strings.HasPrefix(rt.Path, "/api/v1/") || public[rt.Method+" "+rt.Path] {
					continue
				}
				path := strings.NewReplacer(":id", "1", ":sid", "abc").Replace(rt.Path)
				w := httptest.NewRecorder()
				engine.ServeHTTP(w, httptest.NewRequest(rt.Method, path, strings.NewReader("{}")))
				assert.Equal(t, http.StatusUnauthorized, w.Code, rt.Method+" "+rt.Path)
				checked++
			}
			assert.Greater(t, checked, 50)
		})

		convey.Convey("单点登录的开始、回调和失败原因无需登录", func() {
			for _, p := range []struct{ method, path string }{
				{http.MethodPost, "/api/v1/auth/oidc/login"},
				{http.MethodGet, "/api/v1/auth/oidc/callback?state=x"},
				{http.MethodGet, "/api/v1/auth/oidc/result?id=x"},
			} {
				w := httptest.NewRecorder()
				engine.ServeHTTP(w, httptest.NewRequest(p.method, p.path, strings.NewReader("{}")))
				assert.NotEqual(t, http.StatusUnauthorized, w.Code, p.method+" "+p.path)
				assert.NotEqual(t, http.StatusNotFound, w.Code, p.method+" "+p.path)
			}
		})

		convey.Convey("状态接口无需登录", func() {
			ctrl := gomock.NewController(t)
			repo := mock_admin_repo.NewMockAdminRepo(ctrl)
			repo.EXPECT().Find(gomock.Any()).Return(nil, nil)
			admin_repo.RegisterAdmin(repo)
			ssoRepo := mock_sso_repo.NewMockSSORepo(ctrl)
			ssoRepo.EXPECT().Get(gomock.Any()).Return(nil, nil)
			sso_repo.RegisterSSO(ssoRepo)
			w := get(t, engine, "/api/v1/auth/status")
			assert.Equal(t, http.StatusOK, w.Code)
			assert.Contains(t, w.Body.String(), `"initialized":false`)
			assert.Contains(t, w.Body.String(), `"sso_enabled":false`)
		})
	})
}
