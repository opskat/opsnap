package schedule

import (
	"fmt"
	"strconv"
	"strings"
)

// cronSchedule 是标准 5 段 Cron 表达式（分 时 日 月 周）编译后的字段集合。
type cronSchedule struct {
	minutes [60]bool
	hours   [24]bool
	doms    [32]bool // 索引 1-31
	months  [13]bool // 索引 1-12
	dows    [7]bool  // 索引 0-6，0 和 7 都表示周日
	domStar bool
	dowStar bool
}

var (
	cronFieldNames   = [5]string{"分钟", "小时", "日", "月", "星期"}
	cronFieldNamesEn = [5]string{"minute", "hour", "day-of-month", "month", "day-of-week"}
)

// fieldError 第 i 段无效
func fieldError(i int, e *ValidationError) *ValidationError {
	return &ValidationError{Reason: fmt.Sprintf("%s字段无效：%s", cronFieldNames[i], e.Reason),
		En: fmt.Sprintf("invalid %s field: %s", cronFieldNamesEn[i], e.En)}
}

// compileCron 解析标准 5 段 Cron 表达式。出错时返回中英文的原因，可直接作为字段错误提示。
func compileCron(expr string) (cronSchedule, *ValidationError) {
	fields := strings.Fields(strings.TrimSpace(expr))
	if len(fields) != 5 {
		return cronSchedule{}, invalid("cron 表达式需要 5 段（分 时 日 月 周），实际为 %d 段",
			"a cron expression needs 5 fields (minute hour day-of-month month day-of-week), got %d", len(fields))
	}

	var cs cronSchedule
	minutes, err := parseCronField(fields[0], 0, 59)
	if err != nil {
		return cronSchedule{}, fieldError(0, err)
	}
	copy(cs.minutes[:], minutes)

	hours, err := parseCronField(fields[1], 0, 23)
	if err != nil {
		return cronSchedule{}, fieldError(1, err)
	}
	copy(cs.hours[:], hours)

	cs.domStar = strings.TrimSpace(fields[2]) == "*"
	doms, err := parseCronField(fields[2], 1, 31)
	if err != nil {
		return cronSchedule{}, fieldError(2, err)
	}
	copy(cs.doms[:], doms)

	months, err := parseCronField(fields[3], 1, 12)
	if err != nil {
		return cronSchedule{}, fieldError(3, err)
	}
	copy(cs.months[:], months)

	cs.dowStar = strings.TrimSpace(fields[4]) == "*"
	dows, err := parseCronField(fields[4], 0, 7)
	if err != nil {
		return cronSchedule{}, fieldError(4, err)
	}
	for i, v := range dows {
		if i == 7 {
			if v {
				cs.dows[0] = true
			}
			continue
		}
		cs.dows[i] = cs.dows[i] || v
	}

	return cs, nil
}

// parseCronField 解析单个 Cron 字段，支持 *、*/step、a、a-b、a-b/step，以逗号分隔的列表。
// 返回长度为 fieldMax+1 的布尔切片，下标即取值。
func parseCronField(field string, fieldMin, fieldMax int) ([]bool, *ValidationError) {
	out := make([]bool, fieldMax+1)
	items := strings.Split(field, ",")
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			return nil, invalid("空的取值项", "empty value")
		}

		rangePart := item
		step := 1
		if idx := strings.Index(item, "/"); idx >= 0 {
			rangePart = item[:idx]
			stepStr := item[idx+1:]
			s, err := strconv.Atoi(stepStr)
			if err != nil || s <= 0 {
				return nil, invalid("步长 %q 无效", "invalid step %q", stepStr)
			}
			step = s
		}

		lo, hi := fieldMin, fieldMax
		switch {
		case rangePart == "*":
			// lo/hi 已是完整范围
		case strings.Contains(rangePart, "-"):
			parts := strings.SplitN(rangePart, "-", 2)
			l, err1 := strconv.Atoi(parts[0])
			h, err2 := strconv.Atoi(parts[1])
			if err1 != nil || err2 != nil {
				return nil, invalid("取值范围 %q 无效", "invalid range %q", rangePart)
			}
			lo, hi = l, h
		default:
			v, err := strconv.Atoi(rangePart)
			if err != nil {
				return nil, invalid("取值 %q 不是数字", "value %q is not a number", rangePart)
			}
			lo, hi = v, v
		}

		if lo < fieldMin || hi > fieldMax || lo > hi {
			return nil, invalid("取值 %q 超出范围 %d-%d", "value %q is outside %d-%d", item, fieldMin, fieldMax)
		}
		for v := lo; v <= hi; v += step {
			out[v] = true
		}
	}
	return out, nil
}
