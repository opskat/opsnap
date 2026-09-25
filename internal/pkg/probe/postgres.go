package probe

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"

	"github.com/jackc/pgx/v5"

	"github.com/opskat/opsnap/internal/pkg/dsconn"
)

// postgresItems PostgreSQL 的 6 项探测：版本、wal_level、max_wal_senders、复制槽余量、REPLICATION 属性、主控端 pg_dump
func postgresItems(ctx context.Context, conn *dsconn.Conn) []Item {
	items := make([]Item, 0, 6)
	items = append(items, decidePostgresVersion(conn.Info.Version))

	var walLevel string
	if err := scanOne(ctx, conn.DB, "SHOW wal_level", &walLevel); err != nil {
		items = append(items, queryErrorItem("postgres.wal_level", err))
	} else {
		items = append(items, decideWALLevel(walLevel))
	}

	if item, err := postgresMaxWALSendersItem(ctx, conn.DB); err != nil {
		items = append(items, queryErrorItem("postgres.max_wal_senders", err))
	} else {
		items = append(items, item)
	}

	if item, err := postgresReplicationSlotsItem(ctx, conn.DB); err != nil {
		items = append(items, queryErrorItem("postgres.replication_slots", err))
	} else {
		items = append(items, item)
	}

	if item, err := postgresReplicationAttrItem(ctx, conn.DB); err != nil {
		items = append(items, queryErrorItem("postgres.replication_attr", err))
	} else {
		items = append(items, item)
	}

	items = append(items, decidePgDump(conn.Info.Version, lookupTool(ctx, "pg_dump")))
	return items
}

func postgresMaxWALSendersItem(ctx context.Context, db *sql.DB) (Item, error) {
	var n int64
	if err := scanOne(ctx, db, "SHOW max_wal_senders", &n); err != nil {
		return Item{}, err
	}
	return decideMaxWALSenders(n), nil
}

func postgresReplicationSlotsItem(ctx context.Context, db *sql.DB) (Item, error) {
	var maxSlots int64
	if err := scanOne(ctx, db, "SHOW max_replication_slots", &maxSlots); err != nil {
		return Item{}, err
	}
	var used int64
	if err := scanOne(ctx, db, "SELECT count(*) FROM pg_replication_slots", &used); err != nil {
		return Item{}, err
	}
	return decideReplicationSlotMargin(maxSlots, used), nil
}

func postgresReplicationAttrItem(ctx context.Context, db *sql.DB) (Item, error) {
	var role string
	if err := scanOne(ctx, db, "SELECT current_user", &role); err != nil {
		return Item{}, err
	}
	var hasAttr bool
	if err := scanOne(ctx, db, "SELECT rolsuper OR rolreplication FROM pg_roles WHERE rolname = current_user", &hasAttr); err != nil {
		return Item{}, err
	}
	return decideReplicationAttribute(hasAttr, role), nil
}

func decidePostgresVersion(version string) Item {
	return Item{
		Key:   "postgres.version",
		Title: itemTitles["postgres.version"],
		Tier:  TierOK,
		Detail: Text{
			ZhCN: fmt.Sprintf("已读到服务端版本 %s", version),
			En:   fmt.Sprintf("Read server version %s.", version),
		},
	}
}

func decideWALLevel(level string) Item {
	if level == "replica" || level == "logical" {
		return Item{Key: "postgres.wal_level", Title: itemTitles["postgres.wal_level"], Tier: TierOK, Detail: Text{
			ZhCN: fmt.Sprintf("wal_level = %s", level),
			En:   fmt.Sprintf("wal_level = %s.", level),
		}}
	}
	return Item{Key: "postgres.wal_level", Title: itemTitles["postgres.wal_level"], Tier: TierFail, Detail: Text{
		ZhCN: fmt.Sprintf("wal_level 为 %s，物理模式与 WAL 增量都不可用", level),
		En:   fmt.Sprintf("wal_level is %s; both physical mode and WAL-based incremental backup are unavailable.", level),
	}, Fix: Text{
		ZhCN: "ALTER SYSTEM SET wal_level = 'replica';（需重启）",
		En:   "ALTER SYSTEM SET wal_level = 'replica'; (requires a restart).",
	}}
}

func decideMaxWALSenders(n int64) Item {
	if n > 0 {
		return Item{Key: "postgres.max_wal_senders", Title: itemTitles["postgres.max_wal_senders"], Tier: TierOK, Detail: Text{
			ZhCN: fmt.Sprintf("max_wal_senders = %d", n),
			En:   fmt.Sprintf("max_wal_senders = %d.", n),
		}}
	}
	return Item{Key: "postgres.max_wal_senders", Title: itemTitles["postgres.max_wal_senders"], Tier: TierFail, Detail: Text{
		ZhCN: "max_wal_senders 为 0，物理模式与 WAL 增量都不可用",
		En:   "max_wal_senders is 0; both physical mode and WAL-based incremental backup are unavailable.",
	}, Fix: Text{
		ZhCN: "ALTER SYSTEM SET max_wal_senders = 10;（需重启）",
		En:   "ALTER SYSTEM SET max_wal_senders = 10; (requires a restart).",
	}}
}

