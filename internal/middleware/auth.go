package middleware

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/gin-gonic/gin"

	"github.com/opskat/opsnap/internal/pkg/authctx"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/service/auth_svc"
	"github.com/opskat/opsnap/internal/service/token_svc"
)

// SessionCookie 浏览器会话 Cookie 名
const SessionCookie = "opsnap_session"

// secureRequest 通过 HTTPS 访问（直连 TLS，或反向代理声明为 https）时 Cookie 带 Secure
func secureRequest(c *gin.Context) bool {
	return c.Request.TLS != nil || strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https")
}

// SetSessionCookie 写入会话 Cookie：HttpOnly、SameSite=Lax，有效期与服务端会话一致
func SetSessionCookie(c *gin.Context, token string, expires time.Time) {
	// Secure 取决于访问方式：HTTP 访问时带 Secure 浏览器不会保存 Cookie，spec 要求仅 HTTPS 时设置
	http.SetCookie(c.Writer, &http.Cookie{ //nolint:gosec // Secure 按访问协议动态设置，HttpOnly 与 SameSite 固定开启
		Name:     SessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(time.Until(expires).Seconds()),
		HttpOnly: true,
		Secure:   secureRequest(c),
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearSessionCookie 删除浏览器中的会话 Cookie
func ClearSessionCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{ //nolint:gosec // 同上
		Name:     SessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secureRequest(c),
		SameSite: http.SameSiteLaxMode,
	})
}

// bearerToken 取 Authorization: Bearer 后的令牌；没有时返回空串
func bearerToken(c *gin.Context) string {
	scheme, token, ok := strings.Cut(c.GetHeader("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}

// Auth 要求已认证：带 Authorization: Bearer 时按 API 令牌认证，否则校验会话 Cookie；
// 身份放进请求上下文。会话需要顺延时同时刷新 Cookie
func Auth() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		if bearer := bearerToken(c); bearer != "" {
			p, err := token_svc.Token().Authenticate(ctx, bearer)
			if err != nil {
				httputils.HandleResp(c, err)
				c.Abort()
				return
			}
			c.Request = c.Request.WithContext(authctx.With(ctx, p))
			c.Next()
			return
		}
		token, _ := c.Cookie(SessionCookie)
		p, refreshed, err := auth_svc.Auth().AuthenticateSession(ctx, token)
		if err != nil {
			if token != "" {
				ClearSessionCookie(c)
			}
			httputils.HandleResp(c, err)
			c.Abort()
			return
		}
		if refreshed != nil {
			SetSessionCookie(c, refreshed.Token, refreshed.Expires)
		}
		c.Request = c.Request.WithContext(authctx.With(ctx, p))
		c.Next()
	}
}

// RequireSession 只允许浏览器会话调用（修改密码、令牌管理、登录方式等账号类接口）；API 令牌返回 403。
// 必须挂在 Auth 之后
func RequireSession() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		if p := authctx.From(ctx); p == nil || p.Via != authctx.ViaSession {
			httputils.HandleResp(c, i18n.NewForbiddenError(ctx, code.SessionRequired))
			c.Abort()
			return
		}
		c.Next()
	}
}

// SameOrigin 拒绝跨站的写请求（CSRF 防护）：带 Origin 且与当前站点不同源的非 GET/HEAD/OPTIONS 请求返回 403。
// 浏览器发起的跨站写请求一定带 Origin；不带 Origin 的请求来自非浏览器客户端，不构成 CSRF
func SameOrigin() gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			c.Next()
			return
		}
		origin := c.GetHeader("Origin")
		// API 令牌请求不依赖 Cookie，不构成 CSRF
		if origin == "" || bearerToken(c) != "" {
			c.Next()
			return
		}
		u, err := url.Parse(origin)
		if err != nil || !strings.EqualFold(u.Host, c.Request.Host) {
			httputils.HandleResp(c, i18n.NewForbiddenError(c.Request.Context(), code.CrossOriginRejected))
			c.Abort()
			return
		}
		c.Next()
	}
}
