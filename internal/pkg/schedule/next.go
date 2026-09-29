package schedule

import (
	"errors"
	"fmt"
	"time"
)

// maxDayScan 是 Next/Prev 在两次触发之间逐日搜索的上限，避免 Cron 组合（如 2 月 31 日）永不匹配时死循环。
// 4 年多可以覆盖闰年周期，足够判定"确实不会触发"。
const maxDayScan = 4*366 + 1

// ErrNeverFires 表示这个 Spec 在可预见的时间内不会触发（例如 Cron 的日期组合永远不成立）。
var ErrNeverFires = errors.New("按这个计划，在可预见的时间内不会触发")

// Next 返回从 after（不含）往后，按 Spec 所在时区计算的接下来 n 次触发时间，按时间升序排列。
// 夏令时切换时：不存在的时刻顺延到切换后触发，重复的时刻只触发一次。
func (s Spec) Next(after time.Time, n int) ([]time.Time, error) {
	if n <= 0 {
		return nil, nil
	}
	loc, err := time.LoadLocation(s.Timezone)
	if err != nil {
		return nil, fmt.Errorf("时区无效：%w", err)
	}
	var cs cronSchedule
	if s.Kind == KindCron {
		var cerr *ValidationError
		if cs, cerr = compileCron(s.Cron); cerr != nil {
			return nil, cerr
		}
	}

	out := make([]time.Time, 0, n)
	cursor := after
	y, mo, d := cursor.In(loc).Date()
	// scanned 为距上一次找到触发时间的天数：上限按每两次触发之间计算，稀疏但合法的计划（如 2 月 29 日）也能给出多次
	for scanned := 0; len(out) < n; scanned++ {
		if scanned > maxDayScan {
			return nil, ErrNeverFires
		}
		for _, c := range candidatesOnDay(loc, y, mo, d, s, cs) {
			if c.After(cursor) {
				out = append(out, c)
				cursor = c
				scanned = 0
				if len(out) == n {
					break
				}
			}
		}
		y, mo, d = addDays(y, mo, d, 1)
	}
	return out, nil
}

// Prev 返回不晚于 at 的最近一次计划触发时间（恰好等于 at 时返回 at 本身），
// 供调度器判断"上一次计划时间是否已经执行过"以决定是否补跑。
func (s Spec) Prev(at time.Time) (time.Time, error) {
	loc, err := time.LoadLocation(s.Timezone)
	if err != nil {
		return time.Time{}, fmt.Errorf("时区无效：%w", err)
	}
	var cs cronSchedule
	if s.Kind == KindCron {
		var cerr *ValidationError
		if cs, cerr = compileCron(s.Cron); cerr != nil {
			return time.Time{}, cerr
		}
	}

	cursor := at
	y, mo, d := cursor.In(loc).Date()
	for scanned := 0; scanned <= maxDayScan; scanned++ {
		candidates := candidatesOnDay(loc, y, mo, d, s, cs)
		for i := len(candidates) - 1; i >= 0; i-- {
			if !candidates[i].After(cursor) {
				return candidates[i], nil
			}
		}
		y, mo, d = addDays(y, mo, d, -1)
	}
	return time.Time{}, ErrNeverFires
}

// addDays 日历日加减：按 UTC 计算，不经过时区。夏令时在午夜开始的时区（如 America/Santiago）当天没有 00:00，
// 用该时区的午夜推进会落回前一天
func addDays(y int, mo time.Month, d, n int) (int, time.Month, int) {
	return time.Date(y, mo, d+n, 0, 0, 0, 0, time.UTC).Date()
}

// candidatesOnDay 返回给定日历日（按 loc 的墙上时间）内，按升序排列的所有候选触发时间。
func candidatesOnDay(loc *time.Location, y int, mo time.Month, d int, s Spec, cs cronSchedule) []time.Time {
	switch s.Kind {
	case KindHourly:
		out := make([]time.Time, 0, 24)
		for h := 0; h < 24; h++ {
			out = append(out, localDateTime(loc, y, mo, d, h, s.Minute))
		}
		return dedupeSortedTimes(out)
	case KindDaily:
		return []time.Time{localDateTime(loc, y, mo, d, s.Hour, s.Minute)}
	case KindWeekly:
		wd := time.Date(y, mo, d, 0, 0, 0, 0, time.UTC).Weekday()
		if !weekdayIn(s.Weekdays, wd) {
			return nil
		}
		return []time.Time{localDateTime(loc, y, mo, d, s.Hour, s.Minute)}
	case KindCron:
		if !cronDayMatches(cs, y, mo, d) {
			return nil
		}
		out := make([]time.Time, 0, 4)
		for h := 0; h < 24; h++ {
			if !cs.hours[h] {
				continue
			}
			for mi := 0; mi < 60; mi++ {
				if !cs.minutes[mi] {
					continue
				}
				out = append(out, localDateTime(loc, y, mo, d, h, mi))
			}
		}
		return dedupeSortedTimes(out)
	default:
		return nil
	}
}

func cronDayMatches(cs cronSchedule, y int, mo time.Month, d int) bool {
	if !cs.months[int(mo)] {
		return false
	}
	domMatch := d <= 31 && cs.doms[d]
	dowMatch := cs.dows[int(time.Date(y, mo, d, 0, 0, 0, 0, time.UTC).Weekday())]
	switch {
	case cs.domStar && cs.dowStar:
		return true
	case cs.domStar:
		return dowMatch
	case cs.dowStar:
		return domMatch
	default:
		return domMatch || dowMatch
	}
}

func weekdayIn(days []time.Weekday, wd time.Weekday) bool {
	for _, d := range days {
		if d == wd {
			return true
		}
	}
	return false
}

// dedupeSortedTimes 对时间去重（同一绝对时刻只保留一次）并按时间升序排列。
// 输入已经按 (hour, minute) 递增顺序生成；夏令时会导致个别候选折叠到同一绝对时刻。
func dedupeSortedTimes(in []time.Time) []time.Time {
	out := make([]time.Time, 0, len(in))
	for _, t := range in {
		if len(out) > 0 && !t.After(out[len(out)-1]) {
			continue
		}
		out = append(out, t)
	}
	return out
}

// localDateTime 构造 loc 时区下 y-mo-d hour:minute 对应的绝对时间。
//
// 夏令时规则：
//   - 时刻不存在（春季调快，如 02:30 被跳过）：顺延到切换后的等效时刻（如 03:30）；
//   - 时刻重复（秋季调慢，如 01:30 出现两次）：time.Date 会确定性地取其中一次，
//     多次构造得到同一个绝对时刻，从而只触发一次。
func localDateTime(loc *time.Location, y int, mo time.Month, d, hour, minute int) time.Time {
	want := time.Date(y, mo, d, hour, minute, 0, 0, loc)
	gotH, gotM := want.In(loc).Hour(), want.In(loc).Minute()
	if gotH == hour && gotM == minute {
		return want
	}
	// 不存在的时刻：Go 会折叠到切换前的偏移量。用切换后的偏移量修正，顺延到切换后。
	_, offBefore := want.Zone()
	_, offAfter := want.Add(2 * time.Hour).Zone()
	if offAfter != offBefore {
		want = want.Add(time.Duration(offAfter-offBefore) * time.Second)
	}
	return want
}
