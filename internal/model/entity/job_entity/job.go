// Package job_entity 备份任务：配置、字段校验与计划
package job_entity

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/retention"
	"github.com/opskat/opsnap/internal/pkg/schedule"
)

// 数据源类型
const (
	SourceMySQL    = "mysql"
	SourcePostgres = "postgres"
	SourceRedis    = "redis"
	SourceMongoDB  = "mongodb"
	SourceFiles    = "files"
)

// 连接方式
const (
	ConnSSH    = "ssh"    // 经 SSH 服务器，数据库地址是从该服务器上看的
	ConnDirect = "direct" // opsnap 直连
)

// 压缩
const (
	CompressionAuto = "auto"
	CompressionMax  = "max"
	CompressionOff  = "off"
)

const (
	// DefaultTimeoutSec 默认超时：2 小时
	DefaultTimeoutSec = 2 * 60 * 60
	// MinTimeoutSec 超时下限
	MinTimeoutSec = 60
	// MaxTimeoutSec 超时上限：7 天
	MaxTimeoutSec = 7 * 24 * 60 * 60
	// DefaultMongoAuthDB MongoDB 认证库的默认值
	DefaultMongoAuthDB = "admin"

	maxNameLen  = 64
	maxListLen  = 200
	maxItemLen  = 1024
	maxExtraLen = 32
)

// Job 备份任务。DBPassword 是主密钥加密后的密文。
type Job struct {
	ID         int64  `gorm:"column:id;primaryKey;autoIncrement"`
	Name       string `gorm:"column:name;not null"`
	SourceType string `gorm:"column:source_type;not null"`

	// ServerID 经 SSH 连接数据库或备份文件时使用的服务器
	ServerID     int64    `gorm:"column:server_id;not null"`
	ConnMode     string   `gorm:"column:conn_mode;not null"`
	DBHost       string   `gorm:"column:db_host;not null"`
	DBPort       int      `gorm:"column:db_port;not null"`
	DBUser       string   `gorm:"column:db_user;not null"`
	DBPassword   string   `gorm:"column:db_password;not null"`
	Databases    []string `gorm:"column:databases;serializer:json;not null"`
	AllDatabases bool     `gorm:"column:all_databases;not null"`
	AuthDB       string   `gorm:"column:auth_db;not null"`

	Paths    []string `gorm:"column:paths;serializer:json;not null"`
	Excludes []string `gorm:"column:excludes;serializer:json;not null"`

	RepoID int64 `gorm:"column:repo_id;not null"`

	ScheduleKind    string `gorm:"column:schedule_kind;not null"`
	ScheduleTime    string `gorm:"column:schedule_time;not null"`
	ScheduleWeekday int    `gorm:"column:schedule_weekday;not null"`
	ScheduleCron    string `gorm:"column:schedule_cron;not null"`

	RetentionPreset string `gorm:"column:retention_preset;not null"`
	RetentionDays   int    `gorm:"column:retention_days;not null"`
	RetentionWeeks  int    `gorm:"column:retention_weeks;not null"`
	RetentionMonths int    `gorm:"column:retention_months;not null"`

	TimeoutSec    int      `gorm:"column:timeout_sec;not null"`
	BandwidthKbps int      `gorm:"column:bandwidth_kbps;not null"`
	Compression   string   `gorm:"column:compression;not null"`
	ExtraArgs     []string `gorm:"column:extra_args;serializer:json;not null"`

	Paused     bool  `gorm:"column:paused;not null"`
	Createtime int64 `gorm:"column:createtime"`
	Updatetime int64 `gorm:"column:updatetime"`
}

func (Job) TableName() string { return "jobs" }

// Check 任务是否存在
func (j *Job) Check(ctx context.Context) error {
	if j == nil {
		return i18n.NewError(ctx, code.JobNotFound)
	}
	return nil
}

// IsDatabase 数据源是否是数据库
func IsDatabase(t string) bool {
	switch t {
	case SourceMySQL, SourcePostgres, SourceRedis, SourceMongoDB:
		return true
	}
	return false
}

// HasDatabaseList 数据源是否有可勾选的库列表（Redis 整个实例导出）
func HasDatabaseList(t string) bool {
	return IsDatabase(t) && t != SourceRedis
}

// DefaultPort 数据库的默认端口
func DefaultPort(t string) int {
	switch t {
	case SourceMySQL:
		return 3306
	case SourcePostgres:
		return 5432
	case SourceRedis:
		return 6379
	case SourceMongoDB:
		return 27017
	}
	return 0
}

