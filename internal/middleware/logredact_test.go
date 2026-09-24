package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestRedactAccessLog(t *testing.T) {
	t.Run("调试模式的访问日志不记录 OIDC 回调中的授权码与 state", func(t *testing.T) {
		var buf bytes.Buffer
		gin.SetMode(gin.TestMode)
		r := gin.New()
		r.Use(gin.LoggerWithWriter(RedactAccessLog(&buf)))
		r.GET("/api/v1/auth/oidc/callback", func(c *gin.Context) { c.Status(http.StatusFound) })

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
			"/api/v1/auth/oidc/callback?state=STATE-SECRET&code=CODE-SECRET&session_state=keep", nil)
		r.ServeHTTP(httptest.NewRecorder(), req)

		out := buf.String()
		assert.Contains(t, out, "/api/v1/auth/oidc/callback")
		assert.NotContains(t, out, "CODE-SECRET")
		assert.NotContains(t, out, "STATE-SECRET")
		assert.Contains(t, out, "session_state=keep", "只去掉敏感参数的值")
	})
}
