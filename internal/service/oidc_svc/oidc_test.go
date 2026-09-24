package oidc_svc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authapi "github.com/opskat/opsnap/internal/api/auth"
	api "github.com/opskat/opsnap/internal/api/oidc"
	"github.com/opskat/opsnap/internal/pkg/authctx"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/fakeidp"
	"github.com/opskat/opsnap/internal/pkg/testdb"
	"github.com/opskat/opsnap/internal/repository/admin_repo"
	"github.com/opskat/opsnap/internal/repository/oidc_repo"
	"github.com/opskat/opsnap/internal/repository/session_repo"
	"github.com/opskat/opsnap/internal/repository/setting_repo"
	"github.com/opskat/opsnap/internal/service/auth_svc"
	"github.com/opskat/opsnap/internal/service/secret_svc"
)

const redirectURL = "http://opsnap.test/api/v1/auth/oidc/callback"

var meta = auth_svc.ClientMeta{IP: "192.0.2.1", UserAgent: "UA"}

func errCode(err error) int {
	var e *httputils.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}

type env struct {
	ctx  context.Context
	s    *oidcSvc
	idp  *fakeidp.Server
	now  *time.Time
	sess context.Context // 带浏览器会话身份的上下文
}

func setupOIDCTest(t *testing.T) *env {
	ctx := testdb.New(t)
	setting_repo.RegisterSetting(setting_repo.NewSetting())
	admin_repo.RegisterAdmin(admin_repo.NewAdmin())
	session_repo.RegisterSession(session_repo.NewSession())
	oidc_repo.RegisterOIDC(oidc_repo.NewOIDC())
	_, err := secret_svc.Secret().Init(ctx, secret_svc.InitOptions{DataDir: t.TempDir()})
	require.NoError(t, err)

	setupCode, _ := auth_svc.Auth().PrepareSetupCode(ctx)
	_, issued, err := auth_svc.Auth().Setup(ctx, &authapi.SetupRequest{SetupCode: setupCode, Username: "admin", Password: "correct-horse-battery"}, meta)
	require.NoError(t, err)
	p, _, err := auth_svc.Auth().AuthenticateSession(ctx, issued.Token)
	require.NoError(t, err)

	srv := httptest.NewUnstartedServer(nil)
	srv.Start()
	t.Cleanup(srv.Close)
	idp, err := fakeidp.New(srv.URL, "opsnap", "s3cret")
	require.NoError(t, err)
	srv.Config.Handler = idp.Handler()

	now := time.Now()
	s := newOIDC()
	s.now = func() time.Time { return now }
	return &env{ctx: ctx, s: s, idp: idp, now: &now, sess: authctx.With(ctx, p)}
}

func (e *env) save(t *testing.T, mutate ...func(*api.SaveConfigRequest)) (*api.ConfigResponse, error) {
	t.Helper()
	req := &api.SaveConfigRequest{
		DisplayName: "Keycloak", Issuer: e.idp.Issuer, ClientID: "opsnap", ClientSecret: "s3cret",
		RedirectURL: redirectURL,
	}
	for _, m := range mutate {
		m(req)
	}
	return e.s.SaveConfig(e.sess, req)
}

// followAuthorize 模拟浏览器访问 IdP 授权地址，返回回跳到 OpsNap 的查询参数
func followAuthorize(t *testing.T, authURL string) url.Values {
	t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, authURL, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusFound, resp.StatusCode)
	loc, err := url.Parse(resp.Header.Get("Location"))
	require.NoError(t, err)
	require.True(t, len(loc.Query().Get("state")) > 0)
	return loc.Query()
}

func callbackReq(q url.Values) *api.CallbackRequest {
	return &api.CallbackRequest{State: q.Get("state"), Code: q.Get("code"), Error: q.Get("error"), ErrorDescription: q.Get("error_description")}
}

