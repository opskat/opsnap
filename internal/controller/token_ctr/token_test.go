package token_ctr

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

	authapi "github.com/opskat/opsnap/internal/api/auth"
	api "github.com/opskat/opsnap/internal/api/token"
	"github.com/opskat/opsnap/internal/controller/auth_ctr"
	"github.com/opskat/opsnap/internal/middleware"
	"github.com/opskat/opsnap/internal/model/entity/admin_entity"
	"github.com/opskat/opsnap/internal/model/entity/token_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/repository/admin_repo"
	mock_admin_repo "github.com/opskat/opsnap/internal/repository/admin_repo/mock"
	"github.com/opskat/opsnap/internal/repository/session_repo"
	mock_session_repo "github.com/opskat/opsnap/internal/repository/session_repo/mock"
	"github.com/opskat/opsnap/internal/repository/token_repo"
	mock_token_repo "github.com/opskat/opsnap/internal/repository/token_repo/mock"
)

type mocks struct {
	admin   *mock_admin_repo.MockAdminRepo
	session *mock_session_repo.MockSessionRepo
	token   *mock_token_repo.MockTokenRepo
}

func setupTokenTest(t *testing.T) (context.Context, *mocks, *muxtest.TestMux) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	m := &mocks{
		admin:   mock_admin_repo.NewMockAdminRepo(ctrl),
		session: mock_session_repo.NewMockSessionRepo(ctrl),
		token:   mock_token_repo.NewMockTokenRepo(ctrl),
	}
	admin_repo.RegisterAdmin(m.admin)
	session_repo.RegisterSession(m.session)
	token_repo.RegisterToken(m.token)

	testMux := muxtest.NewTestMux(muxtest.WithBaseUrl("http://opsnap.test/api/v1"))
	authCtr, tokenCtr := auth_ctr.NewAuth(), NewToken()
	r := testMux.Group("/api/v1", middleware.SameOrigin())
	authed := r.Group("/", middleware.Auth())
	authed.Bind(authCtr.Me)
	authed.Group("/", middleware.RequireSession()).Bind(authCtr.ChangePassword, tokenCtr.List, tokenCtr.Create, tokenCtr.Revoke)
	return context.Background(), m, testMux
}

func httpErr(err error) (int, int) {
	var e *httputils.Error
	if errors.As(err, &e) {
		return e.Status, e.Code
	}
	return 0, 0
}

func bearer(tok string) muxclient.ClientDoOption {
	return muxclient.WithHeader(http.Header{"Authorization": {"Bearer " + tok}})
}

func TestTokenAuth(t *testing.T) {
	admin := &admin_entity.Admin{ID: admin_entity.AdminID, Username: "admin"}

	convey.Convey("使用 API 令牌访问", t, func() {
		ctx, m, testMux := setupTokenTest(t)
		active := &token_entity.Token{ID: 3, Name: "ci", Lastusedtime: 0}

		convey.Convey("有效令牌可以调用业务接口，并更新最近使用时间", func() {
			m.token.EXPECT().FindByHash(gomock.Any(), gomock.Any()).Return(active, nil)
			m.admin.EXPECT().Get(gomock.Any()).Return(admin, nil).Times(2)
			m.token.EXPECT().TouchLastUsed(gomock.Any(), int64(3), gomock.Any()).Return(nil)
			me := &authapi.MeResponse{}
			require.NoError(t, testMux.Do(ctx, &authapi.MeRequest{}, me, bearer("onp_valid")))
			assert.Equal(t, "admin", me.Username)
			assert.Nil(t, me.Session, "令牌访问没有浏览器会话信息")
		})

		convey.Convey("账号类接口对令牌返回 403", func() {
			m.token.EXPECT().FindByHash(gomock.Any(), gomock.Any()).Return(active, nil).Times(3)
			m.admin.EXPECT().Get(gomock.Any()).Return(admin, nil).Times(3)
			m.token.EXPECT().TouchLastUsed(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
			for _, req := range []any{
				&api.ListRequest{},
				&api.CreateRequest{Name: "x", ExpiresInDays: 30},
				&authapi.ChangePasswordRequest{CurrentPassword: "a", NewPassword: "b"},
			} {
				status, c := httpErr(testMux.Do(ctx, req, &struct{}{}, bearer("onp_valid")))
				assert.Equal(t, http.StatusForbidden, status)
				assert.Equal(t, code.SessionRequired, c)
			}
		})

		convey.Convey("令牌请求不受 Origin 检查限制", func() {
			m.token.EXPECT().FindByHash(gomock.Any(), gomock.Any()).Return(active, nil)
			m.admin.EXPECT().Get(gomock.Any()).Return(admin, nil)
			m.token.EXPECT().TouchLastUsed(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			status, c := httpErr(testMux.Do(ctx, &api.CreateRequest{Name: "x", ExpiresInDays: 30}, &struct{}{},
				muxclient.WithHeader(http.Header{"Authorization": {"Bearer onp_valid"}, "Origin": {"https://ci.example"}})))
			assert.Equal(t, http.StatusForbidden, status)
			assert.Equal(t, code.SessionRequired, c, "被拒绝的原因应是账号类接口，而不是跨站检查")
		})

		convey.Convey("无效、已吊销、已过期的令牌分别返回 401 与对应提示", func() {
			m.token.EXPECT().FindByHash(gomock.Any(), gomock.Any()).Return(nil, nil)
			status, c := httpErr(testMux.Do(ctx, &authapi.MeRequest{}, &authapi.MeResponse{}, bearer("onp_unknown")))
			assert.Equal(t, http.StatusUnauthorized, status)
			assert.Equal(t, code.TokenInvalid, c)

			m.token.EXPECT().FindByHash(gomock.Any(), gomock.Any()).Return(&token_entity.Token{ID: 4, Revoketime: 1}, nil)
			_, c = httpErr(testMux.Do(ctx, &authapi.MeRequest{}, &authapi.MeResponse{}, bearer("onp_revoked")))
			assert.Equal(t, code.TokenRevoked, c)

			m.token.EXPECT().FindByHash(gomock.Any(), gomock.Any()).Return(&token_entity.Token{ID: 5, Expiretime: 1}, nil)
			_, c = httpErr(testMux.Do(ctx, &authapi.MeRequest{}, &authapi.MeResponse{}, bearer("onp_expired")))
			assert.Equal(t, code.TokenExpired, c)
		})
	})
}
