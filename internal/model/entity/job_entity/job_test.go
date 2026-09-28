package job_entity

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/schedule"
)

var han = regexp.MustCompile(`\p{Han}`)

// 计划不合法时字段旁的原因随界面语言给出（docs/specs/2026-09-27-backup-jobs.md「第 4 步」「界面」）
func TestCheckScheduleReasonLanguage(t *testing.T) {
	en := i18n.WithLanguage(context.Background(), code.LangEn)
	zh := i18n.WithLanguage(context.Background(), code.LangZhCN)
	cases := map[string]schedule.Spec{
		"Cron 段数不对":  {Kind: schedule.KindCron, Cron: "* * *", Timezone: "UTC"},
		"Cron 取值越界":  {Kind: schedule.KindCron, Cron: "61 * * * *", Timezone: "UTC"},
		"Cron 步长无效":  {Kind: schedule.KindCron, Cron: "*/0 * * * *", Timezone: "UTC"},
		"Cron 不是数字":  {Kind: schedule.KindCron, Cron: "a * * * *", Timezone: "UTC"},
		"Cron 永不触发":  {Kind: schedule.KindCron, Cron: "0 0 31 2 *", Timezone: "UTC"},
		"每周没有选星期几":   {Kind: schedule.KindWeekly, Hour: 1, Timezone: "UTC"},
		"每小时分钟越界":    {Kind: schedule.KindHourly, Minute: 60, Timezone: "UTC"},
		"未知的频率":      {Kind: "yearly", Timezone: "UTC"},
		"每天小时越界":     {Kind: schedule.KindDaily, Hour: 24, Timezone: "UTC"},
		"每周星期几越界":    {Kind: schedule.KindWeekly, Weekdays: []time.Weekday{9}, Timezone: "UTC"},
		"Cron 范围写反了": {Kind: schedule.KindCron, Cron: "0 5-3 * * *", Timezone: "UTC"},
	}
	for name, s := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := CheckSchedule(en, s)
			msg := errMsg(t, err)
			assert.Contains(t, msg, "Invalid schedule: ")
			assert.Greater(t, len(msg), len("Invalid schedule: "), "带上原因")
			assert.False(t, han.MatchString(msg), "英文界面的原因不含中文：%q", msg)
			assert.NotContains(t, msg, "%!", "格式化参数完整：%q", msg)

			_, err = CheckSchedule(zh, s)
			msg = errMsg(t, err)
			assert.Contains(t, msg, "执行计划不正确：")
			assert.NotContains(t, msg, "%!", "格式化参数完整：%q", msg)
		})
	}
}

func errMsg(t *testing.T, err error) string {
	t.Helper()
	require.Error(t, err)
	var he *httputils.Error
	require.True(t, errors.As(err, &he), "%v", err)
	assert.Equal(t, code.JobScheduleInvalid, he.Code)
	return he.Msg
}
