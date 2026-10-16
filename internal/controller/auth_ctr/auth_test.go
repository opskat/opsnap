package auth_ctr

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/cago-frame/cago/server/mux/muxclient"
	"github.com/cago-frame/cago/server/mux/muxtest"
	"github.com/gin-gonic/gin"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"golang.org/x/crypto/bcrypt"

	authapi "github.com/opskat/opsnap/internal/api/auth"
	"github.com/opskat/opsnap/internal/model/entity/admin_entity"
	"github.com/opskat/opsnap/internal/model/entity/apikey_entity"
	"github.com/opskat/opsnap/internal/model/entity/session_entity"
	"github.com/opskat/opsnap/internal/model/entity/sso_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/repository/admin_repo"
	"github.com/opskat/opsnap/internal/repository/admin_repo/mock_admin_repo"
	"github.com/opskat/opsnap/internal/repository/apikey_repo"
	"github.com/opskat/opsnap/internal/repository/apikey_repo/mock_apikey_repo"
	"github.com/opskat/opsnap/internal/repository/session_repo"
	"github.com/opskat/opsnap/internal/repository/session_repo/mock_session_repo"
	"github.com/opskat/opsnap/internal/repository/sso_repo"
	"github.com/opskat/opsnap/internal/repository/sso_repo/mock_sso_repo"
	"github.com/opskat/opsnap/internal/service/auth_svc"
)

var now = time.Unix(1_800_000_000, 0)

type mocks struct {
	admin   *mock_admin_repo.MockAdminRepo
	session *mock_session_repo.MockSessionRepo
	apikey  *mock_apikey_repo.MockAPIKeyRepo
	sso     *mock_sso_repo.MockSSORepo
}

func setupAuthTest(t *testing.T) (context.Context, *mocks, *muxtest.TestMux) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	m := &mocks{
		admin:   mock_admin_repo.NewMockAdminRepo(ctrl),
		session: mock_session_repo.NewMockSessionRepo(ctrl),
		apikey:  mock_apikey_repo.NewMockAPIKeyRepo(ctrl),
		sso:     mock_sso_repo.NewMockSSORepo(ctrl),
	}
	admin_repo.RegisterAdmin(m.admin)
	session_repo.RegisterSession(m.session)
	apikey_repo.RegisterAPIKey(m.apikey)
	sso_repo.RegisterSSO(m.sso)
	auth_svc.RegisterAuth(auth_svc.NewAuth(func() time.Time { return now }))

	testMux := muxtest.NewTestMux()
	r := testMux.Group("/api/v1")
	ctr := NewAuth()
	r.Group("/").Bind(ctr.Status, ctr.Setup, ctr.Login)
	r.Group("/", auth_svc.Middleware()).Bind(ctr.Logout, ctr.Me, ctr.ChangePassword)
	return context.Background(), m, testMux
}

func newAdmin(t *testing.T) *admin_entity.Admin {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("correct-horse"), bcrypt.MinCost)
	require.NoError(t, err)
	return &admin_entity.Admin{ID: 1, Username: "admin", PasswordHash: string(hash)}
}

func serve(testMux *muxtest.TestMux, method, path string, mod func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if mod != nil {
		mod(req)
	}
	w := httptest.NewRecorder()
	testMux.IRouter.(*gin.Engine).ServeHTTP(w, req)
	return w
}

