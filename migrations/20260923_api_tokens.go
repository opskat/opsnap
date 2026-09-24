package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// api_tokens 只保存令牌的 SHA-256 与用于展示的前缀；expiretime、revoketime、lastusedtime 为 0 表示“无”
func t20260923APITokens() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "20260923_api_tokens",
		Migrate: func(tx *gorm.DB) error {
			return tx.Exec(`CREATE TABLE api_tokens (
	id           INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
	name         TEXT    NOT NULL,
	prefix       TEXT    NOT NULL,
	token_hash   TEXT    NOT NULL UNIQUE,
	expiretime   INTEGER NOT NULL,
	revoketime   INTEGER NOT NULL,
	lastusedtime INTEGER NOT NULL,
	createtime   INTEGER NOT NULL
)`).Error
		},
		Rollback: func(tx *gorm.DB) error {
			return tx.Exec(`DROP TABLE api_tokens`).Error
		},
	}
}
