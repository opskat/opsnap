package auth_svc

import (
	"context"
	"testing"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/glebarez/sqlite"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	authapi "github.com/opskat/opsnap/internal/api/auth"
	"github.com/opskat/opsnap/internal/model/auth"
	"github.com/opskat/opsnap/internal/model/entity/admin_entity"
	"github.com/opskat/opsnap/internal/model/entity/session_entity"
	"github.com/opskat/opsnap/internal/model/entity/sso_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/repository/admin_repo"
	"github.com/opskat/opsnap/internal/repository/apikey_repo"
	"github.com/opskat/opsnap/internal/repository/session_repo"
	"github.com/opskat/opsnap/internal/repository/sso_repo"
	"github.com/opskat/opsnap/migrations"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time          { return c.t }
func (c *fakeClock) Advance(d time.Duration) { c.t = c.t.Add(d) }

// setupAuthTest 用内存 SQLite 和真实仓储搭建服务，时钟可控
func setupAuthTest(t *testing.T) (context.Context, *authSvc, *fakeClock, *gorm.DB) {
	t.Helper()
	// 测试里用最低的 bcrypt 开销
	old := passwordCost
	passwordCost = bcrypt.MinCost
	t.Cleanup(func() { passwordCost = old })

	gdb, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, migrations.RunMigrations(gdb))

	admin_repo.RegisterAdmin(admin_repo.NewAdmin())
	session_repo.RegisterSession(session_repo.NewSession())
	apikey_repo.RegisterAPIKey(apikey_repo.NewAPIKey())
	sso_repo.RegisterSSO(sso_repo.NewSSO())

	clock := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	svc := newAuth(clock.Now)
	return db.WithContextDB(context.Background(), gdb), svc, clock, gdb
}

func mustSetup(t *testing.T, ctx context.Context, svc *authSvc) {
	t.Helper()
	_, err := svc.Setup(ctx, &authapi.SetupRequest{Username: "admin", Password: "correct-horse"})
	require.NoError(t, err)
}

func login(ctx context.Context, svc *authSvc, pw string) (*LoginResult, error) {
	return svc.Login(ctx, &authapi.LoginRequest{Username: "admin", Password: pw})
}

func errCode(t *testing.T, err error) int {
	t.Helper()
	var he *httputils.Error
	require.ErrorAs(t, err, &he)
	return he.Code
}

func TestSetup(t *testing.T) {
	convey.Convey("首次运行创建管理员", t, func() {
		ctx, svc, _, gdb := setupAuthTest(t)
		st, err := svc.Status(ctx)
		require.NoError(t, err)
		assert.False(t, st.Initialized)

		_, err = login(ctx, svc, "whatever")
		assert.Equal(t, code.AdminNotInitialized, errCode(t, err))

		_, err = svc.Setup(ctx, &authapi.SetupRequest{Username: "admin", Password: "short"})
		assert.Equal(t, code.PasswordTooShort, errCode(t, err))

		mustSetup(t, ctx, svc)
		st, _ = svc.Status(ctx)
		assert.True(t, st.Initialized)

		_, err = svc.Setup(ctx, &authapi.SetupRequest{Username: "other", Password: "another-password"})
		assert.Equal(t, code.AdminAlreadyInitialized, errCode(t, err))

		convey.Convey("密码只存 bcrypt 哈希", func() {
			var hash string
			require.NoError(t, gdb.Raw(`SELECT password_hash FROM admin`).Scan(&hash).Error)
			assert.NotContains(t, hash, "correct-horse")
			assert.NoError(t, bcrypt.CompareHashAndPassword([]byte(hash), []byte("correct-horse")))
		})
	})
}

