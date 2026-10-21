// Package sshkey 生成与表示 SSH 密钥：ed25519 密钥对的生成、authorized_keys 一行的格式化，
// 以及私钥到公钥的换算。私钥内容只经内存传递，错误信息不含私钥正文。
package sshkey

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/opskat/opsnap/internal/pkg/sshconn"
)

// Comment 公钥写进 authorized_keys 时用的注释
const Comment = "opsnap"

// PassphraseSize 随机私钥口令的长度
const PassphraseSize = 16

// AuthorizedKeyLine 把公钥格式化成 authorized_keys 的一行：`<type> <base64> <comment>`
func AuthorizedKeyLine(pub ssh.PublicKey) string {
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))
	return line + " " + Comment
}

// PublicKeyOf 由私钥算出 authorized_keys 一行的公钥。私钥无法解析或口令错误时返回
// sshconn 的认证错误（错误信息不含私钥内容）。
func PublicKeyOf(privateKey, passphrase string) (string, error) {
	signer, err := sshconn.ParsePrivateKey(privateKey, passphrase)
	if err != nil {
		return "", err
	}
	return AuthorizedKeyLine(signer.PublicKey()), nil
}

// Generated 一组新生成的 ed25519 密钥；Passphrase 为空表示私钥没有口令
type Generated struct {
	PrivateKey string
	PublicKey  string
	Passphrase string
}

// Generate 生成 ed25519 密钥对，私钥写成 OpenSSH 格式；passphrase 非空时用它加密私钥。
func Generate(passphrase string) (*Generated, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	var block *pem.Block
	if passphrase == "" {
		block, err = ssh.MarshalPrivateKey(priv, "")
	} else {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(passphrase))
	}
	if err != nil {
		return nil, err
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, err
	}
	return &Generated{
		PrivateKey: string(pem.EncodeToMemory(block)),
		PublicKey:  AuthorizedKeyLine(sshPub),
		Passphrase: passphrase,
	}, nil
}
