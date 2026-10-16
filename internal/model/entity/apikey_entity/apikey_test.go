package apikey_entity

import (
	"testing"
	"time"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
)

func TestAPIKeyUsable(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	convey.Convey("API 密钥可用性", t, func() {
		convey.Convey("永不过期且未撤销可用", func() {
			assert.True(t, (&APIKey{}).IsUsable(now))
		})
		convey.Convey("过期时刻起不可用", func() {
			k := &APIKey{ExpiresAt: now.Unix()}
			assert.False(t, k.IsUsable(now))
			assert.True(t, k.IsUsable(now.Add(-time.Second)))
		})
		convey.Convey("撤销后不可用", func() {
			assert.False(t, (&APIKey{RevokedAt: now.Unix()}).IsUsable(now))
		})
	})
}
