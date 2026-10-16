// Package secret 用主密钥对存储的凭据做 AES-256-GCM 加解密
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
)

// EnvKey 主密钥所在的环境变量名
const EnvKey = "OPSNAP_SECRET_KEY"

// hkdfInfo 派生加密密钥时使用的上下文标签，改动会导致已有密文无法解密
const hkdfInfo = "opsnap credential encryption v1"

var (
	// ErrMissingKey 没有配置主密钥
	ErrMissingKey = errors.New("缺少主密钥：请设置环境变量 " + EnvKey + "，opsnap 不会使用默认密钥启动")
	// ErrDecrypt 密文格式错误、被篡改或主密钥不匹配
	ErrDecrypt = errors.New("凭据解密失败：密文损坏或主密钥不匹配")
)

// Secret 凭据加解密
type Secret interface {
	// Encrypt 加密明文，返回 base64 编码的 nonce+密文
	Encrypt(plaintext string) (string, error)
	// Decrypt 解密 Encrypt 的输出
	Decrypt(ciphertext string) (string, error)
}

type aesGCM struct {
	aead cipher.AEAD
}

// New 用主密钥创建加解密器。主密钥可以是任意非空字符串，
// 经 HKDF-SHA256 派生出 32 字节的 AES-256 密钥。
func New(masterKey string) (Secret, error) {
	if strings.TrimSpace(masterKey) == "" {
		return nil, ErrMissingKey
	}
	key, err := hkdf.Key(sha256.New, []byte(masterKey), nil, hkdfInfo, 32)
	if err != nil {
		return nil, fmt.Errorf("派生加密密钥失败: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("创建 AES 加密器失败: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("创建 GCM 加密器失败: %w", err)
	}
	return &aesGCM{aead: aead}, nil
}

func (a *aesGCM) Encrypt(plaintext string) (string, error) {
	nonce := make([]byte, a.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("生成 nonce 失败: %w", err)
	}
	sealed := a.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

func (a *aesGCM) Decrypt(ciphertext string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", ErrDecrypt
	}
	nonceSize := a.aead.NonceSize()
	if len(raw) < nonceSize+a.aead.Overhead() {
		return "", ErrDecrypt
	}
	plain, err := a.aead.Open(nil, raw[:nonceSize], raw[nonceSize:], nil)
	if err != nil {
		return "", ErrDecrypt
	}
	return string(plain), nil
}

// holder 让 atomic.Pointer 能保存接口值（含 nil）
type holder struct{ s Secret }

// defaultSecret 轮换主密钥时在运行中替换，读写都经原子操作
var defaultSecret atomic.Pointer[holder]

// SetDefault 设置默认加解密器，启动、轮换主密钥时和测试中调用
func SetDefault(s Secret) {
	defaultSecret.Store(&holder{s: s})
}

// Default 默认加解密器
func Default() Secret {
	if h := defaultSecret.Load(); h != nil {
		return h.s
	}
	return nil
}

// rotation 让轮换主密钥与凭据的保存互斥。保存凭据是「读出旧密文或用当前主密钥加密 → 落库」，
// 中间可能隔着很久（例如更换仓库密钥要先跑 restic）；如果轮换恰好在这期间提交，
// 落库的就是旧主密钥的密文，之后再也解不开。
var rotation sync.RWMutex

// BeginWrite 保存凭据前调用，落库后调用返回的函数。可以与其他保存并行，轮换会等它们结束。
// 不要嵌套调用：轮换排队时，嵌套的 BeginWrite 会和它互相等待。
func BeginWrite() (done func()) {
	rotation.RLock()
	return rotation.RUnlock
}

// BeginRotate 轮换主密钥时调用：等进行中的保存落库，并挡住新的保存，直到调用返回的函数
func BeginRotate() (done func()) {
	rotation.Lock()
	return rotation.Unlock
}

// Masked 接口读取时代替已保存凭据的占位；提交时原样回传它表示保留已保存的值。
// 服务器、存储位置、任务的数据库密码和通知请求头共用这一个值，面板的 SECRET_MASKED 与它一致。
const Masked = "已设置"

// Mask 已保存凭据（密文非空）读取时显示 Masked，没有时为空串
func Mask(cipher string) string {
	if cipher == "" {
		return ""
	}
	return Masked
}