func (e *env) bind(t *testing.T) {
	t.Helper()
	authURL, err := e.s.BeginBind(e.sess)
	require.NoError(t, err)
	res := e.s.Callback(e.ctx, callbackReq(followAuthorize(t, authURL)), meta)
	require.Empty(t, res.ErrorKind, res.ErrorDescription)
	require.Equal(t, ModeBind, res.Mode)
}

func (e *env) login(t *testing.T, next string) *CallbackResult {
	t.Helper()
	authURL, err := e.s.BeginLogin(e.ctx, next)
	require.NoError(t, err)
	return e.s.Callback(e.ctx, callbackReq(followAuthorize(t, authURL)), meta)
}

func TestOIDCConfig(t *testing.T) {
	convey.Convey("OIDC 配置", t, func() {
		e := setupOIDCTest(t)

		convey.Convey("保存时拉取 discovery；Client Secret 加密落库且不返回", func() {
			resp, err := e.save(t)
			require.NoError(t, err)
			assert.True(t, resp.Configured)
			assert.True(t, resp.HasSecret)
			assert.Equal(t, []string{"openid", "profile", "email"}, resp.Scopes, "Scopes 默认值")
			assert.Equal(t, redirectURL, resp.RedirectURL)
			assert.Nil(t, resp.Binding)

			var raw string
			require.NoError(t, db.Ctx(e.ctx).Raw(`SELECT value FROM settings WHERE key = 'oidc_provider'`).Scan(&raw).Error)
			assert.NotContains(t, raw, "s3cret")

			convey.Convey("编辑时 Client Secret 留空表示不修改", func() {
				_, err := e.save(t, func(r *api.SaveConfigRequest) { r.ClientSecret = ""; r.DisplayName = "SSO" })
				require.NoError(t, err)
				e.bind(t) // 仍能用原 Secret 换取令牌
			})
		})

		convey.Convey("首次配置时 Client Secret 必填", func() {
			_, err := e.save(t, func(r *api.SaveConfigRequest) { r.ClientSecret = "" })
			assert.Equal(t, code.OIDCFieldRequired, errCode(err))
		})

		convey.Convey("回调地址必须是 http(s) 且路径固定", func() {
			_, err := e.save(t, func(r *api.SaveConfigRequest) { r.RedirectURL = "http://opsnap.test/elsewhere" })
			assert.Equal(t, code.OIDCRedirectInvalid, errCode(err))
			_, err = e.save(t, func(r *api.SaveConfigRequest) { r.RedirectURL = "javascript:alert(1)" })
			assert.Equal(t, code.OIDCRedirectInvalid, errCode(err))
		})

		convey.Convey("无法连接 Issuer 时不保存并说明原因", func() {
			_, err := e.save(t, func(r *api.SaveConfigRequest) { r.Issuer = "http://127.0.0.1:1" })
			assert.Equal(t, code.OIDCIssuerUnreachable, errCode(err))
			assert.Contains(t, err.Error(), "http://127.0.0.1:1")
			cfg, _ := e.s.GetConfig(e.sess, &api.GetConfigRequest{})
			assert.False(t, cfg.Configured)
		})

		convey.Convey("Issuer 与 discovery 文档不一致时不保存", func() {
			_, err := e.save(t, func(r *api.SaveConfigRequest) { r.Issuer = e.idp.Issuer + "/" })
			assert.Equal(t, code.OIDCDiscoveryInvalid, errCode(err))
		})

		convey.Convey("已绑定时修改 Issuer 或 Client ID 需要确认，确认后清除绑定", func() {
			_, err := e.save(t)
			require.NoError(t, err)
			e.bind(t)
			_, err = e.save(t, func(r *api.SaveConfigRequest) { r.ClientID = "other" })
			assert.Equal(t, code.OIDCResetConfirmRequired, errCode(err))
			cfg, _ := e.s.GetConfig(e.sess, &api.GetConfigRequest{})
			assert.NotNil(t, cfg.Binding, "未确认时不修改")

			resp, err := e.save(t, func(r *api.SaveConfigRequest) { r.ClientID = "other"; r.ConfirmReset = true })
			require.NoError(t, err)
			assert.Nil(t, resp.Binding)
			assert.Equal(t, "other", resp.ClientID)

			convey.Convey("只改显示名称不需要确认，也不清除绑定", func() {
				e.idp.ClientID = "other"
				e.bind(t)
				resp, err := e.save(t, func(r *api.SaveConfigRequest) { r.ClientID = "other"; r.DisplayName = "SSO" })
				require.NoError(t, err)
				assert.NotNil(t, resp.Binding)
			})
		})
	})
}

