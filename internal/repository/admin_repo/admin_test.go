package admin_repo

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/opskat/opsnap/internal/model/entity/admin_entity"
	"github.com/opskat/opsnap/migrations"
)

func setupDB(t *testing.T) context.Context {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, migrations.RunMigrations(gdb))
	return db.WithContextDB(context.Background(), gdb)
}

func TestAdminRepo(t *testing.T) {
	ctx := setupDB(t)
	repo := NewAdmin()

	got, err := repo.Find(ctx)
	require.NoError(t, err)
	assert.Nil(t, got, "未初始化时返回 nil")

	require.NoError(t, repo.Create(ctx, &admin_entity.Admin{Username: "root", PasswordHash: "h"}))
	got, err = repo.Find(ctx)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, int64(admin_entity.SingletonID), got.ID)
	assert.Equal(t, "root", got.Username)

	t.Run("不能重复创建", func(t *testing.T) {
		assert.Error(t, repo.Create(ctx, &admin_entity.Admin{Username: "other", PasswordHash: "h"}))
	})

	t.Run("更新失败计数和锁定时间", func(t *testing.T) {
		got.FailedCount, got.LockedUntil = 3, 99
		require.NoError(t, repo.Update(ctx, got))
		again, err := repo.Find(ctx)
		require.NoError(t, err)
		assert.Equal(t, 3, again.FailedCount)
		assert.Equal(t, int64(99), again.LockedUntil)
	})
}
