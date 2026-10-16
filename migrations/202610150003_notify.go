package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// T202610150003 通知设置：单行表，CHECK (id = 1) 保证只有一行。
// headers 是 JSON 对象，值为主密钥加密后的密文。
func T202610150003() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202610150003",
		Migrate: func(tx *gorm.DB) error {
			stmts := []string{
				`CREATE TABLE IF NOT EXISTS notify (
					id              INTEGER NOT NULL PRIMARY KEY CHECK (id = 1),
					url             TEXT    NOT NULL,
					headers         TEXT    NOT NULL,
					on_failed       INTEGER NOT NULL,
					on_recovered    INTEGER NOT NULL,
					on_stale        INTEGER NOT NULL,
					on_success      INTEGER NOT NULL,
					stale_hours     INTEGER NOT NULL,
					updatetime      INTEGER NOT NULL DEFAULT 0
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
			return tx.Exec(`DROP TABLE IF EXISTS notify`).Error
		},
	}
}