func TestOIDCBindAndLogin(t *testing.T) {
	convey.Convey("OIDC 绑定与登录", t, func() {
		e := setupOIDCTest(t)

		convey.Convey("未配置时不能发起登录或绑定", func() {
			_, err := e.s.BeginLogin(e.ctx, "/")
			assert.Equal(t, code.OIDCNotConfigured, errCode(err))
			_, err = e.s.BeginBind(e.sess)
			assert.Equal(t, code.OIDCNotConfigured, errCode(err))
		})

		_, err := e.save(t)
		require.NoError(t, err)

		convey.Convey("未绑定时登录按钮不可用", func() {
			_, err := e.s.BeginLogin(e.ctx, "/")
			assert.Equal(t, code.OIDCNotConfigured, errCode(err))
		})

		convey.Convey("授权请求带 state、nonce 与 PKCE（S256）", func() {
			authURL, err := e.s.BeginBind(e.sess)
			require.NoError(t, err)
			q := mustQuery(t, authURL)
			assert.NotEmpty(t, q.Get("state"))
			assert.NotEmpty(t, q.Get("nonce"))
			assert.NotEmpty(t, q.Get("code_challenge"))
			assert.Equal(t, "S256", q.Get("code_challenge_method"))
			assert.Equal(t, redirectURL, q.Get("redirect_uri"))
		})

		convey.Convey("同时发起两次绑定：先完成的生效，后完成的被拒绝且不覆盖已绑定的身份", func() {
			first, err := e.s.BeginBind(e.sess)
			require.NoError(t, err)
			second, err := e.s.BeginBind(e.sess)
			require.NoError(t, err)
			res := e.s.Callback(e.ctx, callbackReq(followAuthorize(t, first)), meta)
			require.Empty(t, res.ErrorKind, res.ErrorDescription)

			e.idp.SetNext(fakeidp.Behavior{Subject: "user-2", Email: "other@example.com"})
			res = e.s.Callback(e.ctx, callbackReq(followAuthorize(t, second)), meta)
			assert.Equal(t, ModeBind, res.Mode)
			assert.Equal(t, ErrAlreadyBound, res.ErrorKind)
			cfg, _ := e.s.GetConfig(e.sess, &api.GetConfigRequest{})
			require.NotNil(t, cfg.Binding)
			assert.Equal(t, "user-1", cfg.Binding.Subject)
		})

		convey.Convey("绑定后记录身份，显示邮箱", func() {
			e.bind(t)
			cfg, _ := e.s.GetConfig(e.sess, &api.GetConfigRequest{})
			require.NotNil(t, cfg.Binding)
			assert.Equal(t, "user-1", cfg.Binding.Subject)
			assert.Equal(t, "ops@example.com", cfg.Binding.Display)
			assert.Zero(t, cfg.Binding.LastLoginAt)

			convey.Convey("已绑定时不能再次绑定", func() {
				_, err := e.s.BeginBind(e.sess)
				assert.Equal(t, code.OIDCAlreadyBound, errCode(err))
			})

			convey.Convey("绑定的身份登录成功：创建会话、回到 next、记录已通过 OIDC 登录", func() {
				res := e.login(t, "/jobs?x=1")
				require.Empty(t, res.ErrorKind, res.ErrorDescription)
				assert.Equal(t, ModeLogin, res.Mode)
				assert.Equal(t, "/jobs?x=1", res.Next)
				require.NotNil(t, res.Session)
				p, _, err := auth_svc.Auth().AuthenticateSession(e.ctx, res.Session.Token)
				require.NoError(t, err)
				assert.Equal(t, "admin", p.Username)
				cfg, _ := e.s.GetConfig(e.sess, &api.GetConfigRequest{})
				assert.NotZero(t, cfg.Binding.LastLoginAt)
			})

			convey.Convey("站外 next 被忽略", func() {
				res := e.login(t, "//evil.example/x")
				assert.Equal(t, "/", res.Next)
			})

			convey.Convey("其他身份登录：未绑定", func() {
				e.idp.SetNext(fakeidp.Behavior{Subject: "someone-else", Email: "x@example.com"})
				res := e.login(t, "/")
				assert.Equal(t, ErrNotBound, res.ErrorKind)
				assert.Nil(t, res.Session)
			})

			convey.Convey("用户在 IdP 取消：带上 IdP 的错误描述", func() {
				e.idp.SetNext(fakeidp.Behavior{Error: "access_denied", ErrorDescription: "User canceled"})
				res := e.login(t, "/")
				assert.Equal(t, ErrIdP, res.ErrorKind)
				assert.Equal(t, "User canceled", res.ErrorDescription)
			})

			convey.Convey("ID Token 的签名、audience、nonce 或有效期不对：校验失败", func() {
				for _, b := range []fakeidp.Behavior{
					{Subject: "user-1", WrongSignature: true},
					{Subject: "user-1", WrongAudience: true},
					{Subject: "user-1", WrongNonce: true},
					{Subject: "user-1", Expired: true},
				} {
					e.idp.SetNext(b)
					res := e.login(t, "/")
					assert.Equal(t, ErrInvalid, res.ErrorKind, "%+v", b)
					assert.Nil(t, res.Session)
				}
			})

			convey.Convey("state 不存在、重复使用或超过 10 分钟：校验失败", func() {
				authURL, _ := e.s.BeginLogin(e.ctx, "/")
				q := followAuthorize(t, authURL)
				forged := url.Values{"state": {"forged"}, "code": {q.Get("code")}}
				assert.Equal(t, ErrInvalid, e.s.Callback(e.ctx, callbackReq(forged), meta).ErrorKind)

				res := e.s.Callback(e.ctx, callbackReq(q), meta)
				require.Empty(t, res.ErrorKind)
				assert.Equal(t, ErrInvalid, e.s.Callback(e.ctx, callbackReq(q), meta).ErrorKind, "state 只能用一次")

				authURL, _ = e.s.BeginLogin(e.ctx, "/")
				q = followAuthorize(t, authURL)
				*e.now = e.now.Add(11 * time.Minute)
				assert.Equal(t, ErrInvalid, e.s.Callback(e.ctx, callbackReq(q), meta).ErrorKind)
			})

			convey.Convey("PKCE 的 code_verifier 不匹配：IdP 拒绝换取令牌，校验失败", func() {
				authURL, err := e.s.BeginLogin(e.ctx, "/")
				require.NoError(t, err)
				q := followAuthorize(t, authURL)
				e.s.mu.Lock()
				e.s.pending[q.Get("state")].verifier = "forged-verifier-forged-verifier-forged-verifier"
				e.s.mu.Unlock()
				res := e.s.Callback(e.ctx, callbackReq(q), meta)
				assert.Equal(t, ErrInvalid, res.ErrorKind)
				assert.Nil(t, res.Session)
			})

			convey.Convey("换取令牌时 IdP 不可达：无法连接", func() {
				authURL, _ := e.s.BeginLogin(e.ctx, "/")
				q := followAuthorize(t, authURL)
				p, _ := oidc_repo.OIDC().GetProvider(e.ctx)
				p.Issuer = "http://127.0.0.1:1"
				require.NoError(t, oidc_repo.OIDC().SaveProvider(e.ctx, p))
				assert.Equal(t, ErrUnreachable, e.s.Callback(e.ctx, callbackReq(q), meta).ErrorKind)
			})

			convey.Convey("解除绑定后不能再用 OIDC 登录", func() {
				_, err := e.s.Unbind(e.sess, &api.UnbindRequest{})
				require.NoError(t, err)
				_, err = e.s.BeginLogin(e.ctx, "/")
				assert.Equal(t, code.OIDCNotConfigured, errCode(err))
			})
		})
	})
}

