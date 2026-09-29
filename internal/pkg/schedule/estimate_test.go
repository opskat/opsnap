package schedule

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEstimateMaxSnapshots(t *testing.T) {
	t.Run("每小时：N 天内最多 N*24 次，加上按周、按月保留与最新一份", func(t *testing.T) {
		s, err := Parse(Spec{Kind: KindHourly, Minute: 0, Timezone: "UTC"})
		require.NoError(t, err)
		k := EstimateMaxSnapshots(s, 7, 4, 6)
		// 上界：7*24（N 天窗口内的次数）+ 4（每周一份）+ 6（每月一份）+ 1（最新一份始终保留）。
		assert.Equal(t, 7*24+4+6+1, k)
	})

	t.Run("每天：N 天内最多 N 次", func(t *testing.T) {
		s, err := Parse(Spec{Kind: KindDaily, Hour: 3, Minute: 0, Timezone: "UTC"})
		require.NoError(t, err)
		k := EstimateMaxSnapshots(s, 7, 4, 6)
		assert.Equal(t, 7+4+6+1, k)
	})

	t.Run("每周：按所选星期几的个数与 N/7 上取整", func(t *testing.T) {
		s, err := Parse(Spec{Kind: KindWeekly, Hour: 3, Minute: 0, Weekdays: []time.Weekday{time.Monday, time.Thursday}, Timezone: "UTC"})
		require.NoError(t, err)
		k := EstimateMaxSnapshots(s, 10, 4, 6)
		// 10 天 = ceil(10/7)=2 周 * 2 天/周 = 4 次
		assert.Equal(t, 4+4+6+1, k)
	})

	t.Run("保留天数、周数、月数为 0 时，仍至少保留最新一份", func(t *testing.T) {
		s, err := Parse(Spec{Kind: KindDaily, Hour: 3, Minute: 0, Timezone: "UTC"})
		require.NoError(t, err)
		k := EstimateMaxSnapshots(s, 0, 0, 0)
		assert.Equal(t, 1, k)
	})

	t.Run("Cron：按分钟数与小时数估算每次匹配日的触发次数", func(t *testing.T) {
		s, err := Parse(Spec{Kind: KindCron, Cron: "*/15 * * * *", Timezone: "UTC"})
		require.NoError(t, err)
		k := EstimateMaxSnapshots(s, 1, 0, 0)
		// 每小时 4 次（0/15/30/45）* 24 小时 = 96 次/天。
		assert.Equal(t, 96+0+0+1, k)
	})

	t.Run("Cron 只在部分月份触发：估算不低于那几个月里 N 天内的实际次数", func(t *testing.T) {
		s, err := Parse(Spec{Kind: KindCron, Cron: "0 3 * 1 *", Timezone: "UTC"})
		require.NoError(t, err)
		// 1 月里任意 30 天窗口都有 30 次触发，保留 30 天时 1 月底最多保留 30 份（加最新一份的余量）
		assert.GreaterOrEqual(t, EstimateMaxSnapshots(s, 30, 0, 0), 30+1)
	})
}
