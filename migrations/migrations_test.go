package migrations

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/go-gormigrate/gormigrate/v2"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// openTempDB 在临时目录里打开一个 SQLite 文件数据库
func openTempDB(t *testing.T) *gorm.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "opsnap.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	require.NoError(t, err)
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

// columnsOf 读取表的列名
func columnsOf(t *testing.T, db *gorm.DB, table string) []string {
	t.Helper()
	var cols []string
	require.NoError(t, db.Raw(`SELECT name FROM pragma_table_info(?)`, table).Scan(&cols).Error)
	return cols
}

// rollbackOnly 用单个迁移构造 gormigrate，用来在已迁移的库上回滚这一条
func rollbackOnly(t *testing.T, db *gorm.DB, m *gormigrate.Migration) error {
	t.Helper()
	g := gormigrate.New(db, &gormigrate.Options{
		TableName: "migrations", IDColumnName: "id", IDColumnSize: 200, UseTransaction: true,
	}, []*gormigrate.Migration{m})
	return g.RollbackMigration(m)
}

func TestCredentialsAndServersMigration(t *testing.T) {
	db := openTempDB(t)
	require.NoError(t, RunMigrations(db))

	// credentials 表：一条凭据一种内容，shared 区分共享与私有
	assert.True(t, db.Migrator().HasTable("credentials"))
	cols := columnsOf(t, db, "credentials")
	for _, c := range []string{"id", "name", "kind", "password", "private_key", "passphrase", "username", "note", "shared", "createtime", "updatetime"} {
		assert.Contains(t, cols, c)
	}

	// servers 只引用凭据，不再自带凭据列
	srvCols := columnsOf(t, db, "servers")
	assert.Contains(t, srvCols, "credential_id")
	for _, c := range []string{"auth_type", "password", "private_key", "passphrase"} {
		assert.NotContains(t, srvCols, c)
	}

	// 共享凭据的名称唯一，私有凭据不受约束
	var indexSQL string
	require.NoError(t, db.Raw(`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = 'uk_credentials_shared_name'`).Scan(&indexSQL).Error)
	assert.Contains(t, indexSQL, "UNIQUE")
	assert.Contains(t, indexSQL, "WHERE shared = 1")

	// 可回滚：先删 servers 再删 credentials，两张表都不在了
	require.NoError(t, rollbackOnly(t, db, T202610180001()))
	assert.False(t, db.Migrator().HasTable("servers"))
	assert.False(t, db.Migrator().HasTable("credentials"))
}

func TestRunMigrations(t *testing.T) {
	convey.Convey("运行迁移", t, func() {
		db := openTempDB(t)

		convey.Convey("在空库上执行后创建迁移记录表和 meta 表", func() {
			require.NoError(t, RunMigrations(db))
			assert.True(t, db.Migrator().HasTable("migrations"))
			assert.True(t, db.Migrator().HasTable("meta"))

			var ids []string
			require.NoError(t, db.Table("migrations").Pluck("id", &ids).Error)
			assert.Contains(t, ids, "202610180001")

			convey.Convey("重复执行不报错，也不重复记录", func() {
				require.NoError(t, RunMigrations(db))
				var count int64
				require.NoError(t, db.Table("migrations").Where("id = ?", "202610180001").Count(&count).Error)
				assert.Equal(t, int64(1), count)
			})
		})
	})
}
