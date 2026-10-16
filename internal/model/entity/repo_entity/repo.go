// Package repo_entity 存储位置（restic 仓库）
package repo_entity

import (
	"context"
	"net"
	"net/url"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/opskat/opsnap/internal/pkg/code"
)

// 存储位置类型
const (
	TypeLocal  = "local"
	TypeSFTP   = "sftp"
	TypeS3     = "s3"
	TypeREST   = "rest"
	TypeWebDAV = "webdav"
)

// 状态
const (
	StatusUnknown    = "unknown"
	StatusOK         = "ok"
	StatusNotMounted = "not_mounted"
	StatusError      = "error"
)

// MarkerFile 本地目录仓库根目录下的标记文件；找不到它就判定 NAS 没有挂载
const MarkerFile = ".opsnap-repo"

// FreeSpaceUnknown 拿不到剩余空间
const FreeSpaceUnknown int64 = -1

const (
	maxNameLen    = 64
	maxMessageLen = 500
)

// Params 地址参数（不含凭据）
type Params struct {
	Path        string `json:"path,omitempty"`
	Host        string `json:"host,omitempty"`
	Port        int    `json:"port,omitempty"`
	Username    string `json:"username,omitempty"`
	HostKey     string `json:"host_key,omitempty"`
	ServerID    int64  `json:"server_id,omitempty"`
	Endpoint    string `json:"endpoint,omitempty"`
	Bucket      string `json:"bucket,omitempty"`
	Prefix      string `json:"prefix,omitempty"`
	Region      string `json:"region,omitempty"`
	AccessKeyID string `json:"access_key_id,omitempty"`
	PathStyle   bool   `json:"path_style,omitempty"`
	URL         string `json:"url,omitempty"`
	Vendor      string `json:"vendor,omitempty"`
}

// Repo 存储位置。Credential、PrivateKey、OpsnapKey 都是主密钥加密后的密文；恢复密钥不保存，只记它的 restic 密钥 ID。
type Repo struct {
	ID     int64  `gorm:"column:id;primaryKey;autoIncrement"`
	Name   string `gorm:"column:name;not null"`
	Type   string `gorm:"column:type;not null"`
	Params Params `gorm:"column:params;serializer:json;not null"`
	// Credential SFTP/REST/WebDAV 的密码，或 S3 的 Secret Access Key
	Credential string `gorm:"column:credential;not null"`
	// PrivateKey SFTP 私钥
	PrivateKey string `gorm:"column:private_key;not null"`

	OpsnapKey            string `gorm:"column:opsnap_key;not null"`
	OpsnapKeyID          string `gorm:"column:opsnap_key_id;not null"`
	OpsnapKeyCreatedAt   int64  `gorm:"column:opsnap_key_created_at;not null"`
	RecoveryKeyID        string `gorm:"column:recovery_key_id;not null"`
	RecoveryKeyCreatedAt int64  `gorm:"column:recovery_key_created_at;not null"`

	Status        string `gorm:"column:status;not null"`
	StatusMessage string `gorm:"column:status_message;not null"`
	StatusAt      int64  `gorm:"column:status_at;not null"`
	StoredSize    int64  `gorm:"column:stored_size;not null"`
	RawSize       int64  `gorm:"column:raw_size;not null"`
	SnapshotCount int    `gorm:"column:snapshot_count;not null"`
	FreeSpace     int64  `gorm:"column:free_space;not null"`
	KeyCount      int    `gorm:"column:key_count;not null"`
	StatsAt       int64  `gorm:"column:stats_at;not null"`

	LastCheckAt      int64  `gorm:"column:last_check_at;not null"`
	LastCheckOK      bool   `gorm:"column:last_check_ok;not null"`
	LastCheckMessage string `gorm:"column:last_check_message;not null"`
	LastPruneAt      int64  `gorm:"column:last_prune_at;not null"`
	LastPruneOK      bool   `gorm:"column:last_prune_ok;not null"`
	LastPruneMessage string `gorm:"column:last_prune_message;not null"`

	Createtime int64 `gorm:"column:createtime"`
	Updatetime int64 `gorm:"column:updatetime"`
}

func (Repo) TableName() string { return "repos" }

// Check 存储位置是否存在
func (r *Repo) Check(ctx context.Context) error {
	if r == nil {
		return i18n.NewError(ctx, code.RepoNotFound)
	}
	return nil
}

