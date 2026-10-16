package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// T202610160001 单点登录（OIDC）：单行表，保存提供方配置、绑定的提供方账号和「允许密码登录」开关。
// client_secret 用主密钥加密；没有这一行时等同未配置、允许密码登录。
func T202610160001() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202610160001",
		Migrate: func(tx *gorm.DB) error {
			return tx.Exec(`CREATE TABLE IF NOT EXISTS sso (
				id             INTEGER NOT NULL PRIMARY KEY CHECK (id = 1),
				issuer         TEXT    NOT NULL DEFAULT '',
				client_id      TEXT    NOT NULL DEFAULT '',
				client_secret  TEXT    NOT NULL DEFAULT '',
				button_label   TEXT    NOT NULL DEFAULT '',
				panel_url      TEXT    NOT NULL DEFAULT '',
				bound_issuer   TEXT    NOT NULL DEFAULT '',
				bound_sub      TEXT    NOT NULL DEFAULT '',
				bound_name     TEXT    NOT NULL DEFAULT '',
				bound_at       INTEGER NOT NULL DEFAULT 0,
				password_login INTEGER NOT NULL DEFAULT 1,
				updatetime     INTEGER NOT NULL DEFAULT 0
			)`).Error
		},
		Rollback: func(tx *gorm.DB) error {
			return tx.Exec(`DROP TABLE IF EXISTS sso`).Error
		},
	}
}
