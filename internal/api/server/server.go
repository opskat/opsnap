// Package server 服务器与代理链接口
package server

import "github.com/cago-frame/cago/server/mux"

// 直接填写私有凭据时的认证方式，取值与密钥库里凭据类型一致（见 internal/api/credential 的 KindPassword / KindSSHKey）。
// 引用共享密钥（CredentialID > 0）时由密钥的类型决定认证方式，这两个字段不再使用。
const (
	AuthPassword = "password" // 密码
	AuthSSHKey   = "ssh_key"  // 私钥（可带口令）
)

// 代理链中一跳的类型
const (
	HopSSH    = "ssh"    // SSH 跳板，从已添加的服务器中选择
	HopSOCKS5 = "socks5" // SOCKS5 代理
	HopHTTP   = "http"   // HTTP CONNECT 代理
)

// 服务器连接状态
const (
	StatusUnknown = "unknown" // 还没有连接过
	StatusOK      = "ok"      // 最近一次连接正常
	StatusFailed  = "failed"  // 从 fail_since 起一直连不上
)

// HopInput 提交的一跳。SSH 跳板只填 server_id；代理填地址，用户名和密码可选。
type HopInput struct {
	Type     string `json:"type"`
	ServerID int64  `json:"server_id"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	// Password 代理密码；占位「已设置」表示保留同一代理（类型、地址、端口、用户名都相同）已保存的密码
	Password string `json:"password"`
}

// ServerInput 提交的服务器配置，整体保存；Create、Update、Test 请求都带这组字段。
//
// 登录凭据有两种来源，由 credential_id 决定：
//   - credential_id > 0：引用一条共享密钥，下面四个 auth_type/password/private_key/passphrase 字段不使用；
//   - credential_id == 0：直接填写私有凭据，此时按 auth_type 提交密码或私钥，
//     占位「已设置」或留空表示保留这台服务器已有的私有凭据。
//
// share_as 只在直接填写时使用：非空表示把这份私有凭据原地存成这个名称的共享密钥；
// 对已经引用共享密钥的服务器传它就当作无操作。
type ServerInput struct {
	Name         string     `json:"name"`
	Host         string     `json:"host"`
	Port         int        `json:"port"`
	Username     string     `json:"username"`
	CredentialID int64      `json:"credential_id"` // > 0：使用这条共享密钥；0：直接填写
	AuthType     string     `json:"auth_type"`     // password | ssh_key，仅 credential_id == 0 时使用
	Password     string     `json:"password"`
	PrivateKey   string     `json:"private_key"`
	Passphrase   string     `json:"passphrase"`
	ShareAs      string     `json:"share_as"` // 直接填写时把它存成这个名称的共享密钥；空表示保持私有
	Hops         []HopInput `json:"hops"`     // 按「本机 → 第 1 跳 → … → 目标」的顺序
	// HostKeyFingerprint 管理员核对过的目标主机 SHA256 指纹（来自测试连接）。
	// 地址和端口不变时留空表示沿用已核对的指纹；没有已核对的指纹时不能保存。
	HostKeyFingerprint string `json:"host_key_fingerprint"`
}

// HopView 返回的一跳；密码只返回占位
type HopView struct {
	Type       string `json:"type"`
	ServerID   int64  `json:"server_id,omitempty"`
	ServerName string `json:"server_name,omitempty"` // SSH 跳板的服务器名称
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Username   string `json:"username"`
	Password   string `json:"password"` // 占位「已设置」或空
}

// CredentialView 服务器用的登录凭据。只返回「有没有内容」，永不返回密文或明文。
// 私有凭据（直接填写的那份）只有这台服务器看得到，name 是空串。
type CredentialView struct {
	ID   int64  `json:"id"`
	Kind string `json:"kind"` // password | ssh_key
	// Name 共享密钥的名称；私有凭据为空串
	Name string `json:"name"`
	// Shared 是共享密钥（能在密钥页看到、被多台服务器选用）
	Shared   bool   `json:"shared"`
	Username string `json:"username"` // 默认用户，仅共享密钥使用
	Note     string `json:"note"`     // 备注，仅共享密钥使用
	// HasSecret 有没有密码（password 类型）或私钥（ssh_key 类型）
	HasSecret bool `json:"has_secret"`
	// HasPassphrase 私钥有没有口令
	HasPassphrase bool `json:"has_passphrase"`
}

// Item 服务器；凭据只返回有没有内容与名称，不返回任何密文或明文
type Item struct {
	ID                 int64           `json:"id"`
	Name               string          `json:"name"`
	Host               string          `json:"host"`
	Port               int             `json:"port"`
	Username           string          `json:"username"`
	Credential         *CredentialView `json:"credential"`
	Hops               []*HopView      `json:"hops"` // 为空表示直连
	HostKeyFingerprint string          `json:"host_key_fingerprint"`
	Status             string          `json:"status"`        // unknown | ok | failed
	LatencyMs          int64           `json:"latency_ms"`    // 最近一次成功连接的总延迟
	FailSince          int64           `json:"fail_since"`    // 从何时起连不上，Unix 秒，0 表示正常
	LastError          string          `json:"last_error"`    // 最近一次失败的原因
	LastCheckAt        int64           `json:"last_check_at"` // 最近一次连接的时间，Unix 秒
	JobCount           int             `json:"job_count"`     // 使用它的任务数
	CreatedAt          int64           `json:"created_at"`
	UpdatedAt          int64           `json:"updated_at"`
}

// ListRequest 列出服务器
type ListRequest struct {
	mux.Meta `path:"/servers" method:"GET"`
}

type ListResponse struct {
	List []*Item `json:"list"`
}

// GetRequest 读取一台服务器
type GetRequest struct {
	mux.Meta `path:"/servers/:id" method:"GET"`
	ID       int64 `uri:"id" binding:"required"`
}

type GetResponse struct {
	Item
}

// CreateRequest 添加服务器；目标主机指纹必须已核对
type CreateRequest struct {
	mux.Meta           `path:"/servers" method:"POST"`
	Name               string     `json:"name"`
	Host               string     `json:"host"`
	Port               int        `json:"port"`
	Username           string     `json:"username"`
	CredentialID       int64      `json:"credential_id"`
	AuthType           string     `json:"auth_type"`
	Password           string     `json:"password"`
	PrivateKey         string     `json:"private_key"`
	Passphrase         string     `json:"passphrase"`
	ShareAs            string     `json:"share_as"`
	Hops               []HopInput `json:"hops"`
	HostKeyFingerprint string     `json:"host_key_fingerprint"`
}

// Input 取出提交的服务器配置
func (r *CreateRequest) Input() *ServerInput {
	return &ServerInput{
		Name: r.Name, Host: r.Host, Port: r.Port, Username: r.Username,
		CredentialID: r.CredentialID, AuthType: r.AuthType,
		Password: r.Password, PrivateKey: r.PrivateKey, Passphrase: r.Passphrase, ShareAs: r.ShareAs,
		Hops: r.Hops, HostKeyFingerprint: r.HostKeyFingerprint,
	}
}

type CreateResponse struct {
	Item
}

// UpdateRequest 修改服务器，整体提交
type UpdateRequest struct {
	mux.Meta           `path:"/servers/:id" method:"PUT"`
	ID                 int64      `uri:"id" binding:"required"`
	Name               string     `json:"name"`
	Host               string     `json:"host"`
	Port               int        `json:"port"`
	Username           string     `json:"username"`
	CredentialID       int64      `json:"credential_id"`
	AuthType           string     `json:"auth_type"`
	Password           string     `json:"password"`
	PrivateKey         string     `json:"private_key"`
	Passphrase         string     `json:"passphrase"`
	ShareAs            string     `json:"share_as"`
	Hops               []HopInput `json:"hops"`
	HostKeyFingerprint string     `json:"host_key_fingerprint"`
}

// Input 取出提交的服务器配置
func (r *UpdateRequest) Input() *ServerInput {
	return &ServerInput{
		Name: r.Name, Host: r.Host, Port: r.Port, Username: r.Username,
		CredentialID: r.CredentialID, AuthType: r.AuthType,
		Password: r.Password, PrivateKey: r.PrivateKey, Passphrase: r.Passphrase, ShareAs: r.ShareAs,
		Hops: r.Hops, HostKeyFingerprint: r.HostKeyFingerprint,
	}
}

type UpdateResponse struct {
	Item
}

// DeleteRequest 删除服务器；被任务使用或被其他服务器用作跳板时拒绝
type DeleteRequest struct {
	mux.Meta `path:"/servers/:id" method:"DELETE"`
	ID       int64 `uri:"id" binding:"required"`
}

type DeleteResponse struct{}

// TestRequest 测试一份未保存的配置。ID 非 0 时表示编辑中的已保存服务器，
// 凭据为占位「已设置」或留空时取它已保存的值。目标没有已核对的指纹时只取回指纹、不发送凭据。
type TestRequest struct {
	mux.Meta           `path:"/servers/test" method:"POST"`
	ID                 int64      `json:"id"`
	Name               string     `json:"name"`
	Host               string     `json:"host"`
	Port               int        `json:"port"`
	Username           string     `json:"username"`
	CredentialID       int64      `json:"credential_id"`
	AuthType           string     `json:"auth_type"`
	Password           string     `json:"password"`
	PrivateKey         string     `json:"private_key"`
	Passphrase         string     `json:"passphrase"`
	ShareAs            string     `json:"share_as"`
	Hops               []HopInput `json:"hops"`
	HostKeyFingerprint string     `json:"host_key_fingerprint"`
}

// Input 取出提交的服务器配置
func (r *TestRequest) Input() *ServerInput {
	return &ServerInput{
		Name: r.Name, Host: r.Host, Port: r.Port, Username: r.Username,
		CredentialID: r.CredentialID, AuthType: r.AuthType,
		Password: r.Password, PrivateKey: r.PrivateKey, Passphrase: r.Passphrase, ShareAs: r.ShareAs,
		Hops: r.Hops, HostKeyFingerprint: r.HostKeyFingerprint,
	}
}

// HopResult 一跳的测试结果
type HopResult struct {
	Index       int    `json:"index"` // 从 1 开始，目标是最后一跳
	Type        string `json:"type"`
	Name        string `json:"name"`
	Addr        string `json:"addr"`
	OK          bool   `json:"ok"`
	LatencyMs   int64  `json:"latency_ms"`
	Reason      string `json:"reason"`       // timeout | auth_failed | fingerprint_mismatch | fingerprint_unconfirmed | unreachable | canceled
	ReasonLabel string `json:"reason_label"` // 超时、认证失败、指纹不符 等
	Message     string `json:"message"`
	Fingerprint string `json:"fingerprint"` // SSH 主机实际出示的 SHA256 指纹
}

// TestResponse 逐跳结果，首个失败处停止。
// Fingerprint 是目标主机实际出示的指纹，供管理员核对后随保存提交。
type TestResponse struct {
	OK          bool         `json:"ok"`
	Hops        []*HopResult `json:"hops"`
	Fingerprint string       `json:"fingerprint"`
}

// TestSavedRequest 测试已保存的服务器，并更新列表里的状态
type TestSavedRequest struct {
	mux.Meta `path:"/servers/:id/test" method:"POST"`
	ID       int64 `uri:"id" binding:"required"`
}

// NewCreateRequest 用一组服务器配置构造添加请求
func NewCreateRequest(in *ServerInput) *CreateRequest {
	return &CreateRequest{
		Name: in.Name, Host: in.Host, Port: in.Port, Username: in.Username,
		CredentialID: in.CredentialID, AuthType: in.AuthType,
		Password: in.Password, PrivateKey: in.PrivateKey, Passphrase: in.Passphrase, ShareAs: in.ShareAs,
		Hops: in.Hops, HostKeyFingerprint: in.HostKeyFingerprint,
	}
}

// NewUpdateRequest 用一组服务器配置构造修改请求
func NewUpdateRequest(id int64, in *ServerInput) *UpdateRequest {
	c := NewCreateRequest(in)
	return &UpdateRequest{
		ID: id, Name: c.Name, Host: c.Host, Port: c.Port, Username: c.Username,
		CredentialID: c.CredentialID, AuthType: c.AuthType,
		Password: c.Password, PrivateKey: c.PrivateKey, Passphrase: c.Passphrase, ShareAs: c.ShareAs,
		Hops: c.Hops, HostKeyFingerprint: c.HostKeyFingerprint,
	}
}

// NewTestRequest 用一组服务器配置构造测试请求；id 为 0 表示新服务器
func NewTestRequest(id int64, in *ServerInput) *TestRequest {
	c := NewCreateRequest(in)
	return &TestRequest{
		ID: id, Name: c.Name, Host: c.Host, Port: c.Port, Username: c.Username,
		CredentialID: c.CredentialID, AuthType: c.AuthType,
		Password: c.Password, PrivateKey: c.PrivateKey, Passphrase: c.Passphrase, ShareAs: c.ShareAs,
		Hops: c.Hops, HostKeyFingerprint: c.HostKeyFingerprint,
	}
}
