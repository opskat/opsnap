package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// T202610150006 备份任务。
// db_password 是主密钥加密后的密文；databases、paths、excludes、extra_args 是 JSON 数组。
// 删除任务只删这一行，不碰已有快照，所以 repo_id、server_id 不设外键。
func T202610150006() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202610150006",
		Migrate: func(tx *gorm.DB) error {
			stmts := []string{
				`CREATE TABLE IF NOT EXISTS jobs (
					id               INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
					name             TEXT    NOT NULL,
					source_type      TEXT    NOT NULL,
					server_id        INTEGER NOT NULL DEFAULT 0,
					conn_mode        TEXT    NOT NULL DEFAULT '',
					db_host          TEXT    NOT NULL DEFAULT '',
					db_port          INTEGER NOT NULL DEFAULT 0,
					db_user          TEXT    NOT NULL DEFAULT '',
					db_password      TEXT    NOT NULL DEFAULT '',
					databases        TEXT    NOT NULL DEFAULT '[]',
					all_databases    INTEGER NOT NULL DEFAULT 0,
					auth_db          TEXT    NOT NULL DEFAULT '',
					paths            TEXT    NOT NULL DEFAULT '[]',
					excludes         TEXT    NOT NULL DEFAULT '[]',
					repo_id          INTEGER NOT NULL,
					schedule_kind    TEXT    NOT NULL,
					schedule_time    TEXT    NOT NULL DEFAULT '',
					schedule_weekday INTEGER NOT NULL DEFAULT 0,
					schedule_cron    TEXT    NOT NULL DEFAULT '',
					retention_preset TEXT    NOT NULL,
					retention_days   INTEGER NOT NULL DEFAULT 0,
					retention_weeks  INTEGER NOT NULL DEFAULT 0,
					retention_months INTEGER NOT NULL DEFAULT 0,
					timeout_sec      INTEGER NOT NULL DEFAULT 7200,
					bandwidth_kbps   INTEGER NOT NULL DEFAULT 0,
					compression      TEXT    NOT NULL DEFAULT 'auto',
					extra_args       TEXT    NOT NULL DEFAULT '[]',
					paused           INTEGER NOT NULL DEFAULT 0,
					createtime       INTEGER NOT NULL DEFAULT 0,
					updatetime       INTEGER NOT NULL DEFAULT 0
				)`,
				`CREATE INDEX IF NOT EXISTS idx_jobs_server_id ON jobs (server_id)`,
				`CREATE INDEX IF NOT EXISTS idx_jobs_repo_id ON jobs (repo_id)`,
			}
			for _, s := range stmts {
				if err := tx.Exec(s).Error; err != nil {
					return err
				}
			}
			return nil
		},
		Rollback: func(tx *gorm.DB) error {
			return tx.Exec(`DROP TABLE IF EXISTS jobs`).Error
		},
	}
}
