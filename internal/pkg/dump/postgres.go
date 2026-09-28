package dump

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/opskat/opsnap/internal/pkg/dsconn"
	"github.com/opskat/opsnap/internal/pkg/probe"
)

// postgresPlan 每个库一份 pg_dump custom 格式归档，勾选时另用 pg_dumpall --globals-only 导出全局对象
type postgresPlan struct {
	src         Source
	opts        Options
	dump, globs *toolInfo
}

func (p *postgresPlan) tools(ctx context.Context, _ *Session) error {
	var err error
	if p.dump, err = pgTool(ctx, "pg_dump", p.src.ServerVersion); err != nil {
		return err
	}
	if p.opts.Globals {
		if p.globs, err = pgTool(ctx, "pg_dumpall", p.src.ServerVersion); err != nil {
			return err
		}
	}
	return nil
}

// pgTool 查找工具；大版本低于服务端时无法导出
func pgTool(ctx context.Context, name, server string) (*toolInfo, error) {
	t, err := findTool(ctx, name, "PostgreSQL 客户端")
	if err != nil {
		return nil, err
	}
	if smaj, _, ok := probe.ParseMajorMinor(server); ok && t.major < smaj {
		return nil, fmt.Errorf("%w：%s 大版本 %d 低于服务端 %d。修复：在主控端安装 PostgreSQL %d 或更新版本的客户端，放在 PATH 或 tools.dir 中",
			ErrToolVersion, name, t.major, smaj, smaj)
	}
	return t, nil
}

func (p *postgresPlan) prepare(ctx context.Context, s *Session) error {
	cfg := p.src.Config
	// 密码文件一行一条，没有办法表示换行
	if strings.ContainsAny(cfg.Password, "\r\n") {
		return fmt.Errorf("%w：密码含换行符，无法写入 pg_dump 的密码文件", ErrInvalidOptions)
	}
	pass, err := s.writeSecret("pgpass", []byte("*:*:*:*:"+pgpassEscape(cfg.Password)+"\n"))
	if err != nil {
		return err
	}
	files, err := s.writeTLS(cfg.TLS)
	if err != nil {
		return err
	}
	if err := p.checkExclusions(ctx, s); err != nil {
		return err
	}
	env := s.toolEnv("PGPASSFILE=" + pass)
	if p.opts.Globals {
		initial := cfg.Database
		if initial == "" {
			initial = dsconn.DefaultPGDatabase
		}
		args := []string{"--globals-only", "--no-password", "--database=" + initial, "--dbname=" + p.connString(s, files, "")}
		s.addTool(PostgresGlobalsFile, p.globs, args, env, checkPGGlobals, nil)
	}
	for _, db := range p.opts.Databases {
		args := []string{"--format=custom", "--compress=0", "--no-password", "--dbname=" + p.connString(s, files, db)}
		for _, ex := range p.opts.ExcludeTables {
			exDB, schema, table := splitPGRule(ex)
			if exDB == db {
				args = append(args, "--exclude-table="+pgIdent(schema)+"."+pgIdent(table))
			}
		}
		s.addTool(PostgresArchiveName(db), p.dump, args, env, checkPGArchive, nil)
	}
	return nil
}

// PostgresArchiveName 库 db 的归档在快照中的文件名：库名按 URL 路径规则转义（"/" 变为 %2F）后加 .dump
func PostgresArchiveName(db string) string { return url.PathEscape(db) + PostgresArchiveExt }

// connString libpq 连接串：经 hostaddr 连本机转发端口，host 保留数据源主机名供 verify-full 校验；不含密码
func (p *postgresPlan) connString(s *Session, files tlsFiles, db string) string {
	cfg := p.src.Config
	kv := [][2]string{
		{"host", cfg.Host}, {"hostaddr", "127.0.0.1"}, {"port", s.localPort()}, {"user", cfg.User},
	}
	if db != "" {
		kv = append(kv, [2]string{"dbname", db})
	}
	kv = append(kv, [2]string{"sslmode", pgSSLMode(cfg.TLS.Mode)}, [2]string{"gssencmode", "disable"},
		[2]string{"connect_timeout", "30"}, [2]string{"application_name", "opsnap"})
	for _, f := range [][2]string{{"sslrootcert", files.ca}, {"sslcert", files.cert}, {"sslkey", files.key}} {
		if f[1] != "" {
			kv = append(kv, f)
		}
	}
	parts := make([]string, len(kv))
	for i, x := range kv {
		parts[i] = x[0] + "='" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(x[1]) + "'"
	}
	return strings.Join(parts, " ")
}

func pgSSLMode(m dsconn.TLSMode) string {
	switch m {
	case dsconn.TLSDisable:
		return "disable"
	case dsconn.TLSRequire:
		return "require"
	case dsconn.TLSVerifyCA:
		return "verify-ca"
	case dsconn.TLSVerifyFull:
		return "verify-full"
	}
	return "prefer"
}

// pgpassEscape 密码文件中 : 与 \ 需要反斜杠转义
func pgpassEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `:`, `\:`).Replace(s)
}

// pgIdent 双引号包住的名字在 pg_dump 的模式里按字面匹配（区分大小写，* ? 不是通配符）
func pgIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

func splitPGRule(rule string) (db, schema, table string) {
	parts := strings.SplitN(rule, ".", 3)
	return parts[0], parts[1], parts[2]
}

// checkExclusions 排除规则未匹配任何表时写进运行日志；只为有排除规则的选中库打开连接
func (p *postgresPlan) checkExclusions(ctx context.Context, s *Session) error {
	matched := map[string]bool{}
	byDB := map[string][]string{}
	for _, ex := range p.opts.ExcludeTables {
		db, _, _ := splitPGRule(ex)
		byDB[db] = append(byDB[db], ex)
	}
	for _, db := range p.opts.Databases {
		rules := byDB[db]
		if len(rules) == 0 {
			continue
		}
		if err := p.matchIn(ctx, db, rules, matched); err != nil {
			return err
		}
	}
	logUnmatched(s, p.opts.ExcludeTables, matched)
	return nil
}

func (p *postgresPlan) matchIn(ctx context.Context, db string, rules []string, matched map[string]bool) error {
	conn, err := connect(ctx, p.src, db)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	for _, ex := range rules {
		_, schema, table := splitPGRule(ex)
		var n int64
		err := conn.QueryRowContext(ctx, "SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace "+
			"WHERE n.nspname = $1 AND c.relname = $2 AND c.relkind IN ('r', 'p', 'v', 'm', 'f')", schema, table).Scan(&n)
		if err != nil {
			return fmt.Errorf("检查排除规则 %s: %w", ex, err)
		}
		matched[ex] = n > 0
	}
	return nil
}
