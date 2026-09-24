package secret

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHashToken(t *testing.T) {
	t.Run("输出 SHA-256 的十六进制摘要，与已落库的哈希保持一致", func(t *testing.T) {
		assert.Equal(t, "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824", HashToken("hello"))
	})

	t.Run("不同令牌得到不同哈希，且哈希不含原文", func(t *testing.T) {
		a, b := HashToken("onp_aaaa"), HashToken("onp_bbbb")
		assert.NotEqual(t, a, b)
		assert.NotContains(t, a, "onp_aaaa")
	})
}
