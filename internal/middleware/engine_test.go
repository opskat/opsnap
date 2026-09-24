package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func clientIP(t *testing.T, trusted []string, peer, xff string) string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	require.NoError(t, ConfigureEngine(r, trusted))
	r.GET("/ip", func(c *gin.Context) { c.String(http.StatusOK, c.ClientIP()) })
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/ip", nil)
	req.RemoteAddr = peer + ":40000"
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	req.Header.Set("X-Real-IP", "198.51.100.99")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Body.String()
}

func TestClientIP(t *testing.T) {
	t.Run("未配置可信代理时忽略 X-Forwarded-For 与 X-Real-IP", func(t *testing.T) {
		assert.Equal(t, "10.0.0.5", clientIP(t, nil, "10.0.0.5", "203.0.113.7"))
	})
	t.Run("对端是可信代理时使用 X-Forwarded-For", func(t *testing.T) {
		assert.Equal(t, "203.0.113.7", clientIP(t, []string{"10.0.0.0/8"}, "10.0.0.5", "203.0.113.7"))
	})
	t.Run("对端不在可信列表中时忽略 X-Forwarded-For", func(t *testing.T) {
		assert.Equal(t, "192.0.2.1", clientIP(t, []string{"10.0.0.0/8"}, "192.0.2.1", "203.0.113.7"))
	})
	t.Run("可信代理地址格式错误时报错", func(t *testing.T) {
		assert.Error(t, ConfigureEngine(gin.New(), []string{"not-an-ip"}))
	})
}
