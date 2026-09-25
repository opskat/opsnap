// Package datasource 定义数据源管理接口的请求与响应。浏览器会话与 API 令牌都可以调用；
// 任何响应都不包含数据库密码、SSH 密码与私钥、私钥口令与 TLS 客户端私钥，只返回是否已保存。
//
// 规则与网络通道相同：先测试后保存；服务器文件的目标主机尚未确认密钥（或与保存的不一致）时，
// 接口不报错，而是在响应的 host_key 中返回待确认的指纹，此时什么也不保存；用户信任后带上 host_key 重试。
package datasource

import (
	"github.com/cago-frame/cago/server/mux"

	channelapi "github.com/opskat/opsnap/internal/api/channel"
)

// Form 数据源设置；测试连接、新建与编辑共用
type Form struct {
	Name string `json:"name"`
	// Kind mysql / postgres / server_file
	Kind string `json:"kind" binding:"required,oneof=mysql postgres server_file"`
	Host string `json:"host"`
	// Port 为 0 时使用默认端口（MySQL 3306，PostgreSQL 5432，服务器文件 22）
	Port     int    `json:"port"`
	Username string `json:"username"`
	// AuthMethod 服务器文件的认证方式 password / key；MySQL / PostgreSQL 忽略（只有密码）
	AuthMethod string `json:"auth_method"`
	// Password、PrivateKey、Passphrase、TLSClientKey 编辑时留空表示沿用已保存的值
	Password   string `json:"password"`
	PrivateKey string `json:"private_key"`
	Passphrase string `json:"passphrase"`
	// Database PostgreSQL 的连接数据库，为空时为 postgres
	Database string `json:"database"`
	// TLSMode MySQL / PostgreSQL 的 TLS 模式，为空时为 prefer（优先加密）
	TLSMode string `json:"tls_mode" binding:"omitempty,oneof=disable prefer require verify_ca verify_full"`
	// TLSCA CA 证书（PEM），可选；两种校验模式下为空时使用系统信任库
	TLSCA string `json:"tls_ca"`
	// TLSClientCert、TLSClientKey 客户端证书与私钥（PEM），可选但必须同时提供；
	// 编辑时私钥留空且仍填写了客户端证书，表示沿用已保存的私钥
	TLSClientCert string `json:"tls_client_cert"`
	TLSClientKey  string `json:"tls_client_key"`
	// ChannelID 经由的网络通道，0 表示从 OpsNap 直连
	ChannelID int64 `json:"channel_id"`
	// HostKey 服务器文件：用户已信任的目标主机密钥指纹（SHA256:...）；为空时编辑沿用已保存的（主机与端口未变时）
	HostKey string `json:"host_key"`
}

// TLS 已加密连接的 TLS 状态
type TLS struct {
	// Version 如 TLSv1.3
	Version string `json:"version"`
	// Verified 是否校验了服务端证书
	Verified bool `json:"verified"`
}

// ServerInfo 连接测试读取的服务端信息
type ServerInfo struct {
	// Version 数据库的服务端版本（MySQL / PostgreSQL）
	Version string `json:"version"`
	// System 服务器文件的 `uname -sm` 输出，如 "Linux x86_64"
	System string `json:"system"`
	// TLS 连接已加密时非空
	TLS *TLS `json:"tls"`
}

