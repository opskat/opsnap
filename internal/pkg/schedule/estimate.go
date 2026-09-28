package schedule

// EstimateMaxSnapshots 按当前计划与保留策略，估算“最多保留约 K 份快照”里的上界 K。
//
// 对应保留规则 1-4：
//  1. 最近 days 天内的成功快照全部保留 —— 估算为这段时间里，按计划最多能触发多少次；
//  2. 更早的按自然周分组，每组保留最后一份，保留最近 weeks 周 —— 最多贡献 weeks 份；
//  3. 按自然月分组，每组保留最后一份，保留最近 months 个月 —— 最多贡献 months 份；
//  4. 本任务最新的一份成功快照始终保留 —— 再加 1，保证即使前三条规则一份都不覆盖
//     （例如计划频率很低、days 很小时），估算值也不会低于实际最少会保留的份数。
//
// 这是一个宽松上界（“约”），允许比真实可能保留的份数偏大，但不允许偏小。
func EstimateMaxSnapshots(s Spec, days, weeks, months int) int {
	if days < 0 {
		days = 0
	}
	if weeks < 0 {
		weeks = 0
	}
	if months < 0 {
		months = 0
	}

	runsInDays := estimateRunsInDays(s, days)
	k := runsInDays + weeks + months + 1
	if k < 1 {
		k = 1
	}
	return k
}

// estimateRunsInDays 估算在任意长度为 days 天的窗口内，按计划最多能触发多少次。
func estimateRunsInDays(s Spec, days int) int {
	if days <= 0 {
		return 0
	}
	switch s.Kind {
	case KindHourly:
		return days * 24
	case KindDaily:
		return days
	case KindWeekly:
		weeksSpan := ceilDiv(days, 7)
		return weeksSpan * len(s.Weekdays)
	case KindCron:
		cs, err := compileCron(s.Cron)
		if err != nil {
			return 0
		}
		return estimateCronRunsInDays(cs, days)
	default:
		return 0
	}
}

func estimateCronRunsInDays(cs cronSchedule, days int) int {
	perMatchingDay := countTrue(cs.minutes[:]) * countTrue(cs.hours[:])

	var matchingDaysUpper int
	dowCount := countTrue(cs.dows[:])
	domCount := countTrue(cs.doms[1:]) // 索引 0 未使用
	switch {
	case cs.domStar && cs.dowStar:
		matchingDaysUpper = days
	case cs.domStar: // 只按星期限制
		matchingDaysUpper = ceilDiv(days, 7) * dowCount
	case cs.dowStar: // 只按日限制；用最短的月长度（28 天）做保守上界
		matchingDaysUpper = ceilDiv(days, 28) * domCount
	default: // 日、星期都限制：按 cron 语义取并集，上界为两者之和
		matchingDaysUpper = ceilDiv(days, 7)*dowCount + ceilDiv(days, 28)*domCount
	}

	// 只在部分月份触发时不按月份比例缩小：保留窗口可能整个落在所选月份里（如 1 月里的 30 天），
	// 按比例得到的是平均值而不是上界

	return perMatchingDay * matchingDaysUpper
}

func countTrue(bs []bool) int {
	n := 0
	for _, b := range bs {
		if b {
			n++
		}
	}
	return n
}

func ceilDiv(a, b int) int {
	if b <= 0 {
		return 0
	}
	if a <= 0 {
		return 0
	}
	return (a + b - 1) / b
}
