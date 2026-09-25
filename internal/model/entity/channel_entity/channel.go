// Package channel_entity 定义网络通道实体。
package channel_entity

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
)

// 通道类型，与 netchain.Kind 取值一致
const (
	KindSSH    = "ssh"
	KindSOCKS5 = "socks5"
)

// 认证方式：SSH 为密码或私钥；SOCKS5 为不认证或用户名密码
const (
	AuthPassword = "password"
	AuthKey      = "key"
	AuthNone     = "none"
)

// 通道状态
const (
	StatusOK             = "ok"               // 最近一次测试成功
	StatusUnreachable    = "unreachable"      // 链路中某一跳无法连接或认证失败
	StatusHostKeyChanged = "host_key_changed" // 链路中某台 SSH 主机出示的密钥与保存的不一致
)

type Channel struct {
	ID         int64  `gorm:"column:id;primaryKey"`
	Name       string `gorm:"column:name"`
	Kind       string `gorm:"column:kind"`
	Host       string `gorm:"column:host"`
	Port       int    `gorm:"column:port"`
	Username   string `gorm:"column:username"`
	AuthMethod string `gorm:"column:auth_method"`
	// Password、PrivateKey、Passphrase 为主密钥加密后的密文，未设置时为空
	Password   string `gorm:"column:password"`
	PrivateKey string `gorm:"column:private_key"`
	Passphrase string `gorm:"column:passphrase"`
	// HostKey 确认过的 SSH 主机密钥 SHA256 指纹
	HostKey string `gorm:"column:host_key"`
	// PresentedHostKey 状态为“主机密钥已变化”且变化的是本通道时，主机现在出示的指纹
	PresentedHostKey string `gorm:"column:presented_host_key"`
	// ViaID 经由的通道，0 表示从 OpsNap 直连
	ViaID  int64  `gorm:"column:via_id"`
	Status string `gorm:"column:status"`
	// StatusCode 最近一次测试失败的原因码，正常为 0
	StatusCode int `gorm:"column:status_code"`
	// StatusDetail 失败详情（HopStatus 的 JSON）
	StatusDetail string `gorm:"column:status_detail"`
	Checktime    int64  `gorm:"column:checktime"`
	Createtime   int64  `gorm:"column:createtime"`
	Updatetime   int64  `gorm:"column:updatetime"`
}

func (Channel) TableName() string { return "channels" }

// Addr host:port
func (c *Channel) Addr() string {
	return net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
}

// Address 展示用地址：ssh://user@host:port 或 socks5://host:port
func (c *Channel) Address() string {
	if c.Kind == KindSSH {
		return fmt.Sprintf("ssh://%s@%s", c.Username, c.Addr())
	}
	return "socks5://" + c.Addr()
}

// HopStatus 最近一次测试失败在第几跳、哪个通道，以及原因详情
type HopStatus struct {
	Hop    int    `json:"hop"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Detail string `json:"detail,omitempty"`
}

// SetFailure 记录失败状态
func (c *Channel) SetFailure(status string, reasonCode int, hs HopStatus) {
	b, _ := json.Marshal(hs)
	c.Status, c.StatusCode, c.StatusDetail = status, reasonCode, string(b)
}

// SetOK 标记为测试通过
func (c *Channel) SetOK() {
	c.Status, c.StatusCode, c.StatusDetail, c.PresentedHostKey = StatusOK, 0, "", ""
}

// HopStatus 解析失败详情；没有或无法解析时返回零值
func (c *Channel) HopStatus() HopStatus {
	var hs HopStatus
	_ = json.Unmarshal([]byte(c.StatusDetail), &hs)
	return hs
}