// FailedHop 最近一次测试失败的位置
type FailedHop struct {
	// Hop 第几跳（从 1 开始，数据源本身是最后一跳）
	Hop int `json:"hop"`
	// ChannelID 失败的是链路中的通道时为通道 ID（“重新确认”针对该通道），失败的是数据源本身时为 0
	ChannelID int64  `json:"channel_id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
}

// Item 一个数据源
type Item struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	// AuthMethod 服务器文件为 password / key；MySQL / PostgreSQL 为 password
	AuthMethod      string `json:"auth_method"`
	HasPassword     bool   `json:"has_password"`
	HasPrivateKey   bool   `json:"has_private_key"`
	HasPassphrase   bool   `json:"has_passphrase"`
	Database        string `json:"database"`
	TLSMode         string `json:"tls_mode"`
	TLSCA           string `json:"tls_ca"`
	TLSClientCert   string `json:"tls_client_cert"`
	HasTLSClientKey bool   `json:"has_tls_client_key"`
	ChannelID       int64  `json:"channel_id"`
	// Address mysql://host:port、postgres://host:port 或 ssh://user@host:port
	Address string `json:"address"`
	// Chain 从 OpsNap 出发的完整链路，最后一跳是数据源本身；直连时只有数据源本身
	Chain []*channelapi.Hop `json:"chain"`
	// Server 最近一次测试成功时读取的服务端信息
	Server *ServerInfo `json:"server"`
	// HostKey 服务器文件目标主机保存的密钥指纹
	HostKey string `json:"host_key"`
	// PresentedHostKey 状态为 host_key_changed 且变化的是目标主机时，主机现在出示的指纹
	PresentedHostKey string `json:"presented_host_key"`
	// Status ok / unreachable / host_key_changed
	Status string `json:"status"`
	// StatusMessage 失败时的原因（按请求语言），例如“第 2 跳 bastion-prod（SSH）：主机密钥已变化”
	StatusMessage string `json:"status_message"`
	// FailedHop 失败时的位置；状态为 host_key_changed 时指明是哪一台
	FailedHop *FailedHop `json:"failed_hop"`
	CheckedAt int64      `json:"checked_at"`
	CreatedAt int64      `json:"created_at"`
}

// ListRequest 列出全部数据源；不会触发测试连接
type ListRequest struct {
	mux.Meta `path:"/datasources" method:"GET"`
}

type ListResponse struct {
	Items []*Item `json:"items"`
}

// GetRequest 按 ID 获取一个数据源（详情页）；不会触发测试连接
type GetRequest struct {
	mux.Meta `path:"/datasources/:id" method:"GET"`
	ID       int64 `uri:"id" binding:"required"`
}

type GetResponse struct {
	Item *Item `json:"item"`
}

// ProbeRequest 测试连接：校验字段与唯一性，沿链路连接数据源、认证并读取服务端信息，不保存
type ProbeRequest struct {
	mux.Meta `path:"/datasources/probe" method:"POST"`
	// ID 编辑已有数据源时传入：唯一性检查排除它自己，秘密留空时沿用它保存的值
	ID         int64 `json:"id"`
	DataSource Form  `json:"data_source"`
}

type ProbeResponse struct {
	// HostKey 非空表示测试暂停在目标主机，等待确认主机密钥
	HostKey *channelapi.HostKeyPrompt `json:"host_key"`
	// Chain 完整链路（最后一跳是数据源本身）
	Chain []*channelapi.Hop `json:"chain"`
	// Server 测试成功时读取的服务端信息
	Server *ServerInfo `json:"server"`
}

// CreateRequest 新建数据源：先测试，成功才保存
type CreateRequest struct {
	mux.Meta   `path:"/datasources" method:"POST"`
	DataSource Form `json:"data_source"`
}

type CreateResponse struct {
	// Item 保存成功时返回
	Item *Item `json:"item"`
	// HostKey 非空表示需要确认目标主机密钥，此时没有保存
	HostKey *channelapi.HostKeyPrompt `json:"host_key"`
}

// UpdateRequest 编辑数据源：先测试，成功才保存；失败时保持编辑前的全部设置
type UpdateRequest struct {
	mux.Meta   `path:"/datasources/:id" method:"PUT"`
	ID         int64 `uri:"id" binding:"required"`
	DataSource Form  `json:"data_source"`
}

type UpdateResponse struct {
	Item    *Item                     `json:"item"`
	HostKey *channelapi.HostKeyPrompt `json:"host_key"`
}

// TestRequest 用已保存的设置测试连接，结果写入列表状态
type TestRequest struct {
	mux.Meta `path:"/datasources/:id/test" method:"POST"`
	ID       int64 `uri:"id" binding:"required"`
}

type TestResponse struct {
	Item *Item `json:"item"`
	// HostKey 目标主机的密钥已变化时返回，供“重新确认”弹窗并列显示；
	// 变化的是链路中的通道时为空，由 item.failed_hop.channel_id 指明该通道，在通道上重新确认
	HostKey *channelapi.HostKeyPrompt `json:"host_key"`
}

// ConfirmHostKeyRequest 重新确认：信任服务器文件目标主机现在出示的密钥，保存后重新测试
type ConfirmHostKeyRequest struct {
	mux.Meta `path:"/datasources/:id/host-key" method:"POST"`
	ID       int64 `uri:"id" binding:"required"`
	// Fingerprint 用户在弹窗中看到并信任的指纹
	Fingerprint string `json:"fingerprint" binding:"required"`
}

type ConfirmHostKeyResponse struct {
	// Item 保存成功时返回
	Item *Item `json:"item"`
	// HostKey 主机出示的密钥与 Fingerprint 不一致时返回，此时没有保存
	HostKey *channelapi.HostKeyPrompt `json:"host_key"`
}

// DeleteRequest 删除数据源：只删除 OpsNap 中的记录与凭据，不影响数据库或服务器
type DeleteRequest struct {
	mux.Meta `path:"/datasources/:id" method:"DELETE"`
	ID       int64 `uri:"id" binding:"required"`
}

type DeleteResponse struct{}
