package probe

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/dsconn"
	"github.com/opskat/opsnap/internal/pkg/l10n"
)

var errBoom = errors.New("boom")

// ---- 假数据库：只回答形如 "SELECT @@GLOBAL.xxx" 的单行单列查询，用于 mysqlBinlogRetentionItem 的单元测试 ----

// queryAnswer 按查询中包含的变量名（如 "@@GLOBAL.expire_logs_days"）给出该行该列的值
type queryAnswer map[string]any

type fakeRetentionConn struct{ answer queryAnswer }

func (c fakeRetentionConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("不支持预处理")
}
func (c fakeRetentionConn) Close() error              { return nil }
func (c fakeRetentionConn) Begin() (driver.Tx, error) { return nil, errors.New("不支持事务") }

func (c fakeRetentionConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	for k, v := range c.answer {
		if strings.Contains(query, k) {
			return &fakeRetentionRows{val: v}, nil
		}
	}
	return nil, fmt.Errorf("意外的查询 %q", query)
}

type fakeRetentionRows struct {
	val  any
	done bool
}

func (r *fakeRetentionRows) Columns() []string { return []string{"v"} }
func (r *fakeRetentionRows) Close() error      { return nil }
func (r *fakeRetentionRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	dest[0] = r.val
	return nil
}

type fakeRetentionConnector struct{ answer queryAnswer }

func (c fakeRetentionConnector) Connect(context.Context) (driver.Conn, error) {
	return fakeRetentionConn(c), nil
}
func (c fakeRetentionConnector) Driver() driver.Driver { return fakeRetentionDriver{} }

type fakeRetentionDriver struct{}

func (fakeRetentionDriver) Open(string) (driver.Conn, error) { return nil, errors.New("不支持") }

// fakeMySQLConn 构造一个只答 answer 中查询的 *dsconn.Conn，Info.Version 为 version
func fakeMySQLConn(t *testing.T, version string, answer queryAnswer) *dsconn.Conn {
	t.Helper()
	db := sql.OpenDB(fakeRetentionConnector{answer: answer})
	t.Cleanup(func() { _ = db.Close() })
	return &dsconn.Conn{Info: dsconn.Info{Version: version}, DB: db}
}

func TestDecideMySQLVersion(t *testing.T) {
	item := decideMySQLVersion("8.0.36")
	assert.Equal(t, "mysql.version", item.Key)
	assert.Equal(t, TierOK, item.Tier)
	assert.Contains(t, item.Detail.ZhCN, "8.0.36")
	assert.Contains(t, item.Detail.En, "8.0.36")
}

func TestDecideMySQLBinlog(t *testing.T) {
	t.Run("开启", func(t *testing.T) {
		item := decideMySQLBinlog(true)
		assert.Equal(t, TierOK, item.Tier)
		assert.Empty(t, item.Fix.ZhCN)
	})
	t.Run("未开启", func(t *testing.T) {
		item := decideMySQLBinlog(false)
		assert.Equal(t, TierFail, item.Tier)
		assert.Contains(t, item.Fix.ZhCN, "log_bin")
		assert.NotEmpty(t, item.Fix.En)
	})
}

func TestDecideMySQLBinlogFormat(t *testing.T) {
	t.Run("ROW", func(t *testing.T) {
		item := decideMySQLBinlogFormat("ROW")
		assert.Equal(t, TierOK, item.Tier)
	})
	t.Run("非 ROW", func(t *testing.T) {
		item := decideMySQLBinlogFormat("STATEMENT")
		assert.Equal(t, TierFail, item.Tier)
		assert.Contains(t, item.Detail.ZhCN, "STATEMENT")
		assert.Contains(t, item.Fix.ZhCN, "binlog_format")
	})
}

func TestDecideMySQLGTID(t *testing.T) {
	t.Run("开启", func(t *testing.T) {
		item := decideMySQLGTID("ON")
		assert.Equal(t, TierOK, item.Tier)
		assert.Empty(t, item.Fix.ZhCN)
	})
	t.Run("未开启只降为风险不是不可用", func(t *testing.T) {
		item := decideMySQLGTID("OFF")
		assert.Equal(t, TierWarn, item.Tier)
		assert.Contains(t, item.Fix.ZhCN, "gtid_mode")
	})
}