func mustQuery(t *testing.T, raw string) url.Values {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	return u.Query()
}

func passwordLogin(t *testing.T, e *env) bool {
	t.Helper()
	st, err := auth_svc.Auth().Status(e.ctx, &authapi.StatusRequest{})
	require.NoError(t, err)
	return st.PasswordLogin
}

func TestPasswordLoginToggle(t *testing.T) {
	convey.Convey("登录方式卡片中的密码登录开关", t, func() {
		e := setupOIDCTest(t)
		_, err := e.save(t)
		require.NoError(t, err)

		convey.Convey("未绑定时不能关闭", func() {
			_, err := e.s.SetPasswordLogin(e.sess, &api.SetPasswordLoginRequest{Enabled: false})
			assert.Equal(t, code.PasswordLoginDisableNotAllowed, errCode(err))
			assert.True(t, passwordLogin(t, e))
		})

		convey.Convey("已绑定但从未用 OIDC 登录过时不能关闭", func() {
			e.bind(t)
			cfg, _ := e.s.GetConfig(e.sess, &api.GetConfigRequest{})
			assert.False(t, cfg.CanDisablePasswordLogin)
			_, err := e.s.SetPasswordLogin(e.sess, &api.SetPasswordLoginRequest{Enabled: false})
			assert.Equal(t, code.PasswordLoginDisableNotAllowed, errCode(err))
		})

		convey.Convey("绑定并用 OIDC 登录过之后可以关闭，也可以再开启", func() {
			e.bind(t)
			require.Empty(t, e.login(t, "/").ErrorKind)
			cfg, _ := e.s.GetConfig(e.sess, &api.GetConfigRequest{})
			assert.True(t, cfg.CanDisablePasswordLogin)
			resp, err := e.s.SetPasswordLogin(e.sess, &api.SetPasswordLoginRequest{Enabled: false})
			require.NoError(t, err)
			assert.False(t, resp.PasswordLogin)
			assert.False(t, passwordLogin(t, e))

			convey.Convey("关闭后 OIDC 登录不受影响", func() {
				assert.Empty(t, e.login(t, "/").ErrorKind)
			})

			convey.Convey("解除绑定时自动重新开启", func() {
				_, err := e.s.Unbind(e.sess, &api.UnbindRequest{})
				require.NoError(t, err)
				assert.True(t, passwordLogin(t, e))
			})

			convey.Convey("确认修改 Client ID（清除绑定）时自动重新开启", func() {
				_, err := e.save(t, func(r *api.SaveConfigRequest) { r.ClientID = "other"; r.ConfirmReset = true })
				require.NoError(t, err)
				assert.True(t, passwordLogin(t, e))
			})

			convey.Convey("重新开启", func() {
				resp, err := e.s.SetPasswordLogin(e.sess, &api.SetPasswordLoginRequest{Enabled: true})
				require.NoError(t, err)
				assert.True(t, resp.PasswordLogin)
			})
		})
	})
}

