// Package run_entity 运行记录：一次备份运行的触发方式、各步骤状态、数据量、快照与日志
package run_entity

import (
	"context"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/opskat/opsnap/internal/pkg/code"
)

// 运行状态
const (
	StatusRunning = "running" // 运行中（含等待空闲名额）
	StatusSuccess = "success" // 成功
	StatusWarning = "warning" // 有警告：快照已生成，但有需要留意的问题（如同步期间文件消失）
	StatusFailed  = "failed"  // 失败：本次运行没有留下快照
	StatusSkipped = "skipped" // 跳过：按计划触发时上一次还在运行（由调度器记录）
)

// Trigger 触发方式：schedule、manual，或 api_key:<密钥名称>（<密钥前缀>…）
type Trigger string

const (
	TriggerSchedule Trigger = "schedule"
	TriggerManual   Trigger = "manual"
	// TriggerAPIKeyPrefix 由 API 密钥触发时的前缀，后接密钥名称和密钥前缀
	TriggerAPIKeyPrefix = "api_key:"
)

// TriggerAPIKey 由某把 API 密钥触发。名称可能重名，带上列表里显示的密钥前缀以确定是哪把密钥。
func TriggerAPIKey(name, keyPrefix string) Trigger {
	if keyPrefix == "" {
		return Trigger(TriggerAPIKeyPrefix + name)
	}
	return Trigger(TriggerAPIKeyPrefix + name + "（" + keyPrefix + "…）")
}

// 步骤
const (
	StepPreflight = "preflight" // 预检
	StepExport    = "export"    // 导出并写入快照
	StepForget    = "forget"    // 清理旧快照
	StepNotify    = "notify"    // 记录并通知
)

// 步骤状态
const (
	StepPending = "pending"
	StepRunning = "running"
	StepSuccess = "success"
	StepWarning = "warning"
	StepFailed  = "failed"
	StepSkipped = "skipped"
)

// 日志级别；「只看警告」显示 warn 与 error
const (
	LevelInfo  = "info"
	LevelWarn  = "warn"
	LevelError = "error"
)

// Step 一个步骤的状态，时间是 Unix 毫秒
type Step struct {
	Key        string `json:"key"`
	Status     string `json:"status"`
	Message    string `json:"message"`
	StartedAt  int64  `json:"started_at"`
	FinishedAt int64  `json:"finished_at"`
}

// LogLine 一行日志，写入前已遮盖凭据。Time 是 Unix 毫秒。
type LogLine struct {
	Time  int64  `json:"t"`
	Level string `json:"level"`
	Text  string `json:"text"`
}

// IsWarning 是否属于「只看警告」的范围
func (l LogLine) IsWarning() bool { return l.Level == LevelWarn || l.Level == LevelError }

// Run 一次运行。StartedAt/FinishedAt 是 Unix 秒。
type Run struct {
	ID             int64     `gorm:"column:id;primaryKey;autoIncrement"`
	JobID          int64     `gorm:"column:job_id;not null"`
	JobName        string    `gorm:"column:job_name;not null"`
	Trigger        string    `gorm:"column:triggered_by;not null"`
	Status         string    `gorm:"column:status;not null"`
	Reason         string    `gorm:"column:reason;not null"`
	NotifyFailed   bool      `gorm:"column:notify_failed;not null"`
	StartedAt      int64     `gorm:"column:started_at;not null"`
	FinishedAt     int64     `gorm:"column:finished_at;not null"`
	DurationMs     int64     `gorm:"column:duration_ms;not null"`
	BytesProcessed int64     `gorm:"column:bytes_processed;not null"`
	BytesAdded     int64     `gorm:"column:bytes_added;not null"`
	SnapshotIDs    []string  `gorm:"column:snapshot_ids;serializer:json;not null"`
	Steps          []Step    `gorm:"column:steps;serializer:json;not null"`
	Log            []LogLine `gorm:"column:log;serializer:json;not null"`
	Createtime     int64     `gorm:"column:createtime"`
}