func TestLoginLockout(t *testing.T) {
	convey.Convey("登录失败锁定", t, func() {
		ctx, svc, clock, _ := setupAuthTest(t)
		mustSetup(t, ctx, svc)
		convey.Convey("每次失败提示剩余次数，第五次锁定", func() {
			for _, remaining := range []string{"还剩 4 次", "还剩 3 次", "还剩 2 次", "还剩 1 次"} {
				_, err := login(ctx, svc, "wrong")
				require.Error(t, err)
				assert.Equal(t, code.LoginFailed, errCode(t, err))
				assert.Contains(t, err.Error(), remaining)
			}
			_, err := login(ctx, svc, "wrong")
			assert.Equal(t, code.LoginLocked, errCode(t, err))
			assert.Contains(t, err.Error(), "10 分钟")

			convey.Convey("锁定期间正确密码也被拒绝，且提示剩余时间", func() {
				clock.Advance(9*time.Minute + 59*time.Second)
				_, err := login(ctx, svc, "correct-horse")
				assert.Equal(t, code.LoginLocked, errCode(t, err))
				assert.Contains(t, err.Error(), "1 分钟")
			})

			convey.Convey("十分钟后解锁并重新计数", func() {
				clock.Advance(10 * time.Minute)
				_, err := login(ctx, svc, "wrong")
				assert.Equal(t, code.LoginFailed, errCode(t, err))
				assert.Contains(t, err.Error(), "还剩 4 次")
				res, err := login(ctx, svc, "correct-horse")
				require.NoError(t, err)
				assert.NotEmpty(t, res.Token)
			})
		})

		convey.Convey("成功登录清零失败计数", func() {
			for i := 0; i < 3; i++ {
				_, _ = login(ctx, svc, "wrong")
			}
			_, err := login(ctx, svc, "correct-horse")
			require.NoError(t, err)
			_, err = login(ctx, svc, "wrong")
			assert.Contains(t, err.Error(), "还剩 4 次")
		})

		convey.Convey("用户名错误同样计入失败", func() {
			_, err := svc.Login(ctx, &authapi.LoginRequest{Username: "nobody", Password: "correct-horse"})
			assert.Equal(t, code.LoginFailed, errCode(t, err))
			assert.Contains(t, err.Error(), "还剩 4 次")
		})
	})
}

func TestSession(t *testing.T) {
	convey.Convey("会话", t, func() {
		ctx, svc, clock, gdb := setupAuthTest(t)
		mustSetup(t, ctx, svc)
		res, err := login(ctx, svc, "correct-horse")
		require.NoError(t, err)

		convey.Convey("令牌只存哈希", func() {
			var hashes []string
			require.NoError(t, gdb.Raw(`SELECT token_hash FROM sessions`).Scan(&hashes).Error)
			require.Len(t, hashes, 1)
			assert.Equal(t, session_entity.HashToken(res.Token), hashes[0])
		})

		convey.Convey("有效会话得到管理员身份", func() {
			a, err := svc.Authenticate(ctx, res.Token, "")
			require.NoError(t, err)
			assert.Equal(t, auth.Actor{Kind: auth.KindAdmin, Name: "admin"}, a)
		})

		convey.Convey("未知令牌、空令牌返回 401", func() {
			for _, tok := range []string{"", "nope"} {
				_, err := svc.Authenticate(ctx, tok, "")
				assert.Equal(t, code.Unauthorized, errCode(t, err))
			}
		})

		convey.Convey("过期后返回 401", func() {
			clock.Advance(sessionTTL + time.Second)
			_, err := svc.Authenticate(ctx, res.Token, "")
			assert.Equal(t, code.Unauthorized, errCode(t, err))
		})

		convey.Convey("退出后令牌失效", func() {
			require.NoError(t, svc.Logout(ctx, res.Token))
			_, err := svc.Authenticate(ctx, res.Token, "")
			assert.Equal(t, code.Unauthorized, errCode(t, err))
		})
	})
}

