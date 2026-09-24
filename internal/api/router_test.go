package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cago-frame/cago/server/mux/muxtest"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// publicRoutes 未登录可访问的接口，必须与 docs/specs/2026-09-23-admin-auth.md「公开接口」一致。
// 新增公开接口要先改 spec，再改这里
var publicRoutes = map[string]bool{
	"GET /api/v1/system/health":      true,
	"GET /api/v1/auth/status":        true,
	"POST /api/v1/auth/setup":        true,
	"POST /api/v1/auth/login":        true,
	"GET /api/v1/auth/oidc/login":    true,
	"GET /api/v1/auth/oidc/callback": true,
}

// sessionOnlyRoutes 只允许浏览器会话调用的账号类接口（spec「API 令牌」：令牌不能管理令牌、修改密码或登录方式）
var sessionOnlyRoutes = map[string]bool{
	"POST /api/v1/auth/password":      true,
	"GET /api/v1/tokens":              true,
	"POST /api/v1/tokens":             true,
	"POST /api/v1/tokens/:id/revoke":  true,
	"GET /api/v1/auth/oidc/config":    true,
	"PUT /api/v1/auth/oidc/config":    true,
	"POST /api/v1/auth/oidc/unbind":   true,
	"GET /api/v1/auth/oidc/bind":      true,
	"PUT /api/v1/auth/password-login": true,
}

// 把两个认证中间件换成哨兵：
//   - 非公开接口必须全部经过 requireAuth（返回 418）
//   - 恰好 sessionOnlyRoutes 中的接口经过 requireSession（返回 451）
func TestRouteGroups(t *testing.T) {
	origAuth, origSession := requireAuth, requireSession
	t.Cleanup(func() { requireAuth, requireSession = origAuth, origSession })
	authPassed := false
	requireAuth = func() gin.HandlerFunc {
		return func(c *gin.Context) {
			if !authPassed {
				c.AbortWithStatus(http.StatusTeapot)
			}
		}
	}
	requireSession = func() gin.HandlerFunc {
		return func(c *gin.Context) { c.AbortWithStatus(http.StatusUnavailableForLegalReasons) }
	}
	testMux := muxtest.NewTestMux()
	require.NoError(t, Router(context.Background(), testMux.Router))
	engine, ok := testMux.IRouter.(*gin.Engine)
	require.True(t, ok)

	call := func(method, path string) int {
		req := httptest.NewRequestWithContext(t.Context(), method, strings.ReplaceAll(path, ":id", "1"), strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		return w.Code
	}

	seen := map[string]bool{}
	for _, route := range engine.Routes() {
		if !strings.HasPrefix(route.Path, "/api/") {
			continue
		}
		key := route.Method + " " + route.Path
		seen[key] = true
		if publicRoutes[key] {
			continue
		}
		authPassed = false
		assert.Equal(t, http.StatusTeapot, call(route.Method, route.Path), "%s 未挂登录校验中间件", key)
		authPassed = true
		got := call(route.Method, route.Path) == http.StatusUnavailableForLegalReasons
		assert.Equal(t, sessionOnlyRoutes[key], got, "%s 是否只允许浏览器会话与预期不符", key)
	}
	for key := range publicRoutes {
		assert.True(t, seen[key], "公开接口 %s 未注册", key)
	}
	for key := range sessionOnlyRoutes {
		assert.True(t, seen[key], "账号类接口 %s 未注册", key)
	}
}
