package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// jobs 每行是一个备份任务（docs/specs/2026-09-27-backup-jobs.md「任务与新建向导」）。
// datasource_id、storage_id、prefix 创建后不能修改；database_names、exclude_tables、schedule_weekdays 为 JSON 数组；
// opt_* 为一并备份的内容（MySQL：routines、triggers、events、users；PostgreSQL：globals）；
// retry_interval 与 timeout 以分钟计；enabled_at 为最近一次启用（或创建）的时间，暂停期间的计划不算错过；
// run_now 为创建时选择了“立即执行一次”，由运行模块处理
func t20260928Jobs() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "20260928_jobs",
		Migrate: func(tx *gorm.DB) error {
			for _, stmt := range []string{`CREATE TABLE jobs (
	id                INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
	name              TEXT    NOT NULL UNIQUE,
	type              TEXT    NOT NULL,
	datasource_id     INTEGER NOT NULL,
	storage_id        INTEGER NOT NULL,
	prefix            TEXT    NOT NULL,
	scope             TEXT    NOT NULL,
	database_names    TEXT    NOT NULL,
	method            TEXT    NOT NULL,
	opt_routines      INTEGER NOT NULL,
	opt_triggers      INTEGER NOT NULL,
	opt_events        INTEGER NOT NULL,
	opt_users         INTEGER NOT NULL,
	opt_globals       INTEGER NOT NULL,
	exclude_tables    TEXT    NOT NULL,
	compression       TEXT    NOT NULL,
	schedule_kind     TEXT    NOT NULL,
	schedule_minute   INTEGER NOT NULL,
	schedule_hour     INTEGER NOT NULL,
	schedule_weekdays TEXT    NOT NULL,
	schedule_cron     TEXT    NOT NULL,
	timezone          TEXT    NOT NULL,
	retain_days       INTEGER NOT NULL,
	retain_weeks      INTEGER NOT NULL,
	retain_months     INTEGER NOT NULL,
	retries           INTEGER NOT NULL,
	retry_interval    INTEGER NOT NULL,
	timeout           INTEGER NOT NULL,
	enabled           INTEGER NOT NULL,
	enabled_at        INTEGER NOT NULL,
	run_now           INTEGER NOT NULL,
	createtime        INTEGER NOT NULL,
	updatetime        INTEGER NOT NULL
)`,
				`CREATE INDEX idx_jobs_datasource_id ON jobs (datasource_id)`,
				`CREATE INDEX idx_jobs_storage_id ON jobs (storage_id)`,
			} {
				if err := tx.Exec(stmt).Error; err != nil {
					return err
				}
			}
			return nil
		},
		Rollback: func(tx *gorm.DB) error {
			return tx.Exec(`DROP TABLE jobs`).Error
		},
	}
}
