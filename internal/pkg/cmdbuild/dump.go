package cmdbuild

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// DBKind 数据库导出类型
type DBKind string

const (
	DBMySQL    DBKind = "mysql"
	DBPostgres DBKind = "postgres"
	DBMongo    DBKind = "mongodb"
	DBRedis    DBKind = "redis"
)

// mongoConfigEnv 存放 mongodump 配置文件内容（含密码）的环境变量
const mongoConfigEnv = "OPSNAP_MONGODUMP_CONFIG"

// mongoScript 把环境变量里的配置经管道交给 mongodump --config=/dev/stdin，
// 其余参数原样经 "$@" 传入；退出码就是 mongodump 的退出码
const mongoScript = `printf '%s' "$` + mongoConfigEnv + `" | mongodump --config=/dev/stdin "$@"`

// DBConn 数据库连接。
// 直连时 Host/Port 是数据库地址；经 SSH 时由调用方建立本地转发，Host/Port 填本地转发地址。
type DBConn struct {
	Host     string
	Port     int
	User     string
	Password string
}

// DumpSpec 一次数据库导出
type DumpSpec struct {
	Kind DBKind
	Conn DBConn
	// Databases 要导出的库。MySQL 可多个；PostgreSQL 恰好一个；MongoDB 至多一个；Redis 忽略
	Databases []string
	// AllDatabases 导出全部库（MySQL/MongoDB）
	AllDatabases bool
	// AuthDatabase MongoDB 认证库，为空时用 admin
	AuthDatabase string
	// ExtraArgs 任务「高级」里的附加参数，追加在末尾
	ExtraArgs []string
}

// Filename 导出流在快照里的文件名
func (s DumpSpec) Filename() string {
	switch s.Kind {
	case DBMySQL:
		return string(s.Kind) + "/databases.sql"
	case DBPostgres:
		return "postgres/" + safeName(first(s.Databases)) + ".dump"
	case DBMongo:
		if s.AllDatabases || len(s.Databases) == 0 {
			return "mongodb/all.archive"
		}
		return "mongodb/" + safeName(s.Databases[0]) + ".archive"
	case DBRedis:
		return "redis/dump.rdb"
	}
	return string(s.Kind) + "/dump"
}

// Dump 生成导出命令。输出写到标准输出、不压缩（交给 restic 压缩），凭据只经环境变量传入。
//
// MongoDB 的 mongodump 只能从命令行或 --config 文件读取密码，因此改为运行
// `sh -c 'printf %s "$OPSNAP_MONGODUMP_CONFIG" | mongodump --config=/dev/stdin ...'`：
// 密码以 YAML 配置的形式放在环境变量里，由 shell 经管道交给 mongodump，既不进参数也不落盘。
// （restic --stdin-from-command 不会把自己的标准输入交给导出命令，所以不能直接用 Cmd.Stdin。）
func Dump(s DumpSpec) (Cmd, error) {
	if s.Conn.Host == "" {
		return Cmd{}, errors.New("缺少数据库地址")
	}
	if strings.HasPrefix(s.Conn.Host, "-") {
		return Cmd{}, fmt.Errorf("数据库地址无效：%q", s.Conn.Host)
	}
	if err := checkPort(s.Conn.Port); err != nil {
		return Cmd{}, err
	}
	for _, db := range s.Databases {
		if err := checkOperand("库名", db); err != nil {
			return Cmd{}, err
		}
	}
	var c Cmd
	var err error
	switch s.Kind {
	case DBMySQL:
		c, err = mysqlDump(s)
	case DBPostgres:
		c, err = pgDump(s)
	case DBMongo:
		c, err = mongoDump(s)
	case DBRedis:
		c = redisDump(s)
	default:
		return Cmd{}, fmt.Errorf("未知的数据库类型：%q", s.Kind)
	}
	if err != nil {
		return Cmd{}, err
	}
	c.Args = append(c.Args, s.ExtraArgs...)
	return c, nil
}

