// Package schedule 按配置的时区计算任务的下次运行时间。
// 预设（每小时、每 6 小时、每天、每周）都展开成 5 段 cron 表达式，用同一套解析器计算，
// 因此夏令时切换时的行为一致：始终保持本地墙钟时刻。
package schedule

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// 计划类型
const (
	Hourly  = "hourly" // 每小时整点
	Every6h = "6h"     // 每 6 小时（本地 0、6、12、18 点）
	Daily   = "daily"  // 每天 Time
	Weekly  = "weekly" // 每周 Weekday 的 Time
	Cron    = "cron"   // 自定义 5 段 cron 表达式
)

// Spec 一个任务的计划
type Spec struct {
	Kind string
	// Time 24 小时制 HH:MM，Daily 与 Weekly 使用
	Time string
	// Weekday 0 表示周日，6 表示周六，Weekly 使用
	Weekday int
	// Cron 5 段表达式，Cron 使用
	Cron string
}

var parser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// expr 展开成 cron 表达式
func (s Spec) expr() (string, error) {
	switch s.Kind {
	case Hourly:
		return "0 * * * *", nil
	case Every6h:
		return "0 */6 * * *", nil
	case Daily:
		h, m, err := parseClock(s.Time)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%d %d * * *", m, h), nil
	case Weekly:
		h, m, err := parseClock(s.Time)
		if err != nil {
			return "", err
		}
		if s.Weekday < 0 || s.Weekday > 6 {
			return "", errors.New("星期须在 0（周日）到 6（周六）之间")
		}
		return fmt.Sprintf("%d %d * * %d", m, h, s.Weekday), nil
	case Cron:
		e := strings.TrimSpace(s.Cron)
		if e == "" {
			return "", errors.New("请填写 cron 表达式")
		}
		if strings.HasPrefix(e, "@every") {
			return "", errors.New("不支持 @every，请用 5 段 cron 表达式")
		}
		if strings.HasPrefix(e, "CRON_TZ=") || strings.HasPrefix(e, "TZ=") {
			return "", errors.New("不支持在表达式里指定时区，计划按设置中的时区执行")
		}
		return e, nil
	}
	return "", fmt.Errorf("未知的计划类型：%q", s.Kind)
}

// parseClock 解析严格的 HH:MM
func parseClock(v string) (hour, minute int, err error) {
	bad := fmt.Errorf("时刻 %q 须写成 HH:MM，例如 03:30", v)
	if len(v) != 5 || v[2] != ':' {
		return 0, 0, bad
	}
	h, err1 := strconv.Atoi(v[:2])
	m, err2 := strconv.Atoi(v[3:])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, bad
	}
	return h, m, nil
}

func (s Spec) parse() (cron.Schedule, error) {
	e, err := s.expr()
	if err != nil {
		return nil, err
	}
	sch, err := parser.Parse(e)
	if err != nil {
		return nil, fmt.Errorf("cron 表达式无效：%w", err)
	}
	return sch, nil
}

// Validate 校验计划，并确认它在可预见的将来会运行
func (s Spec) Validate() error {
	_, err := Next(s, time.Now(), time.UTC)
	return err
}

// Next 返回严格晚于 after 的下次运行时间，按 loc 解释计划；loc 为 nil 时用 UTC。
// 返回值的时区是 loc。
func Next(spec Spec, after time.Time, loc *time.Location) (time.Time, error) {
	if loc == nil {
		loc = time.UTC
	}
	sch, err := spec.parse()
	if err != nil {
		return time.Time{}, err
	}
	after = after.In(loc)
	next := sch.Next(after)
	// 夏令时回拨时同一个墙上时刻出现两次。按固定时刻运行的计划只在第一次运行：
	// 和 after 在同一个墙上分钟的结果跳过。每小时这类按间隔的计划照常每个小时都运行。
	if spec.fixedHour() {
		for !next.IsZero() && sameWallMinute(next, after) {
			next = sch.Next(next)
		}
	}
	if next.IsZero() {
		return time.Time{}, errors.New("这个计划永远不会运行")
	}
	return next, nil
}

// fixedHour 计划是否只在固定的钟点运行（每天、每周、每 6 小时，或小时字段不含 * 的 cron）
func (s Spec) fixedHour() bool {
	switch s.Kind {
	case Daily, Weekly, Every6h:
		return true
	case Cron:
		e := strings.TrimSpace(s.Cron)
		if strings.HasPrefix(e, "@") {
			return e != "@hourly"
		}
		f := strings.Fields(e)
		return len(f) >= 2 && !strings.Contains(f[1], "*")
	}
	return false
}

// sameWallMinute 两个时刻（同一时区）的日期和时分是否相同
func sameWallMinute(a, b time.Time) bool {
	const layout = "2006-01-02 15:04"
	return a.Format(layout) == b.Format(layout)
}

// NextN 返回之后的 n 次运行时间
func NextN(spec Spec, after time.Time, loc *time.Location, n int) ([]time.Time, error) {
	out := make([]time.Time, 0, n)
	for len(out) < n {
		next, err := Next(spec, after, loc)
		if err != nil {
			return nil, err
		}
		out = append(out, next)
		after = next
	}
	return out, nil
}