func TestDecideMySQLBinlogRetention(t *testing.T) {
	t.Run("不少于 7 天", func(t *testing.T) {
		item := decideMySQLBinlogRetention(7 * 24 * 3600)
		assert.Equal(t, TierOK, item.Tier)
	})
	t.Run("0 表示永不过期视为可用", func(t *testing.T) {
		item := decideMySQLBinlogRetention(0)
		assert.Equal(t, TierOK, item.Tier)
	})
	t.Run("少于 7 天为风险", func(t *testing.T) {
		item := decideMySQLBinlogRetention(3 * 24 * 3600)
		assert.Equal(t, TierWarn, item.Tier)
		assert.Contains(t, item.Fix.ZhCN, "604800")
	})
}

// TestMySQLBinlogRetentionItem 5.7 服务端没有 8.0 才引入的 binlog_expire_logs_seconds，
// 只有按天计的 expire_logs_days；探测须按服务端版本挑变量，否则 5.7 上这一项会报查询错误而不是给出三档判定
func TestMySQLBinlogRetentionItem(t *testing.T) {
	t.Run("8.0 起用 binlog_expire_logs_seconds（秒）", func(t *testing.T) {
		conn := fakeMySQLConn(t, "8.0.40", queryAnswer{"@@GLOBAL.binlog_expire_logs_seconds": int64(10 * 24 * 3600)})
		item, err := mysqlBinlogRetentionItem(context.Background(), conn)
		require.NoError(t, err)
		assert.Equal(t, TierOK, item.Tier)
	})
	t.Run("5.7 用 expire_logs_days（天），换算成秒", func(t *testing.T) {
		conn := fakeMySQLConn(t, "5.7.44", queryAnswer{"@@GLOBAL.expire_logs_days": int64(10)})
		item, err := mysqlBinlogRetentionItem(context.Background(), conn)
		require.NoError(t, err)
		assert.Equal(t, TierOK, item.Tier)
		assert.Contains(t, item.Detail.ZhCN, "864000")
		assertMentionsExpireLogsDaysNotSeconds(t, item)
	})
	t.Run("5.7 上 expire_logs_days 为 0 视为永不过期", func(t *testing.T) {
		conn := fakeMySQLConn(t, "5.7.44", queryAnswer{"@@GLOBAL.expire_logs_days": int64(0)})
		item, err := mysqlBinlogRetentionItem(context.Background(), conn)
		require.NoError(t, err)
		assert.Equal(t, TierOK, item.Tier)
		assertMentionsExpireLogsDaysNotSeconds(t, item)
	})
	t.Run("5.7 上保留天数不足 7 天为风险，说明与修复建议用 5.7 实际的 expire_logs_days", func(t *testing.T) {
		conn := fakeMySQLConn(t, "5.7.44", queryAnswer{"@@GLOBAL.expire_logs_days": int64(3)})
		item, err := mysqlBinlogRetentionItem(context.Background(), conn)
		require.NoError(t, err)
		assert.Equal(t, TierWarn, item.Tier)
		assertMentionsExpireLogsDaysNotSeconds(t, item)
		assert.Contains(t, item.Fix.ZhCN, "expire_logs_days")
		assert.Contains(t, item.Fix.En, "expire_logs_days")
		assert.NotContains(t, item.Fix.ZhCN, "SET PERSIST")
		assert.NotContains(t, item.Fix.En, "SET PERSIST")
		assert.NotContains(t, item.Fix.ZhCN, "binlog_expire_logs_seconds")
		assert.NotContains(t, item.Fix.En, "binlog_expire_logs_seconds")
	})
	t.Run("8.0 的说明与修复建议不变", func(t *testing.T) {
		conn := fakeMySQLConn(t, "8.0.40", queryAnswer{"@@GLOBAL.binlog_expire_logs_seconds": int64(3 * 24 * 3600)})
		item, err := mysqlBinlogRetentionItem(context.Background(), conn)
		require.NoError(t, err)
		assert.Equal(t, TierWarn, item.Tier)
		assert.Equal(t,
			"binlog 只保留 259200 秒（约 3.0 天），少于 7 天，中断超过这个时长就无法续传",
			item.Detail.ZhCN)
		assert.Equal(t,
			"Binlog is kept for only 259200 seconds (about 3.0 days), less than 7 days; a longer outage cannot resume.",
			item.Detail.En)
		assert.Equal(t, "SET PERSIST binlog_expire_logs_seconds = 604800;", item.Fix.ZhCN)
		assert.Equal(t, "SET PERSIST binlog_expire_logs_seconds = 604800;", item.Fix.En)
	})
}

