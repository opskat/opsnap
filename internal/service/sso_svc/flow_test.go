package sso_svc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authapi "github.com/opskat/opsnap/internal/api/auth"
	ssoapi "github.com/opskat/opsnap/internal/api/sso"
	"github.com/opskat/opsnap/internal/model/auth"
	"github.com/opskat/opsnap/internal/model/entity/admin_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/repository/admin_repo"
	"github.com/opskat/opsnap/internal/repository/sso_repo"
	"github.com/opskat/opsnap/internal/service/auth_svc"
)

// configure 保存指向假提供方的配置并创建管理员
func configure(t *testing.T, ctx context.Context, svc *ssoSvc, idp *fakeIdP) {
	t.Helper()
	require.NoError(t, admin_repo.Admin().Create(ctx, &admin_entity.Admin{Username: "admin", PasswordHash: "x"}))
	req := update(idp.issuer)
	req.ClientID, req.ClientSecret = idp.clientID, idp.clientSecret
	_, err := svc.Update(ctx, req)
	require.NoError(t, err)
}

// bind 走一遍绑定，绑定当前假提供方的身份
func bind(t *testing.T, ctx context.Context, svc *ssoSvc, idp *fakeIdP) {
	t.Helper()
	st, err := svc.StartBind(asAdmin(ctx))
	require.NoError(t, err)
	c, s := idp.authorize(st.URL)
	res := svc.Callback(ctx, st.Browser, &authapi.OIDCCallbackRequest{Code: c, State: s})
	require.Equal(t, "/settings?tab=sso", res.Redirect)
}

// failed 断言回调失败：跳回 wantPath 带 sso_error，不建会话，返回失败原因
func failed(t *testing.T, svc *ssoSvc, res *CallbackResult, wantPath string) string {
	t.Helper()
	require.Nil(t, res.Session)
	u, err := url.Parse(res.Redirect)
	require.NoError(t, err)
	assert.Equal(t, wantPath, u.Path)
	id := u.Query().Get("sso_error")
	require.NotEmpty(t, id)
	msg := svc.Result(id)
	require.NotEmpty(t, msg)
	return msg
}

// asAdmin 面板会话发起的请求
func asAdmin(ctx context.Context) context.Context {
	return auth.WithActor(ctx, auth.Actor{Kind: auth.KindAdmin, Name: "admin"})
}

func TestSSOBind(t *testing.T) {
	convey.Convey("单点登录：绑定账号", t, func() {
		ctx, svc, clock := setupSSOTest(t)
		idp := newFakeIdP(t, clock.Now)

		convey.Convey("未配置时不能开始绑定", func() {
			_, err := svc.StartBind(asAdmin(ctx))
			assert.Equal(t, code.SSONotConfigured, errCode(t, err))
		})

		convey.Convey("只接受面板会话，API 密钥返回 403", func() {
			configure(t, ctx, svc, idp)
			_, err := svc.StartBind(auth.WithActor(ctx, auth.Actor{Kind: auth.KindAPIKey, ID: 1, Name: "ci"}))
			var he *httputils.Error
			require.ErrorAs(t, err, &he)
			assert.Equal(t, code.SSOBindRequiresSession, he.Code)
			assert.Equal(t, http.StatusForbidden, he.Status)
			_, err = svc.StartBind(ctx)
			assert.Equal(t, code.SSOBindRequiresSession, errCode(t, err), "没有经过认证")
		})

		convey.Convey("授权地址带 PKCE、nonce、回调地址；授权后保存 issuer + sub 和显示名", func() {
			configure(t, ctx, svc, idp)
			st, err := svc.StartBind(asAdmin(ctx))
			require.NoError(t, err)
			assert.NotEmpty(t, st.Browser)
			u, _ := url.Parse(st.URL)
			assert.Equal(t, "https://opsnap.example.com/api/v1/auth/oidc/callback", u.Query().Get("redirect_uri"))
			c, s := idp.authorize(st.URL)
			clock.Advance(time.Minute)
			res := svc.Callback(ctx, st.Browser, &authapi.OIDCCallbackRequest{Code: c, State: s})
			assert.Equal(t, "/settings?tab=sso", res.Redirect)
			assert.Nil(t, res.Session, "绑定不建立新会话")

			v, err := svc.View(ctx)
			require.NoError(t, err)
			require.NotNil(t, v.Binding)
			assert.Equal(t, ssoapi.Binding{Issuer: idp.issuer, Sub: "3", Name: "alice-ops", BoundAt: clock.Now().Unix()}, *v.Binding)

			convey.Convey("没有 preferred_username 时用 name，再没有用 sub", func() {
				_, err := svc.Unbind(ctx)
				require.NoError(t, err)
				idp.mutate = func(c map[string]any) { delete(c, "preferred_username"); c["name"] = "Alice" }
				bind(t, ctx, svc, idp)
				v, _ := svc.View(ctx)
				assert.Equal(t, "Alice", v.Binding.Name)
				_, _ = svc.Unbind(ctx)
				idp.mutate = func(c map[string]any) { delete(c, "preferred_username") }
				bind(t, ctx, svc, idp)
				v, _ = svc.View(ctx)
				assert.Equal(t, "3", v.Binding.Name)
			})

			convey.Convey("解除绑定", func() {
				v, err := svc.Unbind(ctx)
				require.NoError(t, err)
				assert.Nil(t, v.Binding)
			})

			convey.Convey("修改 Issuer 或 Client ID 会解除绑定，只改按钮文字和面板地址不会", func() {
				req := update(idp.issuer)
				req.ClientID, req.ClientSecret = idp.clientID, ""
				req.ButtonLabel, req.PanelURL = "公司 Git", "https://ops.example.com"
				v, err := svc.Update(ctx, req)
				require.NoError(t, err)
				assert.NotNil(t, v.Binding)

				req.ClientID = "another-client"
				v, err = svc.Update(ctx, req)
				require.NoError(t, err)
				assert.Nil(t, v.Binding)
			})

			convey.Convey("绑定失败时回到设置页并带上原因，绑定不变", func() {
				st, err := svc.StartBind(asAdmin(ctx))
				require.NoError(t, err)
				_, s := idp.authorize(st.URL)
				res := svc.Callback(ctx, st.Browser, &authapi.OIDCCallbackRequest{State: s, Error: "access_denied"})
				msg := failed(t, svc, res, "/settings")
				assert.Contains(t, res.Redirect, "tab=sso")
				assert.Equal(t, "Gitea 拒绝了授权（access_denied）。", msg)
				v, _ := svc.View(ctx)
				assert.Equal(t, "3", v.Binding.Sub)
			})
		})
	})
}

