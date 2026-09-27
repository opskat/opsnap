package netchain

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePrivateKey(t *testing.T) {
	k := genKeys(t)

	ok := []struct {
		name       string
		key        []byte
		passphrase string
		want       string
	}{
		{"OpenSSH Ed25519", openSSHPEM(t, k.ed25519, ""), "", "ssh-ed25519"},
		{"OpenSSH RSA", openSSHPEM(t, k.rsa, ""), "", "ssh-rsa"},
		{"OpenSSH ECDSA", openSSHPEM(t, k.ecdsa, ""), "", "ecdsa-sha2-nistp256"},
		{"OpenSSH Ed25519 带口令", openSSHPEM(t, k.ed25519, "pp"), "pp", "ssh-ed25519"},
		{"PEM RSA（PKCS#1）", pkcs1PEM(k.rsa), "", "ssh-rsa"},
		{"PEM ECDSA", ecPEM(t, k.ecdsa), "", "ecdsa-sha2-nistp256"},
		{"PEM Ed25519（PKCS#8）", pkcs8PEM(t, k.ed25519), "", "ssh-ed25519"},
		{"加密的 PEM RSA 带口令", encryptedPKCS1PEM(t, k.rsa, "pp"), "pp", "ssh-rsa"},
		{"未加密私钥忽略多填的口令", openSSHPEM(t, k.ed25519, ""), "unused", "ssh-ed25519"},
	}
	for _, c := range ok {
		t.Run(c.name, func(t *testing.T) {
			s, err := ParsePrivateKey(c.key, []byte(c.passphrase))
			require.NoError(t, err)
			assert.Equal(t, c.want, s.PublicKey().Type())
		})
	}

	bad := []struct {
		name       string
		key        []byte
		passphrase string
		want       error
	}{
		{"加密私钥缺少口令", openSSHPEM(t, k.ed25519, "pp"), "", ErrPassphraseMissing},
		{"加密 PEM 缺少口令", encryptedPKCS1PEM(t, k.rsa, "pp"), "", ErrPassphraseMissing},
		{"口令错误", openSSHPEM(t, k.rsa, "pp"), "nope", ErrPassphraseWrong},
		{"加密 PEM 口令错误", encryptedPKCS1PEM(t, k.rsa, "pp"), "nope", ErrPassphraseWrong},
		{"无法识别的格式", []byte("not a key"), "", ErrKeyInvalid},
		{"空私钥", nil, "", ErrKeyInvalid},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParsePrivateKey(c.key, []byte(c.passphrase))
			require.Error(t, err)
			assert.True(t, errors.Is(err, c.want), "err=%v", err)
		})
	}
}
