package auth_ctr

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/cago-frame/cago/server/mux/muxclient"
	"github.com/cago-frame/cago/server/mux/muxtest"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	api "github.com/opskat/opsnap/internal/api/auth"
	"github.com/opskat/opsnap/internal/middleware"
	"github.com/opskat/opsnap/internal/model/entity/admin_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/password"
	"github.com/opskat/opsnap/internal/repository/admin_repo"
	mock_admin_repo "github.com/opskat/opsnap/internal/repository/admin_repo/mock"
	"github.com/opskat/opsnap/internal/repository/session_repo"
	mock_session_repo "github.com/opskat/opsnap/internal/repository/session_repo/mock"
	"github.com/opskat/opsnap/internal/repository/setting_repo"
	mock_setting_repo "github.com/opskat/opsnap/internal/repository/setting_repo/mock"
	"github.com/opskat/opsnap/internal/service/auth_svc"
)

type mocks struct {
	admin   *mock_admin_repo.MockAdminRepo
	session *mock_session_repo.MockSessionRepo
}

func setupAuthTest(t *testing.T) (context.Context, *mocks, *muxtest.TestMux) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	m := &mocks{admin: mock_admin_repo.NewMockAdminRepo(ctrl), session: mock_session_repo.NewMockSessionRepo(ctrl)}
	admin_repo.RegisterAdmin(m.admin)
	session_repo.RegisterSession(m.session)
	settings := mock_setting_repo.NewMockSettingRepo(ctrl)
	settings.EXPECT().Get(gomock.Any(), gomock.Any()).Return("", false, nil).AnyTimes()
	setting_repo.RegisterSetting(settings)

	testMux := muxtest.NewTestMux(muxtest.WithBaseUrl("http://opsnap.test/api/v1"))
	ctr := NewAuth()
	r := testMux.Group("/api/v1", middleware.SameOrigin())
	r.Group("/").Bind(ctr.Status, ctr.Setup, ctr.Login)
	r.Group("/", middleware.Auth()).Bind(ctr.Logout, ctr.Me, ctr.ChangePassword)
	return context.Background(), m, testMux
}

func status(err error) int {
	var e *httputils.Error
	if errors.As(err, &e) {
		return e.Status
	}
	return 0
}

func errCode(err error) int {
	var e *httputils.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}

func sessionCookie(resp *http.Response) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == middleware.SessionCookie {
			return c
		}
	}
	return nil
}

func withCookie(c *http.Cookie) muxclient.ClientDoOption {
	return withCookieValue(c.Value)
}

func withCookieValue(v string) muxclient.ClientDoOption {
	return muxclient.WithHeader(http.Header{"Cookie": {middleware.SessionCookie + "=" + v}})
}

