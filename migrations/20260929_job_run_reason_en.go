package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// job_runs.reason_en 失败原因的英文（OpsNap 自己的文字为英文，导出工具与数据库的原文不翻译），
// 接口在英文界面显示它；为空时（更早的记录）显示 reason
func t20260929JobRunReasonEn() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "20260929_job_run_reason_en",
		Migrate: func(tx *gorm.DB) error {
			return tx.Exec(`ALTER TABLE job_runs ADD COLUMN reason_en TEXT NOT NULL DEFAULT ''`).Error
		},
		Rollback: func(tx *gorm.DB) error {
			return tx.Exec(`ALTER TABLE job_runs DROP COLUMN reason_en`).Error
		},
	}
}
