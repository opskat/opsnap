package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// RunMigrations 按顺序执行全部迁移。新增迁移时在下面的列表末尾登记。
// 202610180001 是基线迁移，已收敛 v0.1 的全部旧迁移，不要再拆散或重排。
func RunMigrations(db *gorm.DB) error {
	return run(db,
		T202610180001,
	)
}

func run(db *gorm.DB, fs ...func() *gormigrate.Migration) error {
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
