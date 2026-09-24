package auth_svc

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	api "github.com/opskat/opsnap/internal/api/auth"
	"github.com/opskat/opsnap/internal/pkg/code"
)

func TestLogin(t *testing.T) {
	convey.Convey("密码登录与登录保护", t, func() {
		ctx, s, now := setupAuthTest(t)
		setupCode, _ := s.PrepareSetupCode(ctx)
		_, _, err := s.Setup(ctx, setupReq(setupCode), meta)
		require.NoError(t, err)
		login := func(ip, user, pw string) (*api.LoginResponse, *IssuedSession, error) {
			return s.Login(ctx, &api.LoginRequest{Username: user, Password: pw}, ClientMeta{IP: ip, UserAgent: "UA"})
		}

		convey.Convey("用户名和密码正确时创建会话", func() {
			resp, issued, err := login("192.0.2.1", "admin", goodPassword)
			require.NoError(t, err)
			assert.Equal(t, "admin", resp.Username)
			p, _, err := s.AuthenticateSession(ctx, issued.Token)
			require.NoError(t, err)
			assert.Equal(t, "admin", p.Username)
		})

		convey.Convey("用户名错误和密码错误返回同一个错误", func() {
			_, _, err1 := login("192.0.2.1", "nobody", goodPassword)
			_, _, err2 := login("192.0.2.1", "admin", "wrong-password-123")
			assert.Equal(t, code.LoginFailed, errCode(err1))
			assert.Equal(t, code.LoginFailed, errCode(err2))
			assert.Equal(t, err1.Error(), err2.Error())
		})

		convey.Convey("同一 IP 连续失败 5 次后锁定 15 分钟，正确密码也被拒绝", func() {
			for i := 0; i < 5; i++ {
				_, _, err := login("192.0.2.9", "admin", "wrong-password-123")
				assert.Equal(t, code.LoginFailed, errCode(err), "第 %d 次", i+1)
			}
			_, _, err := login("192.0.2.9", "admin", goodPassword)
			assert.Equal(t, code.TooManyAttempts, errCode(err))
			assert.Contains(t, err.Error(), "15 分钟")
			assert.Equal(t, 429, status(err))

			convey.Convey("其他 IP 不受影响", func() {
				_, _, err := login("192.0.2.10", "admin", goodPassword)
				assert.NoError(t, err)
			})

			convey.Convey("提示剩余分钟数，15 分钟后解除", func() {
				*now = now.Add(10*time.Minute + 30*time.Second)
				_, _, err := login("192.0.2.9", "admin", goodPassword)
				assert.Contains(t, err.Error(), "5 分钟")
				*now = now.Add(5 * time.Minute)
				_, _, err = login("192.0.2.9", "admin", goodPassword)
				assert.NoError(t, err)
			})

			convey.Convey("锁定期间设置码请求同样被拒绝", func() {
				_, _, err := s.Setup(ctx, setupReq("AAAA-BBBB-CCCC"), ClientMeta{IP: "192.0.2.9"})
				assert.Equal(t, code.TooManyAttempts, errCode(err))
			})
		})

		convey.Convey("成功登录清零失败计数", func() {
			for i := 0; i < 4; i++ {
				_, _, _ = login("192.0.2.9", "admin", "wrong-password-123")
			}
			_, _, err := login("192.0.2.9", "admin", goodPassword)
			require.NoError(t, err)
			for i := 0; i < 4; i++ {
				_, _, _ = login("192.0.2.9", "admin", "wrong-password-123")
			}
			_, _, err = login("192.0.2.9", "admin", goodPassword)
			assert.NoError(t, err, "清零后再失败 4 次不应锁定")
		})

		convey.Convey("失败间隔超过 15 分钟时重新计数", func() {
			for i := 0; i < 4; i++ {
				_, _, _ = login("192.0.2.9", "admin", "wrong-password-123")
			}
			*now = now.Add(16 * time.Minute)
			_, _, _ = login("192.0.2.9", "admin", "wrong-password-123")
			_, _, err := login("192.0.2.9", "admin", goodPassword)
			assert.NoError(t, err)
		})

		convey.Convey("设置码错误计入失败次数", func() {
			// 管理员已存在时 Setup 直接返回已初始化，不计数；这里用新实例验证
			ctx2, s2, _ := setupAuthTest(t)
			_, _ = s2.PrepareSetupCode(ctx2)
			for i := 0; i < 5; i++ {
				_, _, err := s2.Setup(ctx2, setupReq(fmt.Sprintf("AAAA-BBBB-CCC%d", i)), ClientMeta{IP: "192.0.2.77"})
				assert.Equal(t, code.SetupCodeInvalid, errCode(err))
			}
			_, _, err := s2.Setup(ctx2, setupReq("AAAA-BBBB-CCCC"), ClientMeta{IP: "192.0.2.77"})
			assert.Equal(t, code.TooManyAttempts, errCode(err))
		})
	})
}

func TestLoginGuardConcurrent(t *testing.T) {
	ctx, s, _ := setupAuthTest(t)
	setupCode, _ := s.PrepareSetupCode(ctx)
	_, _, err := s.Setup(ctx, setupReq(setupCode), meta)
	require.NoError(t, err)

	// 同一 IP 并发提交错误密码：在第一次失败被记录前，其余请求不能都通过锁定检查
	const n = 20
	codes := make(chan int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := s.Login(ctx, &api.LoginRequest{Username: "admin", Password: "wrong-password-123"}, ClientMeta{IP: "192.0.2.50"})
			codes <- errCode(err)
		}()
	}
	wg.Wait()
	close(codes)
	failed := 0
	for c := range codes {
		if c == code.LoginFailed {
			failed++
		}
	}
	assert.Equal(t, guardMaxFailures, failed, "实际校验密码的次数不应超过锁定阈值")
}
