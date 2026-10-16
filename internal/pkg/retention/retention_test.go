package retention

import (
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestForgetArgs(t *testing.T) {
	convey.Convey("保留规则映射成 restic forget 参数", t, func() {
		cases := []struct {
			name   string
			policy Policy
			want   []string
		}{
			{"7 天保留期内每一次", Policy{Preset: Preset7Days}, []string{"--keep-within", "7d"}},
			{"30 天保留期内每一次", Policy{Preset: Preset30Days}, []string{"--keep-within", "30d"}},
			{"长期：7 天每天、4 周每周、6 个月每月", Policy{Preset: PresetLongTerm}, []string{"--keep-daily", "7", "--keep-weekly", "4", "--keep-monthly", "6"}},
			{"长期忽略自定义数字", Policy{Preset: PresetLongTerm, Days: 1, Weeks: 1, Months: 1}, []string{"--keep-daily", "7", "--keep-weekly", "4", "--keep-monthly", "6"}},
			{"自定义三个数字", Policy{Preset: PresetCustom, Days: 3, Weeks: 8, Months: 12}, []string{"--keep-daily", "3", "--keep-weekly", "8", "--keep-monthly", "12"}},
			{"自定义某项为 0 时省略", Policy{Preset: PresetCustom, Days: 14, Weeks: 8}, []string{"--keep-daily", "14", "--keep-weekly", "8"}},
			{"自定义只留每月", Policy{Preset: PresetCustom, Months: 24}, []string{"--keep-monthly", "24"}},
		}
		for _, tc := range cases {
			got, err := tc.policy.ForgetArgs()
			require.NoError(t, err, tc.name)
			assert.Equal(t, tc.want, got, tc.name)
		}
	})
	convey.Convey("无效规则报错，避免 forget 没有任何保留条件", t, func() {
		for _, p := range []Policy{
			{},
			{Preset: "forever"},
			{Preset: PresetCustom},
			{Preset: PresetCustom, Days: -1, Weeks: 4},
			{Preset: PresetCustom, Days: 7, Months: -6},
			{Preset: PresetCustom, Days: MaxCount + 1},
		} {
			_, err := p.ForgetArgs()
			assert.Error(t, err, "%+v", p)
			assert.Error(t, p.Validate(), "%+v", p)
		}
	})
}
