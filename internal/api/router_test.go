package api

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cago-frame/cago/server/mux/muxtest"
	"github.com/gin-gonic/gin"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opsnap/web"
)

// setupRouterTest 注册全部路由，返回可直接处理请求的 gin 引擎
func setupRouterTest(t *testing.T) *gin.Engine {
	t.Helper()
	testMux := muxtest.NewTestMux()
	require.NoError(t, Router(context.Background(), testMux.Router))
	engine, ok := testMux.IRouter.(*gin.Engine)
	require.True(t, ok)
	return engine
}

// get 发起一次 GET 请求并返回响应记录
func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

func TestRouter(t *testing.T) {
	engine := setupRouterTest(t)
	index, err := fs.ReadFile(web.Dist(), "index.html")
	require.NoError(t, err)

	convey.Convey("HTTP 路由", t, func() {
		convey.Convey("GET / 返回内嵌的 index.html", func() {
			w := get(t, engine, "/")
			assert.Equal(t, http.StatusOK, w.Code)
			assert.Contains(t, w.Header().Get("Content-Type"), "text/html")
			assert.Equal(t, string(index), w.Body.String())
		})

		convey.Convey("未知的非 API 路径回退到 index.html", func() {
			for _, path := range []string{"/jobs", "/jobs/12/runs", "/settings/api-keys"} {
				w := get(t, engine, path)
				assert.Equal(t, http.StatusOK, w.Code, path)
				assert.Contains(t, w.Header().Get("Content-Type"), "text/html", path)
				assert.Equal(t, string(index), w.Body.String(), path)
			}
		})

		convey.Convey("未知的 /api 路径返回 404 JSON，不回退到 index.html", func() {
			for _, path := range []string{"/api", "/api/", "/api/v1/nope", "/api/v2/jobs"} {
				w := get(t, engine, path)
				assert.Equal(t, http.StatusNotFound, w.Code, path)
				assert.Contains(t, w.Header().Get("Content-Type"), "application/json", path)
				var body map[string]any
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body), path)
				assert.Contains(t, body, "code", path)
				assert.Contains(t, body, "msg", path)
			}
		})

		convey.Convey("非 GET 的未知路径返回 404 JSON", func() {
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/jobs", nil))
			assert.Equal(t, http.StatusNotFound, w.Code)
			assert.Contains(t, w.Header().Get("Content-Type"), "application/json")
		})

		convey.Convey("密钥的生成与 reveal 在需要登录的那一组里", func() {
			for _, tc := range []struct{ method, path string }{
				{http.MethodPost, "/api/v1/credentials/generate"},
				{http.MethodPost, "/api/v1/credentials/1/reveal"},
			} {
				w := httptest.NewRecorder()
				engine.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
				assert.Equal(t, http.StatusUnauthorized, w.Code, tc.path)
				assert.Contains(t, w.Header().Get("Content-Type"), "application/json", tc.path)
			}
		})

		convey.Convey("GET /api/v1/openapi.json 返回 OpenAPI 文档", func() {
			w := get(t, engine, "/api/v1/openapi.json")
			assert.Equal(t, http.StatusOK, w.Code)
			assert.Contains(t, w.Header().Get("Content-Type"), "application/json")
			var doc map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &doc))
			assert.Equal(t, "2.0", doc["swagger"])
			assert.Equal(t, "/api/v1", doc["basePath"])
		})
	})
}
