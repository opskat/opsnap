package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// T202610220001 给备份任务加「待清理」标记：清理旧快照撞上同一存储位置上的并发备份时，
// restic forget 拿不到独占锁，本次运行不再算失败，而是把这件事延后到这里记下来，
// 由调度器在存储位置空闲时补做。forget_pending_at 是首次延后的时间（Unix 秒，0 表示没有），
// forget_pending_error 是最近一次补做失败的原因。
func T202610220001() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202610220001",
		Migrate: func(tx *gorm.DB) error {
			for _, stmt := range []string{
				`ALTER TABLE jobs ADD COLUMN forget_pending_at INTEGER NOT NULL DEFAULT 0`,
				`ALTER TABLE jobs ADD COLUMN forget_pending_error TEXT NOT NULL DEFAULT ''`,
			} {
				if err := tx.Exec(stmt).Error; err != nil {
					return err
				}
			}
			return nil
		},
		Rollback: func(tx *gorm.DB) error {
			for _, col := range []string{"forget_pending_at", "forget_pending_error"} {
				if err := tx.Exec(`ALTER TABLE jobs DROP COLUMN ` + col).Error; err != nil {
					return err
				}
			}
			return nil
		},
	}
}
