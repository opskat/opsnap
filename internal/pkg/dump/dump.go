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
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/dsconn"
	"github.com/opskat/opsnap/internal/pkg/l10n"
	"github.com/opskat/opsnap/internal/pkg/probe"
)

// 本包的错误与运行日志都是 l10n 文字：Error() 为中文，调用方可用 l10n.Text 按查看者的语言显示；
// 导出工具的错误输出与驱动错误作为参数原样代入
var (
	// ErrInvalidOptions 导出选项或数据源配置无效，未启动任何工具
	ErrInvalidOptions error = l10n.Errorf(code.DumpErrInvalidOptions)
	// ErrToolNotFound 在 PATH 与 tools.dir 中都找不到导出工具
	ErrToolNotFound error = l10n.Errorf(code.DumpErrToolNotFound)
	// ErrToolVersion 导出工具版本低于服务端且无法导出（pg_dump / pg_dumpall 大版本）
	ErrToolVersion error = l10n.Errorf(code.DumpErrToolVersion)
	// ErrUnsupportedTLS 导出工具无法按数据源的 TLS 设置连接
	ErrUnsupportedTLS error = l10n.Errorf(code.DumpErrUnsupportedTLS)
	// ErrPrivilege 数据源账号缺少导出所需的权限，错误信息中说明需要的权限
	ErrPrivilege error = l10n.Errorf(code.DumpErrPrivilege)
	// ErrIncomplete 导出工具正常退出，但输出没有通过完整性检查
	ErrIncomplete error = l10n.Errorf(code.DumpErrIncomplete)
)

// errClosed 会话已关闭后继续读取
var errClosed error = l10n.Errorf(code.DumpErrClosed)

// ToolError 导出工具以失败状态退出。Stderr 为工具错误输出的末尾（原文），已去掉秘密，
// StderrOmitted 为超出保留上限而省略的前面的字节数；Err 为经链路连接数据源的错误（如 *netchain.HopError），没有时为 nil
type ToolError struct {
	Tool          string
	ExitCode      int
	Stderr        string
	StderrOmitted int
	Err           error
}

func (e *ToolError) Error() string { return e.Localize(context.Background()) }

// Localize 按 ctx 的语言显示；错误输出原样保留
func (e *ToolError) Localize(ctx context.Context) string {
	msg := l10n.New(code.DumpToolFailed, e.Tool, e.ExitCode).Localize(ctx)
	if e.Stderr != "" {
		msg += ": " + stderrText(ctx, e.StderrOmitted, e.Stderr)
	}
	if e.Err != nil {
		msg += l10n.New(code.DumpViaChannelFailed, e.Err).Localize(ctx)
	}
	return msg
}

