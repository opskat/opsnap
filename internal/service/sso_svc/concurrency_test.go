package sso_svc

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authapi "github.com/opskat/opsnap/internal/api/auth"
	"github.com/opskat/opsnap/internal/model/entity/sso_entity"
	"github.com/opskat/opsnap/internal/pkg/secret"
	"github.com/opskat/opsnap/internal/repository/sso_repo"
	"github.com/opskat/opsnap/internal/service/auth_svc"
)

// hookRepo 在下一次 Get 返回前调用一次 afterGet，模拟「读出配置之后、保存之前」发生的其他操作
type hookRepo struct {
	sso_repo.SSORepo
	afterGet atomic.Pointer[func()]
}

func (r *hookRepo) Get(ctx context.Context) (*sso_entity.SSO, error) {
	s, err := r.SSORepo.Get(ctx)
	if f := r.afterGet.Swap(nil); f != nil {
		(*f)()
	}
	return s, err
}

// meanwhile 在另一个 goroutine 里执行 fn，最多等它 200 毫秒（没有互斥时它会在这期间完成）；
// 返回的函数等它最终完成
func meanwhile(fn func()) (wait func()) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
	}
	return func() { <-done }
}

func TestSSOConcurrentWrites(t *testing.T) {
	convey.Convey("单点登录：并发的保存", t, func() {
		ctx, svc, clock := setupSSOTest(t)
		idp := newFakeIdP(t, clock.Now)
		auth_svc.SetPasswordLoginForced(false)
		configure(t, ctx, svc, idp)
		bind(t, ctx, svc, idp)
		hook := &hookRepo{SSORepo: sso_repo.SSO()}
		sso_repo.RegisterSSO(hook)
		t.Cleanup(func() { sso_repo.RegisterSSO(hook.SSORepo) })

		convey.Convey("解除绑定或切换密码登录时轮换主密钥：不把旧主密钥的密文写回", func() {
			ops := []struct {
				name string
				run  func() error
			}{
				{"关闭密码登录", func() error { _, err := svc.SetPasswordLogin(ctx, setPW(false)); return err }},
				{"打开密码登录", func() error { _, err := svc.SetPasswordLogin(ctx, setPW(true)); return err }},
				{"解除绑定", func() error { _, err := svc.Unbind(ctx); return err }},
			}
			for _, op := range ops {
				name := op.name
				next, err := secret.New("next-master-key")
				require.NoError(t, err)
				rotated, err := next.Encrypt(idp.clientSecret)
				require.NoError(t, err)
				var wait func()
				f := func() {
					wait = meanwhile(func() {
						// 模拟轮换主密钥：在轮换锁内把密文换成新主密钥加密的
						end := secret.BeginRotate()
						defer end()
						assert.NoError(t, db.Ctx(ctx).Model(&sso_entity.SSO{}).Where("id = ?", sso_entity.SingletonID).
							Update("client_secret", rotated).Error)
					})
				}
				hook.afterGet.Store(&f)
				require.NoError(t, op.run(), name)
				wait()
				row, err := hook.SSORepo.Get(ctx)
				require.NoError(t, err)
				assert.Equal(t, rotated, row.ClientSecret, name)
			}
		})

		convey.Convey("保存提供方配置期间关闭密码登录：保存不会把它改回打开", func() {
			var wait func()
			var pwErr error
			f := func() {
				wait = meanwhile(func() { _, pwErr = svc.SetPasswordLogin(ctx, setPW(false)) })
			}
			hook.afterGet.Store(&f)
			req := update(idp.issuer)
			req.ClientSecret, req.ButtonLabel = "", "公司 Git"
			_, err := svc.Update(ctx, req)
			require.NoError(t, err)
			wait()
			require.NoError(t, pwErr)
			v, err := svc.View(ctx)
			require.NoError(t, err)
			assert.Equal(t, "公司 Git", v.ButtonLabel)
			assert.False(t, v.PasswordLogin)
		})

		convey.Convey("授权期间换了提供方：绑定失败，不把旧提供方的账号绑到新配置上", func() {
			_, err := svc.Unbind(ctx)
			require.NoError(t, err)
			other := newFakeIdP(t, clock.Now)
			st, err := svc.StartBind(asAdmin(ctx))
			require.NoError(t, err)
			c, s := idp.authorize(st.URL)
			idp.onToken = func() {
				req := update(other.issuer)
				req.ClientSecret = ""
				_, err := svc.Update(ctx, req)
				assert.NoError(t, err)
			}
			res := svc.Callback(ctx, st.Browser, "", &authapi.OIDCCallbackRequest{Code: c, State: s})
			assert.Equal(t, "授权期间单点登录的提供方配置被修改了，请重新操作。", failed(t, svc, res, "/settings"))
			v, err := svc.View(ctx)
			require.NoError(t, err)
			assert.Equal(t, other.issuer, v.Issuer)
			assert.Nil(t, v.Binding)
		})

		convey.Convey("授权期间解除了绑定：登录不再建立会话", func() {
			st, err := svc.StartLogin(ctx, "/")
			require.NoError(t, err)
			c, s := idp.authorize(st.URL)
			idp.onToken = func() {
				_, err := svc.Unbind(ctx)
				assert.NoError(t, err)
			}
			res := svc.Callback(ctx, st.Browser, "", &authapi.OIDCCallbackRequest{Code: c, State: s})
			assert.Equal(t, "单点登录未启用。", failed(t, svc, res, "/login"))
		})
	})
}