func TestAuthFlow(t *testing.T) {
	admin := &admin_entity.Admin{ID: admin_entity.AdminID, Username: "admin"}

	convey.Convey("首次设置与会话", t, func() {
		ctx, m, testMux := setupAuthTest(t)
		m.admin.EXPECT().Get(gomock.Any()).Return(nil, nil)
		setupCode, err := auth_svc.Auth().PrepareSetupCode(ctx)
		require.NoError(t, err)

		convey.Convey("未登录访问受保护接口返回 401", func() {
			err := testMux.Do(ctx, &api.MeRequest{}, &api.MeResponse{})
			assert.Equal(t, http.StatusUnauthorized, status(err))
			assert.Equal(t, code.Unauthorized, errCode(err))
		})

		convey.Convey("跨站的写请求返回 403，同源的放行", func() {
			req := &api.SetupRequest{SetupCode: "X", Username: "admin", Password: "correct-horse-battery"}
			err := testMux.Do(ctx, req, &api.SetupResponse{},
				muxclient.WithHeader(http.Header{"Origin": {"https://evil.example"}}))
			assert.Equal(t, http.StatusForbidden, status(err))
			assert.Equal(t, code.CrossOriginRejected, errCode(err))

			m.admin.EXPECT().Get(gomock.Any()).Return(nil, nil)
			err = testMux.Do(ctx, req, &api.SetupResponse{},
				muxclient.WithHeader(http.Header{"Origin": {"http://opsnap.test"}}))
			assert.Equal(t, code.SetupCodeInvalid, errCode(err), "同源请求应进入业务处理")
		})

		convey.Convey("设置成功后写入 HttpOnly 会话 Cookie，可用它访问、退出后失效", func() {
			var created *admin_entity.Session
			m.admin.EXPECT().Get(gomock.Any()).Return(nil, nil)
			m.admin.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
			m.session.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, s *admin_entity.Session) error {
					s.ID = 7
					created = s
					return nil
				})
			var httpResp *http.Response
			resp := &api.SetupResponse{}
			err := testMux.Do(ctx, &api.SetupRequest{SetupCode: setupCode, Username: "admin", Password: "correct-horse-battery"},
				resp, muxclient.WithResponse(&httpResp))
			require.NoError(t, err)
			assert.Equal(t, "admin", resp.Username)
			cookie := sessionCookie(httpResp)
			require.NotNil(t, cookie)
			assert.True(t, cookie.HttpOnly)
			assert.Equal(t, http.SameSiteLaxMode, cookie.SameSite)
			assert.Equal(t, "/", cookie.Path)
			assert.Greater(t, cookie.MaxAge, 6*24*3600)

			m.session.EXPECT().FindByTokenHash(gomock.Any(), created.TokenHash).Return(created, nil).AnyTimes()
			m.session.EXPECT().Find(gomock.Any(), int64(7)).Return(created, nil)
			m.admin.EXPECT().Get(gomock.Any()).Return(admin, nil).AnyTimes()
			me := &api.MeResponse{}
			require.NoError(t, testMux.Do(ctx, &api.MeRequest{}, me, withCookie(cookie),
				muxclient.WithHeader(http.Header{"Cookie": {middleware.SessionCookie + "=" + cookie.Value}, "User-Agent": {"UA-Test"}})))
			assert.Equal(t, "admin", me.Username)
			require.NotNil(t, me.Session)
			assert.Equal(t, created.Expiretime, me.Session.ExpiresAt)

			convey.Convey("修改密码时当前密码错误返回 400 与对应错误码", func() {
				err := testMux.Do(ctx, &api.ChangePasswordRequest{CurrentPassword: "wrong-password-1", NewPassword: "brand-new-password"},
					&api.ChangePasswordResponse{}, withCookie(cookie))
				assert.Equal(t, http.StatusBadRequest, status(err))
				assert.Equal(t, code.CurrentPasswordWrong, errCode(err))
			})

			m.session.EXPECT().Delete(gomock.Any(), int64(7)).Return(nil)
			httpResp = nil
			require.NoError(t, testMux.Do(ctx, &api.LogoutRequest{}, &api.LogoutResponse{},
				withCookie(cookie), muxclient.WithResponse(&httpResp)))
			cleared := sessionCookie(httpResp)
			require.NotNil(t, cleared)
			assert.Less(t, cleared.MaxAge, 0)
		})

		convey.Convey("会话标识无效时返回 401 并清除 Cookie", func() {
			m.session.EXPECT().FindByTokenHash(gomock.Any(), gomock.Any()).Return(nil, nil)
			err := testMux.Do(ctx, &api.MeRequest{}, &api.MeResponse{},
				withCookieValue("forged"))
			assert.Equal(t, http.StatusUnauthorized, status(err))
		})

		convey.Convey("密码登录成功写入会话 Cookie，失败返回 400 且不写 Cookie", func() {
			hash, err := password.Hash("correct-horse-battery")
			require.NoError(t, err)
			stored := &admin_entity.Admin{ID: admin_entity.AdminID, Username: "admin", PasswordHash: hash}
			m.admin.EXPECT().Get(gomock.Any()).Return(stored, nil).Times(2)
			m.session.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

			var httpResp *http.Response
			resp := &api.LoginResponse{}
			require.NoError(t, testMux.Do(ctx, &api.LoginRequest{Username: "admin", Password: "correct-horse-battery"},
				resp, muxclient.WithResponse(&httpResp)))
			assert.Equal(t, "admin", resp.Username)
			cookie := sessionCookie(httpResp)
			require.NotNil(t, cookie)
			assert.True(t, cookie.HttpOnly)

			err = testMux.Do(ctx, &api.LoginRequest{Username: "admin", Password: "wrong-password-1"}, &api.LoginResponse{})
			assert.Equal(t, http.StatusBadRequest, status(err))
			assert.Equal(t, code.LoginFailed, errCode(err))
		})

		convey.Convey("已初始化时再次设置返回 409", func() {
			m.admin.EXPECT().Get(gomock.Any()).Return(admin, nil)
			err := testMux.Do(ctx, &api.SetupRequest{SetupCode: setupCode, Username: "admin", Password: "correct-horse-battery"},
				&api.SetupResponse{})
			assert.Equal(t, http.StatusConflict, status(err))
			assert.Equal(t, code.AlreadyInitialized, errCode(err))
		})
	})
}
