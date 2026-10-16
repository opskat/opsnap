package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// T202610150008 「太久没有成功」的告警状态：每个任务至多一行，重启后据此不重发告警。
// 任务删除时由调度器清理对应的行，所以 job_id 不设外键。
func T202610150008() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202610150008",
		Migrate: func(tx *gorm.DB) error {
			return tx.Exec(`CREATE TABLE IF NOT EXISTS stale_alerts (
				job_id     INTEGER NOT NULL PRIMARY KEY,
				since      INTEGER NOT NULL,
				createtime INTEGER NOT NULL DEFAULT 0
			)`).Error
		},
		Rollback: func(tx *gorm.DB) error {
			return tx.Exec(`DROP TABLE IF EXISTS stale_alerts`).Error
		},
	}
}
