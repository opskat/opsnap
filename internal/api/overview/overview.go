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
	// 存储占用与各存储目标的用量由存储侧的任务在这里补充
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
