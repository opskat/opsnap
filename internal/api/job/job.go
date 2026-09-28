// Package job 定义备份任务接口的请求与响应（docs/specs/2026-09-27-backup-jobs.md「任务与新建向导」
// 「编辑、暂停、删除」）。浏览器会话与 API 令牌都可以调用。
package job

import "github.com/cago-frame/cago/server/mux"

// Options 一并备份的内容：MySQL 看 routines/triggers/events/users，PostgreSQL 看 globals，另一类的项保存为 false
type Options struct {
	// Routines 存储过程与函数
	Routines bool `json:"routines"`
	Triggers bool `json:"triggers"`
	Events   bool `json:"events"`
	// Users 账号与权限
	Users bool `json:"users"`
	// Globals 角色、表空间等全局对象
	Globals bool `json:"globals"`
}

// Schedule 执行计划
type Schedule struct {
	// Kind hourly / daily / weekly / cron
	Kind   string `json:"kind"`
	Minute int    `json:"minute"`
	Hour   int    `json:"hour"`
	// Weekdays 每周执行的星期几，0 为周日
	Weekdays []int `json:"weekdays"`
	// Cron 标准 5 段表达式（分 时 日 月 周）
	Cron string `json:"cron"`
	// Timezone IANA 时区名称
	Timezone string `json:"timezone"`
}

// Retention 保留策略
type Retention struct {
	// Days 保留最近 N 天内的全部快照，1–365
	Days int `json:"days"`
	// Weeks 更早的每周保留最后一份，0–520
	Weeks int `json:"weeks"`
	// Months 每月保留最后一份，0–120
	Months int `json:"months"`
}

// Failure 失败处理
type Failure struct {
	// Retries 失败重试次数，0–5
	Retries int `json:"retries"`
	// RetryInterval 重试间隔（分钟），1–120
	RetryInterval int `json:"retry_interval"`
	// Timeout 超时时长（分钟），10–2880
	Timeout int `json:"timeout"`
}

// Item 一个任务
type Item struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	// Type backup
	Type           string `json:"type"`
	DataSourceID   int64  `json:"datasource_id"`
	DataSourceName string `json:"datasource_name"`
	// DataSourceKind mysql / postgres
	DataSourceKind string `json:"datasource_kind"`
	StorageID      int64  `json:"storage_id"`
	StorageName    string `json:"storage_name"`
	Prefix         string `json:"prefix"`
	// Location 仓库内位置：<存储名>:/<路径前缀>
	Location string `json:"location"`
	// Scope instance（整个实例）/ databases（指定数据库）
	Scope     string   `json:"scope"`
	Databases []string `json:"databases"`
	// Method full
	Method        string   `json:"method"`
	Options       Options  `json:"options"`
	ExcludeTables []string `json:"exclude_tables"`
	// Compression none / gzip / zstd
	Compression string    `json:"compression"`
	Schedule    Schedule  `json:"schedule"`
	Retention   Retention `json:"retention"`
	Failure     Failure   `json:"failure"`
	// Enabled false 表示已暂停
	Enabled bool `json:"enabled"`
	// NextRunAt 下一次计划执行时间（秒），暂停时为 0
	NextRunAt int64 `json:"next_run_at"`
	CreatedAt int64 `json:"created_at"`
	UpdatedAt int64 `json:"updated_at"`
}

// Ref 引用某个数据源或存储的任务
type Ref struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// ListRequest 列出全部任务
type ListRequest struct {
	mux.Meta `path:"/jobs" method:"GET"`
}

type ListResponse struct {
	Items []*Item `json:"items"`
}

// GetRequest 按 ID 获取任务
type GetRequest struct {
	mux.Meta `path:"/jobs/:id" method:"GET"`
	ID       int64 `uri:"id" binding:"required"`
}

type GetResponse struct {
	Item *Item `json:"item"`
}

