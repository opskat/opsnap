// Package datasource_entity 定义数据源实体。
package datasource_entity

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
)

// 数据源类型，与 dsconn.Type 取值一致
const (
	KindMySQL      = "mysql"
	KindPostgreSQL = "postgres"
	KindServerFile = "server_file"
)

// 认证方式：MySQL / PostgreSQL 只有密码；服务器文件为密码或私钥
const (
	AuthPassword = "password"
	AuthKey      = "key"
)

// 数据源状态
const (
	StatusOK             = "ok"               // 最近一次测试成功
	StatusUnreachable    = "unreachable"      // 链路中某一跳或数据源本身无法连接、认证失败等
	StatusHostKeyChanged = "host_key_changed" // 链路中某个 SSH 通道或目标主机出示的密钥与保存的不一致
)

type DataSource struct {
	ID         int64  `gorm:"column:id;primaryKey"`
	Name       string `gorm:"column:name"`
	Kind       string `gorm:"column:kind"`
	Host       string `gorm:"column:host"`
	Port       int    `gorm:"column:port"`
	Username   string `gorm:"column:username"`
	AuthMethod string `gorm:"column:auth_method"`
	// Password、PrivateKey、Passphrase、TLSClientKey 为主密钥加密后的密文，未设置时为空
	Password   string `gorm:"column:password"`
	PrivateKey string `gorm:"column:private_key"`
	Passphrase string `gorm:"column:passphrase"`
	// Database PostgreSQL 的连接数据库
	Database string `gorm:"column:database_name"`
	// TLSMode MySQL / PostgreSQL 的 TLS 模式（dsconn.TLSMode）
	TLSMode string `gorm:"column:tls_mode"`
	// TLSCA、TLSClientCert 为 PEM 证书，不是秘密
	TLSCA         string `gorm:"column:tls_ca"`
	TLSClientCert string `gorm:"column:tls_client_cert"`
	TLSClientKey  string `gorm:"column:tls_client_key"`
	// HostKey 服务器文件目标主机确认过的 SSH 主机密钥 SHA256 指纹
	HostKey string `gorm:"column:host_key"`
	// PresentedHostKey 状态为“主机密钥已变化”且变化的是目标主机时，主机现在出示的指纹
	PresentedHostKey string `gorm:"column:presented_host_key"`
	// ChannelID 经由的网络通道，0 表示从 OpsNap 直连
	ChannelID int64 `gorm:"column:channel_id"`
	// Version 数据库的服务端版本；System 服务器文件的 `uname -sm` 输出（最近一次测试成功时读取）
	Version string `gorm:"column:version"`
	System  string `gorm:"column:system"`
	// TLSVersion 最近一次测试成功时连接已加密的 TLS 版本，未加密为空；TLSVerified 是否校验了服务端证书
	TLSVersion  string `gorm:"column:tls_version"`
	TLSVerified bool   `gorm:"column:tls_verified"`
	Status      string `gorm:"column:status"`
	// StatusCode 最近一次测试失败的原因码，正常为 0
	StatusCode int `gorm:"column:status_code"`
	// StatusDetail 失败详情（HopStatus 的 JSON）
	StatusDetail string `gorm:"column:status_detail"`
	Checktime    int64  `gorm:"column:checktime"`
	Createtime   int64  `gorm:"column:createtime"`
	Updatetime   int64  `gorm:"column:updatetime"`
}

func (DataSource) TableName() string { return "datasources" }

// Addr host:port
func (d *DataSource) Addr() string {
	return net.JoinHostPort(d.Host, strconv.Itoa(d.Port))
}

// Address 展示用地址：mysql://host:port、postgres://host:port 或 ssh://user@host:port
func (d *DataSource) Address() string {
	switch d.Kind {
	case KindMySQL:
		return "mysql://" + d.Addr()
	case KindPostgreSQL:
		return "postgres://" + d.Addr()
	}
	return fmt.Sprintf("ssh://%s@%s", d.Username, d.Addr())
}

// HopStatus 最近一次测试失败在第几跳（链路中的通道，或数据源本身）以及原因
type HopStatus struct {
	Hop int `json:"hop"`
	// ChannelID 失败的是链路中的通道时为通道 ID，失败的是数据源本身时为 0
	ChannelID int64  `json:"channel_id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	// Reason 原因（netchain.Reason 或 dsconn.Reason）
	Reason string `json:"reason"`
	// Detail 原文（已去掉秘密）
	Detail string `json:"detail,omitempty"`
}

// SetFailure 记录失败状态
func (d *DataSource) SetFailure(status string, reasonCode int, hs HopStatus) {
	b, _ := json.Marshal(hs)
	d.Status, d.StatusCode, d.StatusDetail = status, reasonCode, string(b)
}

// SetOK 标记为测试通过
func (d *DataSource) SetOK() {
	d.Status, d.StatusCode, d.StatusDetail, d.PresentedHostKey = StatusOK, 0, "", ""
}

// HopStatus 解析失败详情；没有或无法解析时返回零值
func (d *DataSource) HopStatus() HopStatus {
	var hs HopStatus
	_ = json.Unmarshal([]byte(d.StatusDetail), &hs)
	return hs
}
