package auth_ctr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cago-frame/cago/server/mux/muxtest"
	"github.com/gin-gonic/gin"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authapi "github.com/opskat/opsnap/internal/api/auth"
	"github.com/opskat/opsnap/internal/service/auth_svc"
	"github.com/opskat/opsnap/internal/service/sso_svc"
)

// stubSSO 只实现控制器用到的单点登录流程，记录收到的参数
type stubSSO struct {
	sso_svc.SSOSvc
	gotNext    string
	gotBrowser string
	gotSession string
	result     *sso_svc.CallbackResult
}

func (s *stubSSO) StartLogin(_ context.Context, next string) (*sso_svc.Start, error) {
	s.gotNext = next
	return &sso_svc.Start{URL: "https://git.example.com/login/oauth/authorize?state=x", Browser: "browser-123"}, nil
}

func (s *stubSSO) StartBind(context.Context) (*sso_svc.Start, error) {
	return &sso_svc.Start{URL: "https://git.example.com/login/oauth/authorize?state=y", Browser: "browser-456"}, nil
}

func (s *stubSSO) Callback(_ context.Context, browser, session string, _ *authapi.OIDCCallbackRequest) *sso_svc.CallbackResult {
	s.gotBrowser, s.gotSession = browser, session
	return s.result
}

func (s *stubSSO) Result(id string) string {
	if id == "e1" {
		return "Gitea 拒绝了授权（access_denied）。"
	}
	return ""
}

func setupOIDCTest(t *testing.T) (*stubSSO, *muxtest.TestMux) {
	t.Helper()
	stub := &stubSSO{}
	old := sso_svc.SSO()
	sso_svc.RegisterSSO(stub)
	t.Cleanup(func() { sso_svc.RegisterSSO(old) })
	testMux := muxtest.NewTestMux()
	ctr := NewAuth()
	testMux.Group("/api/v1").Bind(ctr.OIDCLogin, ctr.OIDCBind, ctr.OIDCCallback, ctr.OIDCResult)
	return stub, testMux
}

func cookieNamed(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestOIDC(t *testing.T) {
	convey.Convey("单点登录接口", t, func() {
		stub, testMux := setupOIDCTest(t)
		engine := testMux.IRouter.(*gin.Engine)
		do := func(method, path, body string, mod func(*http.Request)) *httptest.ResponseRecorder {
			req := httptest.NewRequest(method, path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			if mod != nil {
				mod(req)
			}
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)
			return w
		}

		convey.Convey("开始登录：返回授权地址，并写只在回调路径带上的 HttpOnly 浏览器 Cookie", func() {
			w := do(http.MethodPost, "/api/v1/auth/oidc/login", `{"next":"/runs"}`, nil)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			var resp struct{ Data authapi.OIDCLoginResponse }
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.Equal(t, "https://git.example.com/login/oauth/authorize?state=x", resp.Data.URL)
			assert.Equal(t, "/runs", stub.gotNext)
			c := cookieNamed(w, OIDCCookie)
			require.NotNil(t, c)
			assert.Equal(t, "browser-123", c.Value)
			assert.Equal(t, "/api/v1/auth/oidc", c.Path)
			assert.Equal(t, 600, c.MaxAge)
			assert.True(t, c.HttpOnly)
			assert.Equal(t, http.SameSiteLaxMode, c.SameSite)
			assert.NotContains(t, w.Body.String(), "browser-123", "浏览器 Cookie 的值不出现在响应体里")
		})

		convey.Convey("开始绑定同样写浏览器 Cookie", func() {
			w := do(http.MethodPost, "/api/v1/settings/sso/bind", `{}`, nil)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			assert.Equal(t, "browser-456", cookieNamed(w, OIDCCookie).Value)
		})

		convey.Convey("回调成功：带上浏览器 Cookie 交给服务，写会话 Cookie，清掉浏览器 Cookie，跳到登录前的页面", func() {
			stub.result = &sso_svc.CallbackResult{
				Redirect: "/runs",
				Session:  &auth_svc.LoginResult{Token: "sess-token", ExpiresAt: time.Now().Add(7 * 24 * time.Hour), Username: "admin"},
			}
			w := do(http.MethodGet, "/api/v1/auth/oidc/callback?code=c&state=s", "", func(r *http.Request) {
				r.AddCookie(&http.Cookie{Name: OIDCCookie, Value: "browser-123", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
			})
			assert.Equal(t, http.StatusFound, w.Code)
			assert.Equal(t, "/runs", w.Header().Get("Location"))
			assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
			assert.Equal(t, "browser-123", stub.gotBrowser)
			sess := cookieNamed(w, auth_svc.SessionCookie)
			require.NotNil(t, sess)
			assert.Equal(t, "sess-token", sess.Value)
			assert.True(t, sess.HttpOnly)
			assert.Equal(t, "/", sess.Path)
			assert.Greater(t, sess.MaxAge, 6*24*3600)
			assert.Less(t, cookieNamed(w, OIDCCookie).MaxAge, 0)
		})

		convey.Convey("回调失败：只跳转，不写会话 Cookie", func() {
			stub.result = &sso_svc.CallbackResult{Redirect: "/login?sso_error=e1"}
			w := do(http.MethodGet, "/api/v1/auth/oidc/callback?error=access_denied&state=s", "", nil)
			assert.Equal(t, http.StatusFound, w.Code)
			assert.Equal(t, "/login?sso_error=e1", w.Header().Get("Location"))
			assert.Nil(t, cookieNamed(w, auth_svc.SessionCookie))
			assert.Equal(t, "", stub.gotBrowser)
			assert.Equal(t, "", stub.gotSession)
		})

		convey.Convey("回调时把面板会话 Cookie 一并交给服务（用来判断失效的请求是不是绑定）", func() {
			stub.result = &sso_svc.CallbackResult{Redirect: "/settings?tab=sso&sso_error=e1"}
			w := do(http.MethodGet, "/api/v1/auth/oidc/callback?code=c&state=s", "", func(r *http.Request) {
				r.AddCookie(&http.Cookie{Name: auth_svc.SessionCookie, Value: "sess-abc", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
			})
			assert.Equal(t, http.StatusFound, w.Code)
			assert.Equal(t, "sess-abc", stub.gotSession)
			assert.Nil(t, cookieNamed(w, auth_svc.SessionCookie), "失败时不改动已有会话")
		})

		convey.Convey("读取失败原因", func() {
			w := do(http.MethodGet, "/api/v1/auth/oidc/result?id=e1", "", nil)
			assert.Contains(t, w.Body.String(), "Gitea 拒绝了授权（access_denied）。")
			w = do(http.MethodGet, "/api/v1/auth/oidc/result?id=zz", "", nil)
			assert.Contains(t, w.Body.String(), `"message":""`)
		})
	})
}
