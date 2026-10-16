package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// T202610150001 初始迁移：创建 meta 键值表，存放实例级的元信息。
// 不要在迁移里使用 AutoMigrate(&entity)，实体结构会随时间变化，必须用确定性的 DDL。
func T202610150001() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202610150001",
		Migrate: func(tx *gorm.DB) error {
			return tx.Exec(`CREATE TABLE IF NOT EXISTS meta (
				key        TEXT    NOT NULL PRIMARY KEY,
				value      TEXT    NOT NULL DEFAULT '',
				updatetime INTEGER NOT NULL DEFAULT 0
			)`).Error
		},
		Rollback: func(tx *gorm.DB) error {
			return tx.Exec(`DROP TABLE IF EXISTS meta`).Error
		},
	}
}
