package dump

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"sort"
	"strings"

	"github.com/go-sql-driver/mysql"

	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/dsconn"
	"github.com/opskat/opsnap/internal/pkg/l10n"
	"github.com/opskat/opsnap/internal/pkg/probe"
)

// mysqlPlan 一次 mysqldump 调用导出所有选中的库，勾选时另生成账号与权限
type mysqlPlan struct {
	src  Source
	opts Options
	tool *toolInfo
	// mariadb 主控端的 mysqldump 来自 MariaDB：TLS 选项写法不同，也没有 --set-gtid-purged
	mariadb bool
}

func (p *mysqlPlan) tools(ctx context.Context, s *Session) error {
	t, err := findTool(ctx, "mysqldump", l10n.New(code.DumpPkgMySQL))
	if err != nil {
		return err
	}
	p.tool = t
	p.mariadb = strings.Contains(t.raw, "MariaDB")
	mode := p.src.Config.TLS.Mode
	if p.mariadb && mode != "" && mode != dsconn.TLSPrefer && mode != dsconn.TLSDisable {
		return l10n.Errorf(code.DumpMariaDBTLS, ErrUnsupportedTLS, t.raw, mode)
	}
	if smaj, smin, ok := probe.ParseMajorMinor(p.src.ServerVersion); ok && (t.major < smaj || t.major == smaj && t.minor < smin) {
		s.logm(l10n.New(code.DumpMySQLDumpOlder, t.major, t.minor, smaj, smin))
	}
	return nil
}

func (p *mysqlPlan) prepare(ctx context.Context, s *Session) error {
	cnf, err := p.optionFile(s)
	if err != nil {
		return err
	}
	cnfPath, err := s.writeSecret("my.cnf", []byte(cnf))
	if err != nil {
		return err
	}

	db, err := connect(ctx, p.src, "")
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	if err := p.checkTables(ctx, db, s); err != nil {
		return err
	}
	var accounts []byte
	if p.opts.Accounts {
		if accounts, err = mysqlAccounts(ctx, db); err != nil {
			return err
		}
	}

	var wrap func(io.Reader) io.Reader
	if p.mariadb {
		wrap = dropSandboxLine
	}
	s.addTool(MySQLDatabasesFile, p.tool, p.args(cnfPath), s.toolEnv(), checkMySQLDump, wrap)
	if p.opts.Accounts {
		s.addFile(MySQLAccountsFile, staticFile(accounts))
	}
	return nil
}

// args mysqldump 参数：选项文件必须是第一个参数；单一事务快照，不含 GTID 清除语句与表空间
func (p *mysqlPlan) args(cnfPath string) []string {
	args := []string{
		"--defaults-file=" + cnfPath,
		"--single-transaction",
		"--quick",
		"--no-tablespaces",
		"--hex-blob",
		"--default-character-set=utf8mb4",
	}
	if p.opts.Routines {
		args = append(args, "--routines")
	}
	if p.opts.Events {
		args = append(args, "--events")
	}
	if !p.opts.Triggers {
		args = append(args, "--skip-triggers")
	}
	if !p.mariadb {
		args = append(args, "--set-gtid-purged=OFF")
	}
	for _, ex := range p.opts.ExcludeTables {
		args = append(args, "--ignore-table="+ex)
	}
	// "--" 之后的参数只作为库名：“整个实例”的库名来自服务端，以 - 开头的库名不能被当成选项
	args = append(args, "--databases", "--")
	return append(args, p.opts.Databases...)
}

// optionFile mysqldump 的选项文件：账号、密码、本机转发端口与 TLS 设置
func (p *mysqlPlan) optionFile(s *Session) (string, error) {
	cfg := p.src.Config
	lines := []string{"[client]", "user=" + optionQuote(cfg.User), "password=" + optionQuote(cfg.Password), "host=127.0.0.1",
		"port=" + s.localPort(), "protocol=TCP"}

	files, err := s.writeTLS(cfg.TLS)
	if err != nil {
		return "", err
	}
	mode := cfg.TLS.Mode
	switch {
	case p.mariadb && mode == dsconn.TLSDisable:
		lines = append(lines, "skip-ssl")
	case p.mariadb:
		lines = append(lines, "ssl")
	default:
		lines = append(lines, "ssl-mode="+oracleSSLMode(mode))
		if mode == dsconn.TLSVerifyFull {
			s.logm(l10n.New(code.DumpVerifyCAOnly))
		}
	}
	for _, kv := range [][2]string{{"ssl-ca", files.ca}, {"ssl-cert", files.cert}, {"ssl-key", files.key}} {
		if kv[1] == "" {
			continue
		}
		lines = append(lines, kv[0]+"="+optionQuote(kv[1]))
	}
	return strings.Join(lines, "\n") + "\n", nil
}

