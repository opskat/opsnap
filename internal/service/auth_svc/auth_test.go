package auth_svc

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	api "github.com/opskat/opsnap/internal/api/auth"
	"github.com/opskat/opsnap/internal/model/entity/admin_entity"
	"github.com/opskat/opsnap/internal/pkg/authctx"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/testdb"
	"github.com/opskat/opsnap/internal/repository/admin_repo"
	"github.com/opskat/opsnap/internal/repository/oidc_repo"
	"github.com/opskat/opsnap/internal/repository/session_repo"
	"github.com/opskat/opsnap/internal/repository/setting_repo"
)

const goodPassword = "correct-horse-battery"

var meta = ClientMeta{IP: "192.0.2.10", UserAgent: "Mozilla/5.0 Test"}

func setupAuthTest(t *testing.T) (context.Context, *authSvc, *time.Time) {
	ctx := testdb.New(t)
	admin_repo.RegisterAdmin(admin_repo.NewAdmin())
	session_repo.RegisterSession(session_repo.NewSession())
	oidc_repo.RegisterOIDC(oidc_repo.NewOIDC())
	setting_repo.RegisterSetting(setting_repo.NewSetting())
	now := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	s := newAuth()
	s.now = func() time.Time { return now }
	return ctx, s, &now
}

func errCode(err error) int {
	var e *httputils.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}

func status(err error) int {
	var e *httputils.Error
	if errors.As(err, &e) {
		return e.Status
	}
	return 0
}

func setupReq(code string) *api.SetupRequest {
	return &api.SetupRequest{SetupCode: code, Username: "admin", Password: goodPassword}
}

func TestSetup(t *testing.T) {
	convey.Convey("首次设置", t, func() {
		ctx, s, _ := setupAuthTest(t)

		setupCode, err := s.PrepareSetupCode(ctx)
		require.NoError(t, err)
		assert.Regexp(t, `^[A-Z2-9]{4}-[A-Z2-9]{4}-[A-Z2-9]{4}$`, setupCode)

		st, err := s.Status(ctx, &api.StatusRequest{})
		require.NoError(t, err)
		assert.False(t, st.Initialized)

		convey.Convey("设置码错误时拒绝，且不创建管理员", func() {
			_, _, err := s.Setup(ctx, setupReq("AAAA-BBBB-CCCC"), meta)
			assert.Equal(t, code.SetupCodeInvalid, errCode(err))
			a, _ := admin_repo.Admin().Get(ctx)
			assert.Nil(t, a)
		})

		convey.Convey("用户名或密码不合规时拒绝", func() {
			for _, u := range []string{"ab", "Admin", "a b c", "admin!", "a123456789012345678901234567890123"} {
				req := setupReq(setupCode)
				req.Username = u
				_, _, err := s.Setup(ctx, req, meta)
				assert.Equal(t, code.UsernameInvalid, errCode(err), u)
			}
			req := setupReq(setupCode)
			req.Password = "short-11chr"
			_, _, err := s.Setup(ctx, req, meta)
			assert.Equal(t, code.PasswordTooShort, errCode(err))
		})

		convey.Convey("设置码忽略大小写和首尾空白", func() {
			resp, _, err := s.Setup(ctx, setupReq("  "+strings.ToLower(setupCode)+" "), meta)
			require.NoError(t, err)
			assert.Equal(t, "admin", resp.Username)
		})

		convey.Convey("成功后创建管理员与会话，只保存哈希", func() {
			resp, issued, err := s.Setup(ctx, setupReq(setupCode), meta)
			require.NoError(t, err)
			assert.Equal(t, "admin", resp.Username)
			require.NotEmpty(t, issued.Token)

			a, _ := admin_repo.Admin().Get(ctx)
			require.NotNil(t, a)
			assert.NotContains(t, a.PasswordHash, goodPassword)
			var sessions []admin_entity.Session
			require.NoError(t, db.Ctx(ctx).Find(&sessions).Error)
			require.Len(t, sessions, 1)
			assert.NotEqual(t, issued.Token, sessions[0].TokenHash)
			assert.Equal(t, meta.IP, sessions[0].IP)
			assert.Equal(t, meta.UserAgent, sessions[0].UserAgent)

			st, _ := s.Status(ctx, &api.StatusRequest{})
			assert.True(t, st.Initialized)

			convey.Convey("再次设置返回已初始化，即使设置码正确", func() {
				_, _, err := s.Setup(ctx, setupReq(setupCode), meta)
				assert.Equal(t, code.AlreadyInitialized, errCode(err))
			})

			convey.Convey("之后不再生成设置码", func() {
				c, err := s.PrepareSetupCode(ctx)
				require.NoError(t, err)
				assert.Empty(t, c)
			})
		})

		convey.Convey("并发提交只有一个成功", func() {
			var wg sync.WaitGroup
			results := make([]error, 8)
			for i := range results {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, _, results[i] = s.Setup(ctx, setupReq(setupCode), meta)
				}()
			}
			wg.Wait()
			ok, done := 0, 0
			for _, err := range results {
				switch {
				case err == nil:
					ok++
				case errCode(err) == code.AlreadyInitialized:
					done++
				default:
					t.Errorf("意外错误: %v", err)
				}
			}
			assert.Equal(t, 1, ok)
			assert.Equal(t, len(results)-1, done)
		})
	})
}

