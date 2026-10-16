package auth_svc

import (
	"strings"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/gin-gonic/gin"

	"github.com/opskat/opsnap/internal/model/auth"
	"github.com/opskat/opsnap/internal/pkg/code"
)

// Middleware 认证中间件：接受会话 Cookie 或 `Authorization: Bearer osk_…`。
// 通过后把发起者写入请求 ctx，后续用 auth.ActorFrom(ctx) 读取；否则返回 401。
func Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		bearer, ok := bearerToken(c.GetHeader("Authorization"))
		if !ok {
			httputils.HandleResp(c, i18n.NewUnauthorizedError(ctx, code.Unauthorized))
			return
		}
		cookie, _ := c.Cookie(SessionCookie)
		actor, err := Auth().Authenticate(ctx, cookie, bearer)
		if err != nil {
			httputils.HandleResp(c, err)
			return
		}
		c.Request = c.Request.WithContext(auth.WithActor(ctx, actor))
		c.Next()
	}
}

// bearerToken 取出 Bearer 令牌；没有 Authorization 头返回空串和 true，
// 头存在但不是 Bearer 格式时返回 false。
func bearerToken(header string) (string, bool) {
	if header == "" {
		return "", true
	}
	scheme, token, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
		return "", false
	}
	return strings.TrimSpace(token), true
}
