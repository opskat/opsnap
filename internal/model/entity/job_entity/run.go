package job_entity

import "fmt"

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
	// FailedStep、Reason 仅失败（Reason 已去掉秘密）；跳过与被重启取消时 Reason 为原因
	FailedStep string `gorm:"column:failed_step"`
	Reason     string `gorm:"column:reason"`
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
	Time    int64  `json:"time"`
	Step    string `json:"step"`
	Message string `json:"message"`
	Omitted int    `json:"omitted,omitempty"`
}

// LogLines 解析保存的执行日志
func (r *Run) LogLines() []LogLine { return decodeList[LogLine](r.Log) }

// SetLog 保存执行日志
func (r *Run) SetLog(lines []LogLine) { r.Log = encodeList(lines) }

// FormatTimeout 超时时长（分钟）的中文描述，用于失败原因“超时（超过 X）”
func FormatTimeout(minutes int) string {
	h, m := minutes/60, minutes%60
	switch {
	case h == 0:
		return fmt.Sprintf("%d 分钟", m)
	case m == 0:
		return fmt.Sprintf("%d 小时", h)
	default:
		return fmt.Sprintf("%d 小时 %d 分钟", h, m)
	}
}
