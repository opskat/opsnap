package secret

import (
	"encoding/base64"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mustNew 用给定主密钥创建加解密器，失败直接终止测试
func mustNew(t *testing.T, key string) Secret {
	t.Helper()
	s, err := New(key)
	require.NoError(t, err)
	return s
}

func TestNew(t *testing.T) {
	convey.Convey("创建加解密器", t, func() {
		convey.Convey("主密钥为空时返回缺少主密钥的错误，并说明环境变量名", func() {
			for _, key := range []string{"", "   ", "\n\t"} {
				_, err := New(key)
				assert.ErrorIs(t, err, ErrMissingKey)
				assert.Contains(t, err.Error(), EnvKey)
			}
		})
		convey.Convey("主密钥非空时创建成功", func() {
			_, err := New("a-long-enough-master-key")
			assert.NoError(t, err)
		})
	})
}

func TestEncryptDecrypt(t *testing.T) {
	convey.Convey("AES-256-GCM 加解密", t, func() {
		s := mustNew(t, "master-key-one")

		convey.Convey("加密后能解密回原文", func() {
			for _, plain := range []string{"p@ssw0rd", "", "中文口令 🔑", "-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n"} {
				ct, err := s.Encrypt(plain)
				require.NoError(t, err)
				if plain != "" {
					assert.NotContains(t, ct, plain)
				}
				got, err := s.Decrypt(ct)
				require.NoError(t, err)
				assert.Equal(t, plain, got)
			}
		})

		convey.Convey("同一明文两次加密的密文不同（随机 nonce）", func() {
			a, err := s.Encrypt("same")
			require.NoError(t, err)
			b, err := s.Encrypt("same")
			require.NoError(t, err)
			assert.NotEqual(t, a, b)
		})

		convey.Convey("用错误的主密钥解密失败", func() {
			ct, err := s.Encrypt("secret-value")
			require.NoError(t, err)
			other := mustNew(t, "master-key-two")
			_, err = other.Decrypt(ct)
			assert.ErrorIs(t, err, ErrDecrypt)
		})

		convey.Convey("密文被篡改时解密失败", func() {
			ct, err := s.Encrypt("secret-value")
			require.NoError(t, err)
			raw, err := base64.StdEncoding.DecodeString(ct)
			require.NoError(t, err)
			raw[len(raw)-1] ^= 0x01
			_, err = s.Decrypt(base64.StdEncoding.EncodeToString(raw))
			assert.ErrorIs(t, err, ErrDecrypt)
		})

		convey.Convey("格式错误的密文解密失败", func() {
			for _, bad := range []string{"", "not base64!!", base64.StdEncoding.EncodeToString([]byte("short"))} {
				_, err := s.Decrypt(bad)
				assert.ErrorIs(t, err, ErrDecrypt)
			}
		})
	})
}

func TestDefault(t *testing.T) {
	convey.Convey("默认加解密器", t, func() {
		s := mustNew(t, "master-key-default")
		SetDefault(s)
		t.Cleanup(func() { SetDefault(nil) })
		assert.Same(t, s, Default())
	})
}
