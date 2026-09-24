// Package token_entity 定义 API 令牌实体。
package token_entity

import "time"

// 令牌状态
const (
	StatusActive  = "active"
	StatusExpired = "expired"
	StatusRevoked = "revoked"
)

type Token struct {
	ID           int64  `gorm:"column:id;primaryKey"`
	Name         string `gorm:"column:name"`
	Prefix       string `gorm:"column:prefix"`
	TokenHash    string `gorm:"column:token_hash"`
	Expiretime   int64  `gorm:"column:expiretime"`
	Revoketime   int64  `gorm:"column:revoketime"`
	Lastusedtime int64  `gorm:"column:lastusedtime"`
	Createtime   int64  `gorm:"column:createtime"`
}

func (Token) TableName() string { return "api_tokens" }

// Status 吊销优先于过期
func (t *Token) Status(now time.Time) string {
	switch {
	case t.Revoketime != 0:
		return StatusRevoked
	case t.Expiretime != 0 && now.Unix() >= t.Expiretime:
		return StatusExpired
	default:
		return StatusActive
	}
}
