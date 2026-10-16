package schedule

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	require.NoError(t, err)
	return loc
}

func TestNext(t *testing.T) {
	sh := mustLoc(t, "Asia/Shanghai")
	ny := mustLoc(t, "America/New_York")
	at := func(loc *time.Location, y int, mo time.Month, d, h, mi int) time.Time {
		return time.Date(y, mo, d, h, mi, 0, 0, loc)
	}
	cases := []struct {
		name  string
		spec  Spec
		after time.Time
		loc   *time.Location
		want  time.Time
	}{
		{"每小时：整点", Spec{Kind: Hourly}, at(sh, 2026, 10, 15, 10, 30), sh, at(sh, 2026, 10, 15, 11, 0)},
		{"每小时：恰在整点则取下一个", Spec{Kind: Hourly}, at(sh, 2026, 10, 15, 10, 0), sh, at(sh, 2026, 10, 15, 11, 0)},
		{"每 6 小时：按本地 0/6/12/18 点", Spec{Kind: Every6h}, at(sh, 2026, 10, 15, 13, 0), sh, at(sh, 2026, 10, 15, 18, 0)},
		{"每 6 小时：跨天", Spec{Kind: Every6h}, at(sh, 2026, 10, 15, 18, 0), sh, at(sh, 2026, 10, 16, 0, 0)},
		{"每天：今天还没到", Spec{Kind: Daily, Time: "03:30"}, at(sh, 2026, 10, 15, 2, 0), sh, at(sh, 2026, 10, 15, 3, 30)},
		{"每天：恰在时刻则取明天", Spec{Kind: Daily, Time: "03:30"}, at(sh, 2026, 10, 15, 3, 30), sh, at(sh, 2026, 10, 16, 3, 30)},
		{"每天：跨月", Spec{Kind: Daily, Time: "00:00"}, at(sh, 2026, 10, 31, 23, 59), sh, at(sh, 2026, 11, 1, 0, 0)},
		// 2026-10-15 是周四
		{"每周：同一天已过", Spec{Kind: Weekly, Weekday: 4, Time: "02:00"}, at(sh, 2026, 10, 15, 3, 0), sh, at(sh, 2026, 10, 22, 2, 0)},
		{"每周：本周日", Spec{Kind: Weekly, Weekday: 0, Time: "06:00"}, at(sh, 2026, 10, 15, 3, 0), sh, at(sh, 2026, 10, 18, 6, 0)},
		{"每周：周六", Spec{Kind: Weekly, Weekday: 6, Time: "23:59"}, at(sh, 2026, 10, 17, 23, 59), sh, at(sh, 2026, 10, 24, 23, 59)},
		{"cron：工作日 9 点", Spec{Kind: Cron, Cron: "0 9 * * 1-5"}, at(sh, 2026, 10, 16, 9, 0), sh, at(sh, 2026, 10, 19, 9, 0)},
		{"夏令时：每天 09:00 保持本地墙钟", Spec{Kind: Daily, Time: "09:00"}, at(ny, 2027, 3, 13, 12, 0), ny, at(ny, 2027, 3, 14, 9, 0)},
		{"夏令时：每周跨越秋季回拨", Spec{Kind: Weekly, Weekday: 0, Time: "09:00"}, at(ny, 2026, 10, 30, 12, 0), ny, at(ny, 2026, 11, 1, 9, 0)},
		// 00:00 UTC 是上海 08:00，当天 01:00 已过
		{"after 的时区不影响按 loc 解释", Spec{Kind: Daily, Time: "01:00"}, at(time.UTC, 2026, 10, 15, 0, 0), sh, at(sh, 2026, 10, 16, 1, 0)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Next(c.spec, c.after, c.loc)
			require.NoError(t, err)
			assert.True(t, c.want.Equal(got), "want %s got %s", c.want, got)
			assert.Equal(t, c.loc.String(), got.Location().String())
		})
	}
}

func TestNextDSTFall(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	// 2026-11-01 回拨日：每小时仍然每小时一次，不重复也不丢
	after := time.Date(2026, 11, 1, 0, 30, 0, 0, ny)
	for i := 0; i < 5; i++ {
		next, err := Next(Spec{Kind: Hourly}, after, ny)
		require.NoError(t, err)
		assert.True(t, next.After(after))
		assert.Zero(t, next.Minute())
		assert.Equal(t, time.Hour, next.Sub(after.Truncate(time.Hour)))
		after = next
	}
}

func TestNextNilLocationIsUTC(t *testing.T) {
	got, err := Next(Spec{Kind: Daily, Time: "01:00"}, time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC), nil)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 10, 15, 1, 0, 0, 0, time.UTC), got)
}

func TestNextInvalidSpec(t *testing.T) {
	bad := []Spec{
		{Kind: ""},
		{Kind: "monthly"},
		{Kind: Daily},
		{Kind: Daily, Time: "25:00"},
		{Kind: Daily, Time: "9:00"},
		{Kind: Weekly, Weekday: 7, Time: "01:00"},
		{Kind: Weekly, Weekday: -1, Time: "01:00"},
		{Kind: Cron, Cron: ""},
		{Kind: Cron, Cron: "not a cron"},
		{Kind: Cron, Cron: "* * * *"},
		{Kind: Cron, Cron: "@every 5m"},
		{Kind: Cron, Cron: "0 0 31 2 *"}, // 永远不会到
	}
	for _, s := range bad {
		_, err := Next(s, time.Now(), time.UTC)
		assert.Error(t, err, "%+v", s)
		assert.Error(t, s.Validate(), "%+v", s)
	}
}

func TestNextN(t *testing.T) {
	sh := mustLoc(t, "Asia/Shanghai")
	got, err := NextN(Spec{Kind: Daily, Time: "03:00"}, time.Date(2026, 10, 15, 4, 0, 0, 0, sh), sh, 3)
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, time.Date(2026, 10, 16, 3, 0, 0, 0, sh), got[0])
	assert.Equal(t, time.Date(2026, 10, 18, 3, 0, 0, 0, sh), got[2])
}

func TestNextFallBackRunsOnce(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	// 2026-11-01 纽约 01:00-02:00 出现两次：01:30 EDT 触发后，下一次是第二天，而不是一小时后的 01:30 EST
	fired := time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC) // 01:30 EDT
	require.Equal(t, "01:30 EDT", fired.In(ny).Format("15:04 MST"))
	next, err := Next(Spec{Kind: Daily, Time: "01:30"}, fired, ny)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 11, 2, 1, 30, 0, 0, ny), next)

	// 调度器在触发后稍晚一点才计算下一次，结果相同
	next, err = Next(Spec{Kind: Daily, Time: "01:30"}, fired.Add(3*time.Second), ny)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 11, 2, 1, 30, 0, 0, ny), next)

	// 自定义 cron 的固定时刻同样只跑一次
	next, err = Next(Spec{Kind: Cron, Cron: "30 1 * * *"}, fired, ny)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 11, 2, 1, 30, 0, 0, ny), next)
}

func TestCronRejectsTimezonePrefix(t *testing.T) {
	// 时区以设置为准，表达式里不能另带
	for _, e := range []string{"CRON_TZ=UTC 0 3 * * *", "TZ=UTC 0 3 * * *"} {
		assert.Error(t, Spec{Kind: Cron, Cron: e}.Validate(), e)
	}
}
