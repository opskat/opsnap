package dsconn

import (
	"context"
	"crypto/tls"
	"database/sql"
	"errors"
	"net/url"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
)

// openDB 打开连接池并在一条连接上完成认证、读取版本与 TLS 状态
func openDB(ctx context.Context, d Dialer, c Config) (*Conn, error) {
	tlsCfg, err := c.tlsConfig()
	if err != nil {
		return nil, err
	}
	var db *sql.DB
	if c.Type == TypeMySQL {
		db, err = mysqlDB(d, c, tlsCfg)
	} else {
		db, err = postgresDB(d, c, tlsCfg)
	}
	if err != nil {
		return nil, wrapError(ctx, err, c.Password)
	}
	info, err := readInfo(ctx, db, c)
	if err != nil {
		_ = db.Close()
		return nil, wrapError(ctx, err, c.Password)
	}
	return &Conn{Info: info, DB: db}, nil
}

func (m TLSMode) prefer() bool { return m == "" || m == TLSPrefer }

func mysqlDB(d Dialer, c Config, tlsCfg *tls.Config) (*sql.DB, error) {
	mc := mysql.NewConfig()
	mc.User = c.User
	mc.Passwd = c.Password
	mc.Net = "tcp"
	mc.Addr = c.addr()
	mc.DialFunc = dialVia(d)
	mc.TLS = tlsCfg
	mc.AllowFallbackToPlaintext = c.TLS.Mode.prefer()
	// 驱动默认把异常写到标准错误，这里不输出，错误经返回值上报
	mc.Logger = &mysql.NopLogger{}
	connector, err := mysql.NewConnector(mc)
	if err != nil {
		return nil, err
	}
	return sql.OpenDB(connector), nil
}

func postgresDB(d Dialer, c Config, tlsCfg *tls.Config) (*sql.DB, error) {
	database := c.Database
	if database == "" {
		database = DefaultPGDatabase
	}
	// 连接串只放不含秘密的字段，密码与 TLS 在解析后直接设置
	u := url.URL{Scheme: "postgres", User: url.User(c.User), Host: c.addr(), Path: "/" + database, RawQuery: "sslmode=disable"}
	pc, err := pgx.ParseConfig(u.String())
	if err != nil {
		return nil, err
	}
	pc.Password = c.Password
	pc.TLSConfig = tlsCfg
	pc.Fallbacks = nil
	if tlsCfg != nil && c.TLS.Mode.prefer() {
		pc.Fallbacks = []*pgconn.FallbackConfig{{Host: pc.Host, Port: pc.Port}}
	}
	pc.DialFunc = dialVia(d)
	// 主机名交给链路末端解析，不在 OpsNap 本机查询
	pc.LookupFunc = func(_ context.Context, host string) ([]string, error) { return []string{host}, nil }
	return stdlib.OpenDB(*pc), nil
}

// readInfo 在同一条连接上读取服务端版本与 TLS 状态（均为只读查询）
func readInfo(ctx context.Context, db *sql.DB, c Config) (Info, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return Info{}, err
	}
	defer func() { _ = conn.Close() }()
	var info Info
	var tlsVersion string
	if c.Type == TypeMySQL {
		var name string
		if err := conn.QueryRowContext(ctx, "SELECT VERSION()").Scan(&info.Version); err != nil {
			return Info{}, err
		}
		if err := conn.QueryRowContext(ctx, "SHOW SESSION STATUS LIKE 'Ssl_version'").Scan(&name, &tlsVersion); err != nil {
			return Info{}, err
		}
	} else {
		if err := conn.QueryRowContext(ctx, "SHOW server_version").Scan(&info.Version); err != nil {
			return Info{}, err
		}
		err := conn.QueryRowContext(ctx, "SELECT COALESCE(version, '') FROM pg_stat_ssl WHERE pid = pg_backend_pid() AND ssl").Scan(&tlsVersion)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return Info{}, err
		}
	}
	if tlsVersion != "" {
		info.TLS = &TLSInfo{Version: tlsVersion, Verified: c.TLS.Mode.verifies()}
	}
	return info, nil
}
