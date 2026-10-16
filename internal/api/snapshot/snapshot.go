// Package snapshot 快照接口：按任务列出快照、浏览文件树、下载文件或数据库导出。
//
// 下载失败在发送响应头之前判定：仓库被锁、存储位置不可访问、快照或路径不存在时返回普通 JSON 错误，
// 不会留下半截文件；响应头发出后若数据流中断，服务端直接中止连接，客户端会看到下载失败。
package snapshot

import "github.com/cago-frame/cago/server/mux"

// 保留原因，对应 restic forget 的保留规则；面板分别显示为 最近、每天、每周、每月
const (
	ReasonLast    = "last"
	ReasonDaily   = "daily"
	ReasonWeekly  = "weekly"
	ReasonMonthly = "monthly"
)

// ListRequest 一个任务的全部快照，按时间倒序
type ListRequest struct {
	mux.Meta `path:"/jobs/:id/snapshots" method:"GET"`
	ID       int64 `uri:"id" binding:"required"`
}

// Item 一个快照。时间是 Unix 秒。
type Item struct {
	// ID 完整快照 ID；ShortID 是前 8 位，面板显示用
	ID      string `json:"id"`
	ShortID string `json:"short_id"`
	Time    int64  `json:"time"`
	// Paths 快照里记录的路径：文件任务是镜像目录，数据库任务是导出文件
	Paths []string `json:"paths"`
	// RunID 产生它的运行，0 表示未知
	RunID int64 `json:"run_id"`
	// BytesAdded 本次新增的数据量；-1 表示未知。
	// 优先取 restic 记录的本快照新增量，没有时取运行记录里的新增量（仅当这次运行只产生一个快照）。
	BytesAdded int64 `json:"bytes_added"`
	// Reasons 被保留的原因，由任务的保留规则演算得出：last | daily | weekly | monthly；演算失败时为空
	Reasons []string `json:"reasons"`
}

type ListResponse struct {
	JobName    string  `json:"job_name"`
	SourceType string  `json:"source_type"`
	Snapshots  []*Item `json:"snapshots"`
}

// FilesRequest 快照里某个目录的直接子项（懒加载一层），path 默认 /
type FilesRequest struct {
	mux.Meta `path:"/jobs/:id/snapshots/:sid/files" method:"GET"`
	ID       int64  `uri:"id" binding:"required"`
	SID      string `uri:"sid" binding:"required"`
	Path     string `form:"path"`
}

// Entry 目录里的一项。MTime 是 Unix 秒。
type Entry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	// Type dir | file | symlink | other
	Type  string `json:"type"`
	Size  int64  `json:"size"`
	MTime int64  `json:"mtime"`
}

type FilesResponse struct {
	Path    string   `json:"path"`
	Entries []*Entry `json:"entries"`
}

// ExportsRequest 数据库快照里的导出文件，以及每个文件的下载名和恢复命令示例。opsnap 不会写回任何线上数据库。
type ExportsRequest struct {
	mux.Meta `path:"/jobs/:id/snapshots/:sid/exports" method:"GET"`
	ID       int64  `uri:"id" binding:"required"`
	SID      string `uri:"sid" binding:"required"`
}

// Export 一个导出文件
type Export struct {
	// Path 快照内路径，下载时作为 path 参数
	Path string `json:"path"`
	Size int64  `json:"size"`
	// Gzip 下载时需要带 gzip=true（MySQL/MariaDB 的 .sql 压缩成 .sql.gz）
	Gzip bool `json:"gzip"`
	// Filename 下载得到的文件名
	Filename string `json:"filename"`
	// RestoreCommand 可复制的恢复命令示例，地址、账号、库名需要按目标环境替换
	RestoreCommand string `json:"restore_command"`
}

type ExportsResponse struct {
	Files []*Export `json:"files"`
}

// DownloadRequest 流式下载。文件任务：path 可重复，所选路径打包成 .tar.gz；
// 数据库任务：path 恰好一个导出文件，按存储的格式原样下载（gzip=true 时压缩为 .gz）。
type DownloadRequest struct {
	mux.Meta `path:"/jobs/:id/snapshots/:sid/download" method:"GET"`
	ID       int64    `uri:"id" binding:"required"`
	SID      string   `uri:"sid" binding:"required"`
	Path     []string `form:"path"`
	Gzip     bool     `form:"gzip"`
}

// DownloadResponse 文档占位：成功时响应是文件流（Content-Disposition 给出文件名）；失败按普通 JSON 错误返回
type DownloadResponse struct{}

// CheckDownloadRequest 下载预检：做与 download 完全相同的检查（已挂载、仓库可访问且没有被锁、快照属于这个任务、
// 路径存在），但不读取数据。面板先预检，通过后才让浏览器发起真正的下载，所以失败原因显示在面板上，
// 浏览器不会保存任何半截文件。参数同 download。
type CheckDownloadRequest struct {
	mux.Meta `path:"/jobs/:id/snapshots/:sid/download/check" method:"GET"`
	ID       int64    `uri:"id" binding:"required"`
	SID      string   `uri:"sid" binding:"required"`
	Path     []string `form:"path"`
	Gzip     bool     `form:"gzip"`
}

type CheckDownloadResponse struct{}
