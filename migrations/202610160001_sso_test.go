package migrations

import (
	"testing"

	"github.com/go-gormigrate/gormigrate/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

	// 可以回滚
	m := gormigrate.New(db, &gormigrate.Options{TableName: "migrations", IDColumnName: "id", IDColumnSize: 200, UseTransaction: true},
		[]*gormigrate.Migration{T202610160001()})
	require.NoError(t, m.RollbackMigration(T202610160001()))
	assert.False(t, db.Migrator().HasTable("sso"))
}
