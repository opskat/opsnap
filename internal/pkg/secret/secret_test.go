package secret

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBox(t *testing.T) {
	key, err := GenerateKey()
	require.NoError(t, err)
	box, err := NewBox(key)
	require.NoError(t, err)

	t.Run("加密后可以还原，且密文不含明文", func(t *testing.T) {
		ct, err := box.Encrypt([]byte("client-secret-123"))
		require.NoError(t, err)
		assert.NotContains(t, ct, "client-secret-123")
		pt, err := box.Decrypt(ct)
		require.NoError(t, err)
		assert.Equal(t, "client-secret-123", string(pt))
	})

	t.Run("同一明文每次加密结果不同", func(t *testing.T) {
		a, _ := box.Encrypt([]byte("x"))
		b, _ := box.Encrypt([]byte("x"))
		assert.NotEqual(t, a, b)
	})

	t.Run("换一把密钥无法解密", func(t *testing.T) {
		ct, _ := box.Encrypt([]byte("x"))
		other, _ := GenerateKey()
		otherBox, _ := NewBox(other)
		_, err := otherBox.Decrypt(ct)
		assert.ErrorIs(t, err, ErrDecrypt)
	})

	t.Run("格式错误的密文返回解密错误", func(t *testing.T) {
		_, err := box.Decrypt("not-a-ciphertext")
		assert.ErrorIs(t, err, ErrDecrypt)
	})

	t.Run("密钥长度不是 32 字节时拒绝", func(t *testing.T) {
		_, err := NewBox([]byte("short"))
		assert.ErrorIs(t, err, ErrInvalidKey)
	})
}

func TestParseKey(t *testing.T) {
	key, _ := GenerateKey()
	t.Run("接受标准 base64 编码的 32 字节", func(t *testing.T) {
		got, err := ParseKey(base64.StdEncoding.EncodeToString(key))
		require.NoError(t, err)
		assert.Equal(t, key, got)
	})
	t.Run("忽略首尾空白", func(t *testing.T) {
		got, err := ParseKey("  " + EncodeKey(key) + "\n")
		require.NoError(t, err)
		assert.Equal(t, key, got)
	})
	t.Run("不是 base64 或长度不对时报错", func(t *testing.T) {
		_, err := ParseKey("@@@")
		assert.ErrorIs(t, err, ErrInvalidKey)
		_, err = ParseKey(base64.StdEncoding.EncodeToString([]byte("16-bytes-only!!!")))
		assert.ErrorIs(t, err, ErrInvalidKey)
	})
}

func TestKeyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, KeyFileName)

	t.Run("文件不存在时读取返回 os.ErrNotExist", func(t *testing.T) {
		_, err := ReadKeyFile(path)
		assert.ErrorIs(t, err, os.ErrNotExist)
	})

	t.Run("写入的文件权限为 0600，且可以读回", func(t *testing.T) {
		key, _ := GenerateKey()
		require.NoError(t, WriteKeyFile(path, key))
		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
		got, err := ReadKeyFile(path)
		require.NoError(t, err)
		assert.Equal(t, key, got)
	})

	t.Run("不覆盖已存在的密钥文件", func(t *testing.T) {
		key, _ := GenerateKey()
		assert.Error(t, WriteKeyFile(path, key))
	})
}
