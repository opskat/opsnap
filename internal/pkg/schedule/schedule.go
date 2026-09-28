// Package schedule 描述备份任务的调度计划（频率、时区）与保留策略，
// 供任务向导的校验/预览与调度器的下一次/上一次触发时间计算共用。
package schedule

import (
	"fmt"
	"sort"
	"time"
)

// Kind 是调度频率的种类。
type Kind string

const (
	// KindHourly 每小时执行一次，在固定分钟触发。
	KindHourly Kind = "hourly"
	// KindDaily 每天在固定时刻执行一次。
	KindDaily Kind = "daily"
	// KindWeekly 在每周选定的一个或多个星期几的固定时刻执行。
	KindWeekly Kind = "weekly"
	// KindCron 使用标准 5 段 Cron 表达式（分 时 日 月 周）。
	KindCron Kind = "cron"
)

// Spec 是一个调度计划。字段按 Kind 解释：
//   - KindHourly：Minute；
//   - KindDaily：Hour、Minute；
//   - KindWeekly：Hour、Minute、Weekdays；
//   - KindCron：Cron。
//
// Timezone 对所有种类都必填，取值为 IANA 时区名称（如 "Asia/Shanghai"）。
type Spec struct {
	Kind     Kind
	Minute   int
	Hour     int
	Weekdays []time.Weekday
	Cron     string
	Timezone string
}

// ValidationError 表示 Spec 未通过校验：Reason 为中文原因（即 Error()），En 为同一原因的英文，
// 都可直接作为字段错误提示，由调用方按界面语言选用。
type ValidationError struct {
	Reason string
	En     string
}

func (e *ValidationError) Error() string { return e.Reason }

// invalid 中英文的同一条原因
func invalid(zh, en string, args ...any) *ValidationError {
	return &ValidationError{Reason: fmt.Sprintf(zh, args...), En: fmt.Sprintf(en, args...)}
}

// NeverFiresEn ErrNeverFires 的英文原因
const NeverFiresEn = "the schedule never fires in the foreseeable future"

// Validate 校验 Spec 是否合法。ok 为 false 时，reason 是可直接展示给用户的原因（中文，英文见 Parse 返回的 *ValidationError）。
func Validate(s Spec) (reason string, ok bool) {
	if e := validate(s); e != nil {
		return e.Reason, false
	}
	return "", true
}

func validate(s Spec) *ValidationError {
	if s.Timezone == "" {
		return invalid("时区不能为空", "a time zone is required")
	}
	if _, err := time.LoadLocation(s.Timezone); err != nil {
		return invalid("时区不是合法的 IANA 时区名称", "not a valid IANA time zone")
	}

	switch s.Kind {
	case KindHourly:
		if s.Minute < 0 || s.Minute > 59 {
			return invalid("分钟必须在 0-59 之间", "minute must be between 0 and 59")
		}
	case KindDaily, KindWeekly:
		if s.Hour < 0 || s.Hour > 23 {
			return invalid("小时必须在 0-23 之间", "hour must be between 0 and 23")
		}
		if s.Minute < 0 || s.Minute > 59 {
			return invalid("分钟必须在 0-59 之间", "minute must be between 0 and 59")
		}
		if s.Kind == KindDaily {
			return nil
		}
		if len(s.Weekdays) == 0 {
			return invalid("至少选择一个星期几", "select at least one day of the week")
		}
		for _, wd := range s.Weekdays {
			if wd < time.Sunday || wd > time.Saturday {
				return invalid("星期几必须在 0-6 之间", "day of the week must be between 0 and 6")
			}
		}
	case KindCron:
		if _, err := compileCron(s.Cron); err != nil {
			return err
		}
	default:
		return invalid("未知的调度类型：%s", "unknown schedule type: %s", s.Kind)
	}
	return nil
}

// Parse 校验并规范化 Spec：Weekdays 去重排序。非法输入返回 *ValidationError，
// 其 Error() 与 En 可直接作为字段错误提示。
func Parse(s Spec) (Spec, error) {
	if e := validate(s); e != nil {
		return Spec{}, e
	}

	out := s
	if s.Kind == KindWeekly {
		out.Weekdays = dedupeSortWeekdays(s.Weekdays)
	}
	return out, nil
}

func dedupeSortWeekdays(in []time.Weekday) []time.Weekday {
	seen := make(map[time.Weekday]bool, len(in))
	out := make([]time.Weekday, 0, len(in))
	for _, wd := range in {
		if !seen[wd] {
			seen[wd] = true
			out = append(out, wd)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
