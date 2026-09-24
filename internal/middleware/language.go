// Package middleware 提供全局 HTTP 中间件。
package middleware

import (
	"strings"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/gin-gonic/gin"

	"github.com/opskat/opsnap/internal/pkg/code"
)

// Language 按 Accept-Language 的首选语言设置错误文案语言：首选为英文时用英文，其余一律中文。
// 前端的 request() 总会带上当前界面语言
func Language() gin.HandlerFunc {
	return func(c *gin.Context) {
		lang := code.LangZhCN
		first, _, _ := strings.Cut(c.GetHeader("Accept-Language"), ",")
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(first)), "en") {
			lang = code.LangEn
		}
		c.Request = c.Request.WithContext(i18n.WithLanguage(c.Request.Context(), lang))
		c.Next()
	}
}
