// Package setting 全局设置接口
package setting

import "github.com/cago-frame/cago/server/mux"

// MasterKeyStatus 主密钥状态，只读
type MasterKeyStatus struct {
	Configured bool   `json:"configured"` // 是否已从 OPSNAP_SECRET_KEY 加载
	Algorithm  string `json:"algorithm"`
	// RotatedAt 最近一次轮换的时间，Unix 秒；没有轮换过为 0
	RotatedAt int64 `json:"rotated_at"`
}

// GetRequest 读取设置
type GetRequest struct {
	mux.Meta `path:"/settings" method:"GET"`
}

type GetResponse struct {
	AdminUsername          string          `json:"admin_username"`
	MaxConcurrentRuns      int             `json:"max_concurrent_runs"`
	RetentionDays          int             `json:"retention_days"`
	Timezone               string          `json:"timezone"`
	ClearMirrorAfterBackup bool            `json:"clear_mirror_after_backup"`
	MasterKey              MasterKeyStatus `json:"master_key"`
	// MirrorDir 文件镜像目录：本机唯一存放明文的地方
	MirrorDir string `json:"mirror_dir"`
}

// UpdateRequest 保存设置；四项一起提交，主密钥状态只读
type UpdateRequest struct {
	mux.Meta               `path:"/settings" method:"PUT"`
	MaxConcurrentRuns      int    `json:"max_concurrent_runs" form:"max_concurrent_runs"`
	RetentionDays          int    `json:"retention_days" form:"retention_days"`
	Timezone               string `json:"timezone" form:"timezone"`
	ClearMirrorAfterBackup bool   `json:"clear_mirror_after_backup" form:"clear_mirror_after_backup"`
}

type UpdateResponse struct {
	GetResponse
}

// RotateMasterKeyRequest 轮换主密钥：用新主密钥重新加密所有保存的凭据（服务器、代理、数据库密码、存储位置、Webhook 请求头），
// 全部成功才生效，任何一项失败都保持原状。生效后本进程立即改用新主密钥；
// 管理员必须把环境变量 OPSNAP_SECRET_KEY 改成新主密钥，否则下次启动会因主密钥不匹配而拒绝启动。
type RotateMasterKeyRequest struct {
	mux.Meta `path:"/settings/master-key/rotate" method:"POST"`
	NewKey   string `json:"new_key"`
}

type RotateMasterKeyResponse struct {
	RotatedAt int64 `json:"rotated_at"`
}
