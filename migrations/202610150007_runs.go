package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// T202610150007 运行记录。
// snapshot_ids、steps、log 是 JSON；日志写入前已遮盖凭据。删除任务不删除它的运行记录，所以 job_id 不设外键。
func T202610150007() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202610150007",
		Migrate: func(tx *gorm.DB) error {
			stmts := []string{
				`CREATE TABLE IF NOT EXISTS runs (
					id              INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
					job_id          INTEGER NOT NULL,
					job_name        TEXT    NOT NULL DEFAULT '',
					triggered_by    TEXT    NOT NULL,
					status          TEXT    NOT NULL,
					reason          TEXT    NOT NULL DEFAULT '',
					notify_failed   INTEGER NOT NULL DEFAULT 0,
					started_at      INTEGER NOT NULL,
					finished_at     INTEGER NOT NULL DEFAULT 0,
					duration_ms     INTEGER NOT NULL DEFAULT 0,
					bytes_processed INTEGER NOT NULL DEFAULT 0,
					bytes_added     INTEGER NOT NULL DEFAULT 0,
					snapshot_ids    TEXT    NOT NULL DEFAULT '[]',
					steps           TEXT    NOT NULL DEFAULT '[]',
					log             TEXT    NOT NULL DEFAULT '[]',
					createtime      INTEGER NOT NULL DEFAULT 0
				)`,
				`CREATE INDEX IF NOT EXISTS idx_runs_job_id ON runs (job_id, id)`,
				`CREATE INDEX IF NOT EXISTS idx_runs_status ON runs (status)`,
			}
			for _, s := range stmts {
				if err := tx.Exec(s).Error; err != nil {
					return err
				}
			}
			return nil
		},
		Rollback: func(tx *gorm.DB) error {
			return tx.Exec(`DROP TABLE IF EXISTS runs`).Error
		},
	}
}
