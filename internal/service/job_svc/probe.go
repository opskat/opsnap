package job_svc

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/opskat/opsnap/internal/model/entity/job_entity"
)

// 探测失败的原因
const (
	ReasonUnreachable = "unreachable" // 连不上
	ReasonAuthFailed  = "auth_failed" // 用户或密码不对
	ReasonTimeout     = "timeout"     // 超时
	ReasonFailed      = "failed"      // 其他错误
)

// ProbeError 数据库探测失败，带面板可展示的原因分类
type ProbeError struct {
	Reason  string
	Message string
}

func (e *ProbeError) Error() string { return e.Message }

// Target 探测的目标。Addr 是 opsnap 本机能连上的地址：直连时是数据库地址，经 SSH 时是本地转发端口。
// Password 是明文，只在进程内传递，不写日志。
type Target struct {
	Type     string
	Addr     string
	User     string
	Password string
	AuthDB   string
}

// Database 一个库
type Database struct {
	Name      string
	SizeBytes int64 // 未知为 -1
}

// Diagnosis 一次连接诊断。Reach 非空表示连不上，Auth 非空表示认证失败；
// 两者都为空时 Missing 是导出所缺的权限名。
type Diagnosis struct {
	Reach   *ProbeError
	Auth    *ProbeError
	Missing []string
}

// Probe 数据库专属的查询，放在小接口后面，单元测试用替身，真实驱动由集成测试覆盖。
type Probe interface {
	// ListDatabases 列出全部库及大小，含系统库
	ListDatabases(ctx context.Context, t Target) ([]Database, error)
	// Diagnose 依次确认可达、认证、导出所需权限
	Diagnose(ctx context.Context, t Target) Diagnosis
}

// ProbeFor 按数据源类型取得探测实现
type ProbeFor func(sourceType string) (Probe, error)

// defaultProbes 真实驱动
func defaultProbes(sourceType string) (Probe, error) {
	switch sourceType {
	case job_entity.SourceMySQL, job_entity.SourceMariaDB:
		return mysqlProbe{}, nil
	case job_entity.SourcePostgres:
		return postgresProbe{}, nil
	case job_entity.SourceMongoDB:
		return mongoProbe{}, nil
	case job_entity.SourceRedis:
		return redisProbe{}, nil
	}
	return nil, fmt.Errorf("数据源 %q 没有数据库探测", sourceType)
}

// requiredMySQLPrivileges mysqldump 以 --single-transaction --routines --triggers 导出所需的权限
var requiredMySQLPrivileges = []string{"SELECT", "SHOW VIEW", "TRIGGER", "LOCK TABLES"}

// fixCommand 缺少权限时的修复命令；没有缺失或没有已知的修复办法返回空串
func fixCommand(sourceType, user string, missing []string) string {
	if len(missing) == 0 {
		return ""
	}
	switch sourceType {
	case job_entity.SourceMySQL, job_entity.SourceMariaDB:
		u := strings.ReplaceAll(user, "'", "''")
		return fmt.Sprintf("GRANT %s ON *.* TO '%s'@'%%';", strings.Join(missing, ", "), u)
	case job_entity.SourcePostgres:
		return fmt.Sprintf("GRANT pg_read_all_data TO %s;", quoteIdent(user))
	case job_entity.SourceMongoDB:
		u := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(user)
		return fmt.Sprintf(`db.getSiblingDB("admin").grantRolesToUser("%s", [{ role: "backup", db: "admin" }])`, u)
	}
	return ""
}

func quoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// asProbeError 把驱动错误归类
func asProbeError(ctx context.Context, err error) *ProbeError {
	var pe *ProbeError
	if errors.As(err, &pe) {
		return pe
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return &ProbeError{Reason: ReasonTimeout, Message: "连接超时"}
	}
	return &ProbeError{Reason: ReasonFailed, Message: err.Error()}
}
