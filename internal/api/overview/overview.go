// Package overview 定义概览接口的请求与响应（docs/specs/2026-09-29-overview-docker.md「概览」）。
// 浏览器会话与 API 令牌都可以读取。
package overview

import (
	"github.com/cago-frame/cago/server/mux"

	"github.com/opskat/opsnap/internal/api/job"
)

// GetRequest 概览数据
type GetRequest struct {
	mux.Meta `path:"/overview" method:"GET"`
	// TZ 浏览器的 IANA 时区，14 天运行按它分天；缺省或无法识别时按 UTC
	TZ string `form:"tz"`
}

type GetResponse struct {
	Protected  Protected   `json:"protected"`
	Success24h SuccessRate `json:"success_24h"`
	// NextRun 已启用任务中最早的下一次运行，没有已启用的任务时为 null
	NextRun *NextRun `json:"next_run"`
	Recent  Recent   `json:"recent"`
	// Timezone 14 天运行实际使用的时区（请求的时区无法识别时为 UTC）
	Timezone string `json:"timezone"`
	// Daily 最近 14 天（含今天）每天的运行，按日期正序，最后一项为今天
	Daily []*DayRuns `json:"daily"`
	// Counts 空状态引导所需的数量
	Counts Counts `json:"counts"`
	// StorageUsage “存储占用”统计：各存储最近一次记录的仓库用量之和
	StorageUsage StorageUsage `json:"storage_usage"`
	// Storages 每个存储一行，按存储的创建顺序
	Storages []*Storage `json:"storages"`
}

// StorageUsage 所有能读到用量的存储之和；状态不是正常或读不到用量的存储不计入，只计入 Unreadable
type StorageUsage struct {
	// PackedBytes 仓库实际占用之和（全部快照引用的数据去重、压缩之后）
	PackedBytes int64 `json:"packed_bytes"`
	// OriginalBytes 仓库中全部快照的原始总大小之和
	OriginalBytes int64 `json:"original_bytes"`
	// Savings 节省比例 1 - PackedBytes/OriginalBytes（0–1），原始总大小为 0 时为 0
	Savings float64 `json:"savings"`
	// Unreadable 无法读取用量的存储数（“有 N 个存储无法读取”）
	Unreadable int `json:"unreadable"`
}

// Storage 一个存储目标。仓库用量是最近一次运行、完整维护或测试连接之后记录的值，读取概览本身不打开仓库；
// 本地目录所在磁盘的用量在每次请求时读取
type Storage struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	// Kind local / s3
	Kind string `json:"kind"`
	// Location 显示用的位置，与存储列表相同（本地为路径，S3 为 Endpoint/Bucket/前缀）
	Location string `json:"location"`
	// Path 本地目录的路径；S3 为空
	Path string `json:"path"`
	// Status 存储状态：ok / wrong_key / unreachable
	Status string `json:"status"`
	// Readable 状态为正常且最近一次读取用量成功；为 false 时下面三项用量为 0，界面显示“—”与 Reason
	Readable bool `json:"readable"`
	// Reason 状态不是正常或读不到用量的原因，按界面语言；Readable 时为空
	Reason string `json:"reason"`
	// PackedBytes 仓库实际占用（全部快照引用的数据去重、压缩之后）
	PackedBytes int64 `json:"packed_bytes"`
	// OriginalBytes 仓库中全部快照的原始总大小
	OriginalBytes int64 `json:"original_bytes"`
	// Snapshots 仓库中的快照数（含其他任务与非 OpsNap 产生的快照）
	Snapshots int `json:"snapshots"`
	// UsageRecordedAt 最近一次读取用量的时间（秒，含读取失败），从未读取为 0
	UsageRecordedAt int64 `json:"usage_recorded_at"`
	// Disk 本地目录所在磁盘的用量；S3 或读取失败时为 null
	Disk *Disk `json:"disk"`
}

// Disk 本地目录所在磁盘的用量（字节）
type Disk struct {
	// UsedBytes 已用
	UsedBytes int64 `json:"used_bytes"`
	// TotalBytes 总量
	TotalBytes int64 `json:"total_bytes"`
	// FreeBytes 剩余（OpsNap 进程可用的空间，不含文件系统为 root 保留的部分）
	FreeBytes int64 `json:"free_bytes"`
}

// Protected 受保护的数据源：至少被一个已启用的任务引用，且该任务至少成功过一次、有可恢复的快照
type Protected struct {
	Count int `json:"count"`
	// ByKind 按数据源类型的数量，按类型名排序；只列出数量大于 0 的类型
	ByKind []*KindCount `json:"by_kind"`
}

type KindCount struct {
	// Kind mysql / postgres
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}

// SuccessRate 开始时间在最近 24 小时内、已经结束的运行；跳过与取消的不计入
type SuccessRate struct {
	// Runs 成功与失败的次数之和
	Runs    int `json:"runs"`
	Success int `json:"success"`
	Failed  int `json:"failed"`
	// SuccessRate 成功 ÷（成功 + 失败），0–1；Runs 为 0 时为 0（界面显示“—”）
	SuccessRate float64 `json:"success_rate"`
}

// NextRun 下一次运行
type NextRun struct {
	// At 计划执行时间（秒）
	At      int64  `json:"at"`
	JobID   int64  `json:"job_id"`
	JobName string `json:"job_name"`
}

// Recent 最近运行
type Recent struct {
	// Items 所有任务中最近的 20 条运行，按触发顺序倒序，含等待中、运行中、跳过与取消
	Items []*RecentRun `json:"items"`
	// Failed 最近 20 条失败的运行，按触发顺序倒序（“失败”筛选）
	Failed []*RecentRun `json:"failed"`
	// Failed24h 最近 24 小时的失败次数（与 Success24h.Failed 同一口径）
	Failed24h int `json:"failed_24h"`
}

// RecentRun 一条运行及其所属任务；运行字段与任务页的运行记录相同（运行中的带实时的已导出量与已运行时间）
type RecentRun struct {
	job.Run
	JobName string `json:"job_name"`
	// JobType backup
	JobType string `json:"job_type"`
	// DataSourceKind mysql / postgres；数据源已不存在时为空
	DataSourceKind string `json:"datasource_kind"`
	// DataSourceAddress 数据源地址，如 mysql://host:port
	DataSourceAddress string `json:"datasource_address"`
}

// DayRuns 一天中开始的成功与失败运行次数；跳过与取消的不计入
type DayRuns struct {
	// Date 该时区中的日期 YYYY-MM-DD
	Date    string `json:"date"`
	Success int    `json:"success"`
	Failed  int    `json:"failed"`
}

// Counts 数据源、存储与任务的数量
type Counts struct {
	DataSources int `json:"datasources"`
	Storages    int `json:"storages"`
	Jobs        int `json:"jobs"`
}
