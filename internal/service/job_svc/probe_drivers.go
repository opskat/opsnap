package job_svc

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const dialTimeout = 10 * time.Second

// netProbeError 网络层错误归类；不是网络错误返回 nil
func netProbeError(ctx context.Context, err error) *ProbeError {
	var ne net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded), ctx.Err() != nil && errors.Is(ctx.Err(), context.DeadlineExceeded):
		return &ProbeError{Reason: ReasonTimeout, Message: "连接超时"}
	case errors.As(err, &ne) && ne.Timeout():
		return &ProbeError{Reason: ReasonTimeout, Message: "连接超时"}
	case errors.As(err, &ne):
		return &ProbeError{Reason: ReasonUnreachable, Message: err.Error()}
	}
	return nil
}

// ---- MySQL / MariaDB ----

type mysqlProbe struct{}

func (mysqlProbe) open(t Target) *sql.DB {
	cfg := mysql.NewConfig()
	cfg.User, cfg.Passwd = t.User, t.Password
	cfg.Net, cfg.Addr = "tcp", t.Addr
	cfg.Timeout, cfg.ReadTimeout = dialTimeout, 30*time.Second
	cfg.TLSConfig = "preferred"
	conn, err := mysql.NewConnector(cfg)
	if err != nil {
		return nil
	}
	return sql.OpenDB(conn)
}

func mysqlError(ctx context.Context, err error) *ProbeError {
	var me *mysql.MySQLError
	if errors.As(err, &me) && (me.Number == 1045 || me.Number == 1044 || me.Number == 1698) {
		return &ProbeError{Reason: ReasonAuthFailed, Message: me.Message}
	}
	if pe := netProbeError(ctx, err); pe != nil {
		return pe
	}
	return &ProbeError{Reason: ReasonFailed, Message: err.Error()}
}