func TestChangePassword(t *testing.T) {
	convey.Convey("修改密码", t, func() {
		ctx, svc, _, _ := setupAuthTest(t)
		mustSetup(t, ctx, svc)
		cur, _ := login(ctx, svc, "correct-horse")
		other, _ := login(ctx, svc, "correct-horse")

		_, err := svc.ChangePassword(ctx, &authapi.ChangePasswordRequest{OldPassword: "bad", NewPassword: "brand-new-password"}, cur.Token)
		assert.Equal(t, code.OldPasswordWrong, errCode(t, err))
		_, err = svc.ChangePassword(ctx, &authapi.ChangePasswordRequest{OldPassword: "correct-horse", NewPassword: "short"}, cur.Token)
		assert.Equal(t, code.PasswordTooShort, errCode(t, err))

		_, err = svc.ChangePassword(ctx, &authapi.ChangePasswordRequest{OldPassword: "correct-horse", NewPassword: "brand-new-password"}, cur.Token)
		require.NoError(t, err)

		_, err = login(ctx, svc, "correct-horse")
		assert.Equal(t, code.LoginFailed, errCode(t, err))
		_, err = login(ctx, svc, "brand-new-password")
		assert.NoError(t, err)

		_, err = svc.Authenticate(ctx, cur.Token, "")
		assert.NoError(t, err, "当前会话保留")
		_, err = svc.Authenticate(ctx, other.Token, "")
		assert.Equal(t, code.Unauthorized, errCode(t, err), "其他会话失效")
	})
}

func TestChangePasswordCountsWrongOldPassword(t *testing.T) {
	ctx, svc, clock, _ := setupAuthTest(t)
	mustSetup(t, ctx, svc)
	cur, err := login(ctx, svc, "correct-horse")
	require.NoError(t, err)
	// 拿到会话或 API 密钥的人不能借修改密码绕过登录的失败锁定来猜原密码
	for i := 0; i < admin_entity.MaxLoginFailures-1; i++ {
		_, err := svc.ChangePassword(ctx, &authapi.ChangePasswordRequest{OldPassword: "guess", NewPassword: "brand-new-password"}, cur.Token)
		assert.Equal(t, code.OldPasswordWrong, errCode(t, err))
	}
	_, err = svc.ChangePassword(ctx, &authapi.ChangePasswordRequest{OldPassword: "guess", NewPassword: "brand-new-password"}, cur.Token)
	assert.Equal(t, code.LoginLocked, errCode(t, err))
	_, err = svc.ChangePassword(ctx, &authapi.ChangePasswordRequest{OldPassword: "correct-horse", NewPassword: "brand-new-password"}, cur.Token)
	assert.Equal(t, code.LoginLocked, errCode(t, err), "锁定期间即使原密码正确也不处理")
	_, err = login(ctx, svc, "correct-horse")
	assert.Equal(t, code.LoginLocked, errCode(t, err), "与登录共用失败计数和锁定")

	clock.Advance(admin_entity.LockDuration + time.Second)
	_, err = svc.ChangePassword(ctx, &authapi.ChangePasswordRequest{OldPassword: "correct-horse", NewPassword: "brand-new-password"}, cur.Token)
	require.NoError(t, err)
}

func TestIssueSession(t *testing.T) {
	convey.Convey("单点登录通过后签发会话", t, func() {
		ctx, svc, clock, _ := setupAuthTest(t)
		convey.Convey("尚未创建管理员时拒绝", func() {
			_, err := svc.IssueSession(ctx)
			assert.Equal(t, code.AdminNotInitialized, errCode(t, err))
		})
		convey.Convey("与密码登录相同的 7 天会话，不受密码锁定影响", func() {
			mustSetup(t, ctx, svc)
			for i := 0; i < 5; i++ {
				_, _ = login(ctx, svc, "wrong-pass")
			}
			res, err := svc.IssueSession(ctx)
			require.NoError(t, err)
			assert.Equal(t, "admin", res.Username)
			assert.Equal(t, clock.Now().Add(7*24*time.Hour), res.ExpiresAt)
			a, err := svc.Authenticate(ctx, res.Token, "")
			require.NoError(t, err)
			assert.Equal(t, auth.KindAdmin, a.Kind)
		})
	})
}

