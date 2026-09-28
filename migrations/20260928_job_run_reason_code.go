package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// job_runs.reason_code 运行记录的固定原因（跳过、重试作废、OpsNap 重启、超时），接口按界面语言显示；
// 其余原因为空，reason 原样显示
func t20260928JobRunReasonCode() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "20260928_job_run_reason_code",
		Migrate: func(tx *gorm.DB) error {
			return tx.Exec(`ALTER TABLE job_runs ADD COLUMN reason_code TEXT NOT NULL DEFAULT ''`).Error
		},
		Rollback: func(tx *gorm.DB) error {
			return tx.Exec(`ALTER TABLE job_runs DROP COLUMN reason_code`).Error
		},
	}
}
