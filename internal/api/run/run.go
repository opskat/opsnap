// Package run 运行记录接口：手动运行、列表与详情、停止、日志与实时日志（SSE）
package run

import (
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/cago-frame/cago/server/mux"
)

// 运行状态
const (
	StatusRunning = "running" // 运行中（含等待空闲名额）
	StatusSuccess = "success" // 成功
	StatusWarning = "warning" // 有警告：快照已生成
	StatusFailed  = "failed"  // 失败：没有留下快照
	StatusSkipped = "skipped" // 计划触发时上一次还在运行，已跳过
)

// Step 一个步骤。key 依次为 preflight（预检）、export（导出并写入快照）、forget（清理旧快照）、notify（记录并通知）；
// status 为 pending | running | success | warning | failed | skipped。时间是 Unix 毫秒，0 表示未开始或未结束。
type Step struct {
	Key        string `json:"key"`
	Label      string `json:"label"`
	Status     string `json:"status"`
	Message    string `json:"message"`
	StartedAt  int64  `json:"started_at"`
	FinishedAt int64  `json:"finished_at"`
}

// Item 一次运行。时间是 Unix 秒，耗时是毫秒。
type Item struct {
	ID      int64  `json:"id"`
	JobID   int64  `json:"job_id"`
	JobName string `json:"job_name"`
	// Trigger schedule | manual | api_key:<密钥名称>（<密钥前缀>…）
	Trigger      string `json:"trigger"`
	TriggerLabel string `json:"trigger_label"`
	// Status running | success | warning | failed | skipped
	Status string `json:"status"`
	// Reason 失败原因（如「超时」「手动停止」「没有挂载：…」）或警告说明
	Reason string `json:"reason"`
	// NotifyFailed 通知发送失败
	NotifyFailed   bool     `json:"notify_failed"`
	StartedAt      int64    `json:"started_at"`
	FinishedAt     int64    `json:"finished_at"`
	DurationMs     int64    `json:"duration_ms"`
	BytesProcessed int64    `json:"bytes_processed"`
	BytesAdded     int64    `json:"bytes_added"`
	SnapshotIDs    []string `json:"snapshot_ids"`
	Steps          []*Step  `json:"steps"`
}

// LogLine 一行日志，凭据已遮盖。t 是 Unix 毫秒；level 为 info | warn | error。
type LogLine struct {
	Time  int64  `json:"t"`
	Level string `json:"level"`
	Text  string `json:"text"`
}

// RunJobRequest 立即运行一次任务（暂停的任务也可以手动运行）。同一任务正在运行时返回错误码 11501。
// 由 API 密钥发起时，运行记录的触发方式写 api_key:<密钥名称>（<密钥前缀>…）。
type RunJobRequest struct {
	mux.Meta `path:"/jobs/:id/run" method:"POST"`
	ID       int64 `uri:"id" binding:"required"`
}

type RunJobResponse struct {
	RunID int64 `json:"run_id"`
}

// ListRequest 运行列表，按开始时间倒序；job_id、status 为空表示不筛选
type ListRequest struct {
	mux.Meta              `path:"/runs" method:"GET"`
	JobID                 int64  `form:"job_id"`
	Status                string `form:"status"`
	httputils.PageRequest `form:",inline"`
}

type ListResponse struct {
	List  []*Item `json:"list"`
	Total int64   `json:"total"`
}

// GetRequest 运行详情（不含日志）
type GetRequest struct {
	mux.Meta `path:"/runs/:id" method:"GET"`
	ID       int64 `uri:"id" binding:"required"`
}

type GetResponse struct {
	Item
}

// StopRequest 停止运行：终止整个进程组，本次运行失败，原因写「手动停止」，不留下快照。confirm 必须为 true。
type StopRequest struct {
	mux.Meta `path:"/runs/:id/stop" method:"POST"`
	ID       int64 `uri:"id" binding:"required"`
	Confirm  bool  `json:"confirm"`
}

type StopResponse struct{}

// LogRequest 完整日志；warnings_only 为 true 时只返回 warn 与 error
type LogRequest struct {
	mux.Meta     `path:"/runs/:id/log" method:"GET"`
	ID           int64 `uri:"id" binding:"required"`
	WarningsOnly bool  `form:"warnings_only"`
}

type LogResponse struct {
	// Running 运行是否仍在进行，进行中时日志还会增长
	Running bool       `json:"running"`
	Lines   []*LogLine `json:"lines"`
}

// StreamLogRequest 实时日志（text/event-stream）。先补发已有的行，再推送新行：
// 每行一个 `event: line`，data 是 LogLine 的 JSON；运行结束时发送 `event: end`，data 为 {"status":"…","reason":"…"}，随后关闭。
// 已结束的运行直接发送全部日志和 end。
type StreamLogRequest struct {
	mux.Meta     `path:"/runs/:id/log/stream" method:"GET"`
	ID           int64 `uri:"id" binding:"required"`
	WarningsOnly bool  `form:"warnings_only"`
}

// StreamLogResponse 文档占位：实际响应是 text/event-stream，事件格式见 StreamLogRequest；运行不存在等错误按普通 JSON 返回
type StreamLogResponse struct{}

// StreamEnd SSE 结束事件的内容
type StreamEnd struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}
