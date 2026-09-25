package probe

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/opskat/opsnap/internal/pkg/dsconn"
)

// mysqlBinlogRetentionThreshold 7 天，单位秒（spec 中的默认阈值）
const mysqlBinlogRetentionThreshold = 7 * 24 * 3600

// mysqlMaxNonInnoDBTables 非 InnoDB 表最多列出的表名数
const mysqlMaxNonInnoDBTables = 5

// mysqlItems MySQL 的 8 项探测：版本、binlog、binlog 格式、GTID、binlog 保留时长、复制权限、非 InnoDB 表、主控端 mysqldump
func mysqlItems(ctx context.Context, conn *dsconn.Conn) []Item {
	items := make([]Item, 0, 8)
	items = append(items, decideMySQLVersion(conn.Info.Version))

	var logBin int64
	if err := scanOne(ctx, conn.DB, "SELECT @@GLOBAL.log_bin", &logBin); err != nil {
		items = append(items, queryErrorItem("mysql.binlog", err))
	} else {
		items = append(items, decideMySQLBinlog(logBin != 0))
	}

	var format string
	if err := scanOne(ctx, conn.DB, "SELECT @@GLOBAL.binlog_format", &format); err != nil {
		items = append(items, queryErrorItem("mysql.binlog_format", err))
	} else {
		items = append(items, decideMySQLBinlogFormat(format))
	}

	var gtidMode string
	if err := scanOne(ctx, conn.DB, "SELECT @@GLOBAL.gtid_mode", &gtidMode); err != nil {
		items = append(items, queryErrorItem("mysql.gtid", err))
	} else {
		items = append(items, decideMySQLGTID(gtidMode))
	}

	var retention int64
	if err := scanOne(ctx, conn.DB, "SELECT @@GLOBAL.binlog_expire_logs_seconds", &retention); err != nil {
		items = append(items, queryErrorItem("mysql.binlog_retention", err))
	} else {
		items = append(items, decideMySQLBinlogRetention(retention))
	}

	if item, err := mysqlReplicationPrivilegesItem(ctx, conn.DB); err != nil {
		items = append(items, queryErrorItem("mysql.replication_privileges", err))
	} else {
		items = append(items, item)
	}

	if item, err := mysqlNonInnoDBTablesItem(ctx, conn.DB); err != nil {
		items = append(items, queryErrorItem("mysql.non_innodb_tables", err))
	} else {
		items = append(items, item)
	}

	items = append(items, decideMySQLDump(conn.Info.Version, lookupTool(ctx, "mysqldump")))
	return items
}

// scanOne 执行只返回一行一列的只读查询
func scanOne(ctx context.Context, db *sql.DB, query string, dest any) error {
	return db.QueryRowContext(ctx, query).Scan(dest)
}

// mysqlCurrentUser 读取 CURRENT_USER()，拆成用户名与允许来源的主机，用于把修复方法中的账号换成实际值
func mysqlCurrentUser(ctx context.Context, db *sql.DB) (user, host string, err error) {
	var current string
	if err := scanOne(ctx, db, "SELECT CURRENT_USER()", &current); err != nil {
		return "", "", err
	}
	u, h, _ := strings.Cut(current, "@")
	return u, h, nil
}

// mysqlReplicationPrivilegesItem 检查当前账号是否同时具备 REPLICATION SLAVE 与 REPLICATION CLIENT（全局授权）
func mysqlReplicationPrivilegesItem(ctx context.Context, db *sql.DB) (Item, error) {
	user, host, err := mysqlCurrentUser(ctx, db)
	if err != nil {
		return Item{}, err
	}
	rows, err := db.QueryContext(ctx, "SHOW GRANTS FOR CURRENT_USER()")
	if err != nil {
		return Item{}, err
	}
	defer func() { _ = rows.Close() }()
	var hasSlave, hasClient bool
	for rows.Next() {
		var grant string
		if err := rows.Scan(&grant); err != nil {
			return Item{}, err
		}
		if !strings.Contains(grant, " ON *.* ") {
			continue
		}
		upper := strings.ToUpper(grant)
		if strings.Contains(upper, "ALL PRIVILEGES") {
			hasSlave, hasClient = true, true
			continue
		}
		hasSlave = hasSlave || strings.Contains(upper, "REPLICATION SLAVE")
		hasClient = hasClient || strings.Contains(upper, "REPLICATION CLIENT")
	}
	if err := rows.Err(); err != nil {
		return Item{}, err
	}
	return decideMySQLReplicationPrivileges(hasSlave, hasClient, user, host), nil
}

