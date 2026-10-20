// Package credential 密钥库（共享的服务器登录凭据）接口。
// 接口只操作共享密钥；密文和明文都不出现在响应里，只告诉调用方「有没有内容」。
package credential

import "github.com/cago-frame/cago/server/mux"

// 密钥类型，与服务器认证方式一一对应
const (
	KindPassword = "password" // 密码
	KindSSHKey   = "ssh_key"  // 私钥（可带口令）
)

// Input 提交的一条密钥。密码、私钥、口令都不回显：编辑时留空表示保留已保存的值，
// 占位「已设置」同样表示保留。
type Input struct {
	// Name 名称，共享范围内唯一
	Name string `json:"name"`
	// Kind password | ssh_key
	Kind string `json:"kind"`
	// Password 类型为 password 时的密码
	Password string `json:"password"`
	// PrivateKey 类型为 ssh_key 时的私钥（OpenSSH 或 PEM 格式）
	PrivateKey string `json:"private_key"`
	// Passphrase 私钥口令，可选；没有就是空串
	Passphrase string `json:"passphrase"`
	// Username 默认用户：选了这条密钥时，服务器上为空的「用户」自动填它
	Username string `json:"username"`
	// Note 备注，只在密钥页显示
	Note string `json:"note"`
}

// ServerRef 使用某条密钥的服务器。地址与用户是明文，供界面写「用户@地址」
type ServerRef struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
}

// Item 一条共享密钥。只返回有没有内容与有没有口令，永不返回密文或明文
type Item struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	// Kind password | ssh_key
	Kind     string `json:"kind"`
	Username string `json:"username"`
	Note     string `json:"note"`
	// HasSecret 有没有密码（password 类型）或私钥（ssh_key 类型）
	HasSecret bool `json:"has_secret"`
	// HasPassphrase 私钥有没有口令
	HasPassphrase bool `json:"has_passphrase"`
	// Servers 使用这条密钥的服务器；没有时是空数组
	Servers []*ServerRef `json:"servers"`
	// CreatedAt Unix 秒
	CreatedAt int64 `json:"created_at"`
	// UpdatedAt Unix 秒
	UpdatedAt int64 `json:"updated_at"`
}

// ListRequest 列出共享密钥，按名称排序
type ListRequest struct {
	mux.Meta `path:"/credentials" method:"GET"`
}

type ListResponse struct {
	List []*Item `json:"list"`
}

// GetRequest 读取一条共享密钥
type GetRequest struct {
	mux.Meta `path:"/credentials/:id" method:"GET"`
	ID       int64 `uri:"id" binding:"required"`
}

type GetResponse struct {
	Item
}

// CreateRequest 添加共享密钥；密码或私钥必填
type CreateRequest struct {
	mux.Meta `path:"/credentials" method:"POST"`
	Name     string `json:"name"`
	// Kind password | ssh_key
	Kind       string `json:"kind"`
	Password   string `json:"password"`
	PrivateKey string `json:"private_key"`
	Passphrase string `json:"passphrase"`
	Username   string `json:"username"`
	Note       string `json:"note"`
}

// Input 取出提交的内容
func (r *CreateRequest) Input() *Input {
	return &Input{
		Name: r.Name, Kind: r.Kind, Password: r.Password, PrivateKey: r.PrivateKey,
		Passphrase: r.Passphrase, Username: r.Username, Note: r.Note,
	}
}

type CreateResponse struct {
	Item
}

// UpdateRequest 整体保存一条共享密钥；密码、私钥、口令留空表示保留已保存的值。
// 类型从一端切到另一端时，不属于新类型的内容会被清掉。
type UpdateRequest struct {
	mux.Meta `path:"/credentials/:id" method:"PUT"`
	ID       int64  `uri:"id" binding:"required"`
	Name     string `json:"name"`
	// Kind password | ssh_key
	Kind       string `json:"kind"`
	Password   string `json:"password"`
	PrivateKey string `json:"private_key"`
	Passphrase string `json:"passphrase"`
	Username   string `json:"username"`
	Note       string `json:"note"`
}

// Input 取出提交的内容
func (r *UpdateRequest) Input() *Input {
	return &Input{
		Name: r.Name, Kind: r.Kind, Password: r.Password, PrivateKey: r.PrivateKey,
		Passphrase: r.Passphrase, Username: r.Username, Note: r.Note,
	}
}

type UpdateResponse struct {
	Item
}

// DeleteRequest 删除一条共享密钥；被服务器使用时拒绝，并带出使用者名称
type DeleteRequest struct {
	mux.Meta `path:"/credentials/:id" method:"DELETE"`
	ID       int64 `uri:"id" binding:"required"`
}

type DeleteResponse struct{}

// NewCreateRequest 用一组提交内容构造添加请求
func NewCreateRequest(in *Input) *CreateRequest {
	return &CreateRequest{
		Name: in.Name, Kind: in.Kind, Password: in.Password, PrivateKey: in.PrivateKey,
		Passphrase: in.Passphrase, Username: in.Username, Note: in.Note,
	}
}

// NewUpdateRequest 用一组提交内容构造修改请求
func NewUpdateRequest(id int64, in *Input) *UpdateRequest {
	c := NewCreateRequest(in)
	return &UpdateRequest{
		ID: id, Name: c.Name, Kind: c.Kind, Password: c.Password, PrivateKey: c.PrivateKey,
		Passphrase: c.Passphrase, Username: c.Username, Note: c.Note,
	}
}