func TestLogin(t *testing.T) {
	convey.Convey("登录接口", t, func() {
		ctx, m, testMux := setupAuthTest(t)
		// 没有单点登录配置：允许密码登录
		ssoCfg := (*sso_entity.SSO)(nil)
		m.sso.EXPECT().Get(gomock.Any()).DoAndReturn(func(context.Context) (*sso_entity.SSO, error) { return ssoCfg, nil }).AnyTimes()

		convey.Convey("密码登录关闭时返回 403，不比对密码、不下发 Cookie", func() {
			ssoCfg = &sso_entity.SSO{Issuer: "https://git.example.com/", ButtonLabel: "Gitea", BoundSub: "3", PasswordLogin: false}
			m.admin.EXPECT().Find(gomock.Any()).Return(newAdmin(t), nil)
			w := serve(testMux, http.MethodPost, "/api/v1/auth/login", func(r *http.Request) {
				r.Body = io.NopCloser(strings.NewReader(`{"username":"admin","password":"correct-horse"}`))
				r.Header.Set("Content-Type", "application/json")
			})
			assert.Equal(t, http.StatusForbidden, w.Code)
			assert.Contains(t, w.Body.String(), "密码登录已关闭，请用 Gitea 登录")
			assert.Contains(t, w.Body.String(), strconv.Itoa(code.PasswordLoginDisabled))
			assert.Empty(t, w.Result().Cookies())
			ssoCfg = nil
		})

		convey.Convey("成功后下发 HttpOnly 会话 Cookie", func() {
			m.admin.EXPECT().Find(gomock.Any()).Return(newAdmin(t), nil)
			m.session.EXPECT().DeleteExpired(gomock.Any(), now.Unix()).Return(nil)
			var stored *session_entity.Session
			m.session.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, s *session_entity.Session) error { stored = s; return nil })

			var httpResp *http.Response
			resp := &authapi.LoginResponse{}
			err := testMux.Do(ctx, &authapi.LoginRequest{Username: "admin", Password: "correct-horse"}, resp, muxclient.WithResponse(&httpResp))
			require.NoError(t, err)
			assert.Equal(t, "admin", resp.Username)

			cookies := httpResp.Cookies()
			require.Len(t, cookies, 1)
			c := cookies[0]
			assert.Equal(t, auth_svc.SessionCookie, c.Name)
			assert.True(t, c.HttpOnly)
			assert.Equal(t, http.SameSiteLaxMode, c.SameSite)
			assert.Equal(t, "/", c.Path)
			assert.Greater(t, c.MaxAge, 0)
			assert.NotEmpty(t, c.Value)
			assert.Equal(t, session_entity.HashToken(c.Value), stored.TokenHash, "库里只有哈希")
		})

		convey.Convey("密码错误时提示剩余次数", func() {
			m.admin.EXPECT().Find(gomock.Any()).Return(newAdmin(t), nil)
			m.admin.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, a *admin_entity.Admin) error {
					assert.Equal(t, 1, a.FailedCount)
					return nil
				})
			err := testMux.Do(ctx, &authapi.LoginRequest{Username: "admin", Password: "bad"}, &authapi.LoginResponse{})
			var he *httputils.Error
			require.ErrorAs(t, err, &he)
			assert.Equal(t, code.LoginFailed, he.Code)
			assert.Contains(t, he.Msg, "还剩 4 次")
		})

		convey.Convey("锁定期间返回锁定提示且不校验密码", func() {
			a := newAdmin(t)
			a.LockedUntil = now.Add(3 * time.Minute).Unix()
			m.admin.EXPECT().Find(gomock.Any()).Return(a, nil)
			err := testMux.Do(ctx, &authapi.LoginRequest{Username: "admin", Password: "correct-horse"}, &authapi.LoginResponse{})
			var he *httputils.Error
			require.ErrorAs(t, err, &he)
			assert.Equal(t, code.LoginLocked, he.Code)
			assert.Contains(t, he.Msg, "3 分钟")
		})
	})
}

