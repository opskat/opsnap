// Package repo 存储位置（restic 仓库）接口
package repo

import "github.com/cago-frame/cago/server/mux"

// 存储位置类型
const (
	TypeLocal  = "local"  // 本地目录（挂载进容器的 NAS 共享目录）
	TypeSFTP   = "sftp"   // SFTP
	TypeS3     = "s3"     // S3 兼容
	TypeREST   = "rest"   // restic REST server
	TypeWebDAV = "webdav" // WebDAV
)

// 存储位置状态
const (
	StatusUnknown    = "unknown"     // 还没有检查过
	StatusOK         = "ok"          // 最近一次检查正常
	StatusNotMounted = "not_mounted" // 本地目录缺少 .opsnap-repo，判定 NAS 没有挂载
	StatusError      = "error"       // 最近一次检查失败
)

// 正在进行的维护
const (
	MaintenanceCheck = "check" // 校验数据
	MaintenancePrune = "prune" // 清理空间
)

// Params 地址参数，按类型取用；凭据字段读取时只返回占位「已设置」
type Params struct {
	// Path 本地目录的绝对路径；SFTP、WebDAV 上的仓库路径
	Path string `json:"path"`
	// Host SFTP 主机
	Host string `json:"host"`
	// Port SFTP 端口，0 表示 22
	Port int `json:"port"`
	// Username SFTP、REST、WebDAV 的用户名
	Username string `json:"username"`
	// Password SFTP、REST、WebDAV 的密码；占位「已设置」表示保留已保存的值，地址或用户改了时须重新填写
	Password string `json:"password"`
	// PrivateKey SFTP 私钥（OpenSSH 格式，无口令）；占位「已设置」表示保留已保存的值
	PrivateKey string `json:"private_key"`
	// HostKey SFTP 主机公钥（authorized_keys 格式），来自 /repos/sftp-host-key，管理员核对指纹后提交
	HostKey string `json:"host_key"`
	// ServerID SFTP 经这台已添加的服务器（及其代理链）连接 NAS；0 表示直连
	ServerID int64 `json:"server_id"`
	// Endpoint S3 地址，形如 http://nas:9000
	Endpoint string `json:"endpoint"`
	Bucket   string `json:"bucket"`
	Prefix   string `json:"prefix"`
	Region   string `json:"region"`
	// AccessKeyID S3 Access Key ID
	AccessKeyID string `json:"access_key_id"`
	// SecretAccessKey S3 Secret Access Key；占位「已设置」表示保留已保存的值
	SecretAccessKey string `json:"secret_access_key"`
	// PathStyle S3 使用路径风格访问 bucket（MinIO 等通常需要）
	PathStyle bool `json:"path_style"`
	// URL REST、WebDAV 地址
	URL string `json:"url"`
	// Vendor WebDAV 的 rclone vendor，为空时用 other
	Vendor string `json:"vendor"`
}

