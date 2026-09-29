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

func TestNextHourly(t *testing.T) {
	loc := mustLoc(t, "UTC")
	s, err := Parse(Spec{Kind: KindHourly, Minute: 30, Timezone: "UTC"})
	require.NoError(t, err)

	after := time.Date(2026, 1, 1, 10, 0, 0, 0, loc)
	got, err := s.Next(after, 3)
	require.NoError(t, err)
	want := []time.Time{
		time.Date(2026, 1, 1, 10, 30, 0, 0, loc),
		time.Date(2026, 1, 1, 11, 30, 0, 0, loc),
		time.Date(2026, 1, 1, 12, 30, 0, 0, loc),
	}
	for i := range want {
		assert.True(t, want[i].Equal(got[i]), "第 %d 次：want %v got %v", i, want[i], got[i])
	}
}

func TestNextDaily(t *testing.T) {
	loc := mustLoc(t, "UTC")
	s, err := Parse(Spec{Kind: KindDaily, Hour: 2, Minute: 0, Timezone: "UTC"})
	require.NoError(t, err)

	// after 恰好等于当天的计划时间：应从下一次算起，而不是重复当次。
	after := time.Date(2026, 1, 1, 2, 0, 0, 0, loc)
	got, err := s.Next(after, 2)
	require.NoError(t, err)
	assert.True(t, got[0].Equal(time.Date(2026, 1, 2, 2, 0, 0, 0, loc)))
	assert.True(t, got[1].Equal(time.Date(2026, 1, 3, 2, 0, 0, 0, loc)))
}

func TestNextWeekly(t *testing.T) {
	loc := mustLoc(t, "UTC")
	// 2026-01-01 是周四。选周一、周五。
	s, err := Parse(Spec{Kind: KindWeekly, Hour: 9, Minute: 0, Weekdays: []time.Weekday{time.Monday, time.Friday}, Timezone: "UTC"})
	require.NoError(t, err)

	after := time.Date(2026, 1, 1, 0, 0, 0, 0, loc) // 周四凌晨
	got, err := s.Next(after, 2)
	require.NoError(t, err)
	assert.True(t, got[0].Equal(time.Date(2026, 1, 2, 9, 0, 0, 0, loc)), "周五 got=%v", got[0])
	assert.True(t, got[1].Equal(time.Date(2026, 1, 5, 9, 0, 0, 0, loc)), "下周一 got=%v", got[1])
}

func TestNextCron(t *testing.T) {
	loc := mustLoc(t, "UTC")
	s, err := Parse(Spec{Kind: KindCron, Cron: "*/15 * * * *", Timezone: "UTC"})
	require.NoError(t, err)

	after := time.Date(2026, 1, 1, 10, 7, 0, 0, loc)
	got, err := s.Next(after, 3)
	require.NoError(t, err)
	want := []time.Time{
		time.Date(2026, 1, 1, 10, 15, 0, 0, loc),
		time.Date(2026, 1, 1, 10, 30, 0, 0, loc),
		time.Date(2026, 1, 1, 10, 45, 0, 0, loc),
	}
	for i := range want {
		assert.True(t, want[i].Equal(got[i]))
	}
}

func TestNextCronNeverMatchesReturnsError(t *testing.T) {
	// 2 月不存在 30 日，dow 为 * 时 dom 必须匹配，永远不会触发。
	s, err := Parse(Spec{Kind: KindCron, Cron: "0 0 30 2 *", Timezone: "UTC"})
	require.NoError(t, err)

	_, err = s.Next(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 1)
	assert.ErrorIs(t, err, ErrNeverFires)
}

func TestNextDSTSpringForwardSkipsForward(t *testing.T) {
	// 美国东部时间 2024-03-10 02:00 -> 03:00，02:30 不存在，应顺延到切换后。
	loc := mustLoc(t, "America/New_York")
	s, err := Parse(Spec{Kind: KindDaily, Hour: 2, Minute: 30, Timezone: "America/New_York"})
	require.NoError(t, err)

	after := time.Date(2024, 3, 9, 12, 0, 0, 0, loc)
	got, err := s.Next(after, 1)
	require.NoError(t, err)

	fired := got[0]
	assert.Equal(t, 2024, fired.In(loc).Year())
	assert.Equal(t, time.March, fired.In(loc).Month())
	assert.Equal(t, 10, fired.In(loc).Day())
	// 不存在的 02:30 顺延到切换后的 03:30 EDT
	assert.True(t, fired.Equal(time.Date(2024, 3, 10, 3, 30, 0, 0, loc)), "fired=%v", fired.In(loc))
}

func TestNextDSTFallBackFiresOnce(t *testing.T) {
	// 美国东部时间 2024-11-03 01:00 EDT -> 01:00 EST，01:30 出现两次，只应触发一次。
	loc := mustLoc(t, "America/New_York")
	s, err := Parse(Spec{Kind: KindDaily, Hour: 1, Minute: 30, Timezone: "America/New_York"})
	require.NoError(t, err)

	after := time.Date(2024, 11, 2, 12, 0, 0, 0, loc)
	got, err := s.Next(after, 2)
	require.NoError(t, err)

	assert.Equal(t, 3, got[0].In(loc).Day())
	assert.Equal(t, 1, got[0].In(loc).Hour())
	assert.Equal(t, 30, got[0].In(loc).Minute())
	// 第二次触发应是第二天，而不是同一天的另一次重复。
	assert.Equal(t, 4, got[1].In(loc).Day())
}

