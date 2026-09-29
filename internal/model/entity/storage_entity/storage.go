// Package storage_entity 定义存储实体。
package storage_entity

import "github.com/opskat/opsnap/internal/pkg/kopiarepo"

// 存储状态
const (
	StatusOK          = "ok"          // 能连接，托管密钥能打开仓库
	StatusWrongKey    = "wrong_key"   // 能连接，但托管密钥打不开仓库
	StatusUnreachable = "unreachable" // 无法连接或不再是仓库
)

type Storage struct {
	ID         int64  `gorm:"column:id;primaryKey"`
	Name       string `gorm:"column:name"`
	Kind       string `gorm:"column:kind"`
	Path       string `gorm:"column:path"`
	Endpoint   string `gorm:"column:endpoint"`
	Region     string `gorm:"column:region"`
	Bucket     string `gorm:"column:bucket"`
	Prefix     string `gorm:"column:prefix"`
	AccessKey  string `gorm:"column:access_key"`
	SecretKey  string `gorm:"column:secret_key"` // 主密钥加密后的密文，未设置时为空
	UseTLS     bool   `gorm:"column:use_tls"`
	SkipVerify bool   `gorm:"column:skip_verify"`
	// LocationKey 规范化后的位置标识（kopiarepo.Location.Key）
	LocationKey string `gorm:"column:location_key"`
	RepoKey     string `gorm:"column:repo_key"` // 主密钥加密后的仓库密钥
	Fingerprint string `gorm:"column:fingerprint"`
	Status      string `gorm:"column:status"`
	// StatusCode 最近一次测试失败时的错误码，正常为 0
	StatusCode   int    `gorm:"column:status_code"`
	StatusDetail string `gorm:"column:status_detail"`
	Checktime    int64  `gorm:"column:checktime"`
	Createtime   int64  `gorm:"column:createtime"`
	Updatetime   int64  `gorm:"column:updatetime"`
	// Usage 仓库用量，只由 storage_repo 的 SetUsage / SetUsageError 写入，Save 不改动
	Usage Usage `gorm:"embedded"`
}

// Usage 仓库用量：最近一次运行、完整维护或测试连接之后读取（docs/specs/2026-09-29-overview-docker.md「存储目标」）
type Usage struct {
	// Snapshots 仓库中的快照数（含其他任务与非 OpsNap 产生的快照）
	Snapshots int `gorm:"column:usage_snapshots"`
	// PackedBytes 全部快照引用的数据去重、压缩后在仓库中的大小
	PackedBytes int64 `gorm:"column:usage_packed_bytes"`
	// OriginalBytes 全部快照的原始总大小
	OriginalBytes int64 `gorm:"column:usage_original_bytes"`
	// Error、ErrorEn 最近一次读取失败的原因（中文、英文），成功时为空；失败时保留上一次读到的数字
	Error   string `gorm:"column:usage_error"`
	ErrorEn string `gorm:"column:usage_error_en"`
	// Checktime 最近一次读取（含失败）的时间（秒），从未读取为 0
	Checktime int64 `gorm:"column:usage_checktime"`
}

func (Storage) TableName() string { return "storages" }

// Location 用解密后的 Secret Key 还原位置参数
func (s *Storage) Location(secretKey string) kopiarepo.Location {
	return kopiarepo.Location{
		Kind:       kopiarepo.Kind(s.Kind),
		Path:       s.Path,
		Endpoint:   s.Endpoint,
		Region:     s.Region,
		Bucket:     s.Bucket,
		Prefix:     s.Prefix,
		AccessKey:  s.AccessKey,
		SecretKey:  secretKey,
		UseTLS:     s.UseTLS,
		SkipVerify: s.SkipVerify,
	}
}

// SetLocation 保存规范化后的位置参数（不含 Secret Key）
func (s *Storage) SetLocation(loc kopiarepo.Location) {
	s.Kind = string(loc.Kind)
	s.Path = loc.Path
	s.Endpoint = loc.Endpoint
	s.Region = loc.Region
	s.Bucket = loc.Bucket
	s.Prefix = loc.Prefix
	s.AccessKey = loc.AccessKey
	s.UseTLS = loc.UseTLS
	s.SkipVerify = loc.SkipVerify
	s.LocationKey = loc.Key()
}
