// Package job_entity 定义备份任务实体及其字段规则（docs/specs/2026-09-27-backup-jobs.md「任务与新建向导」）。
package job_entity

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/opskat/opsnap/internal/model/entity/datasource_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/kopiarepo"
	"github.com/opskat/opsnap/internal/pkg/schedule"
)

// 任务类型；同步为后续版本
const TypeBackup = "backup"

// 备份范围
const (
	ScopeInstance  = "instance"  // 整个实例：每次运行时重新列出库
	ScopeDatabases = "databases" // 指定数据库
)

// MethodFull 仅全量；增量为后续版本
const MethodFull = "full"

// 取值范围
const (
	MaxNameLength    = 64
	MaxPrefixLength  = 128
	MinRetainDays    = 1
	MaxRetainDays    = 365
	MaxRetainWeeks   = 520
	MaxRetainMonths  = 120
	MaxRetries       = 5
	MinRetryInterval = 1       // 分钟
	MaxRetryInterval = 120     // 分钟
	MinTimeout       = 10      // 分钟
	MaxTimeout       = 48 * 60 // 分钟
)

var compressions = []string{string(kopiarepo.CompressionNone), string(kopiarepo.CompressionGzip), string(kopiarepo.CompressionZstd)}

type Job struct {
	ID   int64  `gorm:"column:id;primaryKey"`
	Name string `gorm:"column:name"`
	Type string `gorm:"column:type"`
	// DataSourceID、StorageID、Prefix 决定快照归属，创建后不能修改
	DataSourceID int64  `gorm:"column:datasource_id"`
	StorageID    int64  `gorm:"column:storage_id"`
	Prefix       string `gorm:"column:prefix"`
	Scope        string `gorm:"column:scope"`
	// DatabaseNames 指定数据库时的库名（JSON 数组）
	DatabaseNames string `gorm:"column:database_names"`
	Method        string `gorm:"column:method"`
	// 一并备份：MySQL 看 Routines/Triggers/Events/Users，PostgreSQL 看 Globals，另一类保存为 false
	OptRoutines bool `gorm:"column:opt_routines"`
	OptTriggers bool `gorm:"column:opt_triggers"`
	OptEvents   bool `gorm:"column:opt_events"`
	OptUsers    bool `gorm:"column:opt_users"`
	OptGlobals  bool `gorm:"column:opt_globals"`
	// ExcludeTables 排除表（JSON 数组）：MySQL 为 库.表，PostgreSQL 为 库.模式.表
	ExcludeTables string `gorm:"column:exclude_tables"`
	Compression   string `gorm:"column:compression"`
	ScheduleKind  string `gorm:"column:schedule_kind"`
	// ScheduleMinute、ScheduleHour、ScheduleWeekdays（JSON 数组，0 为周日）、ScheduleCron 按频率使用，其余保存为零值
	ScheduleMinute   int    `gorm:"column:schedule_minute"`
	ScheduleHour     int    `gorm:"column:schedule_hour"`
	ScheduleWeekdays string `gorm:"column:schedule_weekdays"`
	ScheduleCron     string `gorm:"column:schedule_cron"`
	Timezone         string `gorm:"column:timezone"`
	RetainDays       int    `gorm:"column:retain_days"`
	RetainWeeks      int    `gorm:"column:retain_weeks"`
	RetainMonths     int    `gorm:"column:retain_months"`
	Retries          int    `gorm:"column:retries"`
	// RetryInterval、Timeout 以分钟计
	RetryInterval int  `gorm:"column:retry_interval"`
	Timeout       int  `gorm:"column:timeout"`
	Enabled       bool `gorm:"column:enabled"`
	// EnabledAt 最近一次启用（或创建）的时间；此前（含暂停期间）的计划不算错过
	EnabledAt int64 `gorm:"column:enabled_at"`
	// RunNow 创建时选择了“立即执行一次”，由运行模块处理
	RunNow bool `gorm:"column:run_now"`
	// SnapshotCount 最近一次读取仓库时本任务的快照数，由运行模块维护（见 job_repo.SetSnapshotCount）
	SnapshotCount int   `gorm:"column:snapshot_count"`
	Createtime    int64 `gorm:"column:createtime"`
	Updatetime    int64 `gorm:"column:updatetime"`
}

func (Job) TableName() string { return "jobs" }