func oracleSSLMode(m dsconn.TLSMode) string {
	switch m {
	case dsconn.TLSDisable:
		return "DISABLED"
	case dsconn.TLSRequire:
		return "REQUIRED"
	case dsconn.TLSVerifyCA, dsconn.TLSVerifyFull:
		// 工具连接的是 127.0.0.1，VERIFY_IDENTITY 会拿它和证书比对；主机名由 dsconn 在同一链路上校验
		return "VERIFY_CA"
	}
	return "PREFERRED"
}

// optionQuote 按 MySQL / MariaDB 选项文件的规则把值写在双引号中：两端引号按首尾字符去掉；引号内的 # 不是注释；
// 反斜杠转义在去引号之后处理，因此内容中的反斜杠、双引号与控制字符都要转义。任何值都能这样表达
func optionQuote(v string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`, "\b", `\b`).Replace(v) + `"`
}

// checkTables 经 Go 连接列出选中库中的表：排除规则未匹配任何表、非 InnoDB 表都写进运行日志
func (p *mysqlPlan) checkTables(ctx context.Context, db *sql.DB, s *Session) error {
	marks := strings.TrimSuffix(strings.Repeat("?,", len(p.opts.Databases)), ",")
	args := make([]any, len(p.opts.Databases))
	for i, d := range p.opts.Databases {
		args[i] = d
	}
	//nolint:gosec // 拼接的只有 ? 占位符，库名经参数传入
	rows, err := db.QueryContext(ctx, "SELECT TABLE_SCHEMA, TABLE_NAME, TABLE_TYPE, ENGINE FROM information_schema.TABLES WHERE TABLE_SCHEMA IN ("+marks+")", args...)
	if err != nil {
		return l10n.Errorf(code.DumpListTables, err)
	}
	defer func() { _ = rows.Close() }()
	excluded := map[string]bool{}
	for _, ex := range p.opts.ExcludeTables {
		excluded[ex] = false
	}
	var nonInnoDB []string
	for rows.Next() {
		var schema, name, typ string
		var engine sql.NullString
		if err := rows.Scan(&schema, &name, &typ, &engine); err != nil {
			return l10n.Errorf(code.DumpListTables, err)
		}
		key := schema + "." + name
		if _, ok := excluded[key]; ok {
			excluded[key] = true
			continue
		}
		if typ == "BASE TABLE" && !strings.EqualFold(engine.String, "InnoDB") {
			nonInnoDB = append(nonInnoDB, key)
		}
	}
	if err := rows.Err(); err != nil {
		return l10n.Errorf(code.DumpListTables, err)
	}
	logUnmatched(s, p.opts.ExcludeTables, excluded)
	if len(nonInnoDB) > 0 {
		sort.Strings(nonInnoDB)
		s.logm(l10n.New(code.DumpNonInnoDB, len(nonInnoDB), l10n.Join(nonInnoDB, code.ListSep)))
	}
	return nil
}

// logUnmatched 按原顺序把未匹配任何表的排除规则写进运行日志
func logUnmatched(s *Session, rules []string, matched map[string]bool) {
	for _, ex := range rules {
		if !matched[ex] {
			s.logm(l10n.New(code.DumpExcludeUnmatched, ex))
		}
	}
}

