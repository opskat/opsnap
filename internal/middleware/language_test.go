package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/opskat/opsnap/internal/pkg/code"
)

func TestLanguage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.ContextWithFallback = true
	r.Use(Language())
	r.GET("/t", func(c *gin.Context) {
		c.String(http.StatusOK, i18n.T(c.Request.Context(), code.ServerError))
	})
	cases := map[string]string{
		"en":                    "Internal server error",
		"en-US,en;q=0.9":        "Internal server error",
		"zh-CN":                 "服务器内部错误",
		"":                      "服务器内部错误",
		"fr-FR":                 "服务器内部错误",
		"zh-CN,zh;q=0.9,en;q=1": "服务器内部错误",
	}
	for header, want := range cases {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/t", nil)
		if header != "" {
			req.Header.Set("Accept-Language", header)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, want, w.Body.String(), "Accept-Language=%q", header)
	}
}
