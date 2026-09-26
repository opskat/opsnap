package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// datasource_probes 为 datasources 表补充能力探测的结果列：probe_state 为空表示尚未探测过，done 表示
// 已给出结果（probe_items 是 []ProbeItem 的 JSON），unprobeable 表示连接失败或超时、本次“无法探测”
// （probe_error 记录原因，已去掉秘密）；probe_time 是本次结果产生的时间。正在探测由服务在内存中跟踪，
// 不落库，这样进程重启不会有数据源永远卡在“探测中”（docs/specs/2026-09-25-datasources.md「能力探测」）
func t20260925DataSourceProbes() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "20260925_datasource_probes",
		Migrate: func(tx *gorm.DB) error {
			for _, stmt := range []string{
				`ALTER TABLE datasources ADD COLUMN probe_state TEXT NOT NULL DEFAULT ''`,
				`ALTER TABLE datasources ADD COLUMN probe_items TEXT NOT NULL DEFAULT ''`,
				`ALTER TABLE datasources ADD COLUMN probe_error TEXT NOT NULL DEFAULT ''`,
				`ALTER TABLE datasources ADD COLUMN probe_time INTEGER NOT NULL DEFAULT 0`,
			} {
				if err := tx.Exec(stmt).Error; err != nil {
					return err
				}
			}
			return nil
		},
		Rollback: func(tx *gorm.DB) error {
			for _, stmt := range []string{
				`ALTER TABLE datasources DROP COLUMN probe_state`,
				`ALTER TABLE datasources DROP COLUMN probe_items`,
				`ALTER TABLE datasources DROP COLUMN probe_error`,
				`ALTER TABLE datasources DROP COLUMN probe_time`,
			} {
				if err := tx.Exec(stmt).Error; err != nil {
					return err
				}
			}
			return nil
		},
	}
}
