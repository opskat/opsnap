package migrations

import (
	"testing"

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
