package sso_svc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/glebarez/sqlite"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	ssoapi "github.com/opskat/opsnap/internal/api/sso"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/secret"
	"github.com/opskat/opsnap/internal/repository/admin_repo"
	"github.com/opskat/opsnap/internal/repository/session_repo"
	"github.com/opskat/opsnap/internal/repository/sso_repo"
	"github.com/opskat/opsnap/internal/service/auth_svc"
	"github.com/opskat/opsnap/migrations"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time          { return c.t }
func (c *fakeClock) Advance(d time.Duration) { c.t = c.t.Add(d) }

// setupSSOTest 内存 SQLite + 真实仓储 + 测试主密钥，时钟可控
func setupSSOTest(t *testing.T) (context.Context, *ssoSvc, *fakeClock) {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, migrations.RunMigrations(gdb))
	sso_repo.RegisterSSO(sso_repo.NewSSO())
	admin_repo.RegisterAdmin(admin_repo.NewAdmin())
	session_repo.RegisterSession(session_repo.NewSession())

	old := secret.Default()
	sec, err := secret.New("test-master-key")
	require.NoError(t, err)
	secret.SetDefault(sec)
	t.Cleanup(func() { secret.SetDefault(old) })

	clock := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	oldAuth := auth_svc.Auth()
	auth_svc.RegisterAuth(auth_svc.NewAuth(clock.Now))
	t.Cleanup(func() { auth_svc.RegisterAuth(oldAuth) })
	return db.WithContextDB(context.Background(), gdb), newSSO(clock.Now), clock
}

// discoveryServer 只提供发现文档的假提供方；issuer 为空时声明自己的地址（带结尾 /）
func discoveryServer(t *testing.T, mutate func(doc map[string]any)) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		doc := map[string]any{
			"issuer":                 srv.URL + "/",
			"authorization_endpoint": srv.URL + "/login/oauth/authorize",
			"token_endpoint":         srv.URL + "/login/oauth/access_token",
			"jwks_uri":               srv.URL + "/login/oauth/keys",
		}
		if mutate != nil {
			mutate(doc)
		}
		_ = json.NewEncoder(w).Encode(doc)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func errCode(t *testing.T, err error) int {
	t.Helper()
	var he *httputils.Error
	require.ErrorAs(t, err, &he)
	return he.Code
}

func update(issuer string) *ssoapi.UpdateRequest {
	return &ssoapi.UpdateRequest{
		Issuer: issuer, ClientID: "5f0c1e2a", ClientSecret: "s3cret-client",
		ButtonLabel: "Gitea", PanelURL: "https://opsnap.example.com/",
	}
}

