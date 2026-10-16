package sso_svc

import (
	"net/http"
	"testing"

	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ssoapi "github.com/opskat/opsnap/internal/api/sso"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/repository/sso_repo"
	"github.com/opskat/opsnap/internal/service/auth_svc"
)

func setPW(enabled bool) *ssoapi.PasswordLoginRequest {
	return &ssoapi.PasswordLoginRequest{Enabled: enabled}
}

func TestSSOPasswordLogin(t *testing.T) {
	convey.Convey("单点登录：密码登录开关", t, func() {
		ctx, svc, clock := setupSSOTest(t)
		idp := newFakeIdP(t, clock.Now)
		auth_svc.SetPasswordLoginForced(false)
		t.Cleanup(func() { auth_svc.SetPasswordLoginForced(false) })

		convey.Convey("默认允许密码登录，没有绑定时不能关闭", func() {
			v, err := svc.View(ctx)
			require.NoError(t, err)
			assert.True(t, v.PasswordLogin)
			assert.False(t, v.PasswordLoginForced)
			_, err = svc.SetPasswordLogin(ctx, setPW(false))
			assert.Equal(t, code.SSOPasswordLoginNeedsBinding, errCode(t, err))
			configure(t, ctx, svc, idp)
			_, err = svc.SetPasswordLogin(ctx, setPW(false))
			assert.Equal(t, code.SSOPasswordLoginNeedsBinding, errCode(t, err))
		})

		convey.Convey("已绑定后可以关闭", func() {
			configure(t, ctx, svc, idp)
			bind(t, ctx, svc, idp)
			v, err := svc.SetPasswordLogin(ctx, setPW(false))
			require.NoError(t, err)
			assert.False(t, v.PasswordLogin)

			convey.Convey("关闭期间：Issuer、Client ID 不能改，不能解除绑定或停用；其他字段可以改", func() {
				req := update(idp.issuer)
				req.ClientID, req.ClientSecret = "another-client", ""
				_, err := svc.Update(ctx, req)
				var he *httputils.Error
				require.ErrorAs(t, err, &he)
				assert.Equal(t, code.SSOLockedWhilePasswordOff, he.Code)
				assert.Equal(t, http.StatusForbidden, he.Status)

				_, err = svc.Unbind(ctx)
				assert.Equal(t, code.SSOLockedWhilePasswordOff, errCode(t, err))
				_, err = svc.Disable(ctx)
				assert.Equal(t, code.SSOLockedWhilePasswordOff, errCode(t, err))

				req.ClientID, req.ButtonLabel = idp.clientID, "公司 Git"
				v, err := svc.Update(ctx, req)
				require.NoError(t, err)
				assert.Equal(t, "公司 Git", v.ButtonLabel)
				assert.NotNil(t, v.Binding)
				assert.False(t, v.PasswordLogin)
			})

			convey.Convey("已绑定时不能直接换绑另一个账号，要先解除", func() {
				_, err := svc.StartBind(asAdmin(ctx))
				assert.Equal(t, code.SSOAlreadyBound, errCode(t, err))
			})

			convey.Convey("重新打开随时可以", func() {
				v, err := svc.SetPasswordLogin(ctx, setPW(true))
				require.NoError(t, err)
				assert.True(t, v.PasswordLogin)
			})

			convey.Convey("环境变量强制打开时：锁定不生效；解除绑定、修改 Client ID、停用都把保存的开关改回开", func() {
				auth_svc.SetPasswordLoginForced(true)
				v, err := svc.View(ctx)
				require.NoError(t, err)
				assert.True(t, v.PasswordLoginForced)
				assert.False(t, v.PasswordLogin, "保存的值仍是关闭")

				v2, err := svc.Unbind(ctx)
				require.NoError(t, err)
				assert.True(t, v2.PasswordLogin)
				row, _ := sso_repo.SSO().Get(ctx)
				assert.True(t, row.PasswordLogin)

				bind(t, ctx, svc, idp)
				_, err = svc.SetPasswordLogin(ctx, setPW(false))
				require.NoError(t, err)
				req := update(idp.issuer)
				req.ClientID, req.ClientSecret = "another-client", ""
				v3, err := svc.Update(ctx, req)
				require.NoError(t, err)
				assert.Nil(t, v3.Binding)
				assert.True(t, v3.PasswordLogin)

				_, err = svc.Disable(ctx)
				require.NoError(t, err)
				v4, _ := svc.View(ctx)
				assert.True(t, v4.PasswordLogin)
			})
		})
	})
}