func TestSafeNext(t *testing.T) {
	cases := map[string]string{
		"/jobs?x=1":        "/jobs?x=1",
		"":                 "/",
		"//evil.com":       "/",
		"/\\evil.com":      "/",
		"https://evil.com": "/",
		// 浏览器解析跳转地址时会删掉制表符与换行，"/\t/evil.com" 会变成 "//evil.com"
		"/\t/evil.com": "/",
		"/\n/evil.com": "/",
		"/\r/evil.com": "/",
		"/jobs\x00":    "/",
	}
	for in, want := range cases {
		assert.Equal(t, want, SafeNext(in), "next=%q", in)
	}
}

func TestPendingBounded(t *testing.T) {
	e := setupOIDCTest(t)
	_, err := e.save(t)
	require.NoError(t, err)
	e.bind(t)
	// 公开的发起登录接口每次都会新增一条待回调记录；数量必须有上限，避免被刷爆内存
	for i := 0; i < maxPending+5; i++ {
		e.s.pending[fmt.Sprintf("state-%d", i)] = &pending{mode: ModeLogin, created: *e.now}
	}
	authURL, err := e.s.BeginLogin(e.ctx, "/")
	require.NoError(t, err)
	assert.LessOrEqual(t, len(e.s.pending), maxPending)
	// 新发起的登录不会被挤掉
	res := e.s.Callback(e.ctx, callbackReq(followAuthorize(t, authURL)), meta)
	assert.Empty(t, res.ErrorKind)
}

