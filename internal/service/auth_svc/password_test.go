package auth_svc

import (
	"testing"
	"time"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	api "github.com/opskat/opsnap/internal/api/auth"
	"github.com/opskat/opsnap/internal/pkg/authctx"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/repository/admin_repo"
)

const newPassword = "brand-new-password-42"

func TestPasswordChangeAndReset(t *testing.T) {
	convey.Convey("修改密码、账号信息与命令行重置", t, func() {
		ctx, s, now := setupAuthTest(t)
		setupCode, _ := s.PrepareSetupCode(ctx)
		_, current, err := s.Setup(ctx, setupReq(setupCode), meta)
		require.NoError(t, err)
		_, other, err := s.Login(ctx, &api.LoginRequest{Username: "admin", Password: goodPassword}, ClientMeta{IP: "198.51.100.2", UserAgent: "Other"})
		require.NoError(t, err)
		p, _, err := s.AuthenticateSession(ctx, current.Token)
		require.NoError(t, err)
		pctx := authctx.With(ctx, p)

		convey.Convey("账号信息包含密码修改时间与当前会话", func() {
			me, err := s.Me(pctx, &api.MeRequest{})
			require.NoError(t, err)
			assert.Equal(t, "admin", me.Username)
			assert.Equal(t, now.Unix(), me.PasswordUpdatedAt)
			require.NotNil(t, me.Session)
			assert.Equal(t, meta.IP, me.Session.IP)
			assert.Equal(t, meta.UserAgent, me.Session.UserAgent)
			assert.Equal(t, current.Expires.Unix(), me.Session.ExpiresAt)
		})

		convey.Convey("当前密码错误时拒绝，并计入登录保护", func() {
			for i := 0; i < 5; i++ {
				_, err := s.ChangePassword(pctx, &api.ChangePasswordRequest{CurrentPassword: "wrong-password-1", NewPassword: newPassword}, meta)
				assert.Equal(t, code.CurrentPasswordWrong, errCode(err))
			}
			_, err := s.ChangePassword(pctx, &api.ChangePasswordRequest{CurrentPassword: goodPassword, NewPassword: newPassword}, meta)
			assert.Equal(t, code.TooManyAttempts, errCode(err))
		})

		convey.Convey("新密码太短时拒绝", func() {
			_, err := s.ChangePassword(pctx, &api.ChangePasswordRequest{CurrentPassword: goodPassword, NewPassword: "short"}, meta)
			assert.Equal(t, code.PasswordTooShort, errCode(err))
		})

		convey.Convey("修改成功后其他会话失效，当前会话保留，新密码可登录", func() {
			*now = now.Add(time.Hour)
			_, err := s.ChangePassword(pctx, &api.ChangePasswordRequest{CurrentPassword: goodPassword, NewPassword: newPassword}, meta)
			require.NoError(t, err)
			_, _, err = s.AuthenticateSession(ctx, current.Token)
			assert.NoError(t, err)
			_, _, err = s.AuthenticateSession(ctx, other.Token)
			assert.Equal(t, code.Unauthorized, errCode(err))
			_, _, err = s.Login(ctx, &api.LoginRequest{Username: "admin", Password: goodPassword}, meta)
			assert.Equal(t, code.LoginFailed, errCode(err))
			_, _, err = s.Login(ctx, &api.LoginRequest{Username: "admin", Password: newPassword}, meta)
			assert.NoError(t, err)
			a, _ := admin_repo.Admin().Get(ctx)
			assert.Equal(t, now.Unix(), a.PasswordUpdatetime)
		})

		convey.Convey("命令行重置：设置新密码，所有会话失效", func() {
			_, err := s.ResetPassword(ctx, newPassword)
			require.NoError(t, err)
			_, _, err = s.AuthenticateSession(ctx, current.Token)
			assert.Equal(t, code.Unauthorized, errCode(err))
			_, _, err = s.AuthenticateSession(ctx, other.Token)
			assert.Equal(t, code.Unauthorized, errCode(err))
			_, _, err = s.Login(ctx, &api.LoginRequest{Username: "admin", Password: newPassword}, meta)
			assert.NoError(t, err)
		})

		convey.Convey("命令行重置同样校验密码长度", func() {
			_, err := s.ResetPassword(ctx, "short")
			assert.Equal(t, code.PasswordTooShort, errCode(err))
		})
	})

	convey.Convey("尚未创建管理员时命令行重置报错", t, func() {
		ctx, s, _ := setupAuthTest(t)
		_, err := s.ResetPassword(ctx, newPassword)
		assert.Equal(t, code.NotInitialized, errCode(err))
	})
}

func TestPasswordLoginSwitch(t *testing.T) {
	convey.Convey("关闭密码登录", t, func() {
		ctx, s, _ := setupAuthTest(t)
		setupCode, _ := s.PrepareSetupCode(ctx)
		_, _, err := s.Setup(ctx, setupReq(setupCode), meta)
		require.NoError(t, err)

		st, _ := s.Status(ctx, &api.StatusRequest{})
		assert.True(t, st.PasswordLogin, "默认开启")

		require.NoError(t, s.SetPasswordLoginEnabled(ctx, false))

		convey.Convey("关闭后状态接口反映，密码登录被拒绝", func() {
			st, _ := s.Status(ctx, &api.StatusRequest{})
			assert.False(t, st.PasswordLogin)
			_, _, err := s.Login(ctx, &api.LoginRequest{Username: "admin", Password: goodPassword}, meta)
			assert.Equal(t, code.PasswordLoginDisabled, errCode(err))
		})

		convey.Convey("命令行重置密码时重新开启，并在结果中说明", func() {
			res, err := s.ResetPassword(ctx, newPassword)
			require.NoError(t, err)
			assert.True(t, res.PasswordLoginReenabled)
			_, _, err = s.Login(ctx, &api.LoginRequest{Username: "admin", Password: newPassword}, meta)
			assert.NoError(t, err)

			res, err = s.ResetPassword(ctx, goodPassword)
			require.NoError(t, err)
			assert.False(t, res.PasswordLoginReenabled, "本来就开启时不说明")
		})
	})
}