func TestPrevDaily(t *testing.T) {
	loc := mustLoc(t, "UTC")
	s, err := Parse(Spec{Kind: KindDaily, Hour: 2, Minute: 0, Timezone: "UTC"})
	require.NoError(t, err)

	at := time.Date(2026, 1, 3, 1, 0, 0, 0, loc)
	prev, err := s.Prev(at)
	require.NoError(t, err)
	assert.True(t, prev.Equal(time.Date(2026, 1, 2, 2, 0, 0, 0, loc)))

	// 恰好等于计划时间时，视为已到，返回该次本身（供补跑判断"是否已执行过"）。
	atExact := time.Date(2026, 1, 2, 2, 0, 0, 0, loc)
	prev2, err := s.Prev(atExact)
	require.NoError(t, err)
	assert.True(t, prev2.Equal(atExact))
}

// 夏令时在午夜开始的时区（如圣地亚哥 2027-09-05 00:00 → 01:00）：当天的午夜不存在，
// 逐日推进不能停在前一天，也不能跳过当天
func TestMidnightDSTTransition(t *testing.T) {
	loc := mustLoc(t, "America/Santiago")
	t.Run("每天：Next 跨过切换日", func(t *testing.T) {
		s, err := Parse(Spec{Kind: KindDaily, Hour: 3, Minute: 0, Timezone: "America/Santiago"})
		require.NoError(t, err)
		got, err := s.Next(time.Date(2027, 9, 4, 12, 0, 0, 0, loc), 3)
		require.NoError(t, err)
		require.Len(t, got, 3)
		for i, d := range []int{5, 6, 7} {
			assert.True(t, got[i].Equal(time.Date(2027, 9, d, 3, 0, 0, 0, loc)), "第 %d 次 got=%v", i, got[i].In(loc))
		}
	})
	t.Run("每天：Prev 不跳过切换日", func(t *testing.T) {
		s, err := Parse(Spec{Kind: KindDaily, Hour: 3, Minute: 0, Timezone: "America/Santiago"})
		require.NoError(t, err)
		prev, err := s.Prev(time.Date(2027, 9, 6, 1, 0, 0, 0, loc))
		require.NoError(t, err)
		assert.True(t, prev.Equal(time.Date(2027, 9, 5, 3, 0, 0, 0, loc)), "got=%v", prev.In(loc))
	})
	t.Run("每周日：切换日是星期日", func(t *testing.T) {
		s, err := Parse(Spec{Kind: KindWeekly, Hour: 10, Minute: 0, Weekdays: []time.Weekday{time.Sunday}, Timezone: "America/Santiago"})
		require.NoError(t, err)
		got, err := s.Next(time.Date(2027, 9, 1, 0, 0, 0, 0, loc), 2)
		require.NoError(t, err)
		assert.True(t, got[0].Equal(time.Date(2027, 9, 5, 10, 0, 0, 0, loc)), "got=%v", got[0].In(loc))
		assert.True(t, got[1].Equal(time.Date(2027, 9, 12, 10, 0, 0, 0, loc)), "got=%v", got[1].In(loc))
	})
	t.Run("每周六：切换前一天之后不再多触发一次", func(t *testing.T) {
		s, err := Parse(Spec{Kind: KindWeekly, Hour: 23, Minute: 30, Weekdays: []time.Weekday{time.Saturday}, Timezone: "America/Santiago"})
		require.NoError(t, err)
		got, err := s.Next(time.Date(2027, 9, 4, 0, 0, 0, 0, loc), 2)
		require.NoError(t, err)
		assert.True(t, got[0].Equal(time.Date(2027, 9, 4, 23, 30, 0, 0, loc)), "got=%v", got[0].In(loc))
		assert.True(t, got[1].Equal(time.Date(2027, 9, 11, 23, 30, 0, 0, loc)), "got=%v", got[1].In(loc))
	})
}

// 两次触发相隔很久的合法计划（2 月 29 日）也能给出接下来多次执行时间：搜索上限按每次触发之间计算
func TestNextSparseCron(t *testing.T) {
	s, err := Parse(Spec{Kind: KindCron, Cron: "0 0 29 2 *", Timezone: "UTC"})
	require.NoError(t, err)
	got, err := s.Next(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 3)
	require.NoError(t, err)
	require.Len(t, got, 3)
	for i, y := range []int{2028, 2032, 2036} {
		assert.True(t, got[i].Equal(time.Date(y, 2, 29, 0, 0, 0, 0, time.UTC)), "第 %d 次 got=%v", i, got[i])
	}
}

// Cron 的 a/step 按 a 到最大值、步长 step 解释（与 */step 从最小值开始相对）
func TestCronStartStep(t *testing.T) {
	s, err := Parse(Spec{Kind: KindCron, Cron: "5/20 * * * *", Timezone: "UTC"})
	require.NoError(t, err)
	got, err := s.Next(time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC), 4)
	require.NoError(t, err)
	want := []int{5, 25, 45, 5}
	for i, m := range want {
		assert.Equal(t, m, got[i].Minute(), "第 %d 次 got=%v", i, got[i])
	}
	assert.Equal(t, 11, got[3].Hour())
}
