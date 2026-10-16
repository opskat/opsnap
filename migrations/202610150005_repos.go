package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// T202610150005 存储位置（restic 仓库）。
// credential、private_key、opsnap_key 都是主密钥加密后的密文；恢复密钥不保存，只记它的 restic 密钥 ID。
// params 是不含凭据的地址参数（JSON）；status/stats 列缓存最近一次检查与统计，free_space 为 -1 表示未知。
func T202610150005() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202610150005",
		Migrate: func(tx *gorm.DB) error {
			return tx.Exec(`CREATE TABLE IF NOT EXISTS repos (
				id                      INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
				name                    TEXT    NOT NULL,
				type                    TEXT    NOT NULL,
				params                  TEXT    NOT NULL DEFAULT '{}',
				credential              TEXT    NOT NULL DEFAULT '',
				private_key             TEXT    NOT NULL DEFAULT '',
				opsnap_key              TEXT    NOT NULL,
				opsnap_key_id           TEXT    NOT NULL DEFAULT '',
				opsnap_key_created_at   INTEGER NOT NULL DEFAULT 0,
				recovery_key_id         TEXT    NOT NULL DEFAULT '',
				recovery_key_created_at INTEGER NOT NULL DEFAULT 0,
				status                  TEXT    NOT NULL DEFAULT 'unknown',
				status_message          TEXT    NOT NULL DEFAULT '',
				status_at               INTEGER NOT NULL DEFAULT 0,
				stored_size             INTEGER NOT NULL DEFAULT 0,
				raw_size                INTEGER NOT NULL DEFAULT 0,
				snapshot_count          INTEGER NOT NULL DEFAULT 0,
				free_space              INTEGER NOT NULL DEFAULT -1,
				key_count               INTEGER NOT NULL DEFAULT 0,
				stats_at                INTEGER NOT NULL DEFAULT 0,
				last_check_at           INTEGER NOT NULL DEFAULT 0,
				last_check_ok           INTEGER NOT NULL DEFAULT 0,
				last_check_message      TEXT    NOT NULL DEFAULT '',
				last_prune_at           INTEGER NOT NULL DEFAULT 0,
				last_prune_ok           INTEGER NOT NULL DEFAULT 0,
				last_prune_message      TEXT    NOT NULL DEFAULT '',
				createtime              INTEGER NOT NULL DEFAULT 0,
				updatetime              INTEGER NOT NULL DEFAULT 0
			)`).Error
		},
		Rollback: func(tx *gorm.DB) error {
			return tx.Exec(`DROP TABLE IF EXISTS repos`).Error
		},
	}
}