func TestOIDCReauth(t *testing.T) {
	convey.Convey("用 OIDC 再次验证身份（密码登录关闭时查看密钥）", t, func() {
		e := setupOIDCTest(t)
		_, err := e.save(t)
		require.NoError(t, err)
		sessionID := authctx.From(e.sess).SessionID

		convey.Convey("未绑定时不能发起", func() {
			_, err := e.s.BeginReauth(e.sess, "/storage")
			assert.Equal(t, code.OIDCNotConfigured, errCode(err))
		})

		convey.Convey("已绑定", func() {
			e.bind(t)

			convey.Convey("只能由浏览器会话发起", func() {
				_, err := e.s.BeginReauth(e.ctx, "/storage")
				assert.Equal(t, code.SessionRequired, errCode(err))
			})

			convey.Convey("要求 IdP 重新登录，不能靠已有的 IdP 会话静默通过", func() {
				authURL, err := e.s.BeginReauth(e.sess, "/storage")
				require.NoError(t, err)
				u, err := url.Parse(authURL)
				require.NoError(t, err)
				assert.Equal(t, "login", u.Query().Get("prompt"))

				loginURL, err := e.s.BeginLogin(e.ctx, "/")
				require.NoError(t, err)
				u, err = url.Parse(loginURL)
				require.NoError(t, err)
				assert.Empty(t, u.Query().Get("prompt"), "普通登录不强制重新登录")
			})

			reauth := func(next string) *CallbackResult {
				authURL, err := e.s.BeginReauth(e.sess, next)
				require.NoError(t, err)
				return e.s.Callback(e.ctx, callbackReq(followAuthorize(t, authURL)), meta)
			}

			convey.Convey("绑定的身份回来后：回到 next，发起的会话获得一次查看授权，不创建新会话", func() {
				res := reauth("/storage?reveal=3")
				require.Empty(t, res.ErrorKind, res.ErrorDescription)
				assert.Equal(t, ModeReauth, res.Mode)
				assert.Equal(t, "/storage?reveal=3", res.Next)
				assert.Nil(t, res.Session)
				assert.True(t, auth_svc.Auth().ConsumeReauth(sessionID))
			})

			convey.Convey("其他身份回来：未绑定，不授权", func() {
				e.idp.SetNext(fakeidp.Behavior{Subject: "someone-else", Email: "x@example.com"})
				res := reauth("/storage")
				assert.Equal(t, ErrNotBound, res.ErrorKind)
				assert.False(t, auth_svc.Auth().ConsumeReauth(sessionID))
			})
		})
	})
}