// Validate 保存前校验名称、类型、地址参数和必需的凭据（Credential/PrivateKey 此时已是密文或空）
func (r *Repo) Validate(ctx context.Context) error {
	if r.Name == "" || len([]rune(r.Name)) > maxNameLen {
		return i18n.NewError(ctx, code.RepoFieldInvalid, "名称不能为空，且不超过 64 个字")
	}
	p := r.Params
	switch r.Type {
	case TypeLocal:
		if !absClean(p.Path) {
			return i18n.NewError(ctx, code.RepoFieldInvalid, "本地目录须是绝对路径")
		}
	case TypeSFTP:
		switch {
		case !validHost(p.Host):
			return i18n.NewError(ctx, code.RepoFieldInvalid, "SFTP 主机不能为空，也不能含空格或斜杠")
		case p.Port < 0 || p.Port > 65535:
			return i18n.NewError(ctx, code.RepoFieldInvalid, "SFTP 端口须在 1 到 65535 之间")
		case p.Username == "":
			return i18n.NewError(ctx, code.RepoFieldInvalid, "SFTP 用户不能为空")
		case !absClean(p.Path):
			return i18n.NewError(ctx, code.RepoFieldInvalid, "SFTP 仓库路径须是绝对路径")
		case r.Credential == "" && r.PrivateKey == "":
			return i18n.NewError(ctx, code.RepoCredentialRequired, "SFTP 需要密码或私钥")
		case p.HostKey == "":
			return i18n.NewError(ctx, code.RepoHostKeyUnconfirmed)
		}
	case TypeS3:
		switch {
		case !validHTTPURL(p.Endpoint):
			return i18n.NewError(ctx, code.RepoFieldInvalid, "S3 地址须是 http:// 或 https:// 开头的地址")
		case p.Bucket == "" || strings.ContainsAny(p.Bucket, " /"):
			return i18n.NewError(ctx, code.RepoFieldInvalid, "bucket 不能为空，也不能含空格或斜杠")
		case p.AccessKeyID == "":
			return i18n.NewError(ctx, code.RepoFieldInvalid, "Access Key ID 不能为空")
		case r.Credential == "":
			return i18n.NewError(ctx, code.RepoCredentialRequired, "请填写 Secret Access Key")
		}
	case TypeREST:
		if !validHTTPURL(p.URL) {
			return i18n.NewError(ctx, code.RepoFieldInvalid, "REST 地址须是 http:// 或 https:// 开头的地址，且不含用户名密码")
		}
	case TypeWebDAV:
		if !validHTTPURL(p.URL) {
			return i18n.NewError(ctx, code.RepoFieldInvalid, "WebDAV 地址须是 http:// 或 https:// 开头的地址，且不含用户名密码")
		}
	default:
		return i18n.NewError(ctx, code.RepoTypeInvalid)
	}
	return nil
}

func absClean(p string) bool {
	return p != "" && path.IsAbs(p) && !strings.ContainsAny(p, "\r\n")
}

func validHost(h string) bool {
	return h != "" && !strings.ContainsAny(h, " \t\r\n/")
}

func validHTTPURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil
}

// SFTPPort SFTP 端口，未填时为 22
func (r *Repo) SFTPPort() int {
	if r.Params.Port == 0 {
		return 22
	}
	return r.Params.Port
}

// SFTPAddr SFTP 主机地址 host:port
func (r *Repo) SFTPAddr() string {
	return net.JoinHostPort(r.Params.Host, strconv.Itoa(r.SFTPPort()))
}

// MarkerPath 本地目录仓库的标记文件路径
func (r *Repo) MarkerPath() string {
	return filepath.Join(r.Params.Path, MarkerFile)
}

// Address 仓库地址（restic 的 -r 写法），不含任何凭据。
// SFTP 用 restic 自带的 sftp 后端写法，方便在别的机器上直接恢复；WebDAV 需要先配置 rclone 远端 opsnap。
func (r *Repo) Address() string {
	p := r.Params
	switch r.Type {
	case TypeLocal:
		return p.Path
	case TypeSFTP:
		if r.SFTPPort() == 22 {
			return "sftp:" + p.Username + "@" + p.Host + ":" + p.Path
		}
		return "sftp://" + p.Username + "@" + r.SFTPAddr() + "/" + p.Path
	case TypeS3:
		a := "s3:" + strings.TrimRight(p.Endpoint, "/") + "/" + p.Bucket
		if pre := strings.Trim(p.Prefix, "/"); pre != "" {
			a += "/" + pre
		}
		return a
	case TypeREST:
		return "rest:" + p.URL
	case TypeWebDAV:
		return "rclone:opsnap:" + p.Path
	}
	return ""
}

// 恢复密钥的长度要求：去掉分组用的 - 后至少 32 个字符（POST /repos/recovery-key 生成的是 40 个 base32 字符，200 位）
const (
	MinRecoveryKeyLen = 32
	maxRecoveryKeyLen = 256
)

// ValidRecoveryKey 恢复密钥是否可用：只含可见 ASCII 字符（restic 从标准输入按行读取，不能有空白和换行），
// 去掉 - 后不少于 MinRecoveryKeyLen 个字符
func ValidRecoveryKey(s string) bool {
	if len(s) > maxRecoveryKeyLen {
		return false
	}
	n := 0
	for _, c := range s {
		if c <= ' ' || c > '~' {
			return false
		}
		if c != '-' {
			n++
		}
	}
	return n >= MinRecoveryKeyLen
}

// HasRecoveryKey 是否有恢复密钥
func (r *Repo) HasRecoveryKey() bool { return r.RecoveryKeyID != "" }

// DedupRatio 去重压缩比 = 原始数据量 / 实际占用
func (r *Repo) DedupRatio() float64 {
	if r.StoredSize <= 0 {
		return 0
	}
	return float64(r.RawSize) / float64(r.StoredSize)
}

// SetStatus 记录检查结果
func (r *Repo) SetStatus(status, message string, now time.Time) {
	r.Status = status
	r.StatusMessage = truncate(message)
	r.StatusAt = now.Unix()
}

// RecordCheck 记录一次校验数据的结果
func (r *Repo) RecordCheck(ok bool, message string, now time.Time) {
	r.LastCheckAt, r.LastCheckOK, r.LastCheckMessage = now.Unix(), ok, truncate(message)
}

// RecordPrune 记录一次清理空间的结果
func (r *Repo) RecordPrune(ok bool, message string, now time.Time) {
	r.LastPruneAt, r.LastPruneOK, r.LastPruneMessage = now.Unix(), ok, truncate(message)
}

// truncate 只保留末尾的内容：restic 的错误原因通常在最后几行
func truncate(s string) string {
	s = strings.TrimSpace(s)
	if rs := []rune(s); len(rs) > maxMessageLen {
		return "…" + string(rs[len(rs)-maxMessageLen:])
	}
	return s
}
