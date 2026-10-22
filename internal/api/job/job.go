// Package job 备份任务接口。
//
// 字段校验失败时返回 HTTP 400、错误码 11401（JobFieldInvalid），msg 里每行一个字段错误，
// 写法 "[字段名] 原因"，字段名是请求体里的 JSON 路径，例如 "[name] 名称不能为空"、
// "[schedule.time] 时刻须写成 HH:MM"。面板按 /^\[([\w.]+)\] (.*)$/ 逐行拆开，把原因放到对应字段下方。
package job

import "github.com/cago-frame/cago/server/mux"

// 数据源类型
const (
	SourceMySQL    = "mysql"
	SourceMariaDB  = "mariadb"
	SourcePostgres = "postgres"
	SourceRedis    = "redis"
	SourceMongoDB  = "mongodb"
	SourceFiles    = "files"
)

// 连接方式
const (
	ConnSSH    = "ssh"
	ConnDirect = "direct"
)

// 列表筛选
const (
	FilterAll       = "all"
	FilterAttention = "attention"
	FilterPaused    = "paused"
)

// ScheduleInput 计划。kind 为 hourly | 6h | daily | weekly | cron。
type ScheduleInput struct {
	Kind string `json:"kind"`
	// Time 24 小时制 HH:MM，daily 与 weekly 使用
	Time string `json:"time"`
	// Weekday 0 表示周日，6 表示周六，weekly 使用
	Weekday int `json:"weekday"`
	// Cron 5 段 cron 表达式，cron 使用
	Cron string `json:"cron"`
}

// RetentionInput 保留规则。preset 为 7d | 30d | long | custom；custom 时三个数字生效。
type RetentionInput struct {
	Preset string `json:"preset"`
	Days   int    `json:"days"`
	Weeks  int    `json:"weeks"`
	Months int    `json:"months"`
}

// AdvancedInput 高级选项
type AdvancedInput struct {
	// TimeoutSec 超时秒数，0 表示默认 2 小时
	TimeoutSec int `json:"timeout_sec"`
	// BandwidthKbps 限速 KiB/s，0 表示不限速
	BandwidthKbps int `json:"bandwidth_kbps"`
	// Compression auto | max | off，空表示 auto
	Compression string   `json:"compression"`
	ExtraArgs   []string `json:"extra_args"`
}

// JobInput 提交的任务配置，整体保存；Create、Update、Test、Databases 请求都带这组字段。
type JobInput struct {
	Name       string `json:"name"`
	SourceType string `json:"source_type"` // mysql | mariadb | postgres | redis | mongodb | files
	// ServerID 经 SSH 的数据库任务和文件任务使用的服务器
	ServerID int64  `json:"server_id"`
	ConnMode string `json:"conn_mode"` // ssh | direct，仅数据库
	// DBHost、DBPort 经 SSH 时是「从这台服务器上看的」数据库地址，例如容器 IP
	DBHost string `json:"db_host"`
	DBPort int    `json:"db_port"`
	DBUser string `json:"db_user"`
	// DBPassword 空表示没有；占位「已设置」表示保留已保存的密码（连接地址和用户不变时）
	DBPassword   string   `json:"db_password"`
	Databases    []string `json:"databases"`
	AllDatabases bool     `json:"all_databases"` // 全部，含以后新建
	AuthDB       string   `json:"auth_db"`       // MongoDB 认证库，空表示 admin

	Paths    []string `json:"paths"`    // 文件：绝对路径
	Excludes []string `json:"excludes"` // 文件：排除规则，写法同 rsync

	RepoID    int64          `json:"repo_id"`
	Schedule  ScheduleInput  `json:"schedule"`
	Retention RetentionInput `json:"retention"`
	Advanced  AdvancedInput  `json:"advanced"`
	Paused    bool           `json:"paused"`
}

