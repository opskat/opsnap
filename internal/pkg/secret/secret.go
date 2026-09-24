// Package secret 提供主密钥的生成、读取，以及基于主密钥的对称加密（AES-256-GCM）。
// 所有需要还原的敏感配置（OIDC Client Secret、后续的存储凭据等）都经由 Box 加密后落库。
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
)

// KeyFileName 数据目录中主密钥文件的文件名
const KeyFileName = "master.key"

// EnvKey 通过环境变量提供主密钥时使用的变量名
const EnvKey = "OPSNAP_MASTER_KEY"

const (
	keySize       = 32
	cipherVersion = "v1:"
)

var (
	ErrInvalidKey = errors.New("主密钥格式不正确：需要 base64 编码的 32 字节随机数")
	ErrDecrypt    = errors.New("解密失败：密文损坏或主密钥不匹配")
)

// GenerateKey 生成新的随机主密钥
func GenerateKey() ([]byte, error) {
	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("生成主密钥: %w", err)
	}
	return key, nil
}

// EncodeKey 把主密钥编码为文本（密钥文件与环境变量使用同一格式）
func EncodeKey(key []byte) string {
	return base64.StdEncoding.EncodeToString(key)
}

// ParseKey 解析文本形式的主密钥
func ParseKey(s string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(key) != keySize {
		return nil, ErrInvalidKey
	}
	return key, nil
}

// ReadKeyFile 读取密钥文件；文件不存在时返回的错误满足 errors.Is(err, os.ErrNotExist)
func ReadKeyFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path) //nolint:gosec // 路径来自程序配置的数据目录，不是用户输入
	if err != nil {
		return nil, err
	}
	key, err := ParseKey(string(data))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return key, nil
}

// WriteKeyFile 以 0600 权限新建密钥文件；文件已存在时报错，绝不覆盖
func WriteKeyFile(path string, key []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // 同上
	if err != nil {
		return err
	}
	if _, err := f.WriteString(EncodeKey(key) + "\n"); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// Box 使用主密钥加解密
type Box struct {
	aead cipher.AEAD
}

func NewBox(key []byte) (*Box, error) {
	if len(key) != keySize {
		return nil, ErrInvalidKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("初始化加密: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("初始化加密: %w", err)
	}
	return &Box{aead: aead}, nil
}

// Encrypt 返回带版本前缀的文本密文：v1:base64(nonce|ciphertext)
func (b *Box) Encrypt(plaintext []byte) (string, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("生成随机数: %w", err)
	}
	sealed := b.aead.Seal(nonce, nonce, plaintext, nil)
	return cipherVersion + base64.StdEncoding.EncodeToString(sealed), nil
}

func (b *Box) Decrypt(ciphertext string) ([]byte, error) {
	raw, ok := strings.CutPrefix(ciphertext, cipherVersion)
	if !ok {
		return nil, ErrDecrypt
	}
	data, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(data) < b.aead.NonceSize() {
		return nil, ErrDecrypt
	}
	nonce, sealed := data[:b.aead.NonceSize()], data[b.aead.NonceSize():]
	pt, err := b.aead.Open(nil, nonce, sealed, nil)
	if err != nil {
		return nil, ErrDecrypt
	}
	return pt, nil
}
