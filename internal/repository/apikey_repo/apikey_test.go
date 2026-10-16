package apikey_repo

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/opskat/opsnap/internal/model/entity/apikey_entity"
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

func TestAPIKeyRepo(t *testing.T) {
	ctx := setupDB(t)
	repo := NewAPIKey()
	k1 := &apikey_entity.APIKey{Name: "ci", Prefix: "osk_aaaa", KeyHash: "h1", Createtime: 1}
	k2 := &apikey_entity.APIKey{Name: "cron", Prefix: "osk_bbbb", KeyHash: "h2", Createtime: 2}
	require.NoError(t, repo.Create(ctx, k1))
	require.NoError(t, repo.Create(ctx, k2))
	assert.NotZero(t, k1.ID)

	got, err := repo.FindByHash(ctx, "h1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "ci", got.Name)
	none, err := repo.FindByHash(ctx, "nope")
	require.NoError(t, err)
	assert.Nil(t, none)

	require.NoError(t, repo.TouchLastUsed(ctx, k1.ID, 500))
	got, _ = repo.FindByHash(ctx, "h1")
	assert.Equal(t, int64(500), got.LastUsedAt)

	t.Run("撤销后不在列表里，哈希查询仍能看到撤销时间", func(t *testing.T) {
		ok, err := repo.Revoke(ctx, k1.ID, 600)
		require.NoError(t, err)
		assert.True(t, ok)
		list, err := repo.List(ctx)
		require.NoError(t, err)
		require.Len(t, list, 1)
		assert.Equal(t, "cron", list[0].Name)
		got, _ = repo.FindByHash(ctx, "h1")
		assert.Equal(t, int64(600), got.RevokedAt)

		ok, err = repo.Revoke(ctx, k1.ID, 700)
		require.NoError(t, err)
		assert.False(t, ok, "已撤销的密钥不能再次撤销")
		ok, err = repo.Revoke(ctx, 9999, 700)
		require.NoError(t, err)
		assert.False(t, ok)
	})
}