func TestProtectedRoutes(t *testing.T) {
	convey.Convey("受保护的接口", t, func() {
		_, m, testMux := setupAuthTest(t)
		fullKey, prefix, hash, err := apikey_entity.Generate()
		require.NoError(t, err)

		convey.Convey("没有凭据返回 401", func() {
			w := serve(testMux, http.MethodGet, "/api/v1/auth/me", nil)
			assert.Equal(t, http.StatusUnauthorized, w.Code)
		})

		convey.Convey("格式不对或不存在的密钥返回 401", func() {
			m.apikey.EXPECT().FindByHash(gomock.Any(), apikey_entity.HashKey("osk_unknown")).Return(nil, nil)
			w := serve(testMux, http.MethodGet, "/api/v1/auth/me", func(r *http.Request) { r.Header.Set("Authorization", "Bearer osk_unknown") })
			assert.Equal(t, http.StatusUnauthorized, w.Code)
			w = serve(testMux, http.MethodGet, "/api/v1/auth/me", func(r *http.Request) { r.Header.Set("Authorization", "Bearer notakey") })
			assert.Equal(t, http.StatusUnauthorized, w.Code)
		})

		convey.Convey("已撤销的密钥返回 401", func() {
			m.apikey.EXPECT().FindByHash(gomock.Any(), hash).Return(&apikey_entity.APIKey{
				ID: 3, Name: "ci", Prefix: prefix, KeyHash: hash, RevokedAt: now.Unix() - 10,
			}, nil)
			w := serve(testMux, http.MethodGet, "/api/v1/auth/me", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+fullKey) })
			assert.Equal(t, http.StatusUnauthorized, w.Code)
		})

		convey.Convey("已过期的密钥返回 401", func() {
			m.apikey.EXPECT().FindByHash(gomock.Any(), hash).Return(&apikey_entity.APIKey{
				ID: 3, Name: "ci", KeyHash: hash, ExpiresAt: now.Unix() - 1,
			}, nil)
			w := serve(testMux, http.MethodGet, "/api/v1/auth/me", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+fullKey) })
			assert.Equal(t, http.StatusUnauthorized, w.Code)
		})

		convey.Convey("有效密钥通过，/auth/me 返回密钥身份", func() {
			m.apikey.EXPECT().FindByHash(gomock.Any(), hash).Return(&apikey_entity.APIKey{
				ID: 3, Name: "ci", KeyHash: hash, LastUsedAt: now.Unix(),
			}, nil)
			w := serve(testMux, http.MethodGet, "/api/v1/auth/me", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+fullKey) })
			assert.Equal(t, http.StatusOK, w.Code)
			assert.Contains(t, w.Body.String(), `"key_name":"ci"`)
			assert.Contains(t, w.Body.String(), `"kind":"api_key"`)
		})

		convey.Convey("有效会话通过，/auth/me 返回管理员", func() {
			m.session.EXPECT().FindByHash(gomock.Any(), session_entity.HashToken("tok")).Return(
				&session_entity.Session{ExpiresAt: now.Add(time.Hour).Unix()}, nil)
			m.admin.EXPECT().Find(gomock.Any()).Return(newAdmin(t), nil)
			w := serve(testMux, http.MethodGet, "/api/v1/auth/me", func(r *http.Request) {
				r.AddCookie(&http.Cookie{Name: auth_svc.SessionCookie, Value: "tok"}) //nolint:gosec // 测试请求里的 Cookie
			})
			assert.Equal(t, http.StatusOK, w.Code)
			assert.Contains(t, w.Body.String(), `"username":"admin"`)
		})

		convey.Convey("退出登录删除会话并清除 Cookie", func() {
			m.session.EXPECT().FindByHash(gomock.Any(), session_entity.HashToken("tok")).Return(
				&session_entity.Session{ExpiresAt: now.Add(time.Hour).Unix()}, nil)
			m.admin.EXPECT().Find(gomock.Any()).Return(newAdmin(t), nil)
			m.session.EXPECT().Delete(gomock.Any(), session_entity.HashToken("tok")).Return(nil)
			w := serve(testMux, http.MethodPost, "/api/v1/auth/logout", func(r *http.Request) {
				r.AddCookie(&http.Cookie{Name: auth_svc.SessionCookie, Value: "tok"}) //nolint:gosec // 测试请求里的 Cookie
			})
			assert.Equal(t, http.StatusOK, w.Code)
			cs := w.Result().Cookies()
			require.Len(t, cs, 1)
			assert.Equal(t, -1, cs[0].MaxAge)
			assert.True(t, cs[0].HttpOnly)
		})
	})
}

func TestSessionCookieSecure(t *testing.T) {
	cases := []struct {
		name   string
		header string
		tls    bool
		secure bool
	}{
		{"直接 HTTP 访问", "", false, false},
		{"直接 HTTPS 访问", "", true, true},
		{"反向代理转发 HTTPS", "https", false, true},
		{"反向代理转发的协议名大小写不同", "HTTPS", false, true},
		{"反向代理转发 HTTP", "http", false, false},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
		if tc.header != "" {
			c.Request.Header.Set("X-Forwarded-Proto", tc.header)
		}
		if tc.tls {
			c.Request.TLS = &tls.ConnectionState{}
		}
		setSessionCookie(c, "token", 60)
		cookies := w.Result().Cookies()
		require.Len(t, cookies, 1, tc.name)
		assert.Equal(t, tc.secure, cookies[0].Secure, tc.name)
		assert.True(t, cookies[0].HttpOnly, tc.name)
	}
}
