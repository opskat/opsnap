package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// RunMigrations 按顺序执行全部迁移。新增迁移时在下面的列表末尾登记。
func RunMigrations(db *gorm.DB) error {
	return run(db,
		T202610150001,
		T202610150002,
		T202610150003,
		T202610150004,
		T202610150005,
		T202610150006,
		T202610150007,
		T202610150008,
		T202610150009,
		T202610150010,
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
