// Package overview 概览页接口：状态句所需的计数、总量、需要处理的任务、运行时间线和存储位置卡片。
package overview

import (
	"github.com/cago-frame/cago/server/mux"

	"github.com/opskat/opsnap/internal/api/job"
)

// 时间线窗口
const (
	Window7d  = "7d"
	Window30d = "30d"
)

// 需要处理的原因
const (
	KindFailed  = "failed"  // 最近一次有结果的运行失败
	KindWarning = "warning" // 最近一次有结果的运行有警告
	KindStale   = "stale"   // 太久没有成功
)

// GetRequest 概览。window 决定时间线向过去显示多久：7d 同时显示之后 30 小时的计划，30d 显示之后 72 小时的计划。
type GetRequest struct {
	mux.Meta `path:"/overview" method:"GET"`
	// Window 7d | 30d，缺省 7d
	Window string `form:"window"`
}

// Summary 状态句用的计数。运行中的任务算运行正常；Paused 是已暂停且不需要处理的任务，不计入 OK。
type Summary struct {
	Jobs      int `json:"jobs"`
	Attention int `json:"attention"`
	Paused    int `json:"paused"`
	// OK 既不需要处理也没有暂停的任务数
	OK      int `json:"ok"`
	Servers int `json:"servers"`
	Repos   int `json:"repos"`
}

// Totals 总量：受保护的数据（原始）、NAS 上实际占用、快照总数，取自各存储位置最近一次统计
type Totals struct {
	RawBytes    int64 `json:"raw_bytes"`
	StoredBytes int64 `json:"stored_bytes"`
	Snapshots   int   `json:"snapshots"`
}

// AttentionItem 一个需要处理的任务；同一任务只出现一次，失败与太久没有成功同时成立时按失败列出并带上上次成功时间。
// 时间是 Unix 秒，0 表示没有。
type AttentionItem struct {
	JobID      int64  `json:"job_id"`
	JobName    string `json:"job_name"`
	SourceType string `json:"source_type"`
	// Kind failed | warning | stale
	Kind string `json:"kind"`
	// Reason 失败原因或警告说明；太久没有成功时是固定说明
	Reason string `json:"reason"`
	// Since 从何时开始：连续失败（或连续警告）的第一次运行的开始时间，太久没有成功则是越过阈值的时刻
	Since         int64 `json:"since"`
	LastSuccessAt int64 `json:"last_success_at"`
	// RunID 最近一次有结果的运行，查看日志用；没有运行过为 0
	RunID  int64 `json:"run_id"`
	RepoID int64 `json:"repo_id"`
	// RepoNotMounted 任务的存储位置没有挂载，界面给出「检查存储位置」
	RepoNotMounted bool `json:"repo_not_mounted"`
}

// TimelineRun 过去的一次运行
type TimelineRun struct {
	ID        int64 `json:"id"`
	StartedAt int64 `json:"started_at"`
	// Status running | success | warning | failed
	Status string `json:"status"`
}

// TimelineJob 时间线上的一条轨道
type TimelineJob struct {
	JobID      int64  `json:"job_id"`
	Name       string `json:"name"`
	SourceType string `json:"source_type"`
	Paused     bool   `json:"paused"`
	// Schedule 计划，轨道标题下据此写「每天 03:00」
	Schedule job.ScheduleInput `json:"schedule"`
	// LastStatus 最近一次有结果的运行；没有运行过为空
	LastStatus string `json:"last_status"`
	// NextRunAt 下次运行，Unix 秒；已暂停为 0
	NextRunAt int64 `json:"next_run_at"`
	// Runs 窗口内已经发生的运行（不含跳过），按开始时间升序
	Runs []*TimelineRun `json:"runs"`
	// Planned 窗口之后的计划运行时间，升序
	Planned []int64 `json:"planned"`
}

// Timeline 运行时间线。From 是窗口第一天 00:00（设置的时区），To 是 Now 加上向后显示的小时数，都是 Unix 秒。
type Timeline struct {
	Window     string         `json:"window"`
	From       int64          `json:"from"`
	To         int64          `json:"to"`
	AheadHours int            `json:"ahead_hours"`
	Jobs       []*TimelineJob `json:"jobs"`
}

// RepoCard 存储位置卡片
type RepoCard struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
	// Status unknown | ok | not_mounted | error
	Status      string `json:"status"`
	StoredBytes int64  `json:"stored_bytes"`
	RawBytes    int64  `json:"raw_bytes"`
	// Ratio 去重压缩比 = 原始数据量 / 实际占用，没有数据时为 0
	Ratio     float64 `json:"ratio"`
	Snapshots int     `json:"snapshots"`
	Jobs      int     `json:"jobs"`
	// FreeSpace 所在卷剩余字节，未知为 -1
	FreeSpace int64 `json:"free_space"`
}

type GetResponse struct {
	// Now 服务器当前时间，Unix 秒；时间线的「现在」线以它为准
	Now       int64            `json:"now"`
	Summary   Summary          `json:"summary"`
	Totals    Totals           `json:"totals"`
	Attention []*AttentionItem `json:"attention"`
	Timeline  *Timeline        `json:"timeline"`
	Repos     []*RepoCard      `json:"repos"`
}
