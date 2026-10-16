// Package retention 把任务的保留规则映射成 restic forget 参数。
// 表单里「约 N 个快照」的估算在面板 frontend/src/lib/retention.ts，按这里映射出的 forget 规则计算。
package retention

import (
	"errors"
	"fmt"
	"strconv"
)

// Preset 保留规则预设
type Preset string

const (
	// Preset7Days 保留最近 7 天内的每一次备份
	Preset7Days Preset = "7d"
	// Preset30Days 保留最近 30 天内的每一次备份
	Preset30Days Preset = "30d"
	// PresetLongTerm 最近 7 天每天一份，到 4 周每周一份，到 6 个月每月一份
	PresetLongTerm Preset = "long"
	// PresetCustom 与「长期」同一句式，三个数字自定义
	PresetCustom Preset = "custom"
)

// MaxCount 自定义规则中每个数字的上限
const MaxCount = 1000

// Policy 一个任务的保留规则；Days/Weeks/Months 只在 PresetCustom 时生效，0 表示不按该粒度保留
type Policy struct {
	Preset Preset
	Days   int
	Weeks  int
	Months int
}

// rule 归一化后的规则：within 非零时按时间窗口保留，否则按天/周/月计数保留
type rule struct {
	withinDays          int
	days, weeks, months int
}

// normalize 校验并展开预设
func (p Policy) normalize() (rule, error) {
	switch p.Preset {
	case Preset7Days:
		return rule{withinDays: 7}, nil
	case Preset30Days:
		return rule{withinDays: 30}, nil
	case PresetLongTerm:
		return rule{days: 7, weeks: 4, months: 6}, nil
	case PresetCustom:
		for _, n := range []int{p.Days, p.Weeks, p.Months} {
			if n < 0 || n > MaxCount {
				return rule{}, fmt.Errorf("保留数量必须在 0-%d 之间", MaxCount)
			}
		}
		if p.Days == 0 && p.Weeks == 0 && p.Months == 0 {
			return rule{}, errors.New("自定义保留规则至少要有一项大于 0")
		}
		return rule{days: p.Days, weeks: p.Weeks, months: p.Months}, nil
	}
	return rule{}, fmt.Errorf("未知的保留规则：%q", p.Preset)
}

// Validate 校验规则
func (p Policy) Validate() error {
	_, err := p.normalize()
	return err
}

// ForgetArgs 生成 restic forget 的保留参数
func (p Policy) ForgetArgs() ([]string, error) {
	r, err := p.normalize()
	if err != nil {
		return nil, err
	}
	if r.withinDays > 0 {
		return []string{"--keep-within", strconv.Itoa(r.withinDays) + "d"}, nil
	}
	var args []string
	if r.days > 0 {
		args = append(args, "--keep-daily", strconv.Itoa(r.days))
	}
	if r.weeks > 0 {
		args = append(args, "--keep-weekly", strconv.Itoa(r.weeks))
	}
	if r.months > 0 {
		args = append(args, "--keep-monthly", strconv.Itoa(r.months))
	}
	return args, nil
}
