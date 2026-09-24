package password

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHashAndVerify(t *testing.T) {
	h, err := Hash("correct horse battery")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(h, "$argon2id$"), "使用 argon2id 的 PHC 格式")
	assert.NotContains(t, h, "correct horse battery")

	assert.True(t, Verify("correct horse battery", h))
	assert.False(t, Verify("correct horse batterY", h))
	assert.False(t, Verify("", h))

	h2, _ := Hash("correct horse battery")
	assert.NotEqual(t, h, h2, "每次使用不同的盐")

	assert.False(t, Verify("x", "not-a-hash"))
	assert.False(t, Verify("x", "$argon2id$v=19$m=bad$salt$hash"))
}