func decideReplicationSlotMargin(maxSlots, used int64) Item {
	margin := maxSlots - used
	if margin >= 1 {
		return Item{Key: "postgres.replication_slots", Title: itemTitles["postgres.replication_slots"], Tier: TierOK, Detail: Text{
			ZhCN: fmt.Sprintf("max_replication_slots = %d，已用 %d，余量 %d", maxSlots, used, margin),
			En:   fmt.Sprintf("max_replication_slots = %d, %d used, %d free.", maxSlots, used, margin),
		}}
	}
	target := used + 2
	fix := fmt.Sprintf("ALTER SYSTEM SET max_replication_slots = %d;（需重启）", target)
	fixEn := fmt.Sprintf("ALTER SYSTEM SET max_replication_slots = %d; (requires a restart).", target)
	return Item{Key: "postgres.replication_slots", Title: itemTitles["postgres.replication_slots"], Tier: TierFail, Detail: Text{
		ZhCN: fmt.Sprintf("max_replication_slots = %d，已用 %d，没有余量，WAL 增量不可用", maxSlots, used),
		En:   fmt.Sprintf("max_replication_slots = %d, %d used, no free slot; WAL-based incremental backup is unavailable.", maxSlots, used),
	}, Fix: Text{ZhCN: fix, En: fixEn}}
}

func decideReplicationAttribute(hasAttr bool, role string) Item {
	if hasAttr {
		return Item{Key: "postgres.replication_attr", Title: itemTitles["postgres.replication_attr"], Tier: TierOK, Detail: Text{
			ZhCN: fmt.Sprintf("%s 具备 REPLICATION 属性或为超级用户", role),
			En:   fmt.Sprintf("%s has the REPLICATION attribute or is a superuser.", role),
		}}
	}
	fix := fmt.Sprintf("ALTER ROLE %s REPLICATION;", quoteIdent(role))
	return Item{Key: "postgres.replication_attr", Title: itemTitles["postgres.replication_attr"], Tier: TierFail, Detail: Text{
		ZhCN: fmt.Sprintf("%s 没有 REPLICATION 属性，物理模式不可用", role),
		En:   fmt.Sprintf("%s does not have the REPLICATION attribute; physical mode is unavailable.", role),
	}, Fix: Text{ZhCN: fix, En: fix}}
}

func decidePgDump(serverVersion string, tool toolStatus) Item {
	if !tool.Found {
		return Item{Key: "postgres.pg_dump", Title: itemTitles["postgres.pg_dump"], Tier: TierFail, Detail: Text{
			ZhCN: "未在 PATH 或 tools.dir 中找到 pg_dump，逻辑全量备份不可用",
			En:   "pg_dump was not found in PATH or tools.dir; logical full backup is unavailable.",
		}, Fix: pgDumpFix}
	}
	sMajor, _, _ := parseMajorMinor(serverVersion)
	if tool.Err != nil {
		return Item{Key: "postgres.pg_dump", Title: itemTitles["postgres.pg_dump"], Tier: TierFail, Detail: Text{
			ZhCN: fmt.Sprintf("找到 pg_dump，但无法确定其版本：%s", tool.Err),
			En:   fmt.Sprintf("Found pg_dump, but could not determine its version: %s", tool.Err),
		}, Fix: pgDumpFix}
	}
	if tool.Major < sMajor {
		return Item{Key: "postgres.pg_dump", Title: itemTitles["postgres.pg_dump"], Tier: TierFail, Detail: Text{
			ZhCN: fmt.Sprintf("本机 pg_dump（%s）大版本低于服务端（%s）", tool.Raw, serverVersion),
			En:   fmt.Sprintf("The local pg_dump (%s) has an older major version than the server (%s).", tool.Raw, serverVersion),
		}, Fix: pgDumpFix}
	}
	return Item{Key: "postgres.pg_dump", Title: itemTitles["postgres.pg_dump"], Tier: TierOK, Detail: Text{
		ZhCN: fmt.Sprintf("已找到 pg_dump（%s），大版本不低于服务端", tool.Raw),
		En:   fmt.Sprintf("Found pg_dump (%s), whose major version is not older than the server.", tool.Raw),
	}}
}

// simpleIdent 不需要加引号就能在 SQL 中原样使用的角色名（小写字母、数字、下划线、$，不以数字或 $ 开头）
var simpleIdent = regexp.MustCompile(`^[a-z_][a-z0-9_$]*$`)

// quoteIdent 角色名需要引号时（大写、连字符、引号等）按 SQL 标识符规则加双引号，使修复方法可以直接执行
func quoteIdent(name string) string {
	if simpleIdent.MatchString(name) {
		return name
	}
	return pgx.Identifier{name}.Sanitize()
}

var pgDumpFix = Text{
	ZhCN: "安装匹配大版本的 PostgreSQL 客户端，或使用 OpsNap Docker 镜像",
	En:   "Install a PostgreSQL client with a matching major version, or use the OpsNap Docker image.",
}
