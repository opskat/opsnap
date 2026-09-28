package schedule

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestValidate(t *testing.T) {
	t.Run("每小时：分钟必须在 0-59 之间", func(t *testing.T) {
		reason, ok := Validate(Spec{Kind: KindHourly, Minute: 0, Timezone: "UTC"})
		assert.True(t, ok)
		assert.Empty(t, reason)

		reason, ok = Validate(Spec{Kind: KindHourly, Minute: 60, Timezone: "UTC"})
		assert.False(t, ok)
		assert.NotEmpty(t, reason)
	})

	t.Run("每天：小时和分钟都要合法", func(t *testing.T) {
		reason, ok := Validate(Spec{Kind: KindDaily, Hour: 23, Minute: 59, Timezone: "UTC"})
		assert.True(t, ok)
		assert.Empty(t, reason)

		_, ok = Validate(Spec{Kind: KindDaily, Hour: 24, Minute: 0, Timezone: "UTC"})
		assert.False(t, ok)
	})

	t.Run("每周：至少选一个星期几，星期几必须是 0-6", func(t *testing.T) {
		reason, ok := Validate(Spec{Kind: KindWeekly, Hour: 1, Minute: 0, Weekdays: []time.Weekday{time.Monday}, Timezone: "UTC"})
		assert.True(t, ok)
		assert.Empty(t, reason)

		reason, ok = Validate(Spec{Kind: KindWeekly, Hour: 1, Minute: 0, Weekdays: nil, Timezone: "UTC"})
		assert.False(t, ok)
		assert.NotEmpty(t, reason)
	})

	t.Run("Cron：必须是标准 5 段表达式，非法时给出原因", func(t *testing.T) {
		reason, ok := Validate(Spec{Kind: KindCron, Cron: "*/15 * * * *", Timezone: "UTC"})
		assert.True(t, ok)
		assert.Empty(t, reason)

		reason, ok = Validate(Spec{Kind: KindCron, Cron: "* * * *", Timezone: "UTC"})
		assert.False(t, ok)
		assert.NotEmpty(t, reason)

		reason, ok = Validate(Spec{Kind: KindCron, Cron: "60 * * * *", Timezone: "UTC"})
		assert.False(t, ok)
		assert.NotEmpty(t, reason)

		reason, ok = Validate(Spec{Kind: KindCron, Cron: "0 0 32 * *", Timezone: "UTC"})
		assert.False(t, ok, "日字段超出 1-31 范围应当拒绝")
		assert.NotEmpty(t, reason)
	})

	t.Run("时区必须是合法的 IANA 名称", func(t *testing.T) {
		reason, ok := Validate(Spec{Kind: KindDaily, Hour: 1, Minute: 0, Timezone: "Asia/Shanghai"})
		assert.True(t, ok)
		assert.Empty(t, reason)

		reason, ok = Validate(Spec{Kind: KindDaily, Hour: 1, Minute: 0, Timezone: "Not/AZone"})
		assert.False(t, ok)
		assert.NotEmpty(t, reason)

		reason, ok = Validate(Spec{Kind: KindDaily, Hour: 1, Minute: 0, Timezone: ""})
		assert.False(t, ok)
		assert.NotEmpty(t, reason)
	})

	t.Run("未知调度类型被拒绝", func(t *testing.T) {
		_, ok := Validate(Spec{Kind: Kind("yearly"), Timezone: "UTC"})
		assert.False(t, ok)
	})
}

func TestParse(t *testing.T) {
	t.Run("合法输入返回规范化后的 Spec", func(t *testing.T) {
		s, err := Parse(Spec{Kind: KindWeekly, Hour: 1, Minute: 0, Weekdays: []time.Weekday{time.Wednesday, time.Monday, time.Monday}, Timezone: "UTC"})
		assert.NoError(t, err)
		assert.Equal(t, []time.Weekday{time.Monday, time.Wednesday}, s.Weekdays, "去重并排序")
	})

	t.Run("非法输入返回可作为字段错误提示的原因", func(t *testing.T) {
		_, err := Parse(Spec{Kind: KindHourly, Minute: 99, Timezone: "UTC"})
		assert.Error(t, err)
		assert.NotEmpty(t, err.Error())
	})
}