// Item 任务；数据库密码只返回占位
type Item struct {
	ID           int64    `json:"id"`
	Name         string   `json:"name"`
	SourceType   string   `json:"source_type"`
	ServerID     int64    `json:"server_id"`
	ServerName   string   `json:"server_name"`
	ConnMode     string   `json:"conn_mode"`
	DBHost       string   `json:"db_host"`
	DBPort       int      `json:"db_port"`
	DBUser       string   `json:"db_user"`
	DBPassword   string   `json:"db_password"` // 占位「已设置」或空
	Databases    []string `json:"databases"`
	AllDatabases bool     `json:"all_databases"`
	AuthDB       string   `json:"auth_db"`
	Paths        []string `json:"paths"`
	Excludes     []string `json:"excludes"`

	RepoID    int64          `json:"repo_id"`
	RepoName  string         `json:"repo_name"`
	Schedule  ScheduleInput  `json:"schedule"`
	Retention RetentionInput `json:"retention"`
	Advanced  AdvancedInput  `json:"advanced"`
	Paused    bool           `json:"paused"`
	NextRunAt int64          `json:"next_run_at"` // 下次运行时间 Unix 秒；已暂停为 0
	Attention bool           `json:"attention"`   // 需要处理：最近一次运行失败或有警告，或太久没有成功

	// ForgetPendingAt 首次因存储位置被其他备份占用而延后清理旧快照的时间（Unix 秒），0 表示没有
	ForgetPendingAt int64 `json:"forget_pending_at"`
	// ForgetPendingError 最近一次补做清理失败的原因（撞锁不算失败），成功后清空
	ForgetPendingError string `json:"forget_pending_error"`
	CreatedAt          int64  `json:"created_at"`
	UpdatedAt          int64  `json:"updated_at"`

	// LastRun 最近一次运行（不含跳过），可能早于 14 天；没有运行过为 null。只在列表里填写
	LastRun *RunBrief `json:"last_run"`
	// RecentRuns 最近 14 天的运行（不含跳过），按开始时间升序，任务列表的运行带用。只在列表里填写
	RecentRuns []*RunBrief `json:"recent_runs"`
	// Running 正在运行（含排队等待名额），任务列表据此禁用「立即运行」。只在列表里填写
	Running bool `json:"running"`
}

// RunBrief 列表里的一次运行。时间是 Unix 秒。
type RunBrief struct {
	ID         int64  `json:"id"`
	Status     string `json:"status"`
	StartedAt  int64  `json:"started_at"`
	BytesAdded int64  `json:"bytes_added"`
}

// ListRequest 列出任务。filter 为 all | attention | paused；q 按名称、服务器名称、路径做不区分大小写的包含搜索。
type ListRequest struct {
	mux.Meta `path:"/jobs" method:"GET"`
	Filter   string `form:"filter"`
	Q        string `form:"q"`
}

type ListResponse struct {
	List []*Item `json:"list"`
}

// GetRequest 读取一个任务
type GetRequest struct {
	mux.Meta `path:"/jobs/:id" method:"GET"`
	ID       int64 `uri:"id" binding:"required"`
}

type GetResponse struct {
	Item
}

// CreateRequest 新建任务。目标存储位置必须存在且未处于未挂载状态。
type CreateRequest struct {
	mux.Meta     `path:"/jobs" method:"POST"`
	Name         string         `json:"name"`
	SourceType   string         `json:"source_type"`
	ServerID     int64          `json:"server_id"`
	ConnMode     string         `json:"conn_mode"`
	DBHost       string         `json:"db_host"`
	DBPort       int            `json:"db_port"`
	DBUser       string         `json:"db_user"`
	DBPassword   string         `json:"db_password"`
	Databases    []string       `json:"databases"`
	AllDatabases bool           `json:"all_databases"`
	AuthDB       string         `json:"auth_db"`
	Paths        []string       `json:"paths"`
	Excludes     []string       `json:"excludes"`
	RepoID       int64          `json:"repo_id"`
	Schedule     ScheduleInput  `json:"schedule"`
	Retention    RetentionInput `json:"retention"`
	Advanced     AdvancedInput  `json:"advanced"`
	Paused       bool           `json:"paused"`
}

type CreateResponse struct {
	Item
}

