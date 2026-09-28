// Package dump 用官方导出工具（mysqldump、pg_dump、pg_dumpall）经网络链路导出 MySQL / PostgreSQL，
// 把每个导出文件作为流交给调用方（写入 kopia 快照），主控端不落地完整的导出文件。
//
// 一次导出是一个 Session：在 127.0.0.1 开临时端口，把导出工具的连接经链路转发到数据源；
// 交给工具的密码与 TLS 材料只写进运行临时目录（0700）中权限 0600 的文件，命令行不含秘密；
// Close 终止仍在运行的工具、关闭端口与已转发的连接并删除临时目录。
// 进程异常退出留下的临时目录由 Sweep 在启动时清理。
package dump

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/opskat/opsnap/internal/pkg/dsconn"
	"github.com/opskat/opsnap/internal/pkg/probe"
)

var (
	// ErrInvalidOptions 导出选项或数据源配置无效，未启动任何工具
	ErrInvalidOptions = errors.New("导出选项无效")
	// ErrToolNotFound 在 PATH 与 tools.dir 中都找不到导出工具
	ErrToolNotFound = errors.New("找不到导出工具")
	// ErrToolVersion 导出工具版本低于服务端且无法导出（pg_dump / pg_dumpall 大版本）
	ErrToolVersion = errors.New("导出工具版本过低")
	// ErrUnsupportedTLS 导出工具无法按数据源的 TLS 设置连接
	ErrUnsupportedTLS = errors.New("导出工具无法按数据源的 TLS 设置连接")
	// ErrPrivilege 数据源账号缺少导出所需的权限，错误信息中说明需要的权限
	ErrPrivilege = errors.New("数据源账号缺少权限")
	// ErrIncomplete 导出工具正常退出，但输出没有通过完整性检查
	ErrIncomplete = errors.New("导出内容不完整")
)

// errClosed 会话已关闭后继续读取
var errClosed = errors.New("导出已终止")

// ToolError 导出工具以失败状态退出。Stderr 为工具错误输出的末尾，已去掉秘密；
// Err 为经链路连接数据源的错误（如 *netchain.HopError），没有时为 nil
type ToolError struct {
	Tool     string
	ExitCode int
	Stderr   string
	Err      error
}

func (e *ToolError) Error() string {
	msg := fmt.Sprintf("%s 失败（退出码 %d）", e.Tool, e.ExitCode)
	if e.Stderr != "" {
		msg += ": " + e.Stderr
	}
	if e.Err != nil {
		msg += "；经链路连接数据源失败: " + e.Err.Error()
	}
	return msg
}

func (e *ToolError) Unwrap() error { return e.Err }

// Source 要导出的数据源与已建立的链路
type Source struct {
	// Dialer 已建立的网络链路（*netchain.Tunnel），会话期间调用方不能关闭它
	Dialer dsconn.Dialer
	// Config 数据源连接参数（含密码与 TLS 材料），Type 为 MySQL 或 PostgreSQL
	Config dsconn.Config
	// ServerVersion 连接数据源时读到的服务端版本（dsconn.Info.Version），为空时不比较工具版本
	ServerVersion string
}

// Options 导出内容
type Options struct {
	// Databases 要导出的库，至少一个；“整个实例”由调用方在每次运行时列出
	Databases []string
	// Routines、Triggers、Events 仅 MySQL：一并导出存储过程与函数、触发器、事件
	Routines, Triggers, Events bool
	// Accounts 仅 MySQL：另导出非系统账号的 CREATE USER 与 GRANT 语句（accounts.sql）
	Accounts bool
	// Globals 仅 PostgreSQL：另用 pg_dumpall --globals-only 导出角色、表空间等全局对象（globals.sql）
	Globals bool
	// ExcludeTables 排除的表，MySQL 写作 库.表，PostgreSQL 写作 库.模式.表（见 ValidateExcludeTable）
	ExcludeTables []string
	// Log 接收运行日志（已去掉秘密），可为 nil
	Log func(msg string)
}

