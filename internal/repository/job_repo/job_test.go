package job_repo

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/opskat/opsnap/internal/model/entity/job_entity"
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

// newJob 保存一个最小可用的任务
func newJob(t *testing.T, ctx context.Context, name string) *job_entity.Job {
	t.Helper()
	j := &job_entity.Job{
		Name: name, SourceType: job_entity.SourceFiles, RepoID: 1,
		Paths: []string{"/data"}, Excludes: []string{}, Databases: []string{}, ExtraArgs: []string{},
		ScheduleKind: "daily", ScheduleTime: "03:00", RetentionPreset: "30d",
		TimeoutSec: job_entity.DefaultTimeoutSec, Compression: job_entity.CompressionAuto,
	}
	require.NoError(t, NewJob().Create(ctx, j))
	return j
}

func TestForgetPending(t *testing.T) {
	ctx := setupDB(t)
	repo := NewJob()
	clean := newJob(t, ctx, "没有待清理")
	pending := newJob(t, ctx, "待清理")
	other := newJob(t, ctx, "另一个待清理")

	// 默认没有待清理
	list, err := repo.ListForgetPending(ctx)
	require.NoError(t, err)
	assert.Empty(t, list)

	require.NoError(t, repo.SetForgetPending(ctx, other.ID, 200, "密钥不对"))
	require.NoError(t, repo.SetForgetPending(ctx, pending.ID, 100, ""))

	list, err = repo.ListForgetPending(ctx)
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, pending.ID, list[0].ID, "按 ID 升序")
	assert.Equal(t, other.ID, list[1].ID)
	assert.Equal(t, int64(100), list[0].ForgetPendingAt)
	assert.Empty(t, list[0].ForgetPendingError)
	assert.Equal(t, "密钥不对", list[1].ForgetPendingError)

	// 只改这两列：配置不动
	got, err := repo.Find(ctx, pending.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "待清理", got.Name)
	assert.Equal(t, []string{"/data"}, got.Paths)
	assert.Zero(t, got.Updatetime)

	// 清除标记后不再列出
	require.NoError(t, repo.SetForgetPending(ctx, pending.ID, 0, ""))
	list, err = repo.ListForgetPending(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, other.ID, list[0].ID)

	// 没有标记的任务始终不在列表里
	got, err = repo.Find(ctx, clean.ID)
	require.NoError(t, err)
	assert.Zero(t, got.ForgetPendingAt)
}