// UpdateRequest 修改任务，整体提交
type UpdateRequest struct {
	mux.Meta     `path:"/jobs/:id" method:"PUT"`
	ID           int64          `uri:"id" binding:"required"`
	Name         string         `json:"name"`
	SourceType   string         `json:"source_type"`
	ServerID     int64          `json:"server_id"`
	ConnMode     string         `json:"conn_mode"`
	DBHost       string         `json:"db_host"`
	DBPort       int            `json:"db_port"`
	DBUser       string         `json:"db_user"`
	DBPassword   string         `json:"db_password"`
	Databases    []string       `json:"databases"`
	AllDatabases bool           `json:"all_databases"`
	AuthDB       string         `json:"auth_db"`
	Paths        []string       `json:"paths"`
	Excludes     []string       `json:"excludes"`
	RepoID       int64          `json:"repo_id"`
	Schedule     ScheduleInput  `json:"schedule"`
	Retention    RetentionInput `json:"retention"`
	Advanced     AdvancedInput  `json:"advanced"`
	Paused       bool           `json:"paused"`
}

type UpdateResponse struct {
	Item
}

// DeleteRequest 删除任务；存储位置里已有的快照保留
type DeleteRequest struct {
	mux.Meta `path:"/jobs/:id" method:"DELETE"`
	ID       int64 `uri:"id" binding:"required"`
}

type DeleteResponse struct{}

// DuplicateRequest 复制为新任务：名称加「 副本」，保持暂停，沿用已保存的密码
type DuplicateRequest struct {
	mux.Meta `path:"/jobs/:id/duplicate" method:"POST"`
	ID       int64 `uri:"id" binding:"required"`
}

type DuplicateResponse struct {
	Item
}

// PauseRequest 暂停计划；已有快照与手动运行不受影响
type PauseRequest struct {
	mux.Meta `path:"/jobs/:id/pause" method:"POST"`
	ID       int64 `uri:"id" binding:"required"`
}

type PauseResponse struct {
	Item
}

// ResumeRequest 恢复计划
type ResumeRequest struct {
	mux.Meta `path:"/jobs/:id/resume" method:"POST"`
	ID       int64 `uri:"id" binding:"required"`
}

type ResumeResponse struct {
	Item
}

// NextRunsRequest 预览下次运行时间，按设置里的时区计算
type NextRunsRequest struct {
	mux.Meta `path:"/jobs/next-runs" method:"POST"`
	Schedule ScheduleInput `json:"schedule"`
	// Count 返回几次，默认 5，最多 20
	Count int `json:"count"`
}

type NextRunsResponse struct {
	Timezone string  `json:"timezone"`
	Times    []int64 `json:"times"` // Unix 秒
}

// DatabasesRequest 读取数据库列表及大小。ID 非 0 时表示编辑中的已保存任务，密码为占位「已设置」时取它已保存的值。
type DatabasesRequest struct {
	mux.Meta     `path:"/jobs/databases" method:"POST"`
	ID           int64          `json:"id"`
	Name         string         `json:"name"`
	SourceType   string         `json:"source_type"`
	ServerID     int64          `json:"server_id"`
	ConnMode     string         `json:"conn_mode"`
	DBHost       string         `json:"db_host"`
	DBPort       int            `json:"db_port"`
	DBUser       string         `json:"db_user"`
	DBPassword   string         `json:"db_password"`
	Databases    []string       `json:"databases"`
	AllDatabases bool           `json:"all_databases"`
	AuthDB       string         `json:"auth_db"`
	Paths        []string       `json:"paths"`
	Excludes     []string       `json:"excludes"`
	RepoID       int64          `json:"repo_id"`
	Schedule     ScheduleInput  `json:"schedule"`
	Retention    RetentionInput `json:"retention"`
	Advanced     AdvancedInput  `json:"advanced"`
	Paused       bool           `json:"paused"`
}

// Database 一个库
type Database struct {
	Name      string `json:"name"`
	SizeBytes int64  `json:"size_bytes"` // 未知为 -1
	System    bool   `json:"system"`
	// Selectable 系统库为 false
	Selectable bool `json:"selectable"`
}

