package kopiarepo

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateKey(t *testing.T) {
	t.Run("24 个字母数字，每 4 个一组用连字符连接", func(t *testing.T) {
		key, err := GenerateKey()
		require.NoError(t, err)
		assert.Regexp(t, regexp.MustCompile(`^[A-Za-z0-9]{4}(-[A-Za-z0-9]{4}){5}$`), key)
	})

	t.Run("每次生成的密钥不同", func(t *testing.T) {
		a, _ := GenerateKey()
		b, _ := GenerateKey()
		assert.NotEqual(t, a, b)
	})
}

func TestFingerprint(t *testing.T) {
	t.Run("取 SHA-256 十六进制的前 4 位与后 4 位，大写", func(t *testing.T) {
		// sha256("hello") = 2cf24dba...62938b9824
		assert.Equal(t, "2CF2···9824", Fingerprint("hello"))
	})

	t.Run("连字符是密钥的一部分", func(t *testing.T) {
		assert.NotEqual(t, Fingerprint("abcd-efgh"), Fingerprint("abcdefgh"))
	})
}
