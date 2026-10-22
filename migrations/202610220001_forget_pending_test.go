package migrations

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestJobsForgetPendingColumns jobs 表带待清理锚点与最近失败原因：两列非空、默认分别为 0 与空串
func TestJobsForgetPendingColumns(t *testing.T) {
	db := openTempDB(t)
	require.NoError(t, RunMigrations(db))

	cols := columnsOf(t, db, "jobs")
	assert.Contains(t, cols, "forget_pending_at")
	assert.Contains(t, cols, "forget_pending_error")

	// 老任务没有待清理：插入时不写这两列，落成 0 与空串
	require.NoError(t, db.Exec(`INSERT INTO jobs (name, source_type, repo_id, schedule_kind, retention_preset)
		VALUES ('老任务', 'files', 1, 'daily', '30d')`).Error)
	var row struct {
		At    int64  `gorm:"column:forget_pending_at"`
		Error string `gorm:"column:forget_pending_error"`
	}
	require.NoError(t, db.Raw(`SELECT forget_pending_at, forget_pending_error FROM jobs WHERE name = '老任务'`).Scan(&row).Error)
	assert.Zero(t, row.At)
	assert.Empty(t, row.Error)

	// 可回滚：回滚到基线之前这两列就不在了
	require.NoError(t, rollbackOnly(t, db, T202610220001()))
	assert.NotContains(t, columnsOf(t, db, "jobs"), "forget_pending_at")
	assert.NotContains(t, columnsOf(t, db, "jobs"), "forget_pending_error")
}