// 导出文件名
const (
	// MySQLDatabasesFile 所有选中库的 mysqldump 输出
	MySQLDatabasesFile = "databases.sql"
	// MySQLAccountsFile 账号与权限
	MySQLAccountsFile = "accounts.sql"
	// PostgresGlobalsFile pg_dumpall --globals-only 的输出
	PostgresGlobalsFile = "globals.sql"
	// PostgresArchiveExt 每个库一份 custom 格式归档，文件名为转义后的库名加此后缀
	PostgresArchiveExt = ".dump"
)

// runPrefix 运行临时目录名前缀
const runPrefix = "run-"

// Session 一次导出。Files 按顺序逐个读到 EOF（kopiarepo.Writer.WriteSnapshot 即如此），
// 每个导出工具在它的文件第一次被读取时才启动；用完必须 Close。
type Session struct {
	ctx     context.Context //nolint:containedctx // 导出工具在文件首次被读取时才启动，需要沿用运行的 ctx
	dir     string
	log     func(string)
	secrets []string
	fwd     *forwarder
	files   []*File
	runs    []*toolRun

	closed   atomic.Bool
	closeMu  sync.Mutex
	closeErr error
}

// File 一个导出文件的流。读到 io.EOF 表示工具已成功退出且内容通过了完整性检查；
// 其余错误（工具失败、*ToolError、ErrIncomplete、ctx 取消、会话关闭）都表示这份导出不可用
type File struct {
	// Name 快照中的文件名，不含 "/"
	Name string
	src  io.Reader
	s    *Session
	n    atomic.Int64
}

// Read 实现 io.Reader
func (f *File) Read(p []byte) (int, error) {
	if f.s.closed.Load() {
		return 0, errClosed
	}
	n, err := f.src.Read(p)
	f.n.Add(int64(n))
	return n, err
}

// Bytes 已读出的字节数，可在读取的同时从其他 goroutine 调用，用于显示导出进度
func (f *File) Bytes() int64 { return f.n.Load() }

// Files 本次导出的文件，按应读取的顺序排列
func (s *Session) Files() []*File { return s.files }

// Bytes 所有文件已读出的字节数之和
func (s *Session) Bytes() int64 {
	var n int64
	for _, f := range s.files {
		n += f.Bytes()
	}
	return n
}

// Close 终止仍在运行的导出工具，关闭本机端口与已转发的连接，删除运行临时目录。可重复调用
func (s *Session) Close() error {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	if s.closed.Swap(true) {
		return s.closeErr
	}
	for _, r := range s.runs {
		r.stop()
	}
	if s.fwd != nil {
		s.fwd.close()
	}
	s.closeErr = os.RemoveAll(s.dir)
	return s.closeErr
}

func (s *Session) logf(format string, args ...any) {
	if s.log != nil {
		s.log(s.scrub(fmt.Sprintf(format, args...)))
	}
}

// scrub 去掉文本中的秘密
func (s *Session) scrub(text string) string {
	for _, sec := range s.secrets {
		if sec != "" {
			text = strings.ReplaceAll(text, sec, "******")
		}
	}
	return text
}

func (s *Session) addFile(name string, src io.Reader) {
	s.files = append(s.files, &File{Name: name, src: src, s: s})
}

// writeSecret 在运行临时目录中写一个 0600 的文件，返回其路径
func (s *Session) writeSecret(name string, content []byte) (string, error) {
	p := filepath.Join(s.dir, name)
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // 路径由运行临时目录与固定文件名组成
	if err != nil {
		return "", err
	}
	_, err = f.Write(content)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return p, err
}

// localPort 本机转发端口
func (s *Session) localPort() string {
	_, port, _ := net.SplitHostPort(s.fwd.addr())
	return port
}

