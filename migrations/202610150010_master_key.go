package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// T202610150010 主密钥校验值：单行表。check_value 是用主密钥加密的固定内容，
// 启动时据此判断 OPSNAP_SECRET_KEY 是否就是加密现有凭据的那把主密钥（轮换后必须同步修改环境变量）。
func T202610150010() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202610150010",
		Migrate: func(tx *gorm.DB) error {
			return tx.Exec(`CREATE TABLE IF NOT EXISTS master_key (
				id          INTEGER NOT NULL PRIMARY KEY CHECK (id = 1),
				check_value TEXT    NOT NULL,
				rotated_at  INTEGER NOT NULL DEFAULT 0,
				createtime  INTEGER NOT NULL DEFAULT 0
			)`).Error
		},
		Rollback: func(tx *gorm.DB) error {
			return tx.Exec(`DROP TABLE IF EXISTS master_key`).Error
		},
	}
}