// assertMentionsExpireLogsDaysNotSeconds 5.7 上 detail 文案（中英）须提到实际读到的 expire_logs_days，
// 不能提到 8.0 才有的 binlog_expire_logs_seconds
func assertMentionsExpireLogsDaysNotSeconds(t *testing.T, item Item) {
	t.Helper()
	assert.Contains(t, item.Detail.ZhCN, "expire_logs_days")
	assert.Contains(t, item.Detail.En, "expire_logs_days")
	assert.NotContains(t, item.Detail.ZhCN, "binlog_expire_logs_seconds")
	assert.NotContains(t, item.Detail.En, "binlog_expire_logs_seconds")
}

func TestDecideMySQLReplicationPrivileges(t *testing.T) {
	t.Run("两项都有", func(t *testing.T) {
		item := decideMySQLReplicationPrivileges(true, true, "repl", "%")
		assert.Equal(t, TierOK, item.Tier)
	})
	t.Run("缺一项即不可用", func(t *testing.T) {
		item := decideMySQLReplicationPrivileges(true, false, "repl", "%")
		assert.Equal(t, TierFail, item.Tier)
		assert.Contains(t, item.Fix.ZhCN, "GRANT REPLICATION SLAVE, REPLICATION CLIENT")
		assert.Contains(t, item.Fix.ZhCN, "'repl'@'%'", "修复方法必须能直接复制执行，不能留占位符")
		assert.NotContains(t, item.Fix.ZhCN, "<用户>")
	})
}

func TestDecideMySQLNonInnoDBTables(t *testing.T) {
	t.Run("没有", func(t *testing.T) {
		item := decideMySQLNonInnoDBTables(nil, 0)
		assert.Equal(t, TierOK, item.Tier)
		assert.Empty(t, item.Tables)
	})
	t.Run("存在时列出最多 5 张表名与总数", func(t *testing.T) {
		tables := []string{"a.t1", "a.t2", "a.t3", "a.t4", "a.t5", "a.t6", "a.t7"}
		item := decideMySQLNonInnoDBTables(tables, len(tables))
		assert.Equal(t, TierWarn, item.Tier)
		assert.Len(t, item.Tables, 5)
		assert.Equal(t, tables[:5], item.Tables)
		assert.Equal(t, 7, item.TableCount)
		assert.Contains(t, item.Detail.ZhCN, "7")
		assert.Empty(t, item.Fix.ZhCN, "该项没有修复方法")
	})
}

func TestDecideMySQLDump(t *testing.T) {
	t.Run("找不到", func(t *testing.T) {
		item := decideMySQLDump("8.0.36", toolStatus{Found: false})
		assert.Equal(t, TierFail, item.Tier)
		assert.Contains(t, item.Fix.ZhCN, "Docker")
	})
	t.Run("版本不低于服务端", func(t *testing.T) {
		item := decideMySQLDump("8.0.36", toolStatus{Found: true, Tool: Tool{Major: 8, Minor: 0, Raw: "mysqldump  Ver 8.0.40"}})
		assert.Equal(t, TierOK, item.Tier)
	})
	t.Run("版本更高也可用", func(t *testing.T) {
		item := decideMySQLDump("8.0.36", toolStatus{Found: true, Tool: Tool{Major: 8, Minor: 1, Raw: "mysqldump  Ver 8.1.0"}})
		assert.Equal(t, TierOK, item.Tier)
	})
	t.Run("版本更低为风险", func(t *testing.T) {
		item := decideMySQLDump("8.1.0", toolStatus{Found: true, Tool: Tool{Major: 8, Minor: 0, Raw: "mysqldump  Ver 8.0.40"}})
		assert.Equal(t, TierWarn, item.Tier)
	})
	t.Run("找到但无法识别版本视为风险", func(t *testing.T) {
		item := decideMySQLDump("8.0.36", toolStatus{Found: true, Err: errBoom})
		assert.Equal(t, TierWarn, item.Tier)
	})
	t.Run("无法识别版本的原因按两种语言给出，工具输出原样保留", func(t *testing.T) {
		item := decideMySQLDump("8.0.36", toolStatus{Found: true, Err: l10n.Errorf(code.ProbeVersionUnrecognized, "garbage")})
		assert.Equal(t, `找到 mysqldump，但无法确定其版本：无法从 "garbage" 中识别版本号`, item.Detail.ZhCN)
		assert.Equal(t, `Found mysqldump, but could not determine its version: Could not recognize a version number in "garbage"`,
			item.Detail.En)
	})
}
