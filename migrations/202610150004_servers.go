package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// T202610150004 服务器与代理链。
// password、private_key、passphrase 与 hops 里各代理的 password 都是主密钥加密后的密文；
// hops 是按连接顺序排列的 JSON 数组。last_check_at 等列记录最近一次连接的状态。
func T202610150004() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202610150004",
		Migrate: func(tx *gorm.DB) error {
			return tx.Exec(`CREATE TABLE IF NOT EXISTS servers (
				id                   INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
				name                 TEXT    NOT NULL,
				host                 TEXT    NOT NULL,
				port                 INTEGER NOT NULL,
				username             TEXT    NOT NULL,
				auth_type            TEXT    NOT NULL,
				password             TEXT    NOT NULL DEFAULT '',
				private_key          TEXT    NOT NULL DEFAULT '',
				passphrase           TEXT    NOT NULL DEFAULT '',
				hops                 TEXT    NOT NULL DEFAULT '[]',
				host_key_fingerprint TEXT    NOT NULL,
				last_check_at        INTEGER NOT NULL DEFAULT 0,
				last_latency_ms      INTEGER NOT NULL DEFAULT 0,
				fail_since           INTEGER NOT NULL DEFAULT 0,
				last_error           TEXT    NOT NULL DEFAULT '',
				createtime           INTEGER NOT NULL DEFAULT 0,
				updatetime           INTEGER NOT NULL DEFAULT 0
			)`).Error
		},
		Rollback: func(tx *gorm.DB) error {
			return tx.Exec(`DROP TABLE IF EXISTS servers`).Error
		},
	}
}