// mysqlNonInnoDBTablesItem 列出用户库中引擎不是 InnoDB 的表（不含系统库）
func mysqlNonInnoDBTablesItem(ctx context.Context, db *sql.DB) (Item, error) {
	const query = `SELECT table_schema, table_name FROM information_schema.tables
		WHERE table_type = 'BASE TABLE' AND engine IS NOT NULL AND engine <> 'InnoDB'
		AND table_schema NOT IN ('mysql', 'information_schema', 'performance_schema', 'sys')
		ORDER BY table_schema, table_name`
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return Item{}, err
	}
	defer func() { _ = rows.Close() }()
	var tables []string
	for rows.Next() {
		var schema, name string
		if err := rows.Scan(&schema, &name); err != nil {
			return Item{}, err
		}
		tables = append(tables, schema+"."+name)
	}
	if err := rows.Err(); err != nil {
		return Item{}, err
	}
	return decideMySQLNonInnoDBTables(tables, len(tables)), nil
}

func decideMySQLVersion(version string) Item {
	return Item{
		Key:   "mysql.version",
		Title: itemTitles["mysql.version"],
		Tier:  TierOK,
		Detail: Text{
			ZhCN: fmt.Sprintf("已读到服务端版本 %s", version),
			En:   fmt.Sprintf("Read server version %s.", version),
		},
	}
}

func decideMySQLBinlog(enabled bool) Item {
	if enabled {
		return Item{Key: "mysql.binlog", Title: itemTitles["mysql.binlog"], Tier: TierOK, Detail: Text{
			ZhCN: "binlog 已开启（log_bin = ON）",
			En:   "Binlog is enabled (log_bin = ON).",
		}}
	}
	return Item{Key: "mysql.binlog", Title: itemTitles["mysql.binlog"], Tier: TierFail, Detail: Text{
		ZhCN: "binlog 未开启（log_bin = OFF），增量备份不可用",
		En:   "Binlog is disabled (log_bin = OFF); incremental backup is unavailable.",
	}, Fix: Text{
		ZhCN: "在 my.cnf 中设置 log_bin（需重启）",
		En:   "Set log_bin in my.cnf (requires a restart).",
	}}
}

func decideMySQLBinlogFormat(format string) Item {
	if format == "ROW" {
		return Item{Key: "mysql.binlog_format", Title: itemTitles["mysql.binlog_format"], Tier: TierOK, Detail: Text{
			ZhCN: "binlog_format = ROW",
			En:   "binlog_format = ROW.",
		}}
	}
	return Item{Key: "mysql.binlog_format", Title: itemTitles["mysql.binlog_format"], Tier: TierFail, Detail: Text{
		ZhCN: fmt.Sprintf("binlog_format 为 %s，不是 ROW，增量备份不可用", format),
		En:   fmt.Sprintf("binlog_format is %s, not ROW; incremental backup is unavailable.", format),
	}, Fix: Text{
		ZhCN: "SET PERSIST binlog_format = 'ROW';",
		En:   "SET PERSIST binlog_format = 'ROW';",
	}}
}

func decideMySQLGTID(mode string) Item {
	if mode == "ON" {
		return Item{Key: "mysql.gtid", Title: itemTitles["mysql.gtid"], Tier: TierOK, Detail: Text{
			ZhCN: "gtid_mode = ON",
			En:   "gtid_mode = ON.",
		}}
	}
	return Item{Key: "mysql.gtid", Title: itemTitles["mysql.gtid"], Tier: TierWarn, Detail: Text{
		ZhCN: fmt.Sprintf("gtid_mode 未开启（当前 %s），增量只能按位点续传", mode),
		En:   fmt.Sprintf("gtid_mode is not ON (currently %s); incremental backup can only resume by binlog position.", mode),
	}, Fix: Text{
		ZhCN: "在 my.cnf 中设置 gtid_mode=ON、enforce_gtid_consistency=ON（需重启）",
		En:   "Set gtid_mode=ON and enforce_gtid_consistency=ON in my.cnf (requires a restart).",
	}}
}

func decideMySQLBinlogRetention(seconds int64) Item {
	if seconds == 0 || seconds >= mysqlBinlogRetentionThreshold {
		var detail, detailEn string
		if seconds == 0 {
			detail = "binlog 永不过期（binlog_expire_logs_seconds = 0）"
			detailEn = "Binlog never expires (binlog_expire_logs_seconds = 0)."
		} else {
			detail = fmt.Sprintf("binlog 保留 %d 秒（约 %.1f 天），不少于 7 天", seconds, float64(seconds)/86400)
			detailEn = fmt.Sprintf("Binlog is kept for %d seconds (about %.1f days), at least 7 days.", seconds, float64(seconds)/86400)
		}
		return Item{Key: "mysql.binlog_retention", Title: itemTitles["mysql.binlog_retention"], Tier: TierOK,
			Detail: Text{ZhCN: detail, En: detailEn}}
	}
	return Item{Key: "mysql.binlog_retention", Title: itemTitles["mysql.binlog_retention"], Tier: TierWarn, Detail: Text{
		ZhCN: fmt.Sprintf("binlog 只保留 %d 秒（约 %.1f 天），少于 7 天，中断超过这个时长就无法续传", seconds, float64(seconds)/86400),
		En:   fmt.Sprintf("Binlog is kept for only %d seconds (about %.1f days), less than 7 days; a longer outage cannot resume.", seconds, float64(seconds)/86400),
	}, Fix: Text{
		ZhCN: "SET PERSIST binlog_expire_logs_seconds = 604800;",
		En:   "SET PERSIST binlog_expire_logs_seconds = 604800;",
	}}
}

