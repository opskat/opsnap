package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// T202610150002 认证与设置：单管理员、登录会话、API 密钥、全局设置。
// admin 与 settings 都是单行表，用 CHECK (id = 1) 保证只有一行。
// 会话令牌与 API 密钥只存哈希。
func T202610150002() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202610150002",
		Migrate: func(tx *gorm.DB) error {
			stmts := []string{
				`CREATE TABLE IF NOT EXISTS admin (
					id            INTEGER NOT NULL PRIMARY KEY CHECK (id = 1),
					username      TEXT    NOT NULL,
					password_hash TEXT    NOT NULL,
					failed_count  INTEGER NOT NULL DEFAULT 0,
					locked_until  INTEGER NOT NULL DEFAULT 0,
					createtime    INTEGER NOT NULL DEFAULT 0,
					updatetime    INTEGER NOT NULL DEFAULT 0
				)`,
				`CREATE TABLE IF NOT EXISTS sessions (
					id         INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
					token_hash TEXT    NOT NULL,
					expires_at INTEGER NOT NULL,
					createtime INTEGER NOT NULL DEFAULT 0
				)`,
				`CREATE UNIQUE INDEX IF NOT EXISTS uk_sessions_token_hash ON sessions (token_hash)`,
				`CREATE TABLE IF NOT EXISTS api_keys (
					id           INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
					name         TEXT    NOT NULL,
					prefix       TEXT    NOT NULL,
					key_hash     TEXT    NOT NULL,
					expires_at   INTEGER NOT NULL DEFAULT 0,
					last_used_at INTEGER NOT NULL DEFAULT 0,
					revoked_at   INTEGER NOT NULL DEFAULT 0,
					createtime   INTEGER NOT NULL DEFAULT 0
				)`,
				`CREATE UNIQUE INDEX IF NOT EXISTS uk_api_keys_key_hash ON api_keys (key_hash)`,
				`CREATE TABLE IF NOT EXISTS settings (
					id                        INTEGER NOT NULL PRIMARY KEY CHECK (id = 1),
					max_concurrent_runs       INTEGER NOT NULL,
					retention_days            INTEGER NOT NULL,
					timezone                  TEXT    NOT NULL,
					clear_mirror_after_backup INTEGER NOT NULL DEFAULT 0,
					updatetime                INTEGER NOT NULL DEFAULT 0
				)`,
			}
			for _, s := range stmts {
				if err := tx.Exec(s).Error; err != nil {
					return err
				}
			}
			return nil
		},
		Rollback: func(tx *gorm.DB) error {
			for _, t := range []string{"settings", "api_keys", "sessions", "admin"} {
				if err := tx.Exec(`DROP TABLE IF EXISTS ` + t).Error; err != nil {
					return err
				}
			}
			return nil
		},
	}
}
