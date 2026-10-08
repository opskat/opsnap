package migrations

import (
	"strings"

	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// storages.usage_* 仓库用量（docs/specs/2026-09-29-overview-docker.md「存储目标」）：每次运行、完整维护与测试连接之后
// 记录快照数、全部快照去重压缩后的占用与原始总大小；usage_error / usage_error_en 为最近一次读取失败的原因（中文、英文），
// 成功时为空；usage_checktime 为最近一次读取的时间（秒），0 表示从未读取。概览读取这些记录，不打开仓库
func t20260929StorageUsage() *gormigrate.Migration {
	columns := []string{
		"usage_snapshots INTEGER NOT NULL DEFAULT 0",
		"usage_packed_bytes INTEGER NOT NULL DEFAULT 0",
		"usage_original_bytes INTEGER NOT NULL DEFAULT 0",
		"usage_error TEXT NOT NULL DEFAULT ''",
		"usage_error_en TEXT NOT NULL DEFAULT ''",
		"usage_checktime INTEGER NOT NULL DEFAULT 0",
	}
	return &gormigrate.Migration{
		ID: "20260929_storage_usage",
		Migrate: func(tx *gorm.DB) error {
			for _, c := range columns {
				if err := tx.Exec(`ALTER TABLE storages ADD COLUMN ` + c).Error; err != nil {
					return err
				}
			}
			return nil
		},
		Rollback: func(tx *gorm.DB) error {
			for i := len(columns) - 1; i >= 0; i-- {
				name, _, _ := strings.Cut(columns[i], " ")
				if err := tx.Exec(`ALTER TABLE storages DROP COLUMN ` + name).Error; err != nil {
					return err
				}
			}
			return nil
		},
	}
}
