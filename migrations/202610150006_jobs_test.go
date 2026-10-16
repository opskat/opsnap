package migrations

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
