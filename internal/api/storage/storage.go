// Package storage 定义存储管理接口的请求与响应。浏览器会话与 API 令牌都可以调用；
// 任何响应都不包含 S3 Secret Key，也不包含仓库密钥（生成密钥接口返回的是尚未保存的新密钥）。
package storage

import "github.com/cago-frame/cago/server/mux"

// Location 存储类型与位置参数；新建、编辑与测试连接共用
type Location struct {
	// Kind local / s3
	Kind string `json:"kind" binding:"required,oneof=local s3"`
	// Path 本地目录的绝对路径
	Path string `json:"path"`

	Endpoint  string `json:"endpoint"`
	Region    string `json:"region"`
	Bucket    string `json:"bucket"`
	Prefix    string `json:"prefix"`
	AccessKey string `json:"access_key"`
	// SecretKey 编辑时留空表示沿用已保存的值
	SecretKey  string `json:"secret_key"`
	UseTLS     bool   `json:"use_tls"`
	SkipVerify bool   `json:"skip_verify"`
}

// Item 列表中的一个存储
type Item struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
	Path string `json:"path"`

	Endpoint     string `json:"endpoint"`
	Region       string `json:"region"`
	Bucket       string `json:"bucket"`
	Prefix       string `json:"prefix"`
	AccessKey    string `json:"access_key"`
	HasSecretKey bool   `json:"has_secret_key"`
	UseTLS       bool   `json:"use_tls"`
	SkipVerify   bool   `json:"skip_verify"`

	// Location 展示用位置：本地为绝对路径，S3 为 s3://<bucket>/<前缀>
	Location    string `json:"location"`
	Fingerprint string `json:"fingerprint"`
	Encryption  string `json:"encryption"`
	// Status ok / wrong_key / unreachable
	Status string `json:"status"`
	// StatusMessage 无法连接或密钥不正确时的原因（按请求语言）
	StatusMessage string `json:"status_message"`
	CheckedAt     int64  `json:"checked_at"`
	CreatedAt     int64  `json:"created_at"`
}

// ListRequest 列出全部存储；不会触发测试连接
type ListRequest struct {
	mux.Meta `path:"/storages" method:"GET"`
}

type ListResponse struct {
	Items []*Item `json:"items"`
}

// ProbeRequest 测试连接：校验字段与唯一性，检查能否连接、能否写入，并判断目标位置属于哪种情况
type ProbeRequest struct {
	mux.Meta `path:"/storages/probe" method:"POST"`
	// ID 编辑已有存储时传入：唯一性检查排除它自己，Secret Key 留空时沿用它保存的值
	ID       int64    `json:"id"`
	Name     string   `json:"name"`
	Location Location `json:"location"`
}

type ProbeResponse struct {
	// State empty / repository / not_empty
	State string `json:"state"`
	// CreatedAt 仓库格式文件的写入时间（秒），仅 repository 且取得到时非 0
	CreatedAt int64  `json:"created_at"`
	Location  string `json:"location"`
	// LocationChanged 编辑时位置是否与已保存的不同
	LocationChanged bool `json:"location_changed"`
}

// CreateRequest 新建存储。目标位置为空时用 Key 建库（需 ConfirmSaved）；已是仓库时用 Key 解锁
type CreateRequest struct {
	mux.Meta `path:"/storages" method:"POST"`
	Name     string   `json:"name"`
	Location Location `json:"location"`
	Key      string   `json:"key"`
	// ConfirmSaved 用户已勾选“我已保存这把密钥”；在空位置建库时必须为 true
	ConfirmSaved bool `json:"confirm_saved"`
}

type CreateResponse struct {
	Item *Item `json:"item"`
	// Snapshots 解锁已有仓库时仓库中的快照数
	Snapshots int `json:"snapshots"`
}

// UpdateRequest 编辑存储。位置变化时需 ConfirmLocationChange；新位置已是仓库时需 Key 解锁
type UpdateRequest struct {
	mux.Meta              `path:"/storages/:id" method:"PUT"`
	ID                    int64    `uri:"id" binding:"required"`
	Name                  string   `json:"name"`
	Location              Location `json:"location"`
	Key                   string   `json:"key"`
	ConfirmLocationChange bool     `json:"confirm_location_change"`
}

type UpdateResponse struct {
	Item      *Item `json:"item"`
	Snapshots int   `json:"snapshots"`
}

// TestRequest 用已保存的参数与托管密钥测试连接，结果写入列表状态
type TestRequest struct {
	mux.Meta `path:"/storages/:id/test" method:"POST"`
	ID       int64 `uri:"id" binding:"required"`
}

type TestResponse struct {
	Item      *Item `json:"item"`
	Snapshots int   `json:"snapshots"`
}

// UnlockRequest 重新解锁：用新输入的密钥打开仓库，成功后改用它作为托管密钥
type UnlockRequest struct {
	mux.Meta `path:"/storages/:id/unlock" method:"POST"`
	ID       int64  `uri:"id" binding:"required"`
	Key      string `json:"key"`
}

type UnlockResponse struct {
	Item      *Item `json:"item"`
	Snapshots int   `json:"snapshots"`
}

// DeleteRequest 删除存储：只删除 OpsNap 中的记录、托管的密钥与本机缓存，存储里的数据保留
type DeleteRequest struct {
	mux.Meta `path:"/storages/:id" method:"DELETE"`
	ID       int64 `uri:"id" binding:"required"`
}

type DeleteResponse struct{}

// KeyRequest 生成新密钥（Key 为空时）或计算自设密码的指纹
type KeyRequest struct {
	mux.Meta `path:"/storages/key" method:"POST"`
	Key      string `json:"key"`
}

type KeyResponse struct {
	Key         string `json:"key"`
	Fingerprint string `json:"fingerprint"`
	Encryption  string `json:"encryption"`
}
