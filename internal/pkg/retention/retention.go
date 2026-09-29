// Package retention 按任务的保留策略选出要保留和要删除的快照，纯函数，不接触仓库。
package retention

import "time"

// Policy 保留策略；小于等于 0 的项不生效
type Policy struct {
	// Days 最近 N 天内（从 now 往前推 N×24 小时，含边界）的快照全部保留
	Days int
	// Weeks 更早的快照按自然周（周一开始）分组，各留最后一份，只留最近 W 周（含 now 所在的周）
	Weeks int
	// Months 更早的快照按自然月分组，各留最后一份，只留最近 M 个月（含 now 所在的月）
	Months int
}

// Snapshot 任务的一份成功快照
type Snapshot struct {
	ID   string
	Time time.Time
}

// Select 按策略选出要保留与要删除的快照 ID，各自按输入顺序给出；每份快照只出现在其中一边。
// now 为本次运行的开始时间；周、月按 loc（任务时区，nil 为 UTC）的日历划分。
// “更早的快照”指最近 N 天之外的快照，周、月分组只在它们之中挑最后一份。
// 时间最新的一份始终保留。
func Select(snaps []Snapshot, p Policy, loc *time.Location, now time.Time) (keep, remove []string) {
	if len(snaps) == 0 {
		return nil, nil
	}
	if loc == nil {
		loc = time.UTC
	}
	kept := make([]bool, len(snaps))

	latest := 0
	for i, s := range snaps {
		if s.Time.After(snaps[latest].Time) {
			latest = i
		}
	}
	kept[latest] = true

	cutoff := now.Add(-time.Duration(p.Days) * 24 * time.Hour)
	var older []int
	for i, s := range snaps {
		if p.Days > 0 && !s.Time.Before(cutoff) {
			kept[i] = true
			continue
		}
		older = append(older, i)
	}

	if p.Weeks > 0 {
		first := weekStart(now, loc).AddDate(0, 0, -7*(p.Weeks-1))
		keepLastPerGroup(snaps, older, kept, func(t time.Time) (time.Time, bool) {
			w := weekStart(t, loc)
			return w, !w.Before(first)
		})
	}
	if p.Months > 0 {
		first := monthStart(now, loc).AddDate(0, -(p.Months - 1), 0)
		keepLastPerGroup(snaps, older, kept, func(t time.Time) (time.Time, bool) {
			m := monthStart(t, loc)
			return m, !m.Before(first)
		})
	}

	for i, s := range snaps {
		if kept[i] {
			keep = append(keep, s.ID)
		} else {
			remove = append(remove, s.ID)
		}
	}
	return keep, remove
}

// keepLastPerGroup 把 idx 中的快照按 group 分组，在窗口内的每组保留时间最晚的一份
func keepLastPerGroup(snaps []Snapshot, idx []int, kept []bool, group func(time.Time) (time.Time, bool)) {
	last := map[time.Time]int{}
	for _, i := range idx {
		g, ok := group(snaps[i].Time)
		if !ok {
			continue
		}
		if j, seen := last[g]; !seen || snaps[i].Time.After(snaps[j].Time) {
			last[g] = i
		}
	}
	for _, i := range last {
		kept[i] = true
	}
}

// weekStart 返回 t 在 loc 中所在自然周的周一，以 UTC 零点表示的日历日期，不受夏令时影响
func weekStart(t time.Time, loc *time.Location) time.Time {
	y, m, d := t.In(loc).Date()
	day := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	offset := (int(day.Weekday()) + 6) % 7 // 周一为 0
	return day.AddDate(0, 0, -offset)
}

// monthStart 返回 t 在 loc 中所在自然月的 1 日，以 UTC 零点表示
func monthStart(t time.Time, loc *time.Location) time.Time {
	y, m, _ := t.In(loc).Date()
	return time.Date(y, m, 1, 0, 0, 0, 0, time.UTC)
}
