package auth_svc

import (
	"net/http"
	"testing"
	"time"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	api "github.com/opskat/opsnap/internal/api/auth"
	"github.com/opskat/opsnap/internal/pkg/code"
)

func TestReauthenticate(t *testing.T) {
	convey.Convey("查看密钥前再次验证身份", t, func() {
		ctx, s, now := setupAuthTest(t)
		setupCode, _ := s.PrepareSetupCode(ctx)
		_, _, err := s.Setup(ctx, setupReq(setupCode), meta)
		require.NoError(t, err)

		convey.Convey("密码正确时通过", func() {
			assert.NoError(t, s.VerifyPassword(ctx, goodPassword, meta))
		})

		convey.Convey("密码错误时提示并计入登录保护，锁定期间正确密码也被拒绝", func() {
			for range 4 {
				assert.Equal(t, code.ReauthPasswordWrong, errCode(s.VerifyPassword(ctx, "wrong", meta)))
			}
			// 第 5 次失败来自登录：两者共用同一个计数
			_, _, err := s.Login(ctx, &api.LoginRequest{Username: "admin", Password: "wrong"}, meta)
			assert.Equal(t, code.LoginFailed, errCode(err))

			err = s.VerifyPassword(ctx, goodPassword, meta)
			assert.Equal(t, http.StatusTooManyRequests, status(err))
			assert.Equal(t, code.TooManyAttempts, errCode(err))
		})

		convey.Convey("密码登录关闭时不接受密码", func() {
			require.NoError(t, s.SetPasswordLoginEnabled(ctx, false))
			err := s.VerifyPassword(ctx, goodPassword, meta)
			assert.Equal(t, http.StatusForbidden, status(err))
			assert.Equal(t, code.PasswordLoginDisabled, errCode(err))
		})

		convey.Convey("OIDC 重新验证的授权：只属于发起的会话，5 分钟内使用一次", func() {
			s.GrantReauth(7)
			assert.False(t, s.ConsumeReauth(8), "其他会话不能使用")
			assert.True(t, s.ConsumeReauth(7))
			assert.False(t, s.ConsumeReauth(7), "每次查看都要重新验证")

			s.GrantReauth(7)
			*now = now.Add(5*time.Minute + time.Second)
			assert.False(t, s.ConsumeReauth(7), "超过 5 分钟失效")
		})
	})
}
