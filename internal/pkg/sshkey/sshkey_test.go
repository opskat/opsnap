package sshkey

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerate(t *testing.T) {
	t.Run("无口令的 ed25519 私钥与公钥配对", func(t *testing.T) {
		got, err := Generate("")
		require.NoError(t, err)
		assert.Empty(t, got.Passphrase)
		assert.True(t, strings.HasPrefix(got.PrivateKey, "-----BEGIN OPENSSH PRIVATE KEY-----"))
		assert.True(t, strings.HasPrefix(got.PublicKey, "ssh-ed25519 "))
		assert.True(t, strings.HasSuffix(got.PublicKey, " "+Comment))
		assert.NotContains(t, got.PublicKey, "\n")

		pub, err := PublicKeyOf(got.PrivateKey, "")
		require.NoError(t, err)
		assert.Equal(t, got.PublicKey, pub)
	})

	t.Run("带口令的私钥只能用这个口令解开", func(t *testing.T) {
		got, err := Generate("right-pass")
		require.NoError(t, err)
		assert.Equal(t, "right-pass", got.Passphrase)

		pub, err := PublicKeyOf(got.PrivateKey, "right-pass")
		require.NoError(t, err)
		assert.Equal(t, got.PublicKey, pub)

		_, err = PublicKeyOf(got.PrivateKey, "")
		assert.Error(t, err, "带口令的私钥不能用空口令解析")
		_, err = PublicKeyOf(got.PrivateKey, "wrong-pass")
		assert.Error(t, err)
	})

	t.Run("两次生成不同", func(t *testing.T) {
		a, err := Generate("")
		require.NoError(t, err)
		b, err := Generate("")
		require.NoError(t, err)
		assert.NotEqual(t, a.PrivateKey, b.PrivateKey)
		assert.NotEqual(t, a.PublicKey, b.PublicKey)
	})
}

func TestPublicKeyOfInvalidKey(t *testing.T) {
	_, err := PublicKeyOf("not a key", "")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "not a key", "错误文案不带私钥内容")
}
