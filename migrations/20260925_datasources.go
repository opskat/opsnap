package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// datasources 每行是一个数据源（MySQL、PostgreSQL 或服务器文件）。password、private_key、passphrase、
// tls_client_key 为主密钥加密后的密文，未设置时为空；tls_ca 与 tls_client_cert 是证书（PEM），不是秘密；
// host_key 为服务器文件目标主机确认过的 SSH 主机密钥 SHA256 指纹，presented_host_key 为最近一次测试时
// 目标主机出示的、与保存的不一致的指纹；channel_id 为经由的网络通道（0 表示从 OpsNap 直连）；
// version、system、tls_version、tls_verified 为最近一次测试成功时读取的服务端信息；
// status_code 为最近一次测试失败的原因码（0 表示正常），status_detail 记录失败在第几跳等详情（JSON）
func t20260925DataSources() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "20260925_datasources",
		Migrate: func(tx *gorm.DB) error {
			return tx.Exec(`CREATE TABLE datasources (
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
	database_name      TEXT    NOT NULL,
	tls_mode           TEXT    NOT NULL,
	tls_ca             TEXT    NOT NULL,
	tls_client_cert    TEXT    NOT NULL,
	tls_client_key     TEXT    NOT NULL,
	host_key           TEXT    NOT NULL,
	presented_host_key TEXT    NOT NULL,
	channel_id         INTEGER NOT NULL,
	version            TEXT    NOT NULL,
	system             TEXT    NOT NULL,
	tls_version        TEXT    NOT NULL,
	tls_verified       INTEGER NOT NULL,
	status             TEXT    NOT NULL,
	status_code        INTEGER NOT NULL,
	status_detail      TEXT    NOT NULL,
	checktime          INTEGER NOT NULL,
	createtime         INTEGER NOT NULL,
	updatetime         INTEGER NOT NULL
)`).Error
		},
		Rollback: func(tx *gorm.DB) error {
			return tx.Exec(`DROP TABLE datasources`).Error
		},
	}
}
