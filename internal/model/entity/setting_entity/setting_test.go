package setting_entity

import (
	"context"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
)

func TestSettingValidate(t *testing.T) {
	ctx := context.Background()
	convey.Convey("设置校验", t, func() {
		convey.Convey("默认值合法且时区为 Asia/Shanghai", func() {
			s := Default()
			assert.Equal(t, "Asia/Shanghai", s.Timezone)
			assert.Equal(t, 2, s.MaxConcurrentRuns)
			assert.Equal(t, 90, s.RetentionDays)
			assert.False(t, s.ClearMirrorAfterBackup)
			assert.NoError(t, s.Validate(ctx))
			assert.NotNil(t, s.Location())
		})
		convey.Convey("非法取值被拒绝", func() {
			cases := map[string]func(s *Setting){
				"并发数为 0":    func(s *Setting) { s.MaxConcurrentRuns = 0 },
				"并发数过大":     func(s *Setting) { s.MaxConcurrentRuns = 65 },
				"保留天数为 0":   func(s *Setting) { s.RetentionDays = 0 },
				"保留天数过大":    func(s *Setting) { s.RetentionDays = 3651 },
				"时区不存在":     func(s *Setting) { s.Timezone = "Mars/Base" },
				"时区为空":      func(s *Setting) { s.Timezone = "" },
				"时区为 Local": func(s *Setting) { s.Timezone = "Local" },
			}
			for name, mut := range cases {
				s := Default()
				mut(s)
				assert.Error(t, s.Validate(ctx), name)
			}
		})
		convey.Convey("合法时区通过", func() {
			s := Default()
			s.Timezone = "America/New_York"
			assert.NoError(t, s.Validate(ctx))
		})
	})
}