// stderrText 错误输出的末尾；省略了前面的内容时先注明省略了多少字节
func stderrText(ctx context.Context, omitted int, text string) string {
	if omitted == 0 {
		return text
	}
	return l10n.New(code.DumpStderrOmitted, omitted).Localize(ctx) + "\n" + text
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
	// Log 接收运行日志（按任何语言显示都已去掉秘密），可为 nil
	Log func(msg l10n.Localizer)
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
	log     func(l10n.Localizer)
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

// logm 写一行运行日志；按哪种语言显示都去掉秘密
func (s *Session) logm(m l10n.Localizer) {
	if s.log != nil {
		s.log(l10n.Func(func(ctx context.Context) string { return s.scrub(m.Localize(ctx)) }))
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

// CheckTools 不连接数据源，只检查这次导出需要的工具：能否在 PATH → tools.dir 中找到并读出版本；
// PostgreSQL 的 pg_dump / pg_dumpall 大版本不低于 serverVersion（数据源最近一次测试时读到的版本，未知时为空，不比较）；
// MariaDB 的 mysqldump 无法保证的 TLS 模式。运行的“准备”步骤用它在发起连接之前失败；
// 连接后 Start 仍按实际读到的服务端版本再检查一次
func CheckTools(ctx context.Context, typ dsconn.Type, tlsMode dsconn.TLSMode, serverVersion string, opts Options) error {
	src := Source{Config: dsconn.Config{Type: typ, TLS: dsconn.TLSConfig{Mode: tlsMode}}, ServerVersion: serverVersion}
	// 版本低于服务端的提示由 Start 写进日志，这里不重复
	s := &Session{}
	var p planner
	switch typ {
	case dsconn.TypeMySQL:
		p = &mysqlPlan{src: src, opts: opts}
	case dsconn.TypePostgreSQL:
		p = &postgresPlan{src: src, opts: opts}
	default:
		return l10n.Errorf(code.DumpUnsupportedType, ErrInvalidOptions, typ)
	}
	return p.tools(ctx, s)
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
		return l10n.Errorf(code.DumpUnsupportedType, ErrInvalidOptions, typ)
	}
	if src.Dialer == nil {
		return l10n.Errorf(code.DumpNoDialer, ErrInvalidOptions)
	}
	if err := src.Config.Validate(); err != nil {
		return l10n.Errorf(code.WrapFullColon, ErrInvalidOptions, err)
	}
	if len(opts.Databases) == 0 {
		return l10n.Errorf(code.DumpNoDatabases, ErrInvalidOptions)
	}
	for _, db := range opts.Databases {
		if db == "" {
			return l10n.Errorf(code.DumpEmptyDatabase, ErrInvalidOptions)
		}
	}
	if typ == dsconn.TypeMySQL && opts.Globals {
		return l10n.Errorf(code.DumpGlobalsPGOnly, ErrInvalidOptions)
	}
	if typ == dsconn.TypePostgreSQL && (opts.Accounts || opts.Routines || opts.Triggers || opts.Events) {
		return l10n.Errorf(code.DumpMySQLOnlyOptions, ErrInvalidOptions)
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
	want, format := 2, l10n.New(code.DumpExcludeFormatMySQL)
	if typ == dsconn.TypePostgreSQL {
		want, format = 3, l10n.New(code.DumpExcludeFormatPG)
	}
	parts := strings.Split(rule, ".")
	ok := len(parts) == want
	for _, p := range parts {
		if p == "" || strings.TrimSpace(p) != p {
			ok = false
		}
	}
	if !ok {
		return l10n.Errorf(code.DumpExcludeFormat, ErrInvalidOptions, rule, format)
	}
	return nil
}

// findTool 按 PATH → tools.dir 查找导出工具并读取版本
func findTool(ctx context.Context, name string, pkg l10n.Message) (*toolInfo, error) {
	path, ok := probe.ToolPath(name)
	if !ok {
		return nil, l10n.Errorf(code.DumpToolMissing, ErrToolNotFound, name, pkg, name)
	}
	major, minor, raw, err := probe.ToolVersion(ctx, path)
	if err != nil {
		return nil, l10n.Errorf(code.DumpToolVersionUnknown, name, path, err)
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
		return nil, l10n.Errorf(code.DumpConnect, err)
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
			return out, l10n.Errorf(code.DumpNoSystemCA, ErrUnsupportedTLS)
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

// libraryPathVars 传给导出工具的动态库搜索路径：探测与“准备”步骤读取工具版本时继承 OpsNap 的完整环境，
// 导出时也要能找到同样的动态库（如放在 tools.dir 中、自带库的客户端）
var libraryPathVars = []string{"LD_LIBRARY_PATH", "DYLD_LIBRARY_PATH", "DYLD_FALLBACK_LIBRARY_PATH"}

// toolEnv 导出工具的环境变量：不继承 OpsNap 的其他环境（避免 MYSQL_PWD、PG* 等变量混入），
// HOME 指向运行临时目录，工具不会读到主控端用户目录下的配置与证书
func (s *Session) toolEnv(extra ...string) []string {
	env := []string{"HOME=" + s.dir, "PATH=" + os.Getenv("PATH"), "LC_ALL=C"}
	for _, k := range libraryPathVars {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return append(env, extra...)
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
