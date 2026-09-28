package datasource_svc

import (
	"context"
	"database/sql"
	"fmt"

	api "github.com/opskat/opsnap/internal/api/datasource"
	"github.com/opskat/opsnap/internal/pkg/dsconn"
)

// DatabaseLister 对已打开的连接实时列出数据库名与数据量；可替换为测试用的假实现（SetDatabaseLister），
// 使控制器测试不依赖真实的 MySQL / PostgreSQL 连接
type DatabaseLister func(ctx context.Context, typ dsconn.Type, conn *dsconn.Conn) ([]api.Database, error)

// ListDatabases DatabaseLister 的真实实现（docs/specs/2026-09-27-backup-jobs.md「第 2 步：内容与方式」）：
// MySQL 的“整个实例”不包含 information_schema、performance_schema、sys、mysql；
// PostgreSQL 的“整个实例”指所有允许连接的非模板库
func ListDatabases(ctx context.Context, typ dsconn.Type, conn *dsconn.Conn) ([]api.Database, error) {
	switch typ {
	case dsconn.TypeMySQL:
		return listMySQLDatabases(ctx, conn.DB)
	case dsconn.TypePostgreSQL:
		return listPostgresDatabases(ctx, conn.DB)
	default:
		return nil, fmt.Errorf("不支持读取数据库列表的数据源类型 %q", typ)
	}
}

// ListOpenDatabases 用当前的库列表实现（ListDatabases，测试中可由 SetDatabaseLister 替换）列出已打开连接上的库，
// 供备份运行时确认“整个实例”的范围与“指定数据库”是否仍存在
func ListOpenDatabases(ctx context.Context, typ dsconn.Type, conn *dsconn.Conn) ([]api.Database, error) {
	return defaultDataSource.databaseLister()(ctx, typ, conn)
}

// listMySQLDatabases 按库聚合全部表的 data_length + index_length，不含系统库；没有表的库数据量为 0
func listMySQLDatabases(ctx context.Context, db *sql.DB) ([]api.Database, error) {
	const query = `SELECT s.schema_name, CAST(COALESCE(SUM(t.data_length + t.index_length), 0) AS UNSIGNED)
		FROM information_schema.schemata s
		LEFT JOIN information_schema.tables t ON t.table_schema = s.schema_name
		WHERE s.schema_name NOT IN ('mysql', 'information_schema', 'performance_schema', 'sys')
		GROUP BY s.schema_name
		ORDER BY s.schema_name`
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanDatabases(rows)
}

// listPostgresDatabases 非模板、允许连接的库；当前账号没有连接权限的库数据量记为 0，不因此整体失败
func listPostgresDatabases(ctx context.Context, db *sql.DB) ([]api.Database, error) {
	const query = `SELECT d.datname,
		CASE WHEN pg_catalog.has_database_privilege(d.datname, 'CONNECT')
			THEN pg_catalog.pg_database_size(d.datname) ELSE 0 END
		FROM pg_catalog.pg_database d
		WHERE d.datistemplate = false AND d.datallowconn = true
		ORDER BY d.datname`
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanDatabases(rows)
}

func scanDatabases(rows *sql.Rows) ([]api.Database, error) {
	out := make([]api.Database, 0)
	for rows.Next() {
		var d api.Database
		if err := rows.Scan(&d.Name, &d.Size); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
