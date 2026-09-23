// Package migrations 管理元数据库的表结构变更。
package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// RunMigrations 按顺序执行迁移。只允许追加新的迁移函数，不修改已发布的迁移；
// 迁移中使用确定性的 DDL，不使用 AutoMigrate(&entity)，避免实体变化影响旧迁移
func RunMigrations(db *gorm.DB) error {
	return run(db)
}

func run(db *gorm.DB, fs ...func() *gormigrate.Migration) error {
	if len(fs) == 0 {
		return nil
	}
	ms := make([]*gormigrate.Migration, 0, len(fs))
	for _, f := range fs {
		ms = append(ms, f())
	}
	m := gormigrate.New(db, &gormigrate.Options{
		TableName:                 "migrations",
		IDColumnName:              "id",
		IDColumnSize:              200,
		UseTransaction:            true,
		ValidateUnknownMigrations: true,
	}, ms)
	return m.Migrate()
}
