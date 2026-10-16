package setting_repo

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/opskat/opsnap/internal/model/entity/setting_entity"
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

func TestSettingRepo(t *testing.T) {
	ctx := setupDB(t)
	repo := NewSetting()

	got, err := repo.Get(ctx)
	require.NoError(t, err)
	assert.Nil(t, got, "未保存过时返回 nil")

	s := setting_entity.Default()
	s.MaxConcurrentRuns, s.ClearMirrorAfterBackup, s.Timezone = 5, true, "UTC"
	require.NoError(t, repo.Save(ctx, s))
	// 再保存一次是覆盖，不是新增
	s.RetentionDays = 30
	require.NoError(t, repo.Save(ctx, s))

	got, err = repo.Get(ctx)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, 5, got.MaxConcurrentRuns)
	assert.Equal(t, 30, got.RetentionDays)
	assert.True(t, got.ClearMirrorAfterBackup)
	assert.Equal(t, "UTC", got.Timezone)
}
