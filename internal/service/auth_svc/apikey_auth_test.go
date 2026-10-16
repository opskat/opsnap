package auth_svc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opsnap/internal/model/auth"
	"github.com/opskat/opsnap/internal/model/entity/apikey_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/repository/apikey_repo"
)

// newKey 直接经仓储写入一把密钥，返回完整密钥
func newKey(t *testing.T, ctx context.Context, name string, expiresAt int64) (string, *apikey_entity.APIKey) {
	t.Helper()
	full, prefix, hash, err := apikey_entity.Generate()
	require.NoError(t, err)
	k := &apikey_entity.APIKey{Name: name, Prefix: prefix, KeyHash: hash, ExpiresAt: expiresAt}
	require.NoError(t, apikey_repo.APIKey().Create(ctx, k))
	return full, k
}

func TestAuthenticateAPIKey(t *testing.T) {
	convey.Convey("API 密钥认证", t, func() {
		ctx, svc, clock, _ := setupAuthTest(t)
		full, k := newKey(t, ctx, "ci", 0)

		convey.Convey("有效密钥得到密钥身份并记录最近使用", func() {
			a, err := svc.Authenticate(ctx, "", full)
			require.NoError(t, err)
			assert.Equal(t, auth.Actor{Kind: auth.KindAPIKey, ID: k.ID, Name: "ci", Prefix: k.Prefix}, a)
			got, _ := apikey_repo.APIKey().FindByHash(ctx, k.KeyHash)
			assert.Equal(t, clock.Now().Unix(), got.LastUsedAt)
		})

		convey.Convey("无效密钥返回 401", func() {
			for _, bad := range []string{"osk_deadbeef", "wrong", "osk_", full + "x"} {
				_, err := svc.Authenticate(ctx, "", bad)
				assert.Equal(t, code.Unauthorized, errCode(t, err), bad)
			}
		})

		convey.Convey("撤销立即生效", func() {
			_, err := svc.Authenticate(ctx, "", full)
			require.NoError(t, err)
			ok, err := apikey_repo.APIKey().Revoke(ctx, k.ID, clock.Now().Unix())
			require.NoError(t, err)
			require.True(t, ok)
			_, err = svc.Authenticate(ctx, "", full)
			assert.Equal(t, code.Unauthorized, errCode(t, err))
		})

		convey.Convey("过期返回 401", func() {
			expiring, _ := newKey(t, ctx, "tmp", clock.Now().Add(30*24*time.Hour).Unix())
			_, err := svc.Authenticate(ctx, "", expiring)
			require.NoError(t, err)
			clock.Advance(30 * 24 * time.Hour)
			_, err = svc.Authenticate(ctx, "", expiring)
			assert.Equal(t, code.Unauthorized, errCode(t, err))
		})

		convey.Convey("带了 Bearer 就只认密钥，不回退到会话", func() {
			mustSetup(t, ctx, svc)
			res, err := login(ctx, svc, "correct-horse")
			require.NoError(t, err)
			_, err = svc.Authenticate(ctx, res.Token, "osk_invalid")
			assert.Equal(t, code.Unauthorized, errCode(t, err))
		})
	})
}

func TestMiddleware(t *testing.T) {
	ctx, svc, _, _ := setupAuthTest(t)
	prev := Auth()
	RegisterAuth(svc)
	t.Cleanup(func() { RegisterAuth(prev) })
	mustSetup(t, ctx, svc)
	full, _ := newKey(t, ctx, "ci", 0)
	res, err := login(ctx, svc, "correct-horse")
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Middleware())
	r.GET("/x", func(c *gin.Context) {
		a, ok := auth.ActorFrom(c.Request.Context())
		if !ok {
			c.String(http.StatusInternalServerError, "no actor")
			return
		}
		c.String(http.StatusOK, a.String())
	})
	do := func(mod func(*http.Request)) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req = req.WithContext(ctx)
		mod(req)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	convey.Convey("认证中间件", t, func() {
		assert.Equal(t, http.StatusUnauthorized, do(func(*http.Request) {}).Code)
		w := do(func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+full) })
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "api_key:ci", w.Body.String())
		w = do(func(r *http.Request) { r.AddCookie(&http.Cookie{Name: SessionCookie, Value: res.Token}) }) //nolint:gosec // 测试请求里的 Cookie
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "admin", w.Body.String())
		assert.Equal(t, http.StatusUnauthorized, do(func(r *http.Request) { r.Header.Set("Authorization", "Bearer osk_nope") }).Code)
		assert.Equal(t, http.StatusUnauthorized, do(func(r *http.Request) { r.Header.Set("Authorization", "Basic abc") }).Code)
	})
}