func (p mysqlProbe) ListDatabases(ctx context.Context, t Target) ([]Database, error) {
	db := p.open(t)
	if db == nil {
		return nil, &ProbeError{Reason: ReasonFailed, Message: "无法创建 MySQL 连接"}
	}
	defer func() { _ = db.Close() }()
	rows, err := db.QueryContext(ctx, `SELECT s.schema_name, COALESCE(SUM(t.data_length + t.index_length), 0)
		FROM information_schema.schemata s
		LEFT JOIN information_schema.tables t ON t.table_schema = s.schema_name
		GROUP BY s.schema_name ORDER BY s.schema_name`)
	if err != nil {
		return nil, mysqlError(ctx, err)
	}
	defer func() { _ = rows.Close() }()
	var out []Database
	for rows.Next() {
		var d Database
		if err := rows.Scan(&d.Name, &d.SizeBytes); err != nil {
			return nil, mysqlError(ctx, err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, mysqlError(ctx, err)
	}
	return out, nil
}

func (p mysqlProbe) Diagnose(ctx context.Context, t Target) Diagnosis {
	db := p.open(t)
	if db == nil {
		return Diagnosis{Reach: &ProbeError{Reason: ReasonFailed, Message: "无法创建 MySQL 连接"}}
	}
	defer func() { _ = db.Close() }()
	if err := db.PingContext(ctx); err != nil {
		pe := mysqlError(ctx, err)
		if pe.Reason == ReasonAuthFailed {
			return Diagnosis{Auth: pe}
		}
		return Diagnosis{Reach: pe}
	}
	rows, err := db.QueryContext(ctx, "SHOW GRANTS")
	if err != nil {
		return Diagnosis{Auth: mysqlError(ctx, err)}
	}
	defer func() { _ = rows.Close() }()
	var grants []string
	for rows.Next() {
		var g string
		if err := rows.Scan(&g); err != nil {
			return Diagnosis{Auth: mysqlError(ctx, err)}
		}
		grants = append(grants, g)
	}
	if err := rows.Err(); err != nil {
		return Diagnosis{Auth: mysqlError(ctx, err)}
	}
	return Diagnosis{Missing: missingMySQL(grants)}
}

// missingMySQL 从 SHOW GRANTS 的输出算出缺少的权限。
// 各条授权取并集（不区分全局还是某个库），所以只是宽松的预检，真正的导出仍可能因库级权限不全而失败。
func missingMySQL(grants []string) []string {
	have := map[string]bool{}
	for _, g := range grants {
		rest, ok := strings.CutPrefix(strings.TrimSpace(g), "GRANT ")
		if !ok {
			continue
		}
		list, _, ok := strings.Cut(rest, " ON ")
		if !ok {
			continue
		}
		for _, p := range strings.Split(list, ",") {
			p = strings.ToUpper(strings.TrimSpace(p))
			if p == "ALL PRIVILEGES" || p == "ALL" {
				return nil
			}
			have[p] = true
		}
	}
	var missing []string
	for _, p := range requiredMySQLPrivileges {
		if !have[p] {
			missing = append(missing, p)
		}
	}
	return missing
}

// ---- PostgreSQL ----

type postgresProbe struct{}

func (postgresProbe) open(t Target, database string) (*sql.DB, error) {
	host, port, err := net.SplitHostPort(t.Addr)
	if err != nil {
		return nil, err
	}
	cfg, err := pgx.ParseConfig("sslmode=prefer connect_timeout=10")
	if err != nil {
		return nil, err
	}
	p, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return nil, err
	}
	cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.Database = host, uint16(p), t.User, t.Password, database
	return stdlib.OpenDB(*cfg), nil
}

func pgError(ctx context.Context, err error) *ProbeError {
	var pe *pgconn.PgError
	if errors.As(err, &pe) && (pe.Code == "28P01" || pe.Code == "28000") {
		return &ProbeError{Reason: ReasonAuthFailed, Message: pe.Message}
	}
	if p := netProbeError(ctx, err); p != nil {
		return p
	}
	return &ProbeError{Reason: ReasonFailed, Message: err.Error()}
}

func (p postgresProbe) ListDatabases(ctx context.Context, t Target) ([]Database, error) {
	db, err := p.open(t, "postgres")
	if err != nil {
		return nil, &ProbeError{Reason: ReasonFailed, Message: err.Error()}
	}
	defer func() { _ = db.Close() }()
	rows, err := db.QueryContext(ctx,
		`SELECT datname, pg_database_size(datname) FROM pg_database WHERE datallowconn OR datistemplate ORDER BY datname`)
	if err != nil {
		return nil, pgError(ctx, err)
	}
	defer func() { _ = rows.Close() }()
	var out []Database
	for rows.Next() {
		var d Database
		if err := rows.Scan(&d.Name, &d.SizeBytes); err != nil {
			return nil, pgError(ctx, err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, pgError(ctx, err)
	}
	return out, nil
}

func (p postgresProbe) Diagnose(ctx context.Context, t Target) Diagnosis {
	db, err := p.open(t, "postgres")
	if err != nil {
		return Diagnosis{Reach: &ProbeError{Reason: ReasonFailed, Message: err.Error()}}
	}
	defer func() { _ = db.Close() }()
	if err := db.PingContext(ctx); err != nil {
		pe := pgError(ctx, err)
		if pe.Reason == ReasonAuthFailed {
			return Diagnosis{Auth: pe}
		}
		return Diagnosis{Reach: pe}
	}
	var ok bool
	// pg_read_all_data 从 PostgreSQL 14 起存在；更老的版本查询出错时不判缺权限
	err = db.QueryRowContext(ctx,
		`SELECT r.rolsuper OR pg_has_role(r.oid, 'pg_read_all_data', 'USAGE') FROM pg_roles r WHERE r.rolname = current_user`).Scan(&ok)
	if err != nil || ok {
		return Diagnosis{}
	}
	return Diagnosis{Missing: []string{"pg_read_all_data"}}
}

// ---- MongoDB ----

type mongoProbe struct{}

func (mongoProbe) connect(ctx context.Context, t Target) (*mongo.Client, error) {
	opts := options.Client().SetHosts([]string{t.Addr}).SetDirect(true).
		SetServerSelectionTimeout(dialTimeout).SetConnectTimeout(dialTimeout)
	if t.User != "" {
		opts.SetAuth(options.Credential{Username: t.User, Password: t.Password, AuthSource: t.AuthDB})
	}
	return mongo.Connect(ctx, opts)
}

func mongoError(ctx context.Context, err error) *ProbeError {
	var ce mongo.CommandError
	if errors.As(err, &ce) && (ce.Code == 18 || ce.Code == 13 || strings.Contains(ce.Message, "Authentication failed")) {
		return &ProbeError{Reason: ReasonAuthFailed, Message: ce.Message}
	}
	if strings.Contains(err.Error(), "Authentication failed") || strings.Contains(err.Error(), "AuthenticationFailed") {
		return &ProbeError{Reason: ReasonAuthFailed, Message: err.Error()}
	}
	if strings.Contains(err.Error(), "server selection error") {
		return &ProbeError{Reason: ReasonUnreachable, Message: err.Error()}
	}
	if p := netProbeError(ctx, err); p != nil {
		return p
	}
	return &ProbeError{Reason: ReasonFailed, Message: err.Error()}
}

func (p mongoProbe) ListDatabases(ctx context.Context, t Target) ([]Database, error) {
	c, err := p.connect(ctx, t)
	if err != nil {
		return nil, mongoError(ctx, err)
	}
	defer func() { _ = c.Disconnect(context.WithoutCancel(ctx)) }()
	res, err := c.ListDatabases(ctx, bson.D{})
	if err != nil {
		return nil, mongoError(ctx, err)
	}
	out := make([]Database, 0, len(res.Databases))
	for _, d := range res.Databases {
		out = append(out, Database{Name: d.Name, SizeBytes: d.SizeOnDisk})
	}
	return out, nil
}

func (p mongoProbe) Diagnose(ctx context.Context, t Target) Diagnosis {
	c, err := p.connect(ctx, t)
	if err != nil {
		return Diagnosis{Reach: mongoError(ctx, err)}
	}
	defer func() { _ = c.Disconnect(context.WithoutCancel(ctx)) }()
	if err := c.Ping(ctx, nil); err != nil {
		pe := mongoError(ctx, err)
		if pe.Reason == ReasonAuthFailed {
			return Diagnosis{Auth: pe}
		}
		return Diagnosis{Reach: pe}
	}
	if t.User == "" {
		return Diagnosis{}
	}
	var status struct {
		AuthInfo struct {
			Roles []struct {
				Role string `bson:"role"`
			} `bson:"authenticatedUserRoles"`
		} `bson:"authInfo"`
	}
	err = c.Database("admin").RunCommand(ctx, bson.D{{Key: "connectionStatus", Value: 1}}).Decode(&status)
	if err != nil {
		return Diagnosis{}
	}
	for _, r := range status.AuthInfo.Roles {
		switch r.Role {
		case "root", "backup", "__system", "readAnyDatabase":
			return Diagnosis{}
		}
	}
	return Diagnosis{Missing: []string{"backup"}}
}

// ---- Redis ----

type redisProbe struct{}

func (redisProbe) client(t Target) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr: t.Addr, Username: t.User, Password: t.Password,
		DialTimeout: dialTimeout, ReadTimeout: 30 * time.Second, MaxRetries: -1,
	})
}

func redisError(ctx context.Context, err error) *ProbeError {
	msg := err.Error()
	if strings.Contains(msg, "NOAUTH") || strings.Contains(msg, "WRONGPASS") || strings.Contains(msg, "invalid username-password") {
		return &ProbeError{Reason: ReasonAuthFailed, Message: msg}
	}
	if p := netProbeError(ctx, err); p != nil {
		return p
	}
	return &ProbeError{Reason: ReasonFailed, Message: msg}
}

// ListDatabases Redis 整个实例导出，没有库列表
func (redisProbe) ListDatabases(context.Context, Target) ([]Database, error) {
	return nil, nil
}

func (p redisProbe) Diagnose(ctx context.Context, t Target) Diagnosis {
	c := p.client(t)
	defer func() { _ = c.Close() }()
	if err := c.Ping(ctx).Err(); err != nil {
		pe := redisError(ctx, err)
		if pe.Reason == ReasonAuthFailed {
			return Diagnosis{Auth: pe}
		}
		return Diagnosis{Reach: pe}
	}
	return Diagnosis{}
}