// mysqlAccounts 生成非系统账号的 CREATE USER 与 GRANT 语句：先建全部账号（角色在前），再授权
func mysqlAccounts(ctx context.Context, db *sql.DB) ([]byte, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, l10n.Errorf(code.DumpAccounts, err)
	}
	defer func() { _ = conn.Close() }()
	var current string
	if err := conn.QueryRowContext(ctx, "SELECT CURRENT_USER()").Scan(&current); err != nil {
		return nil, l10n.Errorf(code.DumpAccounts, err)
	}
	// MySQL 8.0.17 起把认证串中的二进制内容以十六进制输出，导出文件才能原样导入；其他版本没有该变量，忽略错误
	_, _ = conn.ExecContext(ctx, "SET SESSION print_identified_with_as_hex = ON")

	accounts, err := queryAccounts(ctx, conn, "SELECT User, Host FROM mysql.user "+
		"WHERE User NOT LIKE 'mysql.%' AND User <> 'mariadb.sys' AND NOT (User = 'root' AND Host = 'localhost') ORDER BY User, Host")
	if err != nil {
		return nil, accountsError(err, current)
	}
	roles, err := queryAccounts(ctx, conn, "SELECT DISTINCT FROM_USER, FROM_HOST FROM mysql.role_edges")
	var me *mysql.MySQLError
	if err != nil && (!errors.As(err, &me) || me.Number != 1146) { // 没有 role_edges 表（MySQL 5.7、MariaDB）时没有角色
		return nil, accountsError(err, current)
	}
	isRole := map[[2]string]bool{}
	for _, r := range roles {
		isRole[r] = true
	}
	sort.SliceStable(accounts, func(i, j int) bool { return isRole[accounts[i]] && !isRole[accounts[j]] })

	var creates, grants []string
	for _, a := range accounts {
		name := "`" + strings.ReplaceAll(a[0], "`", "``") + "`@`" + strings.ReplaceAll(a[1], "`", "``") + "`"
		var create string
		if err := conn.QueryRowContext(ctx, "SHOW CREATE USER "+name).Scan(&create); err != nil {
			return nil, accountsError(err, current)
		}
		if rest, ok := strings.CutPrefix(create, "CREATE USER "); ok {
			create = "CREATE USER IF NOT EXISTS " + rest
		}
		creates = append(creates, create+";\n")
		gs, err := queryStrings(ctx, conn, "SHOW GRANTS FOR "+name)
		if err != nil {
			return nil, accountsError(err, current)
		}
		for _, g := range gs {
			grants = append(grants, g+";\n")
		}
	}
	out := "-- OpsNap: MySQL 非系统账号与权限\n" + strings.Join(creates, "") + strings.Join(grants, "")
	return []byte(out), nil
}

func queryStrings(ctx context.Context, conn *sql.Conn, q string) ([]string, error) {
	rows, err := conn.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func queryAccounts(ctx context.Context, conn *sql.Conn, q string) ([][2]string, error) {
	rows, err := conn.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out [][2]string
	for rows.Next() {
		var a [2]string
		if err := rows.Scan(&a[0], &a[1]); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// accountsError 读取账号与权限失败；缺少权限时说明需要 mysql 系统库的 SELECT 权限并给出授权语句
func accountsError(err error, current string) error {
	var me *mysql.MySQLError
	if errors.As(err, &me) {
		switch me.Number {
		case 1044, 1142, 1143, 1227:
			user, host := current, "%"
			if i := strings.LastIndex(current, "@"); i >= 0 {
				user, host = current[:i], current[i+1:]
			}
			return l10n.Errorf(code.DumpAccountsPrivilege, ErrPrivilege, me.Message, user, host)
		}
	}
	return l10n.Errorf(code.DumpAccounts, err)
}

// mariadbSandbox MariaDB 10.11.8 起 mysqldump 输出的第一行。MySQL 官方的 mysql 客户端把其中的 \- 当作未知命令，
// 导入在第一行就失败；去掉它不影响 MariaDB 客户端导入
const mariadbSandbox = `/*M!999999\- enable the sandbox mode */`

// dropSandboxLine 输出以 mariadbSandbox 开头时去掉第一行，其余内容原样透传
func dropSandboxLine(r io.Reader) io.Reader {
	return &sandboxFilter{br: bufio.NewReader(r)}
}

type sandboxFilter struct {
	br      *bufio.Reader
	checked bool
}

func (f *sandboxFilter) Read(p []byte) (int, error) {
	if !f.checked {
		f.checked = true
		// 输出不足一行时 Peek 返回错误，交给下面的 Read 原样返回
		if head, err := f.br.Peek(len(mariadbSandbox)); err == nil && bytes.Equal(head, []byte(mariadbSandbox)) {
			if _, err := f.br.ReadSlice('\n'); err != nil && !errors.Is(err, bufio.ErrBufferFull) {
				return 0, err
			}
		}
	}
	return f.br.Read(p)
}
