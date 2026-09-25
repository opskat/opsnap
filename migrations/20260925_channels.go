package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// channels 每行是一个网络通道（SSH 跳板或 SOCKS5 代理）。password、private_key、passphrase
// 为主密钥加密后的密文，未设置时为空；host_key 为确认过的 SSH 主机密钥 SHA256 指纹；
// presented_host_key 为最近一次测试时主机出示的、与保存的不一致的指纹；
// via_id 为经由的通道（0 表示从 OpsNap 直连）；status_code 为最近一次测试失败的原因码（0 表示正常），
// status_detail 记录失败在第几跳等详情（JSON）
func t20260925Channels() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "20260925_channels",
		Migrate: func(tx *gorm.DB) error {
			return tx.Exec(`CREATE TABLE channels (
	id                 INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
	name               TEXT    NOT NULL UNIQUE,
	kind               TEXT    NOT NULL,
	host               TEXT    NOT NULL,
	port               INTEGER NOT NULL,
	username           TEXT    NOT NULL,
	auth_method        TEXT    NOT NULL,
	password           TEXT    NOT NULL,
	private_key        TEXT    NOT NULL,
	passphrase         TEXT    NOT NULL,
	host_key           TEXT    NOT NULL,
	presented_host_key TEXT    NOT NULL,
	via_id             INTEGER NOT NULL,
	status             TEXT    NOT NULL,
	status_code        INTEGER NOT NULL,
	status_detail      TEXT    NOT NULL,
	checktime          INTEGER NOT NULL,
	createtime         INTEGER NOT NULL,
	updatetime         INTEGER NOT NULL
)`).Error
		},
		Rollback: func(tx *gorm.DB) error {
			return tx.Exec(`DROP TABLE channels`).Error
		},
	}
}
