package probe

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

var errBoom = errors.New("boom")

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
		item := decideMySQLDump("8.0.36", toolStatus{Found: true, Major: 8, Minor: 0, Raw: "mysqldump  Ver 8.0.40"})
		assert.Equal(t, TierOK, item.Tier)
	})
	t.Run("版本更高也可用", func(t *testing.T) {
		item := decideMySQLDump("8.0.36", toolStatus{Found: true, Major: 8, Minor: 1, Raw: "mysqldump  Ver 8.1.0"})
		assert.Equal(t, TierOK, item.Tier)
	})
	t.Run("版本更低为风险", func(t *testing.T) {
		item := decideMySQLDump("8.1.0", toolStatus{Found: true, Major: 8, Minor: 0, Raw: "mysqldump  Ver 8.0.40"})
		assert.Equal(t, TierWarn, item.Tier)
	})
	t.Run("找到但无法识别版本视为风险", func(t *testing.T) {
		item := decideMySQLDump("8.0.36", toolStatus{Found: true, Err: errBoom})
		assert.Equal(t, TierWarn, item.Tier)
	})
}