func TestSSOLogin(t *testing.T) {
	convey.Convey("单点登录：登录", t, func() {
		ctx, svc, clock := setupSSOTest(t)
		idp := newFakeIdP(t, clock.Now)

		convey.Convey("未配置或未绑定时不能开始登录", func() {
			_, err := svc.StartLogin(ctx, "/")
			assert.Equal(t, code.SSONotEnabled, errCode(t, err))
			configure(t, ctx, svc, idp)
			_, err = svc.StartLogin(ctx, "/")
			assert.Equal(t, code.SSONotEnabled, errCode(t, err))
		})

		convey.Convey("已绑定", func() {
			configure(t, ctx, svc, idp)
			bind(t, ctx, svc, idp)
			start := func(next string) (*Start, string, string) {
				st, err := svc.StartLogin(ctx, next)
				require.NoError(t, err)
				c, s := idp.authorize(st.URL)
				return st, c, s
			}

			convey.Convey("绑定的账号登录成功：建立 7 天会话，回到登录前的页面", func() {
				st, c, s := start("/runs?status=failed")
				res := svc.Callback(ctx, st.Browser, &authapi.OIDCCallbackRequest{Code: c, State: s})
				require.NotNil(t, res.Session)
				assert.Equal(t, "/runs?status=failed", res.Redirect)
				assert.Equal(t, clock.Now().Add(7*24*time.Hour), res.Session.ExpiresAt)
				a, err := auth_svc.Auth().Authenticate(ctx, res.Session.Token, "")
				require.NoError(t, err)
				assert.Equal(t, "admin", a.Name)
			})

			convey.Convey("next 不是站内路径时回到首页", func() {
				for _, next := range []string{"", "//evil.example.com", "https://evil.example.com", "/\\evil", "/login", "/setup"} {
					st, c, s := start(next)
					res := svc.Callback(ctx, st.Browser, &authapi.OIDCCallbackRequest{Code: c, State: s})
					require.NotNil(t, res.Session, next)
					assert.Equal(t, "/", res.Redirect, next)
				}
			})

			convey.Convey("密码登录被锁定时仍可单点登录，OIDC 失败也不计入密码错误次数", func() {
				a, _ := admin_repo.Admin().Find(ctx)
				a.FailedCount, a.LockedUntil = 5, clock.Now().Add(10*time.Minute).Unix()
				require.NoError(t, admin_repo.Admin().Update(ctx, a))
				st, c, s := start("/")
				assert.NotNil(t, svc.Callback(ctx, st.Browser, &authapi.OIDCCallbackRequest{Code: c, State: s}).Session)

				a.FailedCount, a.LockedUntil = 0, 0
				require.NoError(t, admin_repo.Admin().Update(ctx, a))
				idp.sub = "4"
				st, c, s = start("/")
				failed(t, svc, svc.Callback(ctx, st.Browser, &authapi.OIDCCallbackRequest{Code: c, State: s}), "/login")
				a, _ = admin_repo.Admin().Find(ctx)
				assert.Equal(t, 0, a.FailedCount)
			})

			convey.Convey("各种拒绝：跳回登录页，显示对应原因，不建立会话", func() {
				otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
				require.NoError(t, err)
				cases := []struct {
					name  string
					setup func()
					want  string
				}{
					{"不是绑定的账号", func() { idp.sub, idp.name = "4", "bob" }, "Gitea 账号 bob 不是绑定的管理员账号，换成绑定的账号再试。"},
					{"issuer 不一致", func() { idp.mutate = func(c map[string]any) { c["iss"] = "https://evil.example.com/" } }, "Gitea 返回的身份令牌无效：issuer"},
					{"audience 不一致", func() { idp.mutate = func(c map[string]any) { c["aud"] = "other-app" } }, "Gitea 返回的身份令牌无效：audience"},
					{"签名错误", func() { idp.signKey = otherKey }, "Gitea 返回的身份令牌无效：签名"},
					{"已过期", func() {
						idp.mutate = func(c map[string]any) { c["exp"] = clock.Now().Add(-time.Minute).Unix() }
					}, "Gitea 返回的身份令牌无效：已过期"},
					{"nonce 不一致", func() { idp.mutate = func(c map[string]any) { c["nonce"] = "forged" } }, "Gitea 返回的身份令牌无效：nonce"},
					{"令牌端点拒绝", func() { idp.tokenError = "invalid_grant" }, "Gitea 拒绝了授权（invalid_grant）。"},
				}
				for _, c := range cases {
					idp.sub, idp.name, idp.mutate, idp.signKey, idp.tokenError = "3", "alice-ops", nil, nil, ""
					c.setup()
					st, code, state := start("/")
					msg := failed(t, svc, svc.Callback(ctx, st.Browser, &authapi.OIDCCallbackRequest{Code: code, State: state}), "/login")
					assert.Contains(t, msg, c.want, c.name)
					assert.NotContains(t, msg, code, c.name+"：提示里不含授权码")
				}
			})

			convey.Convey("用户在提供方拒绝授权；错误码里的异常字符不原样显示", func() {
				st, _, s := start("/")
				msg := failed(t, svc, svc.Callback(ctx, st.Browser, &authapi.OIDCCallbackRequest{State: s, Error: "access_denied"}), "/login")
				assert.Equal(t, "Gitea 拒绝了授权（access_denied）。", msg)
				st, _, s = start("/")
				msg = failed(t, svc, svc.Callback(ctx, st.Browser, &authapi.OIDCCallbackRequest{State: s, Error: "<script>alert(1)</script>"}), "/login")
				assert.NotContains(t, msg, "<script>")
				assert.Contains(t, msg, "拒绝了授权")
			})

			convey.Convey("登录请求无效：重放、超过 10 分钟、不认识的 state、换了浏览器", func() {
				want := "登录请求已过期（超过 10 分钟）或已用过，请重新点击登录。"
				st, c, s := start("/")
				require.NotNil(t, svc.Callback(ctx, st.Browser, &authapi.OIDCCallbackRequest{Code: c, State: s}).Session)
				assert.Equal(t, want, failed(t, svc, svc.Callback(ctx, st.Browser, &authapi.OIDCCallbackRequest{Code: c, State: s}), "/login"), "重放")

				st, c, s = start("/")
				clock.Advance(10*time.Minute + time.Second)
				assert.Equal(t, want, failed(t, svc, svc.Callback(ctx, st.Browser, &authapi.OIDCCallbackRequest{Code: c, State: s}), "/login"), "过期")

				assert.Equal(t, want, failed(t, svc, svc.Callback(ctx, "x", &authapi.OIDCCallbackRequest{Code: "c", State: "unknown"}), "/login"), "不认识")

				st, c, s = start("/")
				assert.Equal(t, want, failed(t, svc, svc.Callback(ctx, "another-browser", &authapi.OIDCCallbackRequest{Code: c, State: s}), "/login"), "换了浏览器")
				assert.Equal(t, want, failed(t, svc, svc.Callback(ctx, "", &authapi.OIDCCallbackRequest{Code: c, State: s}), "/login"), "没有浏览器 Cookie")
				_ = st
			})

			convey.Convey("连不上提供方", func() {
				st, c, s := start("/")
				idp.srv.Close()
				msg := failed(t, svc, svc.Callback(ctx, st.Browser, &authapi.OIDCCallbackRequest{Code: c, State: s}), "/login")
				assert.True(t, strings.HasPrefix(msg, "连不上 Gitea（"+idp.issuer+"）："), msg)
				_, err := svc.StartLogin(ctx, "/")
				assert.Equal(t, code.SSOProviderUnreachable, errCode(t, err))
			})

			convey.Convey("配置被停用后回调不再建立会话", func() {
				st, c, s := start("/")
				require.NoError(t, sso_repo.SSO().Delete(ctx))
				assert.Equal(t, "单点登录未启用。", failed(t, svc, svc.Callback(ctx, st.Browser, &authapi.OIDCCallbackRequest{Code: c, State: s}), "/login"))
			})

			convey.Convey("失败原因的 id 不认识时为空", func() {
				assert.Empty(t, svc.Result("nope"))
			})
		})
	})
}