// mysqlDump mysqldump：单事务一致性快照，密码走 MYSQL_PWD
func mysqlDump(s DumpSpec) (Cmd, error) {
	args := []string{
		"--host=" + s.Conn.Host,
		"--port=" + strconv.Itoa(s.Conn.Port),
		"--protocol=TCP",
	}
	if s.Conn.User != "" {
		args = append(args, "--user="+s.Conn.User)
	}
	args = append(args,
		"--single-transaction",
		"--quick",
		"--routines",
		"--triggers",
		"--hex-blob",
		"--no-tablespaces",
		"--default-character-set=utf8mb4",
	)
	switch {
	case s.AllDatabases:
		args = append(args, "--all-databases")
	case len(s.Databases) > 0:
		args = append(args, "--databases")
		args = append(args, s.Databases...)
	default:
		return Cmd{}, errors.New("没有选择要导出的库")
	}
	c := Cmd{Name: "mysqldump", Args: args}
	if s.Conn.Password != "" {
		c.Env = []string{env("MYSQL_PWD", s.Conn.Password)}
	}
	return c, nil
}

// pgDump pg_dump：自定义格式、不压缩、不提示输入密码，密码走 PGPASSWORD
func pgDump(s DumpSpec) (Cmd, error) {
	if s.AllDatabases || len(s.Databases) != 1 {
		return Cmd{}, errors.New("pg_dump 每次只能导出一个库")
	}
	db := s.Databases[0]
	// pg_dump 会把含 = 或 URI 前缀的库名当成连接串，可能借此改写连接参数
	if strings.Contains(db, "=") || strings.HasPrefix(db, "postgres://") || strings.HasPrefix(db, "postgresql://") {
		return Cmd{}, fmt.Errorf("库名不能是连接串：%q", db)
	}
	args := []string{
		"--host=" + s.Conn.Host,
		"--port=" + strconv.Itoa(s.Conn.Port),
	}
	if s.Conn.User != "" {
		args = append(args, "--username="+s.Conn.User)
	}
	args = append(args, "--no-password", "--format=custom", "--compress=0", "--dbname="+db)
	c := Cmd{Name: "pg_dump", Args: args}
	if s.Conn.Password != "" {
		c.Env = []string{env("PGPASSWORD", s.Conn.Password)}
	}
	return c, nil
}

// mongoDump mongodump --archive（不加 --gzip）；有密码时经 sh 包装，见 Dump 的说明
func mongoDump(s DumpSpec) (Cmd, error) {
	if len(s.Databases) > 1 {
		return Cmd{}, errors.New("mongodump 每次只能导出一个库或全部库")
	}
	args := []string{
		"--host=" + s.Conn.Host,
		"--port=" + strconv.Itoa(s.Conn.Port),
	}
	if s.Conn.User != "" {
		authDB := s.AuthDatabase
		if authDB == "" {
			authDB = "admin"
		}
		args = append(args, "--username="+s.Conn.User, "--authenticationDatabase="+authDB)
	}
	args = append(args, "--archive")
	if !s.AllDatabases && len(s.Databases) == 1 {
		args = append(args, "--db="+s.Databases[0])
	}
	if s.Conn.Password == "" {
		return Cmd{Name: "mongodump", Args: args}, nil
	}
	// JSON 字符串同时是合法的 YAML 双引号标量，可安全转义任意密码
	quoted, err := json.Marshal(s.Conn.Password)
	if err != nil {
		return Cmd{}, err
	}
	return Cmd{
		Name: "sh",
		Args: append([]string{"-c", mongoScript, "mongodump"}, args...),
		Env:  []string{env(mongoConfigEnv, "password: "+string(quoted)+"\n")},
	}, nil
}

// redisDump redis-cli --rdb -：把 RDB 写到标准输出，密码走 REDISCLI_AUTH
func redisDump(s DumpSpec) Cmd {
	args := []string{"-h", s.Conn.Host, "-p", strconv.Itoa(s.Conn.Port)}
	if s.Conn.User != "" {
		args = append(args, "--user", s.Conn.User)
	}
	args = append(args, "--rdb", "-")
	c := Cmd{Name: "redis-cli", Args: args}
	if s.Conn.Password != "" {
		c.Env = []string{env("REDISCLI_AUTH", s.Conn.Password)}
	}
	return c
}

// first 返回第一个元素，没有时返回空串
func first(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	return ss[0]
}

// safeName 把库名里的路径分隔符换掉，避免在快照里形成多级目录
func safeName(s string) string {
	return strings.NewReplacer("/", "_", `\`, "_").Replace(s)
}
