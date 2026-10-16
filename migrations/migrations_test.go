package migrations

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
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

func TestRunMigrations(t *testing.T) {
	convey.Convey("运行迁移", t, func() {
		db := openTempDB(t)

		convey.Convey("在空库上执行后创建迁移记录表和 meta 表", func() {
			require.NoError(t, RunMigrations(db))
			assert.True(t, db.Migrator().HasTable("migrations"))
			assert.True(t, db.Migrator().HasTable("meta"))

			var ids []string
			require.NoError(t, db.Table("migrations").Pluck("id", &ids).Error)
			assert.Contains(t, ids, "202610150001")

			convey.Convey("重复执行不报错，也不重复记录", func() {
				require.NoError(t, RunMigrations(db))
				var count int64
				require.NoError(t, db.Table("migrations").Where("id = ?", "202610150001").Count(&count).Error)
				assert.Equal(t, int64(1), count)
			})
		})
	})
}
