package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// job_runs 每行是任务的一次运行（docs/specs/2026-09-27-backup-jobs.md「执行」「运行记录」）。
// status：queued / running / success / failed / canceled / skipped；trigger_kind：schedule / manual / catchup / retry
// （重试为第 retry_attempt 次，共 retry_total 次）；scheduled_at 为计划与补跑对应的计划时间（秒）；
// started_at、finished_at 以毫秒计；log 为执行日志（JSON 数组，最多 1000 行）。每个任务最多保留 1000 条。
// jobs.snapshot_count 为最近一次读取仓库时本任务的快照数，供任务列表显示
func t20260928JobRuns() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "20260928_job_runs",
		Migrate: func(tx *gorm.DB) error {
			for _, stmt := range []string{`CREATE TABLE job_runs (
	id             INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
	job_id         INTEGER NOT NULL,
	status         TEXT    NOT NULL,
	trigger_kind   TEXT    NOT NULL,
	retry_attempt  INTEGER NOT NULL,
	retry_total    INTEGER NOT NULL,
	scheduled_at   INTEGER NOT NULL,
	started_at     INTEGER NOT NULL,
	finished_at    INTEGER NOT NULL,
	exported_bytes INTEGER NOT NULL,
	uploaded_bytes INTEGER NOT NULL,
	snapshot_id    TEXT    NOT NULL,
	failed_step    TEXT    NOT NULL,
	reason         TEXT    NOT NULL,
	log            TEXT    NOT NULL,
	createtime     INTEGER NOT NULL,
	updatetime     INTEGER NOT NULL
)`,
				`CREATE INDEX idx_job_runs_job_id ON job_runs (job_id, id)`,
				`CREATE INDEX idx_job_runs_status ON job_runs (status)`,
				`ALTER TABLE jobs ADD COLUMN snapshot_count INTEGER NOT NULL DEFAULT 0`,
			} {
				if err := tx.Exec(stmt).Error; err != nil {
					return err
				}
			}
			return nil
		},
		Rollback: func(tx *gorm.DB) error {
			for _, stmt := range []string{`DROP TABLE job_runs`, `ALTER TABLE jobs DROP COLUMN snapshot_count`} {
				if err := tx.Exec(stmt).Error; err != nil {
					return err
				}
			}
			return nil
		},
	}
}