// CreateRequest 新建并启用任务（向导第 5 步“创建任务”）
type CreateRequest struct {
	mux.Meta `path:"/jobs" method:"POST"`
	// Type backup（sync 为后续版本）
	Type         string `json:"type"`
	DataSourceID int64  `json:"datasource_id"`
	StorageID    int64  `json:"storage_id"`
	Prefix       string `json:"prefix"`

	Name          string    `json:"name"`
	Scope         string    `json:"scope"`
	Databases     []string  `json:"databases"`
	Method        string    `json:"method"`
	Options       Options   `json:"options"`
	ExcludeTables []string  `json:"exclude_tables"`
	Compression   string    `json:"compression"`
	Schedule      Schedule  `json:"schedule"`
	Retention     Retention `json:"retention"`
	Failure       Failure   `json:"failure"`
	// RunNow 创建后“立即执行一次”；false 为“等下一次计划”。保存在任务上，由运行模块处理
	RunNow bool `json:"run_now"`
}

type CreateResponse struct {
	Item *Item `json:"item"`
}

// UpdateRequest 编辑任务。数据源、存储与路径前缀创建后不能修改：留空（0 或空串）表示不变，与保存的不同时报错
type UpdateRequest struct {
	mux.Meta     `path:"/jobs/:id" method:"PUT"`
	ID           int64  `uri:"id" binding:"required"`
	DataSourceID int64  `json:"datasource_id"`
	StorageID    int64  `json:"storage_id"`
	Prefix       string `json:"prefix"`

	Name          string    `json:"name"`
	Scope         string    `json:"scope"`
	Databases     []string  `json:"databases"`
	Method        string    `json:"method"`
	Options       Options   `json:"options"`
	ExcludeTables []string  `json:"exclude_tables"`
	Compression   string    `json:"compression"`
	Schedule      Schedule  `json:"schedule"`
	Retention     Retention `json:"retention"`
	Failure       Failure   `json:"failure"`
}

type UpdateResponse struct {
	Item *Item `json:"item"`
}

// PauseRequest 暂停：计划不再触发，已在运行或排队的运行不受影响
type PauseRequest struct {
	mux.Meta `path:"/jobs/:id/pause" method:"POST"`
	ID       int64 `uri:"id" binding:"required"`
}

type PauseResponse struct {
	Item *Item `json:"item"`
}

// EnableRequest 启用：从下一次计划时间开始，暂停期间的计划不算错过
type EnableRequest struct {
	mux.Meta `path:"/jobs/:id/enable" method:"POST"`
	ID       int64 `uri:"id" binding:"required"`
}

type EnableResponse struct {
	Item *Item `json:"item"`
}

// DeleteRequest 删除任务；正在运行或排队时拒绝。DeleteSnapshots 为 true 时同时删除仓库中本任务的快照
type DeleteRequest struct {
	mux.Meta        `path:"/jobs/:id" method:"DELETE"`
	ID              int64 `uri:"id" binding:"required"`
	DeleteSnapshots bool  `form:"delete_snapshots"`
}

type DeleteResponse struct {
	// SnapshotsDeleted 已删除的快照份数
	SnapshotsDeleted int `json:"snapshots_deleted"`
	// SnapshotsFailed 找到但未能删除的快照份数
	SnapshotsFailed int `json:"snapshots_failed"`
	// SnapshotsMessage 快照未能全部删除时的提示（按请求语言），全部删除或未要求删除时为空；
	// 无法打开存储时份数未知，SnapshotsFailed 为 0，只有这条提示
	SnapshotsMessage string `json:"snapshots_message"`
}

// SchedulePreviewRequest 向导第 4 步的预览：接下来三次执行时间与最多保留的快照份数
type SchedulePreviewRequest struct {
	mux.Meta  `path:"/jobs/schedule-preview" method:"POST"`
	Schedule  Schedule  `json:"schedule"`
	Retention Retention `json:"retention"`
}

type SchedulePreviewResponse struct {
	// NextRuns 接下来三次执行时间，RFC 3339，带所选时区的偏移
	NextRuns []string `json:"next_runs"`
	// MaxSnapshots 按当前计划最多保留约 K 份快照
	MaxSnapshots int `json:"max_snapshots"`
}
