package job_entity

import (
	"context"
	"strconv"
	"strings"

	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/l10n"
)

// 运行状态（docs/specs/2026-09-27-backup-jobs.md「运行记录」）
const (
	RunQueued   = "queued" // 等待中
	RunRunning  = "running"
	RunSuccess  = "success"
	RunFailed   = "failed"
	RunCanceled = "canceled"
	RunSkipped  = "skipped"
)

// 触发方式
const (
	TriggerSchedule = "schedule" // 计划
	TriggerManual   = "manual"   // 手动（立即执行、创建后立即执行一次）
	TriggerCatchUp  = "catchup"  // 补跑
	TriggerRetry    = "retry"    // 重试 RetryAttempt/RetryTotal
)

// 执行步骤（docs/specs/2026-09-27-backup-jobs.md「执行」）
const (
	StepPrepare   = "prepare"   // 准备
	StepConnect   = "connect"   // 连接数据源
	StepExport    = "export"    // 导出并写入仓库
	StepVerify    = "verify"    // 校验
	StepRetention = "retention" // 应用保留策略
)

const (
	// MaxLogLines 每次运行最多保留的日志行数（含省略标记）
	MaxLogLines = 1000
	// MaxRunsPerJob 每个任务最多保留的运行记录数
	MaxRunsPerJob = 1000
)

// Run 一次运行的记录
type Run struct {
	ID           int64  `gorm:"column:id;primaryKey"`
	JobID        int64  `gorm:"column:job_id"`
	Status       string `gorm:"column:status"`
	Trigger      string `gorm:"column:trigger_kind"`
	RetryAttempt int    `gorm:"column:retry_attempt"`
	RetryTotal   int    `gorm:"column:retry_total"`
	// ScheduledAt 计划与补跑对应的计划时间（秒），其余为 0
	ScheduledAt int64 `gorm:"column:scheduled_at"`
	// StartedAt、FinishedAt 以毫秒计；尚未开始或结束时为 0
	StartedAt     int64 `gorm:"column:started_at"`
	FinishedAt    int64 `gorm:"column:finished_at"`
	ExportedBytes int64 `gorm:"column:exported_bytes"`
	UploadedBytes int64 `gorm:"column:uploaded_bytes"`
	// SnapshotID 仅成功
	SnapshotID string `gorm:"column:snapshot_id"`
	// FailedStep、Reason 仅失败（Reason 已去掉秘密）；跳过与被重启取消时 Reason 为原因。
	// Reason 为中文，ReasonEn 为同一原因的英文（其中导出工具与数据库的原文不翻译）；ReasonEn 为空时两种语言都显示 Reason
	FailedStep string `gorm:"column:failed_step"`
	Reason     string `gorm:"column:reason"`
	ReasonEn   string `gorm:"column:reason_en"`
	// ReasonCode OpsNap 给出的固定原因（见 SetFixedReason），接口按界面语言显示；其余原因为空，Reason 原样显示
	ReasonCode string `gorm:"column:reason_code"`
	// Log 执行日志（LogLine 的 JSON 数组）
	Log        string `gorm:"column:log"`
	Createtime int64  `gorm:"column:createtime"`
	Updatetime int64  `gorm:"column:updatetime"`
}

func (Run) TableName() string { return "job_runs" }

// Active 等待中或运行中
func (r *Run) Active() bool { return r.Status == RunQueued || r.Status == RunRunning }

// LogLine 执行日志的一行；Omitted 大于 0 时是省略标记
type LogLine struct {
	// Time 毫秒
	Time int64  `json:"time"`
	Step string `json:"step"`
	// Message 中文；MessageEn 为英文，与中文相同或更早的记录中没有时为空
	Message   string `json:"message"`
	MessageEn string `json:"message_en,omitempty"`
	Omitted   int    `json:"omitted,omitempty"`
}

// Text 按 ctx 的界面语言显示这一行
func (l LogLine) Text(ctx context.Context) string { return l10n.Pick(ctx, l.Message, l.MessageEn) }

// LogLines 解析保存的执行日志
func (r *Run) LogLines() []LogLine { return decodeList[LogLine](r.Log) }

// SetLog 保存执行日志
func (r *Run) SetLog(lines []LogLine) { r.Log = encodeList(lines) }

// 固定原因（Run.ReasonCode）：跳过、重试作废、OpsNap 重启与超时，界面按语言显示
const (
	ReasonStillRunning = "still_running" // 上一次仍在运行（跳过）
	ReasonRetryVoided  = "retry_voided"  // 下一次计划时间已到，重试作废
	ReasonInterrupted  = "interrupted"   // OpsNap 重启，运行中断
	ReasonRestart      = "restart"       // OpsNap 重启时仍在排队
	reasonTimeout      = "timeout:"      // 超时（超过 X），后跟超时时长（分钟）
)

// TimeoutReason 超时的固定原因，带任务的超时时长（分钟）
func TimeoutReason(minutes int) string { return reasonTimeout + strconv.Itoa(minutes) }

// ReasonText 固定原因按 ctx 的语言给出的文案；不是固定原因时 ok 为 false
func ReasonText(ctx context.Context, reasonCode string) (text string, ok bool) {
	switch reasonCode {
	case ReasonStillRunning:
		return i18n.T(ctx, code.JobReasonStillRunning), true
	case ReasonRetryVoided:
		return i18n.T(ctx, code.JobReasonRetryVoided), true
	case ReasonInterrupted:
		return i18n.T(ctx, code.JobReasonInterrupted), true
	case ReasonRestart:
		return i18n.T(ctx, code.JobReasonRestart), true
	}
	if m, found := strings.CutPrefix(reasonCode, reasonTimeout); found {
		if minutes, err := strconv.Atoi(m); err == nil {
			return i18n.T(ctx, code.JobReasonTimeout, formatDuration(ctx, minutes)), true
		}
	}
	return "", false
}

// SetFixedReason 记录固定原因：ReasonCode 供接口按界面语言显示，Reason 同时保存中文原文（写进运行日志）
func (r *Run) SetFixedReason(reasonCode string) {
	r.ReasonCode, r.ReasonEn = reasonCode, ""
	r.Reason, _ = ReasonText(i18n.WithLanguage(context.Background(), code.LangZhCN), reasonCode)
}

// SetReason 记录其余的原因：中文与英文（已去掉秘密）
func (r *Run) SetReason(zh, en string) {
	r.ReasonCode, r.Reason, r.ReasonEn = "", zh, en
	if en == zh {
		r.ReasonEn = ""
	}
}

// DisplayReason 按 ctx 的语言给出的原因：固定原因取对应语言的文案，其余取对应语言的文字
// （更早的记录只有中文时显示中文）
func (r *Run) DisplayReason(ctx context.Context) string {
	if text, ok := ReasonText(ctx, r.ReasonCode); ok {
		return text
	}
	return l10n.Pick(ctx, r.Reason, r.ReasonEn)
}

// formatDuration 时长（分钟）的描述，如“2 小时 30 分钟”“2 h 30 min”
func formatDuration(ctx context.Context, minutes int) string {
	h, m := minutes/60, minutes%60
	switch {
	case h == 0:
		return i18n.T(ctx, code.JobDurationMinutes, m)
	case m == 0:
		return i18n.T(ctx, code.JobDurationHours, h)
	default:
		return i18n.T(ctx, code.JobDurationHoursMinutes, h, m)
	}
}
