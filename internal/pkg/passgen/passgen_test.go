package passgen

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 容易看错的字符一个都不能出现
var lookalikes = []string{"0", "O", "1", "l", "I"}

func TestRandom(t *testing.T) {
	t.Run("长度正确且不含容易看错的字符", func(t *testing.T) {
		for _, n := range []int{1, 16, 32, 64} {
			got, err := Random(n)
			require.NoError(t, err)
			assert.Len(t, []rune(got), n)
			for _, c := range lookalikes {
				assert.False(t, strings.Contains(got, c), "%q 里出现了 %q", got, c)
			}
		}
	})

	t.Run("两次生成不同", func(t *testing.T) {
		a, err := Random(32)
		require.NoError(t, err)
		b, err := Random(32)
		require.NoError(t, err)
		assert.NotEqual(t, a, b)
	})

	t.Run("各字符集都会用到：大小写字母、数字与符号", func(t *testing.T) {
		var all strings.Builder
		for range 200 {
			s, err := Random(32)
			require.NoError(t, err)
			all.WriteString(s)
		}
		text := all.String()
		assert.Regexp(t, "[a-z]", text)
		assert.Regexp(t, "[A-Z]", text)
		assert.Regexp(t, "[0-9]", text)
		assert.Regexp(t, `[!@#$%^&*()\-_=+\[\]{};:,.?/~]`, text)
	})

	t.Run("长度必须大于 0", func(t *testing.T) {
		_, err := Random(0)
		assert.Error(t, err)
	})
}

func TestPassword(t *testing.T) {
	got, err := Password()
	require.NoError(t, err)
	assert.Len(t, []rune(got), 32)
}
