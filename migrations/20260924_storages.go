package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// storages 每行是一个存储（一个 kopia 仓库）。secret_key 与 repo_key 为主密钥加密后的密文；
// location_key 是规范化后的位置标识，保证同一位置只被一个存储使用；
// status_code 为最近一次测试失败时的错误码（0 表示正常），status_detail 为失败详情
func t20260924Storages() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "20260924_storages",
		Migrate: func(tx *gorm.DB) error {
			return tx.Exec(`CREATE TABLE storages (
	id            INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
	name          TEXT    NOT NULL UNIQUE,
	kind          TEXT    NOT NULL,
	path          TEXT    NOT NULL,
	endpoint      TEXT    NOT NULL,
	region        TEXT    NOT NULL,
	bucket        TEXT    NOT NULL,
	prefix        TEXT    NOT NULL,
	access_key    TEXT    NOT NULL,
	secret_key    TEXT    NOT NULL,
	use_tls       INTEGER NOT NULL,
	skip_verify   INTEGER NOT NULL,
	location_key  TEXT    NOT NULL UNIQUE,
	repo_key      TEXT    NOT NULL,
	fingerprint   TEXT    NOT NULL,
	status        TEXT    NOT NULL,
	status_code   INTEGER NOT NULL,
	status_detail TEXT    NOT NULL,
	checktime     INTEGER NOT NULL,
	createtime    INTEGER NOT NULL,
	updatetime    INTEGER NOT NULL
)`).Error
		},
		Rollback: func(tx *gorm.DB) error {
			return tx.Exec(`DROP TABLE storages`).Error
		},
	}
}
