package migrations

import (
	"testing"

	"github.com/go-gormigrate/gormigrate/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthSettingsTables(t *testing.T) {
	db := openTempDB(t)
	require.NoError(t, RunMigrations(db))
	for _, table := range []string{"admin", "sessions", "api_keys", "settings"} {
		assert.True(t, db.Migrator().HasTable(table), table)
	}
	// API 密钥表只有哈希列，没有保存明文的列
	var cols []string
	require.NoError(t, db.Raw(`SELECT name FROM pragma_table_info('api_keys')`).Scan(&cols).Error)
	assert.Contains(t, cols, "key_hash")
	assert.NotContains(t, cols, "key")
	// 管理员和设置都是单行表
	require.NoError(t, db.Exec(`INSERT INTO admin (id, username, password_hash) VALUES (1, 'a', 'h')`).Error)
	assert.Error(t, db.Exec(`INSERT INTO admin (id, username, password_hash) VALUES (2, 'b', 'h')`).Error)
}

func TestJobsTable(t *testing.T) {
	db := openTempDB(t)
	require.NoError(t, RunMigrations(db))
	assert.True(t, db.Migrator().HasTable("jobs"))
	var cols []string
	require.NoError(t, db.Raw(`SELECT name FROM pragma_table_info('jobs')`).Scan(&cols).Error)
	assert.Contains(t, cols, "db_password")
	// 数据库密码只存密文列，没有明文列
	assert.NotContains(t, cols, "password")
	// 删除任务不牵连别的表：没有外键
	var fks int
	require.NoError(t, db.Raw(`SELECT COUNT(*) FROM pragma_foreign_key_list('jobs')`).Scan(&fks).Error)
	assert.Zero(t, fks)
}

func TestSSOTable(t *testing.T) {
	db := openTempDB(t)
	require.NoError(t, RunMigrations(db))
	assert.True(t, db.Migrator().HasTable("sso"))
	// 单行表；没有填写的行默认允许密码登录
	require.NoError(t, db.Exec(`INSERT INTO sso (id) VALUES (1)`).Error)
	assert.Error(t, db.Exec(`INSERT INTO sso (id) VALUES (2)`).Error)
	var pw int
	require.NoError(t, db.Raw(`SELECT password_login FROM sso WHERE id = 1`).Scan(&pw).Error)
	assert.Equal(t, 1, pw)
}

// TestBaselineNotifyPanelUrl 原独立迁移 202610150009 补的 panel_url 列已并入 notify 建表语句。
func TestBaselineNotifyPanelUrl(t *testing.T) {
	db := openTempDB(t)
	require.NoError(t, RunMigrations(db))
	var cols []string
	require.NoError(t, db.Raw(`SELECT name FROM pragma_table_info('notify')`).Scan(&cols).Error)
	assert.Contains(t, cols, "panel_url")
}

// TestBaselineCreatesAllTables 基线一次建出 v0.1 全部业务表。
func TestBaselineCreatesAllTables(t *testing.T) {
	db := openTempDB(t)
	require.NoError(t, RunMigrations(db))
	for _, table := range []string{
		"meta", "admin", "sessions", "api_keys", "settings",
		"notify", "servers", "repos", "jobs", "runs",
		"stale_alerts", "master_key", "sso",
	} {
		assert.True(t, db.Migrator().HasTable(table), table)
	}
}

// TestBaselineRollback 基线可整体回滚。
func TestBaselineRollback(t *testing.T) {
	db := openTempDB(t)
	require.NoError(t, RunMigrations(db))
	m := gormigrate.New(db, &gormigrate.Options{TableName: "migrations", IDColumnName: "id", IDColumnSize: 200, UseTransaction: true},
		[]*gormigrate.Migration{T202610180001()})
	require.NoError(t, m.RollbackMigration(T202610180001()))
	for _, table := range []string{
		"meta", "admin", "sessions", "api_keys", "settings",
		"notify", "servers", "repos", "jobs", "runs",
		"stale_alerts", "master_key", "sso",
	} {
		assert.False(t, db.Migrator().HasTable(table), table)
	}
}