// Start 准备一次导出：校验选项，查找导出工具并比较版本，建立运行临时目录与凭据文件，
// 在 127.0.0.1 开临时端口转发到数据源，并经链路查询排除规则是否匹配、非 InnoDB 表（MySQL）
// 和账号与权限（MySQL，勾选时）。提示写进 opts.Log。
// dir 为运行临时目录的父目录（Sweep 清理同一目录）；ctx 约束整个会话，取消时终止导出工具。
// 返回错误时已清理所有临时文件与端口。
func Start(ctx context.Context, dir string, src Source, opts Options) (_ *Session, err error) {
	if err := validate(src, opts); err != nil {
		return nil, err
	}
	s := &Session{ctx: ctx, log: opts.Log, secrets: []string{src.Config.Password}}
	var p planner
	if src.Config.Type == dsconn.TypeMySQL {
		p = &mysqlPlan{src: src, opts: opts}
	} else {
		p = &postgresPlan{src: src, opts: opts}
	}
	if err := p.tools(ctx, s); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if s.dir, err = os.MkdirTemp(dir, runPrefix); err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = s.Close()
		}
	}()
	target := net.JoinHostPort(src.Config.Host, strconv.Itoa(src.Config.Port))
	if s.fwd, err = listen(ctx, src.Dialer, target); err != nil {
		return nil, err
	}
	if err := p.prepare(ctx, s); err != nil {
		return nil, err
	}
	return s, nil
}

// planner 一种数据源的导出步骤
type planner interface {
	// tools 查找导出工具并检查版本与 TLS 支持，不创建任何文件
	tools(ctx context.Context, s *Session) error
	// prepare 写凭据文件、经链路查询目录，并登记导出文件
	prepare(ctx context.Context, s *Session) error
}

func validate(src Source, opts Options) error {
	typ := src.Config.Type
	if typ != dsconn.TypeMySQL && typ != dsconn.TypePostgreSQL {
		return fmt.Errorf("%w：不支持导出 %q 类型的数据源", ErrInvalidOptions, typ)
	}
	if src.Dialer == nil {
		return fmt.Errorf("%w：缺少网络链路", ErrInvalidOptions)
	}
	if err := src.Config.Validate(); err != nil {
		return fmt.Errorf("%w：%w", ErrInvalidOptions, err)
	}
	if len(opts.Databases) == 0 {
		return fmt.Errorf("%w：至少选择一个库", ErrInvalidOptions)
	}
	for _, db := range opts.Databases {
		if db == "" {
			return fmt.Errorf("%w：库名为空", ErrInvalidOptions)
		}
	}
	if typ == dsconn.TypeMySQL && opts.Globals {
		return fmt.Errorf("%w：全局对象只适用于 PostgreSQL", ErrInvalidOptions)
	}
	if typ == dsconn.TypePostgreSQL && (opts.Accounts || opts.Routines || opts.Triggers || opts.Events) {
		return fmt.Errorf("%w：账号与权限、存储过程、触发器、事件只适用于 MySQL", ErrInvalidOptions)
	}
	for _, ex := range opts.ExcludeTables {
		if err := ValidateExcludeTable(typ, ex); err != nil {
			return err
		}
	}
	return nil
}

// ValidateExcludeTable 检查一条排除规则的格式：MySQL 为 库.表，PostgreSQL 为 库.模式.表，
// 每一段都不能为空，也不能有首尾空白。格式不对时返回包装了 ErrInvalidOptions 的错误
func ValidateExcludeTable(typ dsconn.Type, rule string) error {
	want, format := 2, "库.表"
	if typ == dsconn.TypePostgreSQL {
		want, format = 3, "库.模式.表"
	}
	parts := strings.Split(rule, ".")
	ok := len(parts) == want
	for _, p := range parts {
		if p == "" || strings.TrimSpace(p) != p {
			ok = false
		}
	}
	if !ok {
		return fmt.Errorf("%w：排除规则 %q 应写作 %s", ErrInvalidOptions, rule, format)
	}
	return nil
}

// findTool 按 PATH → tools.dir 查找导出工具并读取版本
func findTool(ctx context.Context, name, pkg string) (*toolInfo, error) {
	path, ok := probe.ToolPath(name)
	if !ok {
		return nil, fmt.Errorf("%w：%s（先在 PATH 中查找，再在配置项 tools.dir 指定的目录中查找）。修复：在主控端安装 %s，或把 %s 放进 tools.dir",
			ErrToolNotFound, name, pkg, name)
	}
	major, minor, raw, err := probe.ToolVersion(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("无法确定 %s（%s）的版本: %w", name, path, err)
	}
	return &toolInfo{name: name, path: path, major: major, minor: minor, raw: raw}, nil
}