// Item 存储位置；凭据只返回占位
type Item struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
	// Params 地址参数；凭据只返回占位
	Params Params `json:"params"`
	// Address 仓库地址，不含凭据
	Address string `json:"address"`
	// Encryption 加密方式，restic 仓库固定为 AES-256
	Encryption string `json:"encryption"`
	// Status unknown | ok | not_mounted | error
	Status        string `json:"status"`
	StatusMessage string `json:"status_message"`
	StatusAt      int64  `json:"status_at"`
	// StoredSize 实际占用（restic stats raw-data），字节
	StoredSize int64 `json:"stored_size"`
	// RawSize 原始数据量（restic stats restore-size），字节
	RawSize int64 `json:"raw_size"`
	// DedupRatio 去重压缩比 = 原始数据量 / 实际占用，没有数据时为 0
	DedupRatio    float64 `json:"dedup_ratio"`
	SnapshotCount int     `json:"snapshot_count"`
	// FreeSpace NAS 剩余空间，字节；拿不到时为 null
	FreeSpace *int64 `json:"free_space"`
	KeyCount  int    `json:"key_count"`
	// StatsAt 统计数据的更新时间，0 表示还没有统计
	StatsAt int64 `json:"stats_at"`
	// NoRecoveryKey 没有恢复密钥，界面显示警告「没有恢复密钥」
	NoRecoveryKey    bool   `json:"no_recovery_key"`
	LastCheckAt      int64  `json:"last_check_at"`
	LastCheckOK      bool   `json:"last_check_ok"`
	LastCheckMessage string `json:"last_check_message"`
	LastPruneAt      int64  `json:"last_prune_at"`
	LastPruneOK      bool   `json:"last_prune_ok"`
	LastPruneMessage string `json:"last_prune_message"`
	// Maintenance 正在进行的维护：check | prune，空表示没有
	Maintenance string `json:"maintenance"`
	JobCount    int    `json:"job_count"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
}

// ListRequest 列出存储位置
type ListRequest struct {
	mux.Meta `path:"/repos" method:"GET"`
}

type ListResponse struct {
	List []*Item `json:"list"`
}

// GetRequest 读取一个存储位置
type GetRequest struct {
	mux.Meta `path:"/repos/:id" method:"GET"`
	ID       int64 `uri:"id" binding:"required"`
}

type GetResponse struct {
	Item
}

// CreateRequest 添加存储位置。保存前先测试访问：位置为空就初始化新仓库；
// 已经是 restic 仓库时必须提供 ExistingPassword 才能接入。
type CreateRequest struct {
	mux.Meta `path:"/repos" method:"POST"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	// Params 按类型填写的地址参数与凭据
	Params Params `json:"params"`
	// ExistingPassword 已是 restic 仓库时，它现有的密码；只用于接入，不保存
	ExistingPassword string `json:"existing_password"`
	// RecoveryKey 同时加入一把恢复密钥；不传默认加入
	RecoveryKey *bool `json:"recovery_key"`
	// RecoveryKeyValue 恢复密钥：先经 POST /repos/recovery-key 取得并展示给管理员，保存时原样提交；opsnap 不保存
	RecoveryKeyValue string `json:"recovery_key_value"`
	// RecoveryKeySaved 管理员确认「已存进密码管理器」；加入恢复密钥时必须为 true
	RecoveryKeySaved bool `json:"recovery_key_saved"`
}

// WantRecoveryKey 是否要加入恢复密钥（默认加入）
func (r *CreateRequest) WantRecoveryKey() bool { return r.RecoveryKey == nil || *r.RecoveryKey }

type CreateResponse struct {
	Item
	// Adopted 接入的是已有仓库
	Adopted bool `json:"adopted"`
	// RecoveryKeyError 仓库已保存，但恢复密钥没有加入成功的原因；可稍后在密钥对话框里重新生成
	RecoveryKeyError string `json:"recovery_key_error"`
}

// UpdateRequest 修改名称和地址参数（类型不能改）；保存前用 opsnap 密钥验证仍能打开仓库
type UpdateRequest struct {
	mux.Meta `path:"/repos/:id" method:"PUT"`
	ID       int64  `uri:"id" binding:"required"`
	Name     string `json:"name"`
	// Params 地址参数；凭据为占位「已设置」时保留已保存的值
	Params Params `json:"params"`
}

type UpdateResponse struct {
	Item
}

// DeleteRequest 删除存储位置（NAS 上的数据不动）；被任务使用时拒绝
type DeleteRequest struct {
	mux.Meta `path:"/repos/:id" method:"DELETE"`
	ID       int64 `uri:"id" binding:"required"`
}

type DeleteResponse struct{}