func encodeList[T any](v []T) string {
	if v == nil {
		v = []T{}
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func decodeList[T any](s string) []T {
	out := []T{}
	_ = json.Unmarshal([]byte(s), &out)
	if out == nil {
		out = []T{}
	}
	return out
}

// Databases 指定的数据库，整个实例时为空
func (j *Job) Databases() []string { return decodeList[string](j.DatabaseNames) }

func (j *Job) SetDatabases(v []string) { j.DatabaseNames = encodeList(v) }

// ExcludeTableList 排除表
func (j *Job) ExcludeTableList() []string { return decodeList[string](j.ExcludeTables) }

func (j *Job) SetExcludeTables(v []string) { j.ExcludeTables = encodeList(v) }

// Weekdays 每周执行的星期几（0 为周日），频率不是每周时为空
func (j *Job) Weekdays() []int { return decodeList[int](j.ScheduleWeekdays) }

func (j *Job) SetWeekdays(v []int) { j.ScheduleWeekdays = encodeList(v) }

// Schedule 调度计划
func (j *Job) Schedule() schedule.Spec {
	s := schedule.Spec{Kind: schedule.Kind(j.ScheduleKind), Minute: j.ScheduleMinute, Hour: j.ScheduleHour,
		Cron: j.ScheduleCron, Timezone: j.Timezone}
	for _, d := range j.Weekdays() {
		s.Weekdays = append(s.Weekdays, time.Weekday(d))
	}
	return s
}

// Ref 本任务在仓库中的快照（来源为本任务的前缀且带本任务标签）
func (j *Job) Ref() kopiarepo.JobRef {
	return kopiarepo.JobRef{JobID: j.ID, Prefix: j.Prefix}
}

// Location 仓库内位置：<存储名>:/<路径前缀>
func (j *Job) Location(storageName string) string {
	return storageName + ":/" + j.Prefix
}

// NextRun 启用时 after 之后的下一次计划执行时间；暂停或计划无效时返回 false
func (j *Job) NextRun(after time.Time) (time.Time, bool) {
	if !j.Enabled {
		return time.Time{}, false
	}
	next, err := j.Schedule().Next(after, 1)
	if err != nil || len(next) == 0 {
		return time.Time{}, false
	}
	return next[0], true
}

// Overlaps 两个任务在同一存储中的路径前缀相同，或一个是另一个的上级
func (j *Job) Overlaps(o *Job) bool {
	if j.StorageID != o.StorageID {
		return false
	}
	a, b := j.Prefix, o.Prefix
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

// ValidPrefix 路径前缀规则：只含字母、数字和 ._-/，1–128 个字符，不以 / 开头或结尾，不含 .. 或连续的 /
func ValidPrefix(p string) bool {
	if p == "" || len(p) > MaxPrefixLength || p[0] == '/' || p[len(p)-1] == '/' ||
		strings.Contains(p, "..") || strings.Contains(p, "//") {
		return false
	}
	for _, r := range p {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._-/", r)
		if !ok {
			return false
		}
	}
	return true
}

// cleanList 去掉首尾空白、空项与重复项，保持原顺序
func cleanList(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// validExclude 排除表每段非空：MySQL 为 库.表（2 段），PostgreSQL 为 库.模式.表（3 段）
func validExclude(kind, s string) bool {
	parts := strings.Split(s, ".")
	want := 2
	if kind == datasource_entity.KindPostgreSQL {
		want = 3
	}
	if len(parts) != want {
		return false
	}
	for _, p := range parts {
		if strings.TrimSpace(p) == "" {
			return false
		}
	}
	return true
}

// CheckSchedule 校验并规范化计划：时区须为 IANA 时区，频率各字段在范围内，且按计划能够触发
func CheckSchedule(ctx context.Context, s schedule.Spec) (schedule.Spec, error) {
	if s.Timezone == "" || s.Timezone == "Local" {
		return s, i18n.NewError(ctx, code.JobTimezoneInvalid)
	}
	if _, err := time.LoadLocation(s.Timezone); err != nil {
		return s, i18n.NewError(ctx, code.JobTimezoneInvalid)
	}
	parsed, err := schedule.Parse(s)
	if err != nil {
		return s, i18n.NewError(ctx, code.JobScheduleInvalid, err.Error())
	}
	// 只保留该频率用到的字段
	switch parsed.Kind {
	case schedule.KindHourly:
		parsed.Hour, parsed.Weekdays, parsed.Cron = 0, nil, ""
	case schedule.KindDaily:
		parsed.Weekdays, parsed.Cron = nil, ""
	case schedule.KindWeekly:
		parsed.Cron = ""
	case schedule.KindCron:
		parsed.Minute, parsed.Hour, parsed.Weekdays = 0, 0, nil
		parsed.Cron = strings.Join(strings.Fields(parsed.Cron), " ")
	}
	if _, err := parsed.Next(time.Now(), 1); err != nil {
		if errors.Is(err, schedule.ErrNeverFires) {
			return s, i18n.NewError(ctx, code.JobScheduleInvalid, err.Error())
		}
		return s, err
	}
	return parsed, nil
}

// CheckRetention 校验保留策略的取值范围
func CheckRetention(ctx context.Context, days, weeks, months int) error {
	switch {
	case days < MinRetainDays || days > MaxRetainDays:
		return i18n.NewError(ctx, code.JobRetentionDaysInvalid)
	case weeks < 0 || weeks > MaxRetainWeeks:
		return i18n.NewError(ctx, code.JobRetentionWeeksInvalid)
	case months < 0 || months > MaxRetainMonths:
		return i18n.NewError(ctx, code.JobRetentionMonthsInvalid)
	}
	return nil
}

// Check 按向导规则校验并规范化任务字段（dsKind 为所选数据源的类型）：去掉名称首尾空白，
// 库名与排除表去掉空行和重复，清空另一类数据源与未选频率用到的字段。
// 不检查跨任务的规则（重名、前缀冲突）与数据源、存储是否存在
func (j *Job) Check(ctx context.Context, dsKind string) error {
	if j.Type != TypeBackup {
		return i18n.NewError(ctx, code.JobTypeUnsupported)
	}
	j.Name = strings.TrimSpace(j.Name)
	if j.Name == "" || utf8.RuneCountInString(j.Name) > MaxNameLength {
		return i18n.NewError(ctx, code.JobNameInvalid)
	}

	switch j.Scope {
	case ScopeInstance:
		j.SetDatabases(nil)
	case ScopeDatabases:
		dbs := cleanList(j.Databases())
		if len(dbs) == 0 {
			return i18n.NewError(ctx, code.JobDatabasesRequired)
		}
		j.SetDatabases(dbs)
	default:
		return i18n.NewError(ctx, code.JobScopeInvalid)
	}
	if j.Method != MethodFull {
		return i18n.NewError(ctx, code.JobMethodUnsupported)
	}
	if dsKind == datasource_entity.KindPostgreSQL {
		j.OptRoutines, j.OptTriggers, j.OptEvents, j.OptUsers = false, false, false, false
	} else {
		j.OptGlobals = false
	}
	excludes := cleanList(j.ExcludeTableList())
	for _, s := range excludes {
		if !validExclude(dsKind, s) {
			return i18n.NewError(ctx, code.JobExcludeInvalid, s)
		}
	}
	j.SetExcludeTables(excludes)

	if !ValidPrefix(j.Prefix) {
		return i18n.NewError(ctx, code.JobPrefixInvalid)
	}
	if !slices.Contains(compressions, j.Compression) {
		return i18n.NewError(ctx, code.JobCompressionInvalid)
	}

	spec, err := CheckSchedule(ctx, j.Schedule())
	if err != nil {
		return err
	}
	j.ScheduleKind, j.ScheduleMinute, j.ScheduleHour, j.ScheduleCron = string(spec.Kind), spec.Minute, spec.Hour, spec.Cron
	days := make([]int, 0, len(spec.Weekdays))
	for _, d := range spec.Weekdays {
		days = append(days, int(d))
	}
	j.SetWeekdays(days)

	if err := CheckRetention(ctx, j.RetainDays, j.RetainWeeks, j.RetainMonths); err != nil {
		return err
	}
	switch {
	case j.Retries < 0 || j.Retries > MaxRetries:
		return i18n.NewError(ctx, code.JobRetriesInvalid)
	case j.RetryInterval < MinRetryInterval || j.RetryInterval > MaxRetryInterval:
		return i18n.NewError(ctx, code.JobRetryIntervalInvalid)
	case j.Timeout < MinTimeout || j.Timeout > MaxTimeout:
		return i18n.NewError(ctx, code.JobTimeoutInvalid)
	}
	return nil
}