func (Run) TableName() string { return "runs" }

// New 一次刚触发的运行：状态运行中，四个步骤都待执行
func New(jobID int64, jobName string, trig Trigger, now time.Time) *Run {
	return &Run{
		JobID: jobID, JobName: jobName, Trigger: string(trig), Status: StatusRunning,
		StartedAt: now.Unix(), SnapshotIDs: []string{}, Log: []LogLine{},
		Steps: []Step{
			{Key: StepPreflight, Status: StepPending},
			{Key: StepExport, Status: StepPending},
			{Key: StepForget, Status: StepPending},
			{Key: StepNotify, Status: StepPending},
		},
		Createtime: now.Unix(),
	}
}

// Check 运行记录是否存在
func (r *Run) Check(ctx context.Context) error {
	if r == nil {
		return i18n.NewError(ctx, code.RunNotFound)
	}
	return nil
}

// Running 是否仍在运行
func (r *Run) Running() bool { return r.Status == StatusRunning }

// Finished 是否已有结果（成功、有警告或失败；跳过不算）
func (r *Run) Finished() bool {
	return r.Status == StatusSuccess || r.Status == StatusWarning || r.Status == StatusFailed
}

// Succeeded 是否生成了快照（成功或有警告）
func (r *Run) Succeeded() bool { return r.Status == StatusSuccess || r.Status == StatusWarning }

// SetStep 更新一个步骤；进入运行中时记开始时间，其余状态记结束时间
func (r *Run) SetStep(key, status, message string, now time.Time) {
	for i := range r.Steps {
		s := &r.Steps[i]
		if s.Key != key {
			continue
		}
		s.Status = status
		if message != "" {
			s.Message = message
		}
		if status == StepRunning {
			s.StartedAt = now.UnixMilli()
		} else if status != StepPending {
			if s.StartedAt == 0 {
				s.StartedAt = now.UnixMilli()
			}
			s.FinishedAt = now.UnixMilli()
		}
		return
	}
}

// StepStatus 某个步骤的状态
func (r *Run) StepStatus(key string) string {
	for _, s := range r.Steps {
		if s.Key == key {
			return s.Status
		}
	}
	return ""
}

// Finish 记下结果、结束时间与耗时；还没开始的步骤记为跳过
func (r *Run) Finish(status, reason string, now time.Time, took time.Duration) {
	r.Status = status
	r.Reason = reason
	r.FinishedAt = now.Unix()
	r.DurationMs = max(took.Milliseconds(), 0)
	for i := range r.Steps {
		if r.Steps[i].Status == StepPending {
			r.Steps[i].Status = StepSkipped
		}
	}
}

// Interrupt 进程退出时仍在运行：进行中的步骤记为失败，运行失败；通知步骤留给随后的补发通知
func (r *Run) Interrupt(reason string, now time.Time) {
	for i := range r.Steps {
		if r.Steps[i].Status == StepRunning && r.Steps[i].Key != StepNotify {
			r.SetStep(r.Steps[i].Key, StepFailed, reason, now)
		}
	}
	took := now.Sub(time.Unix(r.StartedAt, 0))
	notify := r.StepStatus(StepNotify)
	r.Finish(StatusFailed, reason, now, took)
	// Finish 把待执行的步骤记为跳过；通知步骤随后补发，保持进行中
	if notify == StepPending || notify == StepRunning {
		r.SetStep(StepNotify, StepRunning, "", now)
	}
}

// TriggerLabel 触发方式的展示文字
func TriggerLabel(t string) string {
	switch {
	case t == string(TriggerSchedule):
		return "计划"
	case t == string(TriggerManual):
		return "手动"
	case strings.HasPrefix(t, TriggerAPIKeyPrefix):
		return "API 密钥 " + strings.TrimPrefix(t, TriggerAPIKeyPrefix)
	}
	return t
}
