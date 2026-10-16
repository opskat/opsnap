package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// T202610150009 通知设置增加面板地址：Webhook 消息里的链接用它拼成能直接打开的完整地址。
func T202610150009() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202610150009",
		Migrate: func(tx *gorm.DB) error {
			return tx.Exec(`ALTER TABLE notify ADD COLUMN panel_url TEXT NOT NULL DEFAULT ''`).Error
		},
		Rollback: func(tx *gorm.DB) error {
			return tx.Exec(`ALTER TABLE notify DROP COLUMN panel_url`).Error
		},
	}
}
