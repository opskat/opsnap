package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// admins 只允许一行（id = 1），靠主键约束保证并发首次设置只有一个成功；
// sessions 只保存会话标识的 SHA-256
func t20260923Auth() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "20260923_auth",
		Migrate: func(tx *gorm.DB) error {
			for _, stmt := range []string{
				`CREATE TABLE admins (
	id                 INTEGER NOT NULL PRIMARY KEY CHECK (id = 1),
	username           TEXT    NOT NULL,
	password_hash      TEXT    NOT NULL,
	password_updatetime INTEGER NOT NULL,
	createtime         INTEGER NOT NULL,
	updatetime         INTEGER NOT NULL
)`,
				`CREATE TABLE sessions (
	id         INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
	token_hash TEXT    NOT NULL UNIQUE,
	admin_id   INTEGER NOT NULL,
	user_agent TEXT    NOT NULL,
	ip         TEXT    NOT NULL,
	expiretime INTEGER NOT NULL,
	createtime INTEGER NOT NULL,
	updatetime INTEGER NOT NULL
)`,
				`CREATE INDEX idx_sessions_admin_id ON sessions (admin_id)`,
			} {
				if err := tx.Exec(stmt).Error; err != nil {
					return err
				}
			}
			return nil
		},
		Rollback: func(tx *gorm.DB) error {
			if err := tx.Exec(`DROP TABLE sessions`).Error; err != nil {
				return err
			}
			return tx.Exec(`DROP TABLE admins`).Error
		},
	}
}