func TestSession(t *testing.T) {
	convey.Convey("会话", t, func() {
		ctx, s, now := setupAuthTest(t)
		setupCode, _ := s.PrepareSetupCode(ctx)
		_, issued, err := s.Setup(ctx, setupReq(setupCode), meta)
		require.NoError(t, err)

		convey.Convey("有效会话可以认证", func() {
			p, _, err := s.AuthenticateSession(ctx, issued.Token)
			require.NoError(t, err)
			assert.Equal(t, "admin", p.Username)
			assert.Equal(t, authctx.ViaSession, p.Via)
		})

		convey.Convey("未知或空的会话标识返回未登录", func() {
			_, _, err := s.AuthenticateSession(ctx, "nope")
			assert.Equal(t, code.Unauthorized, errCode(err))
			_, _, err = s.AuthenticateSession(ctx, "")
			assert.Equal(t, code.Unauthorized, errCode(err))
		})

		convey.Convey("连续 7 天不使用即过期", func() {
			*now = now.Add(admin_entity.SessionTTL)
			_, _, err := s.AuthenticateSession(ctx, issued.Token)
			assert.Equal(t, code.Unauthorized, errCode(err))
		})

		convey.Convey("每次使用顺延过期时间", func() {
			*now = now.Add(6 * 24 * time.Hour)
			_, refreshed, err := s.AuthenticateSession(ctx, issued.Token)
			require.NoError(t, err)
			require.NotNil(t, refreshed)
			assert.Equal(t, now.Add(admin_entity.SessionTTL).Unix(), refreshed.Expires.Unix())
			*now = now.Add(6 * 24 * time.Hour)
			_, _, err = s.AuthenticateSession(ctx, issued.Token)
			assert.NoError(t, err, "顺延后 12 天时仍有效")
		})

		convey.Convey("一分钟内的重复使用不重复写库", func() {
			*now = now.Add(30 * time.Second)
			_, refreshed, err := s.AuthenticateSession(ctx, issued.Token)
			require.NoError(t, err)
			assert.Nil(t, refreshed)
		})

		convey.Convey("退出后会话立即失效", func() {
			p, _, _ := s.AuthenticateSession(ctx, issued.Token)
			require.NoError(t, s.Logout(ctx, p))
			_, _, err := s.AuthenticateSession(ctx, issued.Token)
			assert.Equal(t, code.Unauthorized, errCode(err))
		})
	})
}