// IsSystemDatabase 是否是系统库，系统库不可选
func IsSystemDatabase(sourceType, name string) bool {
	switch sourceType {
	case SourceMySQL:
		return slices.Contains([]string{"information_schema", "performance_schema", "mysql", "sys"}, name)
	case SourcePostgres:
		return slices.Contains([]string{"template0", "template1"}, name)
	case SourceMongoDB:
		return slices.Contains([]string{"admin", "local", "config"}, name)
	}
	return false
}

// Schedule 任务的计划
func (j *Job) Schedule() schedule.Spec {
	return schedule.Spec{Kind: j.ScheduleKind, Time: j.ScheduleTime, Weekday: j.ScheduleWeekday, Cron: j.ScheduleCron}
}

// Retention 任务的保留规则
func (j *Job) Retention() retention.Policy {
	return retention.Policy{
		Preset: retention.Preset(j.RetentionPreset),
		Days:   j.RetentionDays, Weeks: j.RetentionWeeks, Months: j.RetentionMonths,
	}
}

// NextRun 下次运行时间；已暂停或计划不合法时 ok 为 false，调度器据此跳过
func (j *Job) NextRun(after time.Time, loc *time.Location) (next time.Time, ok bool) {
	if j.Paused {
		return time.Time{}, false
	}
	next, err := schedule.Next(j.Schedule(), after, loc)
	if err != nil {
		return time.Time{}, false
	}
	return next, true
}

// DBAddr 数据库地址 host:port
func (j *Job) DBAddr() string { return net.JoinHostPort(j.DBHost, strconv.Itoa(j.DBPort)) }

// FieldError 一个字段的校验错误。Field 是请求里的 JSON 字段路径，例如 name、schedule.time。
type FieldError struct {
	Field   string
	Message string
}

// FieldErrors 一次校验的全部字段错误
type FieldErrors []FieldError

// Err 转成对外错误：错误码 JobFieldInvalid，消息每行一个字段，写法 "[字段名] 原因"；没有错误返回 nil
func (e FieldErrors) Err(ctx context.Context) error {
	if len(e) == 0 {
		return nil
	}
	lines := make([]string, 0, len(e))
	for _, f := range e {
		lines = append(lines, "["+f.Field+"] "+f.Message)
	}
	return i18n.NewError(ctx, code.JobFieldInvalid, strings.Join(lines, "\n"))
}

// Has 是否包含某个字段的错误
func (e FieldErrors) Has(field string) bool {
	return slices.ContainsFunc(e, func(f FieldError) bool { return f.Field == field })
}

// ValidateConfig 校验不依赖其他资源的字段：名称、数据源与连接、库与路径、计划、保留规则、高级选项。
// 服务器和存储位置是否存在及是否挂载由 service 检查。
func (j *Job) ValidateConfig() FieldErrors {
	var errs FieldErrors
	add := func(field, msg string) { errs = append(errs, FieldError{field, msg}) }

	if j.Name == "" || len([]rune(j.Name)) > maxNameLen {
		add("name", "名称不能为空，且不超过 64 个字")
	}
	switch {
	case IsDatabase(j.SourceType):
		j.validateDatabase(add)
	case j.SourceType == SourceFiles:
		j.validateFiles(add)
	default:
		add("source_type", "数据源须是 mysql、postgres、redis、mongodb 或 files")
	}

	if j.RepoID <= 0 {
		add("repo_id", "请选择存到哪个存储位置")
	}
	errs = append(errs, j.ValidateSchedule()...)
	if err := j.Retention().Validate(); err != nil {
		add("retention", err.Error())
	}
	j.validateAdvanced(add)
	return errs
}

// ValidateSchedule 校验计划，错误归到对应的输入框：schedule.kind / time / weekday / cron
func (j *Job) ValidateSchedule() FieldErrors {
	err := j.Schedule().Validate()
	if err == nil {
		return nil
	}
	field := "schedule.kind"
	switch j.ScheduleKind {
	case schedule.Daily, schedule.Weekly:
		field = "schedule.weekday"
		if (schedule.Spec{Kind: schedule.Daily, Time: j.ScheduleTime}).Validate() != nil {
			field = "schedule.time"
		}
	case schedule.Cron:
		field = "schedule.cron"
	}
	return FieldErrors{{Field: field, Message: err.Error()}}
}

