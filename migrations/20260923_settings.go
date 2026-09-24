package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// settings 键值表：保存系统级配置与主密钥校验值
func t20260923Settings() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "20260923_settings",
		Migrate: func(tx *gorm.DB) error {
			return tx.Exec(`CREATE TABLE settings (
	key        TEXT    NOT NULL PRIMARY KEY,
	value      TEXT    NOT NULL,
	updatetime INTEGER NOT NULL
)`).Error
		},
		Rollback: func(tx *gorm.DB) error {
			return tx.Exec(`DROP TABLE settings`).Error
		},
	}
}
