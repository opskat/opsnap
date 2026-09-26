package netchain

import (
	"errors"
	"fmt"

	"golang.org/x/crypto/ssh"
)

// ParsePrivateKey 解析 OpenSSH 或 PEM 格式的 RSA、ECDSA、Ed25519 私钥。
// 私钥已加密时需要口令：缺少时返回 ErrPassphraseMissing，错误时返回 ErrPassphraseWrong；
// 其他无法解析的情况返回 ErrKeyInvalid。未加密的私钥忽略口令。
func ParsePrivateKey(key, passphrase []byte) (ssh.Signer, error) {
	signer, err := ssh.ParsePrivateKey(key)
	if err == nil {
		return signer, nil
	}
	var missing *ssh.PassphraseMissingError
	if !errors.As(err, &missing) {
		return nil, fmt.Errorf("%w: %w", ErrKeyInvalid, err)
	}
	if len(passphrase) == 0 {
		return nil, ErrPassphraseMissing
	}
	signer, err = ssh.ParsePrivateKeyWithPassphrase(key, passphrase)
	if err != nil {
		// 私钥已确认是加密的，解密后仍无法解析只能是口令不对（旧式 PEM 的填充校验并不总能识别）
		return nil, ErrPassphraseWrong
	}
	return signer, nil
}