func (j *Job) validateDatabase(add func(field, msg string)) {
	switch j.ConnMode {
	case ConnSSH:
		if j.ServerID <= 0 {
			add("server_id", "请选择一台服务器")
		}
	case ConnDirect:
	default:
		add("conn_mode", "连接方式须是 ssh 或 direct")
	}
	if !validHost(j.DBHost) || strings.HasPrefix(j.DBHost, "-") {
		add("db_host", "数据库地址不能为空，不能以 - 开头，也不能含空格或斜杠")
	}
	if j.DBPort < 1 || j.DBPort > 65535 {
		add("db_port", "端口须在 1 到 65535 之间")
	}
	if j.SourceType != SourceRedis && j.DBUser == "" {
		add("db_user", "数据库用户不能为空")
	}
	if strings.ContainsAny(j.DBUser, "\r\n") {
		add("db_user", "数据库用户不能含换行")
	}
	if HasDatabaseList(j.SourceType) {
		if j.AllDatabases {
			if len(j.Databases) > 0 {
				add("databases", "选了「全部，含以后新建」就不要再勾选具体的库")
			}
		} else {
			if len(j.Databases) == 0 {
				add("databases", "请至少勾选一个库，或打开「全部，含以后新建」")
			}
			seen := map[string]bool{}
			for _, d := range j.Databases {
				switch {
				case strings.TrimSpace(d) == "" || strings.ContainsAny(d, "\r\n") || len(d) > maxItemLen:
					add("databases", "库名不能为空，也不能含换行")
				case strings.HasPrefix(d, "-"):
					// 导出工具会把它当成选项（见 cmdbuild.Dump）
					add("databases", fmt.Sprintf("库名 %s 不能以 - 开头", d))
				case j.SourceType == SourcePostgres && (strings.Contains(d, "=") ||
					strings.HasPrefix(d, "postgres://") || strings.HasPrefix(d, "postgresql://")):
					// pg_dump 会把它当成连接串
					add("databases", fmt.Sprintf("库名 %s 不能是连接串", d))
				case IsSystemDatabase(j.SourceType, d):
					add("databases", fmt.Sprintf("%s 是系统库，不可选", d))
				case seen[d]:
					add("databases", fmt.Sprintf("库 %s 重复", d))
				}
				seen[d] = true
			}
		}
	}
	if j.SourceType == SourceMongoDB && (j.AuthDB == "" || strings.ContainsAny(j.AuthDB, " \r\n/")) {
		add("auth_db", "认证库不能为空，也不能含空格或斜杠")
	}
}

func (j *Job) validateFiles(add func(field, msg string)) {
	if j.ServerID <= 0 {
		add("server_id", "请选择一台服务器")
	}
	if len(j.Paths) == 0 {
		add("paths", "请至少填写一个要备份的路径")
	}
	if len(j.Paths) > maxListLen {
		add("paths", fmt.Sprintf("路径最多 %d 条", maxListLen))
	}
	for _, p := range j.Paths {
		switch {
		case !strings.HasPrefix(p, "/"):
			add("paths", fmt.Sprintf("%q 须是以 / 开头的绝对路径", p))
		case strings.ContainsAny(p, "\r\n\x00") || len(p) > maxItemLen:
			add("paths", fmt.Sprintf("%q 含非法字符或太长", p))
		case slices.Contains(strings.Split(p, "/"), ".."):
			add("paths", fmt.Sprintf("%q 不能含 ..", p))
		}
	}
	if len(j.Excludes) > maxListLen {
		add("excludes", fmt.Sprintf("排除规则最多 %d 条", maxListLen))
	}
	for _, e := range j.Excludes {
		if strings.TrimSpace(e) == "" || strings.ContainsAny(e, "\r\n\x00") || len(e) > maxItemLen {
			add("excludes", "排除规则不能为空，也不能含换行")
			break
		}
	}
}

func (j *Job) validateAdvanced(add func(field, msg string)) {
	if j.TimeoutSec < MinTimeoutSec || j.TimeoutSec > MaxTimeoutSec {
		add("advanced.timeout_sec", "超时须在 1 分钟到 7 天之间")
	}
	if j.BandwidthKbps < 0 {
		add("advanced.bandwidth_kbps", "限速不能为负数，0 表示不限速")
	}
	switch j.Compression {
	case CompressionAuto, CompressionMax, CompressionOff:
	default:
		add("advanced.compression", "压缩须是 auto、max 或 off")
	}
	if len(j.ExtraArgs) > maxExtraLen {
		add("advanced.extra_args", fmt.Sprintf("附加参数最多 %d 个", maxExtraLen))
	}
	for _, a := range j.ExtraArgs {
		if strings.TrimSpace(a) == "" || strings.ContainsAny(a, "\r\n\x00") || len(a) > maxItemLen {
			add("advanced.extra_args", "附加参数不能为空，也不能含换行")
			break
		}
	}
}

func validHost(h string) bool {
	return h != "" && !strings.ContainsAny(h, " \t\r\n/")
}
