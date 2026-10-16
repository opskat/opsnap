// Package session_entity 管理员登录会话
package session_entity

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// Session 登录会话；只保存令牌的哈希
type Session struct {
	ID         int64  `gorm:"column:id;primaryKey;autoIncrement"`
	TokenHash  string `gorm:"column:token_hash;not null"`
	ExpiresAt  int64  `gorm:"column:expires_at;not null"`
	Createtime int64  `gorm:"column:createtime"`
}

func (Session) TableName() string { return "sessions" }

// Expired 在 now 时刻是否已过期
func (s *Session) Expired(now time.Time) bool {
	return s.ExpiresAt <= now.Unix()
}

// NewToken 生成会话令牌，返回令牌本身和存库用的哈希
func NewToken() (token, hash string, err error) {
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		return "", "", err
	}
	token = hex.EncodeToString(buf)
	return token, HashToken(token), nil
}

// HashToken 计算令牌的 SHA-256 十六进制哈希
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
