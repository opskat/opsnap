// Package apikey_entity API 密钥
package apikey_entity

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// KeyPrefix 完整密钥的固定前缀
const KeyPrefix = "osk_"

// APIKey API 密钥；只保存哈希和用于识别的前缀，不保存完整密钥。ExpiresAt 为 0 表示永不过期。
type APIKey struct {
	ID         int64  `gorm:"column:id;primaryKey;autoIncrement"`
	Name       string `gorm:"column:name;not null"`
	Prefix     string `gorm:"column:prefix;not null"`
	KeyHash    string `gorm:"column:key_hash;not null"`
	ExpiresAt  int64  `gorm:"column:expires_at;not null"`
	LastUsedAt int64  `gorm:"column:last_used_at;not null"`
	RevokedAt  int64  `gorm:"column:revoked_at;not null"`
	Createtime int64  `gorm:"column:createtime"`
}

func (APIKey) TableName() string { return "api_keys" }

// IsUsable 在 now 时刻是否可用：未撤销且未过期
func (k *APIKey) IsUsable(now time.Time) bool {
	if k.RevokedAt != 0 {
		return false
	}
	return k.ExpiresAt == 0 || now.Unix() < k.ExpiresAt
}

// prefixLen 列表里展示的密钥前缀长度（含 osk_）
const prefixLen = 12

// Generate 生成一把新密钥：返回完整密钥、展示用前缀和存库用的哈希。
// 密钥由 32 字节随机数的十六进制加 osk_ 前缀组成。
func Generate() (full, prefix, hash string, err error) {
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		return "", "", "", err
	}
	full = KeyPrefix + hex.EncodeToString(buf)
	return full, full[:prefixLen], HashKey(full), nil
}

// HashKey 计算完整密钥的 SHA-256 十六进制哈希。密钥本身是高熵随机串，不需要慢哈希。
func HashKey(full string) string {
	sum := sha256.Sum256([]byte(full))
	return hex.EncodeToString(sum[:])
}