// DatabasesResponse 读取失败不是接口错误：OK 为 false，Reason 与 Message 供面板原地显示并提供重试。
type DatabasesResponse struct {
	OK          bool        `json:"ok"`
	List        []*Database `json:"list"`
	Reason      string      `json:"reason"` // SSH 链路某一跳失败时同服务器测试的原因（timeout、auth_failed、fingerprint_mismatch 等，Message 写明第几跳），否则 unreachable | auth_failed | timeout | failed
	ReasonLabel string      `json:"reason_label"`
	Message     string      `json:"message"`
}

// TestRequest 测试一份未保存的配置，字段同 DatabasesRequest
type TestRequest struct {
	mux.Meta     `path:"/jobs/test" method:"POST"`
	ID           int64          `json:"id"`
	Name         string         `json:"name"`
	SourceType   string         `json:"source_type"`
	ServerID     int64          `json:"server_id"`
	ConnMode     string         `json:"conn_mode"`
	DBHost       string         `json:"db_host"`
	DBPort       int            `json:"db_port"`
	DBUser       string         `json:"db_user"`
	DBPassword   string         `json:"db_password"`
	Databases    []string       `json:"databases"`
	AllDatabases bool           `json:"all_databases"`
	AuthDB       string         `json:"auth_db"`
	Paths        []string       `json:"paths"`
	Excludes     []string       `json:"excludes"`
	RepoID       int64          `json:"repo_id"`
	Schedule     ScheduleInput  `json:"schedule"`
	Retention    RetentionInput `json:"retention"`
	Advanced     AdvancedInput  `json:"advanced"`
	Paused       bool           `json:"paused"`
}

// Check 检查项的 key
const (
	CheckSSH     = "ssh"      // SSH 链路可达
	CheckDBReach = "db_reach" // 数据库可达
	CheckDBAuth  = "db_auth"  // 认证通过
	CheckDBPrivs = "db_privs" // 权限足够导出
	CheckRepo    = "repo"     // 目标存储位置可达、已挂载
)

// CheckItem 一个检查项。前一项失败时后面的项 Skipped 为 true（OK 为 false，不算失败原因）。
type CheckItem struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	OK      bool   `json:"ok"`
	Skipped bool   `json:"skipped"`
	Reason  string `json:"reason"` // 失败原因；成功为空
	// Fix 有修复办法时给出可复制的命令，例如缺少权限时的 GRANT 语句
	Fix string `json:"fix"`
}

// TestResponse 按顺序的检查结果；OK 表示全部检查项都通过
type TestResponse struct {
	OK     bool         `json:"ok"`
	Checks []*CheckItem `json:"checks"`
}

// Input 取出提交的任务配置
func (r *CreateRequest) Input() *JobInput {
	return &JobInput{Name: r.Name, SourceType: r.SourceType, ServerID: r.ServerID, ConnMode: r.ConnMode, DBHost: r.DBHost, DBPort: r.DBPort, DBUser: r.DBUser, DBPassword: r.DBPassword, Databases: r.Databases, AllDatabases: r.AllDatabases, AuthDB: r.AuthDB, Paths: r.Paths, Excludes: r.Excludes, RepoID: r.RepoID, Schedule: r.Schedule, Retention: r.Retention, Advanced: r.Advanced, Paused: r.Paused}
}

// Input 取出提交的任务配置
func (r *UpdateRequest) Input() *JobInput {
	return &JobInput{Name: r.Name, SourceType: r.SourceType, ServerID: r.ServerID, ConnMode: r.ConnMode, DBHost: r.DBHost, DBPort: r.DBPort, DBUser: r.DBUser, DBPassword: r.DBPassword, Databases: r.Databases, AllDatabases: r.AllDatabases, AuthDB: r.AuthDB, Paths: r.Paths, Excludes: r.Excludes, RepoID: r.RepoID, Schedule: r.Schedule, Retention: r.Retention, Advanced: r.Advanced, Paused: r.Paused}
}