type toolInfo struct {
	name, path   string
	major, minor int
	raw          string
}

// openDB 经链路打开数据源的 Go 连接池（测试可替换）
var openDB = func(ctx context.Context, d dsconn.Dialer, cfg dsconn.Config) (*sql.DB, error) {
	c, err := dsconn.Open(ctx, d, cfg)
	if err != nil {
		return nil, err
	}
	return c.DB, nil
}

// connect 经链路打开连接池；错误中没有秘密（dsconn 已去除）
func connect(ctx context.Context, src Source, database string) (*sql.DB, error) {
	cfg := src.Config
	if database != "" {
		cfg.Database = database
	}
	db, err := openDB(ctx, src.Dialer, cfg)
	if err != nil {
		return nil, fmt.Errorf("连接数据源: %w", err)
	}
	return db, nil
}

// systemCABundles 常见系统的 CA 证书包位置（与 Go crypto/x509 的查找列表一致）
var systemCABundles = []string{
	"/etc/ssl/certs/ca-certificates.crt",
	"/etc/pki/tls/certs/ca-bundle.crt",
	"/etc/ssl/ca-bundle.pem",
	"/etc/pki/tls/cacert.pem",
	"/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem",
	"/etc/ssl/cert.pem",
}

// tlsFiles 交给导出工具的 TLS 文件路径，没有的为空
type tlsFiles struct {
	ca, cert, key string
}

// writeTLS 写出导出工具需要的 TLS 文件：校验模式下的 CA（未提供时用系统 CA 证书包），以及客户端证书与私钥
func (s *Session) writeTLS(t dsconn.TLSConfig) (tlsFiles, error) {
	var out tlsFiles
	var err error
	if t.Mode == dsconn.TLSDisable {
		return out, nil
	}
	if t.Mode == dsconn.TLSVerifyCA || t.Mode == dsconn.TLSVerifyFull {
		if len(t.CA) > 0 {
			if out.ca, err = s.writeSecret("ca.pem", t.CA); err != nil {
				return out, err
			}
		} else if out.ca = systemCABundle(); out.ca == "" {
			return out, fmt.Errorf("%w：找不到系统 CA 证书包，导出工具无法校验服务端证书。修复：在数据源的 TLS 设置中提供 CA 证书", ErrUnsupportedTLS)
		}
	}
	if len(t.ClientCert) > 0 {
		if out.cert, err = s.writeSecret("client-cert.pem", t.ClientCert); err != nil {
			return out, err
		}
		if out.key, err = s.writeSecret("client-key.pem", t.ClientKey); err != nil {
			return out, err
		}
	}
	return out, nil
}

func systemCABundle() string {
	candidates := systemCABundles
	if f := os.Getenv("SSL_CERT_FILE"); f != "" {
		candidates = append([]string{f}, candidates...)
	}
	for _, p := range candidates {
		if info, err := os.Stat(p); err == nil && !info.IsDir() { //nolint:gosec // 只检查固定位置或 SSL_CERT_FILE 指定的证书包是否存在，不读取内容

			return p
		}
	}
	return ""
}

// toolEnv 导出工具的环境变量：不继承 OpsNap 的环境（避免 MYSQL_PWD、PG* 等变量混入），
// HOME 指向运行临时目录，工具不会读到主控端用户目录下的配置与证书
func (s *Session) toolEnv(extra ...string) []string {
	return append([]string{"HOME=" + s.dir, "PATH=" + os.Getenv("PATH"), "LC_ALL=C"}, extra...)
}

// staticFile 内存中已生成的导出文件（MySQL 账号与权限）
func staticFile(b []byte) io.Reader { return bytes.NewReader(b) }

// Sweep 删除 dir 中进程异常退出留下的运行临时目录（其中可能有密码与 TLS 私钥）。dir 不存在时不算错误
func Sweep(dir string) error {
	ents, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), runPrefix) {
			errs = append(errs, os.RemoveAll(filepath.Join(dir, e.Name())))
		}
	}
	return errors.Join(errs...)
}
