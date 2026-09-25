package probe

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDecidePostgresVersion(t *testing.T) {
	item := decidePostgresVersion("16.4")
	assert.Equal(t, "postgres.version", item.Key)
	assert.Equal(t, TierOK, item.Tier)
	assert.Contains(t, item.Detail.ZhCN, "16.4")
}

func TestDecideWALLevel(t *testing.T) {
	for _, level := range []string{"replica", "logical"} {
		item := decideWALLevel(level)
		assert.Equalf(t, TierOK, item.Tier, "level=%s", level)
	}
	item := decideWALLevel("minimal")
	assert.Equal(t, TierFail, item.Tier)
	assert.Contains(t, item.Fix.ZhCN, "wal_level")
	assert.Contains(t, item.Fix.ZhCN, "重启")
}

func TestDecideMaxWALSenders(t *testing.T) {
	assert.Equal(t, TierOK, decideMaxWALSenders(10).Tier)
	item := decideMaxWALSenders(0)
	assert.Equal(t, TierFail, item.Tier)
	assert.Contains(t, item.Fix.ZhCN, "max_wal_senders")
}

func TestDecideReplicationSlotMargin(t *testing.T) {
	t.Run("有余量", func(t *testing.T) {
		item := decideReplicationSlotMargin(10, 5)
		assert.Equal(t, TierOK, item.Tier)
	})
	t.Run("没有余量：修复方法给出已用加二的具体数值", func(t *testing.T) {
		item := decideReplicationSlotMargin(5, 5)
		assert.Equal(t, TierFail, item.Tier)
		assert.Contains(t, item.Fix.ZhCN, "max_replication_slots = 7")
		assert.NotContains(t, item.Fix.ZhCN, "<已用")
	})
}

func TestDecideReplicationAttribute(t *testing.T) {
	t.Run("有属性", func(t *testing.T) {
		item := decideReplicationAttribute(true, "repl")
		assert.Equal(t, TierOK, item.Tier)
	})
	t.Run("没有：修复方法带具体角色名", func(t *testing.T) {
		item := decideReplicationAttribute(false, "repl")
		assert.Equal(t, TierFail, item.Tier)
		assert.Contains(t, item.Fix.ZhCN, "ALTER ROLE repl REPLICATION;")
	})
}

func TestDecidePgDump(t *testing.T) {
	t.Run("找不到", func(t *testing.T) {
		item := decidePgDump("16.4", toolStatus{Found: false})
		assert.Equal(t, TierFail, item.Tier)
		assert.Contains(t, item.Fix.ZhCN, "Docker")
	})
	t.Run("大版本不低于服务端", func(t *testing.T) {
		item := decidePgDump("16.4", toolStatus{Found: true, Major: 16, Raw: "pg_dump (PostgreSQL) 16.4"})
		assert.Equal(t, TierOK, item.Tier)
	})
	t.Run("大版本更高也可用", func(t *testing.T) {
		item := decidePgDump("16.4", toolStatus{Found: true, Major: 17, Raw: "pg_dump (PostgreSQL) 17.0"})
		assert.Equal(t, TierOK, item.Tier)
	})
	t.Run("大版本更低视为不可用（无风险档）", func(t *testing.T) {
		item := decidePgDump("16.4", toolStatus{Found: true, Major: 15, Raw: "pg_dump (PostgreSQL) 15.2"})
		assert.Equal(t, TierFail, item.Tier)
	})
}