func TestStatusSSO(t *testing.T) {
	convey.Convey("登录页公开信息：是否可以单点登录", t, func() {
		ctx, svc, _, _ := setupAuthTest(t)
		mustSetup(t, ctx, svc)
		st, err := svc.Status(ctx)
		require.NoError(t, err)
		assert.False(t, st.SSOEnabled)
		assert.Empty(t, st.SSOLabel)

		cfg := &sso_entity.SSO{Issuer: "https://git.example.com/", ClientID: "cid", ClientSecret: "c", ButtonLabel: "Gitea", PasswordLogin: true}
		require.NoError(t, sso_repo.SSO().Save(ctx, cfg))
		st, _ = svc.Status(ctx)
		assert.False(t, st.SSOEnabled, "已配置但未绑定")

		cfg.BoundIssuer, cfg.BoundSub = "https://git.example.com/", "3"
		require.NoError(t, sso_repo.SSO().Save(ctx, cfg))
		st, _ = svc.Status(ctx)
		assert.True(t, st.SSOEnabled)
		assert.Equal(t, "Gitea", st.SSOLabel)
	})
}

func TestPasswordLoginSwitch(t *testing.T) {
	convey.Convey("密码登录开关", t, func() {
		ctx, svc, _, _ := setupAuthTest(t)
		mustSetup(t, ctx, svc)
		SetPasswordLoginForced(false)
		t.Cleanup(func() { SetPasswordLoginForced(false) })
		cfg := &sso_entity.SSO{
			Issuer: "https://git.example.com/", ClientID: "cid", ClientSecret: "c", ButtonLabel: "Gitea",
			BoundIssuer: "https://git.example.com/", BoundSub: "3", PasswordLogin: true,
		}
		require.NoError(t, sso_repo.SSO().Save(ctx, cfg))

		convey.Convey("开着时照常登录，状态里 password_login 为 true", func() {
			st, _ := svc.Status(ctx)
			assert.True(t, st.PasswordLogin)
			_, err := login(ctx, svc, "correct-horse")
			assert.NoError(t, err)
		})

		convey.Convey("关闭后登录接口返回 403，不计入错误次数；状态里 password_login 为 false", func() {
			cfg.PasswordLogin = false
			require.NoError(t, sso_repo.SSO().Save(ctx, cfg))
			st, _ := svc.Status(ctx)
			assert.False(t, st.PasswordLogin)
			_, err := login(ctx, svc, "correct-horse")
			var he *httputils.Error
			require.ErrorAs(t, err, &he)
			assert.Equal(t, code.PasswordLoginDisabled, he.Code)
			assert.Equal(t, 403, he.Status)
			assert.Contains(t, he.Msg, "密码登录已关闭，请用 Gitea 登录")
			a, _ := admin_repo.Admin().Find(ctx)
			assert.Equal(t, 0, a.FailedCount)

			convey.Convey("修改密码仍然可用", func() {
				_, err := svc.ChangePassword(ctx, &authapi.ChangePasswordRequest{OldPassword: "correct-horse", NewPassword: "battery-staple"}, "")
				assert.NoError(t, err)
			})

			convey.Convey("环境变量强制打开后可以登录", func() {
				SetPasswordLoginForced(true)
				st, _ := svc.Status(ctx)
				assert.True(t, st.PasswordLogin)
				_, err := login(ctx, svc, "correct-horse")
				assert.NoError(t, err)
			})
		})
	})
}

func TestParsePasswordLoginEnv(t *testing.T) {
	for v, want := range map[string]bool{"1": true, "true": true, "TRUE": true, " true ": true, "": false, "0": false, "yes": false, "false": false} {
		assert.Equal(t, want, ParsePasswordLoginEnv(v), v)
	}
}