// Input 取出提交的任务配置
func (r *DatabasesRequest) Input() *JobInput {
	return &JobInput{Name: r.Name, SourceType: r.SourceType, ServerID: r.ServerID, ConnMode: r.ConnMode, DBHost: r.DBHost, DBPort: r.DBPort, DBUser: r.DBUser, DBPassword: r.DBPassword, Databases: r.Databases, AllDatabases: r.AllDatabases, AuthDB: r.AuthDB, Paths: r.Paths, Excludes: r.Excludes, RepoID: r.RepoID, Schedule: r.Schedule, Retention: r.Retention, Advanced: r.Advanced, Paused: r.Paused}
}

// Input 取出提交的任务配置
func (r *TestRequest) Input() *JobInput {
	return &JobInput{Name: r.Name, SourceType: r.SourceType, ServerID: r.ServerID, ConnMode: r.ConnMode, DBHost: r.DBHost, DBPort: r.DBPort, DBUser: r.DBUser, DBPassword: r.DBPassword, Databases: r.Databases, AllDatabases: r.AllDatabases, AuthDB: r.AuthDB, Paths: r.Paths, Excludes: r.Excludes, RepoID: r.RepoID, Schedule: r.Schedule, Retention: r.Retention, Advanced: r.Advanced, Paused: r.Paused}
}

// NewCreateRequest 用一份任务配置构造新建请求
func NewCreateRequest(in *JobInput) *CreateRequest {
	return &CreateRequest{Name: in.Name, SourceType: in.SourceType, ServerID: in.ServerID, ConnMode: in.ConnMode, DBHost: in.DBHost, DBPort: in.DBPort, DBUser: in.DBUser, DBPassword: in.DBPassword, Databases: in.Databases, AllDatabases: in.AllDatabases, AuthDB: in.AuthDB, Paths: in.Paths, Excludes: in.Excludes, RepoID: in.RepoID, Schedule: in.Schedule, Retention: in.Retention, Advanced: in.Advanced, Paused: in.Paused}
}

// NewUpdateRequest 用一份任务配置构造修改请求
func NewUpdateRequest(id int64, in *JobInput) *UpdateRequest {
	return &UpdateRequest{ID: id, Name: in.Name, SourceType: in.SourceType, ServerID: in.ServerID, ConnMode: in.ConnMode, DBHost: in.DBHost, DBPort: in.DBPort, DBUser: in.DBUser, DBPassword: in.DBPassword, Databases: in.Databases, AllDatabases: in.AllDatabases, AuthDB: in.AuthDB, Paths: in.Paths, Excludes: in.Excludes, RepoID: in.RepoID, Schedule: in.Schedule, Retention: in.Retention, Advanced: in.Advanced, Paused: in.Paused}
}

// NewTestRequest 用一份任务配置构造测试请求；id 为 0 表示还没保存的任务
func NewTestRequest(id int64, in *JobInput) *TestRequest {
	return &TestRequest{ID: id, Name: in.Name, SourceType: in.SourceType, ServerID: in.ServerID, ConnMode: in.ConnMode, DBHost: in.DBHost, DBPort: in.DBPort, DBUser: in.DBUser, DBPassword: in.DBPassword, Databases: in.Databases, AllDatabases: in.AllDatabases, AuthDB: in.AuthDB, Paths: in.Paths, Excludes: in.Excludes, RepoID: in.RepoID, Schedule: in.Schedule, Retention: in.Retention, Advanced: in.Advanced, Paused: in.Paused}
}

// NewDatabasesRequest 用一份任务配置构造读取库列表的请求；id 为 0 表示还没保存的任务
func NewDatabasesRequest(id int64, in *JobInput) *DatabasesRequest {
	return &DatabasesRequest{ID: id, Name: in.Name, SourceType: in.SourceType, ServerID: in.ServerID, ConnMode: in.ConnMode, DBHost: in.DBHost, DBPort: in.DBPort, DBUser: in.DBUser, DBPassword: in.DBPassword, Databases: in.Databases, AllDatabases: in.AllDatabases, AuthDB: in.AuthDB, Paths: in.Paths, Excludes: in.Excludes, RepoID: in.RepoID, Schedule: in.Schedule, Retention: in.Retention, Advanced: in.Advanced, Paused: in.Paused}
}