// SFTPHostKeyRequest 取回 SFTP 主机出示的公钥与指纹，供管理员核对
type SFTPHostKeyRequest struct {
	mux.Meta `path:"/repos/sftp-host-key" method:"POST"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	// ServerID 经这台服务器连接 NAS；0 表示直连
	ServerID int64 `json:"server_id"`
}

type SFTPHostKeyResponse struct {
	// Fingerprint SHA256 指纹
	Fingerprint string `json:"fingerprint"`
	// HostKey 公钥（authorized_keys 格式），核对后随保存提交到 host_key
	HostKey string `json:"host_key"`
}

// KeyStatus 一把密钥的状态
type KeyStatus struct {
	Present bool `json:"present"`
	// ID restic 密钥 ID
	ID        string `json:"id"`
	CreatedAt int64  `json:"created_at"`
}

// Keys 两把密钥的状态
type Keys struct {
	OpsnapKey     KeyStatus `json:"opsnap_key"`
	RecoveryKey   KeyStatus `json:"recovery_key"`
	NoRecoveryKey bool      `json:"no_recovery_key"`
}

// KeysRequest 查看两把密钥的状态和创建时间
type KeysRequest struct {
	mux.Meta `path:"/repos/:id/keys" method:"GET"`
	ID       int64 `uri:"id" binding:"required"`
}

type KeysResponse struct {
	Keys
}

// RotateKeyRequest 更换 opsnap 密钥：先加新密钥并验证，再删旧密钥；任何一步失败都保持原状
type RotateKeyRequest struct {
	mux.Meta `path:"/repos/:id/keys/rotate" method:"POST"`
	ID       int64 `uri:"id" binding:"required"`
}

type RotateKeyResponse struct {
	Keys
}

// NewRecoveryKeyRequest 生成一把恢复密钥的值供界面展示、复制和下载；不保存、不加入任何仓库。
// 管理员存进密码管理器后，随添加存储位置或「生成恢复密钥」请求提交。
type NewRecoveryKeyRequest struct {
	mux.Meta `path:"/repos/recovery-key" method:"POST"`
}

type NewRecoveryKeyResponse struct {
	RecoveryKeyValue string `json:"recovery_key_value"`
}

// GenerateRecoveryKeyRequest 把管理员已保存的恢复密钥加入仓库；已有恢复密钥时拒绝
type GenerateRecoveryKeyRequest struct {
	mux.Meta `path:"/repos/:id/keys/recovery" method:"POST"`
	ID       int64 `uri:"id" binding:"required"`
	// RecoveryKeyValue 经 POST /repos/recovery-key 取得并展示过的恢复密钥；opsnap 不保存
	RecoveryKeyValue string `json:"recovery_key_value"`
	// RecoveryKeySaved 管理员确认「已存进密码管理器」，必须为 true
	RecoveryKeySaved bool `json:"recovery_key_saved"`
}

type GenerateRecoveryKeyResponse struct {
	Keys
}

// RemoveRecoveryKeyRequest 移除恢复密钥
type RemoveRecoveryKeyRequest struct {
	mux.Meta `path:"/repos/:id/keys/recovery" method:"DELETE"`
	ID       int64 `uri:"id" binding:"required"`
}

type RemoveRecoveryKeyResponse struct {
	Keys
}

// RecoveryBundleRequest 下载恢复包：仓库地址、恢复步骤和 restic 命令，不含任何密钥或密码
type RecoveryBundleRequest struct {
	mux.Meta `path:"/repos/:id/recovery-bundle" method:"GET"`
	ID       int64 `uri:"id" binding:"required"`
}

type RecoveryBundleResponse struct {
	Filename string `json:"filename"`
	Content  string `json:"content"`
}

// RecheckRequest 重新检查：本地目录先检查 .opsnap-repo，再刷新统计（占用、快照数、剩余空间、密钥数量）
type RecheckRequest struct {
	mux.Meta `path:"/repos/:id/recheck" method:"POST"`
	ID       int64 `uri:"id" binding:"required"`
}

type RecheckResponse struct {
	Item
}

// CheckRequest 校验数据（restic check 并抽样读取数据）；在后台独占执行
type CheckRequest struct {
	mux.Meta `path:"/repos/:id/check" method:"POST"`
	ID       int64 `uri:"id" binding:"required"`
}

type CheckResponse struct {
	Item
}

// PruneRequest 清理空间（restic prune）；在后台独占执行
type PruneRequest struct {
	mux.Meta `path:"/repos/:id/prune" method:"POST"`
	ID       int64 `uri:"id" binding:"required"`
}

type PruneResponse struct {
	Item
}

// UnlockRequest 解除仓库锁（restic unlock --remove-all）；必须 confirm 为 true
type UnlockRequest struct {
	mux.Meta `path:"/repos/:id/unlock" method:"POST"`
	ID       int64 `uri:"id" binding:"required"`
	Confirm  bool  `json:"confirm"`
}

type UnlockResponse struct{}