func TestSSOProvider(t *testing.T) {
	convey.Convey("单点登录：配置提供方", t, func() {
		ctx, svc, _ := setupSSOTest(t)

		convey.Convey("未配置时按钮文字默认是 Gitea", func() {
			v, err := svc.View(ctx)
			require.NoError(t, err)
			assert.False(t, v.Configured)
			assert.Equal(t, "Gitea", v.ButtonLabel)
			assert.Empty(t, v.ClientSecret)
		})

		convey.Convey("检查通过后保存；Client Secret 加密保存、不回显；返回回调地址", func() {
			srv := discoveryServer(t, nil)
			resp, err := svc.Update(ctx, update(srv.URL+"/"))
			require.NoError(t, err)
			assert.True(t, resp.Configured)
			assert.Equal(t, srv.URL+"/", resp.Issuer)
			assert.Equal(t, secret.Masked, resp.ClientSecret)
			assert.Equal(t, "https://opsnap.example.com/api/v1/auth/oidc/callback", resp.CallbackURL)

			row, err := sso_repo.SSO().Get(ctx)
			require.NoError(t, err)
			assert.NotEqual(t, "s3cret-client", row.ClientSecret)
			plain, err := secret.Default().Decrypt(row.ClientSecret)
			require.NoError(t, err)
			assert.Equal(t, "s3cret-client", plain)
			assert.True(t, row.PasswordLogin)

			convey.Convey("再次保存时 Client Secret 留空或为「已设置」都保留原值", func() {
				for _, keep := range []string{"", secret.Masked} {
					req := update(srv.URL + "/")
					req.ClientSecret = keep
					req.ButtonLabel = "公司 Git"
					_, err := svc.Update(ctx, req)
					require.NoError(t, err)
					row, _ := sso_repo.SSO().Get(ctx)
					plain, _ := secret.Default().Decrypt(row.ClientSecret)
					assert.Equal(t, "s3cret-client", plain)
					assert.Equal(t, "公司 Git", row.ButtonLabel)
				}
			})

			convey.Convey("停用后回到未配置", func() {
				d, err := svc.Disable(ctx)
				require.NoError(t, err)
				assert.False(t, d.Configured)
				row, _ := sso_repo.SSO().Get(ctx)
				assert.Nil(t, row)
			})
		})

		convey.Convey("第一次保存必须填写 Client Secret", func() {
			srv := discoveryServer(t, nil)
			req := update(srv.URL + "/")
			req.ClientSecret = ""
			_, err := svc.Update(ctx, req)
			assert.Equal(t, code.SSOClientSecretRequired, errCode(t, err))
		})

		convey.Convey("字段不合法时不去读取发现文档", func() {
			hit := false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
			defer srv.Close()
			req := update(srv.URL + "/")
			req.PanelURL = "opsnap.lan"
			_, err := svc.Update(ctx, req)
			assert.Equal(t, code.SSOPanelURLInvalid, errCode(t, err))
			assert.False(t, hit)
		})

		convey.Convey("检查不通过时不保存，并写明地址和原因", func() {
			cases := []struct {
				name   string
				issuer func() string
				code   int
				want   []string
			}{
				{"连不上", func() string {
					srv := httptest.NewServer(http.NotFoundHandler())
					u := srv.URL
					srv.Close()
					return u + "/"
				}, code.SSODiscoveryFailed, []string{"/.well-known/openid-configuration", "connection refused"}},
				{"HTTP 404", func() string {
					srv := httptest.NewServer(http.NotFoundHandler())
					t.Cleanup(srv.Close)
					return srv.URL + "/"
				}, code.SSODiscoveryFailed, []string{"HTTP 404"}},
				{"不是 JSON", func() string {
					srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						_, _ = w.Write([]byte("<html>gitea</html>"))
					}))
					t.Cleanup(srv.Close)
					return srv.URL + "/"
				}, code.SSODiscoveryFailed, []string{"不是合法的 OIDC 发现文档"}},
				{"缺少令牌端点", func() string {
					return discoveryServer(t, func(doc map[string]any) { delete(doc, "token_endpoint") }).URL + "/"
				}, code.SSODiscoveryFailed, []string{"token_endpoint"}},
				{"issuer 不一致（少了结尾 /）", func() string {
					return discoveryServer(t, nil).URL
				}, code.SSOIssuerMismatch, []string{"提供方声明的 issuer 是 http://"}},
			}
			for _, c := range cases {
				_, err := svc.Update(ctx, update(c.issuer()))
				require.Error(t, err, c.name)
				assert.Equal(t, c.code, errCode(t, err), c.name)
				for _, w := range c.want {
					assert.Contains(t, err.Error(), w, c.name)
				}
				row, _ := sso_repo.SSO().Get(ctx)
				assert.Nil(t, row, c.name)
			}
		})

		convey.Convey("发现文档 10 秒内没有响应算超时", func() {
			block := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-block }))
			defer func() { close(block); srv.Close() }()
			svc.discoveryTimeout = 50 * time.Millisecond
			_, err := svc.Update(ctx, update(srv.URL+"/"))
			assert.Equal(t, code.SSODiscoveryFailed, errCode(t, err))
			assert.Contains(t, err.Error(), "超时")
		})
	})
}
