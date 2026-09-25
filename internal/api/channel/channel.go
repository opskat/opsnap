// Package channel 定义网络通道管理接口的请求与响应。浏览器会话与 API 令牌都可以调用；
// 任何响应都不包含 SSH 密码、私钥、私钥口令与 SOCKS5 密码，只返回是否已保存。
//
// 主机密钥：测试进行到尚未确认密钥（或密钥与保存的不一致）的这台 SSH 主机时，接口不报错，
// 而是在响应的 host_key 中返回待确认的指纹，此时什么也不保存；用户信任后带上 host_key 重试。
package channel

import "github.com/cago-frame/cago/server/mux"

// Form 通道设置；测试连接、新建与编辑共用
type Form struct {
	Name string `json:"name"`
	// Kind ssh / socks5
	Kind string `json:"kind" binding:"required,oneof=ssh socks5"`
	Host string `json:"host"`
	// Port 为 0 时使用默认端口（SSH 22，SOCKS5 1080）
	Port int `json:"port"`
	// Username SSH 用户名（必填），或 SOCKS5 用户名（可选，与密码要么都填要么都不填）
	Username string `json:"username"`
	// AuthMethod SSH 的认证方式 password / key；SOCKS5 忽略（按是否填写用户名决定）
	AuthMethod string `json:"auth_method"`
	// Password、PrivateKey、Passphrase 编辑时留空表示沿用已保存的值
	Password   string `json:"password"`
	PrivateKey string `json:"private_key"`
	Passphrase string `json:"passphrase"`
	// ViaID 经由的通道，0 表示从 OpsNap 直连
	ViaID int64 `json:"via_id"`
	// HostKey 用户已信任的 SSH 主机密钥指纹（SHA256:...）；为空时编辑沿用已保存的（主机与端口未变时）
	HostKey string `json:"host_key"`
}

// Ref 引用通道的对象
type Ref struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// UsedBy 直接经由该通道的数据源与通道
type UsedBy struct {
	DataSources []*Ref `json:"data_sources"`
	Channels    []*Ref `json:"channels"`
}

// Hop 链路中的一跳，从 OpsNap 出发按顺序排列
type Hop struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Address string `json:"address"`
}

// HostKeyPrompt 待用户确认的 SSH 主机密钥
type HostKeyPrompt struct {
	// Hop 这台主机是链路的第几跳（从 1 开始）
	Hop int `json:"hop"`
	// Name 通道名称
	Name string `json:"name"`
	// Address host:port
	Address string `json:"address"`
	KeyType string `json:"key_type"`
	// Fingerprint 主机现在出示的 SHA256 指纹
	Fingerprint string `json:"fingerprint"`
	// Changed 为 true 表示与保存的指纹不一致（Saved 为保存的指纹），否则为首次连接
	Changed bool   `json:"changed"`
	Saved   string `json:"saved"`
}

// Item 列表中的一个通道
type Item struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	// AuthMethod SSH 为 password / key；SOCKS5 为 none / password
	AuthMethod    string `json:"auth_method"`
	HasPassword   bool   `json:"has_password"`
	HasPrivateKey bool   `json:"has_private_key"`
	HasPassphrase bool   `json:"has_passphrase"`
	ViaID         int64  `json:"via_id"`
	// Address ssh://user@host:port 或 socks5://host:port
	Address string `json:"address"`
	// Chain 从 OpsNap 出发到本通道（含）的完整链路
	Chain []*Hop `json:"chain"`
	// HostKey 保存的 SSH 主机密钥指纹
	HostKey string `json:"host_key"`
	// PresentedHostKey 状态为 host_key_changed 且变化的是本通道时，主机现在出示的指纹
	PresentedHostKey string  `json:"presented_host_key"`
	UsedBy           *UsedBy `json:"used_by"`
	// Status ok / unreachable / host_key_changed
	Status string `json:"status"`
	// StatusMessage 失败时的原因（按请求语言），例如“第 1 跳 office-socks（SOCKS5）：认证失败”
	StatusMessage string `json:"status_message"`
	CheckedAt     int64  `json:"checked_at"`
	CreatedAt     int64  `json:"created_at"`
}

// ListRequest 列出全部通道；不会触发测试连接
type ListRequest struct {
	mux.Meta `path:"/channels" method:"GET"`
}

type ListResponse struct {
	Items []*Item `json:"items"`
}

// ProbeRequest 测试连接：校验字段、唯一性、环路与链路长度，沿链路依次建立连接，不保存
type ProbeRequest struct {
	mux.Meta `path:"/channels/probe" method:"POST"`
	// ID 编辑已有通道时传入：唯一性检查排除它自己，秘密留空时沿用它保存的值
	ID      int64 `json:"id"`
	Channel Form  `json:"channel"`
}

type ProbeResponse struct {
	// HostKey 非空表示测试暂停在这台主机，等待确认主机密钥
	HostKey *HostKeyPrompt `json:"host_key"`
	// Chain 完整链路（含本通道）
	Chain []*Hop `json:"chain"`
}

// CreateRequest 新建通道：先测试，成功才保存
type CreateRequest struct {
	mux.Meta `path:"/channels" method:"POST"`
	Channel  Form `json:"channel"`
}

type CreateResponse struct {
	// Item 保存成功时返回
	Item *Item `json:"item"`
	// HostKey 非空表示需要确认主机密钥，此时没有保存
	HostKey *HostKeyPrompt `json:"host_key"`
}

// UpdateRequest 编辑通道：先测试，成功才保存；失败时保持编辑前的全部设置
type UpdateRequest struct {
	mux.Meta `path:"/channels/:id" method:"PUT"`
	ID       int64 `uri:"id" binding:"required"`
	Channel  Form  `json:"channel"`
}

type UpdateResponse struct {
	Item    *Item          `json:"item"`
	HostKey *HostKeyPrompt `json:"host_key"`
}

// TestRequest 用已保存的设置测试连接，结果写入列表状态
type TestRequest struct {
	mux.Meta `path:"/channels/:id/test" method:"POST"`
	ID       int64 `uri:"id" binding:"required"`
}

type TestResponse struct {
	Item *Item `json:"item"`
	// HostKey 本通道的主机密钥已变化时返回，供“重新确认”弹窗并列显示
	HostKey *HostKeyPrompt `json:"host_key"`
}

// ConfirmHostKeyRequest 重新确认：信任 SSH 通道现在出示的主机密钥，保存后重新测试
type ConfirmHostKeyRequest struct {
	mux.Meta `path:"/channels/:id/host-key" method:"POST"`
	ID       int64 `uri:"id" binding:"required"`
	// Fingerprint 用户在弹窗中看到并信任的指纹（SHA256:...）；空白或其他格式直接拒绝，不会清掉已信任的密钥
	Fingerprint string `json:"fingerprint" binding:"required,startswith=SHA256:"`
}

type ConfirmHostKeyResponse struct {
	// Item 保存成功时返回
	Item *Item `json:"item"`
	// HostKey 主机出示的密钥与 Fingerprint 不一致时返回，此时没有保存
	HostKey *HostKeyPrompt `json:"host_key"`
}

// DeleteRequest 删除通道：只删除 OpsNap 中的记录与凭据；仍被数据源或通道引用时拒绝
type DeleteRequest struct {
	mux.Meta `path:"/channels/:id" method:"DELETE"`
	ID       int64 `uri:"id" binding:"required"`
}

type DeleteResponse struct{}
