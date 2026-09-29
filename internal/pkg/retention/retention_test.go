package retention

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

func TestSelect(t *testing.T) {
	utc := time.UTC
	shanghai := mustLoc(t, "Asia/Shanghai")
	newYork := mustLoc(t, "America/New_York")
	at := func(loc *time.Location, y int, m time.Month, d, h, mi int) time.Time {
		return time.Date(y, m, d, h, mi, 0, 0, loc)
	}

	cases := []struct {
		name  string
		p     Policy
		loc   *time.Location
		now   time.Time
		snaps []Snapshot
		keep  []string
	}{
		{
			name: "没有快照",
			p:    Policy{Days: 7, Weeks: 4, Months: 6},
			loc:  utc,
			now:  at(utc, 2026, 9, 28, 2, 0),
		},
		{
			name: "最近 N 天内全部保留，边界恰好 N×24 小时也保留，早一分钟不保留",
			p:    Policy{Days: 2},
			loc:  utc,
			// 2026-09-28 是周一
			now: at(utc, 2026, 9, 28, 2, 0),
			snaps: []Snapshot{
				{ID: "older", Time: at(utc, 2026, 9, 26, 1, 59)},
				{ID: "edge", Time: at(utc, 2026, 9, 26, 2, 0)},
				{ID: "a", Time: at(utc, 2026, 9, 27, 2, 0)},
				{ID: "b", Time: at(utc, 2026, 9, 28, 1, 0)},
			},
			keep: []string{"edge", "a", "b"},
		},
		{
			name: "N 天按 24 小时计，不按自然日：跨夏令时切换的那天也是 24 小时",
			p:    Policy{Days: 1},
			loc:  newYork,
			// 2026-11-01 纽约夏令时结束，当天 25 小时；运行开始于 11-01 12:00 EST
			now: at(newYork, 2026, 11, 1, 12, 0),
			snaps: []Snapshot{
				// 往前 24 小时是 10-31 13:00 EDT
				{ID: "12:30", Time: at(newYork, 2026, 10, 31, 12, 30)},
				{ID: "13:00", Time: at(newYork, 2026, 10, 31, 13, 0)},
				{ID: "latest", Time: at(newYork, 2026, 11, 1, 11, 0)},
			},
			keep: []string{"13:00", "latest"},
		},
		{
			name: "更早的快照按自然周（周一开始）各留最后一份，只留最近 W 周（含本周）",
			p:    Policy{Days: 1, Weeks: 3},
			loc:  utc,
			// 周一；本周 09-28 起，最近 3 周为 09-14、09-21、09-28 三周
			now: at(utc, 2026, 9, 28, 12, 0),
			snaps: []Snapshot{
				{ID: "w0913-sun", Time: at(utc, 2026, 9, 13, 23, 0)},
				{ID: "w0914-mon", Time: at(utc, 2026, 9, 14, 0, 0)},
				{ID: "w0914-sun", Time: at(utc, 2026, 9, 20, 23, 59)},
				{ID: "w0921-tue", Time: at(utc, 2026, 9, 22, 1, 0)},
				{ID: "w0921-sun", Time: at(utc, 2026, 9, 27, 11, 0)},
				{ID: "w0928-mon", Time: at(utc, 2026, 9, 28, 11, 0)},
			},
			keep: []string{"w0914-sun", "w0921-sun", "w0928-mon"},
		},
		{
			name: "周按任务时区分组：UTC 周日晚上在上海已是周一",
			p:    Policy{Weeks: 3},
			loc:  shanghai,
			now:  at(shanghai, 2026, 9, 28, 20, 0),
			snaps: []Snapshot{
				// 上海 09-20 周日 23:00，是上海 09-14 这一周的最后一份
				{ID: "sh-sun-2300", Time: at(utc, 2026, 9, 20, 15, 0)},
				// 上海 09-21 周一 00:30（UTC 仍是周日），与周二同属上海 09-21 这一周，不是最后一份
				{ID: "sh-mon-0030", Time: at(utc, 2026, 9, 20, 16, 30)},
				{ID: "sh-tue", Time: at(shanghai, 2026, 9, 22, 10, 0)},
				{ID: "latest", Time: at(shanghai, 2026, 9, 28, 19, 0)},
			},
			keep: []string{"sh-sun-2300", "sh-tue", "latest"},
		},
		{
			name: "更早的快照按自然月各留最后一份，只留最近 M 个月（含本月），按任务时区",
			p:    Policy{Days: 3, Months: 3},
			loc:  shanghai,
			now:  at(shanghai, 2026, 9, 28, 2, 0),
			snaps: []Snapshot{
				{ID: "jun", Time: at(shanghai, 2026, 6, 30, 23, 0)},
				{ID: "jul-early", Time: at(shanghai, 2026, 7, 1, 0, 0)},
				// 上海 07-31 23:30 在 UTC 仍是 7 月，但 08-01 00:30 上海在 UTC 是 7 月 31 日
				{ID: "jul-last", Time: at(shanghai, 2026, 7, 31, 23, 30)},
				{ID: "aug-first-utc-jul", Time: at(shanghai, 2026, 8, 1, 0, 30)},
				{ID: "aug-last", Time: at(shanghai, 2026, 8, 31, 22, 0)},
				{ID: "sep-old", Time: at(shanghai, 2026, 9, 10, 2, 0)},
				{ID: "sep-older", Time: at(shanghai, 2026, 9, 3, 2, 0)},
				{ID: "sep-recent", Time: at(shanghai, 2026, 9, 27, 2, 0)},
			},
			keep: []string{"jul-last", "aug-last", "sep-old", "sep-recent"},
		},
		{
			name: "月跨年：一月时最近 3 个月为 11、12、1 月",
			p:    Policy{Months: 3},
			loc:  utc,
			now:  at(utc, 2027, 1, 15, 0, 0),
			snaps: []Snapshot{
				{ID: "oct", Time: at(utc, 2026, 10, 31, 0, 0)},
				{ID: "nov", Time: at(utc, 2026, 11, 30, 0, 0)},
				{ID: "dec", Time: at(utc, 2026, 12, 31, 0, 0)},
				{ID: "jan", Time: at(utc, 2027, 1, 14, 0, 0)},
			},
			keep: []string{"nov", "dec", "jan"},
		},
		{
			name: "周与月同时选中同一份只保留一次",
			p:    Policy{Weeks: 1, Months: 1},
			loc:  utc,
			now:  at(utc, 2026, 9, 30, 12, 0),
			snaps: []Snapshot{
				{ID: "mon", Time: at(utc, 2026, 9, 28, 0, 0)},
				{ID: "tue", Time: at(utc, 2026, 9, 29, 0, 0)},
			},
			keep: []string{"tue"},
		},
		{
			name: "周、月组内只看更早的快照：最近 N 天之外的最后一份另行保留",
			p:    Policy{Days: 1, Weeks: 1},
			loc:  utc,
			now:  at(utc, 2026, 9, 30, 12, 0),
			snaps: []Snapshot{
				{ID: "mon", Time: at(utc, 2026, 9, 28, 0, 0)},
				{ID: "tue", Time: at(utc, 2026, 9, 29, 0, 0)},
				{ID: "wed", Time: at(utc, 2026, 9, 30, 0, 0)},
			},
			keep: []string{"tue", "wed"},
		},
		{
			name: "最新一份始终保留，即使超出所有规则",
			p:    Policy{Days: 7, Weeks: 2, Months: 1},
			loc:  utc,
			now:  at(utc, 2026, 9, 28, 2, 0),
			snaps: []Snapshot{
				{ID: "old", Time: at(utc, 2025, 1, 1, 0, 0)},
				{ID: "newest", Time: at(utc, 2026, 3, 1, 0, 0)},
				{ID: "mid", Time: at(utc, 2025, 6, 1, 0, 0)},
			},
			keep: []string{"newest"},
		},
		{
			name: "所有规则为 0 时只留最新一份",
			p:    Policy{},
			loc:  utc,
			now:  at(utc, 2026, 9, 28, 2, 0),
			snaps: []Snapshot{
				{ID: "a", Time: at(utc, 2026, 9, 28, 0, 0)},
				{ID: "b", Time: at(utc, 2026, 9, 28, 1, 0)},
			},
			keep: []string{"b"},
		},
		{
			name: "负数按不生效处理",
			p:    Policy{Days: -1, Weeks: -1, Months: -1},
			loc:  utc,
			now:  at(utc, 2026, 9, 28, 2, 0),
			snaps: []Snapshot{
				{ID: "a", Time: at(utc, 2026, 9, 28, 0, 0)},
				{ID: "b", Time: at(utc, 2026, 9, 28, 1, 0)},
			},
			keep: []string{"b"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			keep, remove := Select(c.snaps, c.p, c.loc, c.now)
			var wantRemove []string
			kept := map[string]bool{}
			for _, id := range c.keep {
				kept[id] = true
			}
			for _, s := range c.snaps {
				if !kept[s.ID] {
					wantRemove = append(wantRemove, s.ID)
				}
			}
			assert.ElementsMatch(t, c.keep, keep, "保留")
			assert.ElementsMatch(t, wantRemove, remove, "删除")
		})
	}
}

// 输入顺序不影响结果，结果按输入顺序给出
func TestSelectKeepsInputOrder(t *testing.T) {
	now := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	snaps := []Snapshot{
		{ID: "new", Time: now.Add(-time.Hour)},
		{ID: "old2", Time: now.Add(-50 * 24 * time.Hour)},
		{ID: "mid", Time: now.Add(-2 * time.Hour)},
		{ID: "old1", Time: now.Add(-60 * 24 * time.Hour)},
	}
	keep, remove := Select(snaps, Policy{Days: 1}, time.UTC, now)
	assert.Equal(t, []string{"new", "mid"}, keep)
	assert.Equal(t, []string{"old2", "old1"}, remove)
}