func decideMySQLReplicationPrivileges(hasSlave, hasClient bool, user, host string) Item {
	if hasSlave && hasClient {
		return Item{Key: "mysql.replication_privileges", Title: itemTitles["mysql.replication_privileges"], Tier: TierOK, Detail: Text{
			ZhCN: "已具备 REPLICATION SLAVE 与 REPLICATION CLIENT",
			En:   "Has both REPLICATION SLAVE and REPLICATION CLIENT.",
		}}
	}
	grant := fmt.Sprintf("GRANT REPLICATION SLAVE, REPLICATION CLIENT ON *.* TO '%s'@'%s';", user, host)
	return Item{Key: "mysql.replication_privileges", Title: itemTitles["mysql.replication_privileges"], Tier: TierFail, Detail: Text{
		ZhCN: "缺少 REPLICATION SLAVE 或 REPLICATION CLIENT，增量备份不可用",
		En:   "Missing REPLICATION SLAVE or REPLICATION CLIENT; incremental backup is unavailable.",
	}, Fix: Text{ZhCN: grant, En: grant}}
}

func decideMySQLNonInnoDBTables(tables []string, total int) Item {
	if total == 0 {
		return Item{Key: "mysql.non_innodb_tables", Title: itemTitles["mysql.non_innodb_tables"], Tier: TierOK, Detail: Text{
			ZhCN: "没有非 InnoDB 表",
			En:   "No non-InnoDB tables.",
		}}
	}
	shown := tables
	if len(shown) > mysqlMaxNonInnoDBTables {
		shown = shown[:mysqlMaxNonInnoDBTables]
	}
	return Item{Key: "mysql.non_innodb_tables", Title: itemTitles["mysql.non_innodb_tables"], Tier: TierWarn, Detail: Text{
		ZhCN: fmt.Sprintf("存在 %d 张非 InnoDB 表，全量备份时需要锁表：%s", total, strings.Join(shown, "、")),
		En:   fmt.Sprintf("%d non-InnoDB tables exist; a full backup needs to lock tables: %s", total, strings.Join(shown, ", ")),
	}, Tables: shown, TableCount: total}
}

func decideMySQLDump(serverVersion string, tool toolStatus) Item {
	if !tool.Found {
		return Item{Key: "mysql.mysqldump", Title: itemTitles["mysql.mysqldump"], Tier: TierFail, Detail: Text{
			ZhCN: "未在 PATH 或 tools.dir 中找到 mysqldump，全量备份不可用",
			En:   "mysqldump was not found in PATH or tools.dir; full backup is unavailable.",
		}, Fix: mysqlDumpFix}
	}
	sMajor, sMinor, _ := parseMajorMinor(serverVersion)
	if tool.Err != nil {
		return Item{Key: "mysql.mysqldump", Title: itemTitles["mysql.mysqldump"], Tier: TierWarn, Detail: Text{
			ZhCN: fmt.Sprintf("找到 mysqldump，但无法确定其版本：%s", tool.Err),
			En:   fmt.Sprintf("Found mysqldump, but could not determine its version: %s", tool.Err),
		}, Fix: mysqlDumpFix}
	}
	if tool.Major < sMajor || (tool.Major == sMajor && tool.Minor < sMinor) {
		return Item{Key: "mysql.mysqldump", Title: itemTitles["mysql.mysqldump"], Tier: TierWarn, Detail: Text{
			ZhCN: fmt.Sprintf("本机 mysqldump（%s）版本低于服务端（%s）", tool.Raw, serverVersion),
			En:   fmt.Sprintf("The local mysqldump (%s) is older than the server (%s).", tool.Raw, serverVersion),
		}, Fix: mysqlDumpFix}
	}
	return Item{Key: "mysql.mysqldump", Title: itemTitles["mysql.mysqldump"], Tier: TierOK, Detail: Text{
		ZhCN: fmt.Sprintf("已找到 mysqldump（%s），版本不低于服务端", tool.Raw),
		En:   fmt.Sprintf("Found mysqldump (%s), not older than the server.", tool.Raw),
	}}
}

var mysqlDumpFix = Text{
	ZhCN: "安装匹配版本的 MySQL 客户端，或使用 OpsNap Docker 镜像",
	En:   "Install a matching MySQL client, or use the OpsNap Docker image.",
}
