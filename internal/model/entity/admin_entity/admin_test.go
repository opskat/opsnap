package admin_entity

import (
	"testing"
	"time"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
)

func TestAdminLockout(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	convey.Convey("管理员登录失败锁定", t, func() {
		a := &Admin{Username: "admin"}

		convey.Convey("前四次失败返回递减的剩余次数且不锁定", func() {
			for want := 4; want >= 1; want-- {
				remaining, locked := a.RecordFailure(now)
				assert.Equal(t, want, remaining)
				assert.False(t, locked)
				assert.False(t, a.IsLocked(now))
			}
		})

		convey.Convey("第五次失败锁定十分钟", func() {
			var locked bool
			for i := 0; i < 5; i++ {
				_, locked = a.RecordFailure(now)
			}
			assert.True(t, locked)
			assert.True(t, a.IsLocked(now.Add(10*time.Minute-time.Second)))
			assert.False(t, a.IsLocked(now.Add(10*time.Minute)))
			assert.Equal(t, 10*time.Minute, a.LockRemaining(now))
		})

		convey.Convey("重置后重新计数", func() {
			a.RecordFailure(now)
			a.ResetFailures()
			remaining, _ := a.RecordFailure(now)
			assert.Equal(t, 4, remaining)
		})
	})
}
