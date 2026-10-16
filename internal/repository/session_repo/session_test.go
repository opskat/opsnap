package session_repo

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/opskat/opsnap/internal/model/entity/session_entity"
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

func TestSessionRepo(t *testing.T) {
	ctx := setupDB(t)
	repo := NewSession()
	for _, h := range []string{"a", "b", "c"} {
		require.NoError(t, repo.Create(ctx, &session_entity.Session{TokenHash: h, ExpiresAt: 100}))
	}

	got, err := repo.FindByHash(ctx, "a")
	require.NoError(t, err)
	require.NotNil(t, got)
	missing, err := repo.FindByHash(ctx, "zzz")
	require.NoError(t, err)
	assert.Nil(t, missing)

	require.NoError(t, repo.Delete(ctx, "a"))
	got, _ = repo.FindByHash(ctx, "a")
	assert.Nil(t, got)

	require.NoError(t, repo.DeleteExcept(ctx, "b"))
	b, _ := repo.FindByHash(ctx, "b")
	c, _ := repo.FindByHash(ctx, "c")
	assert.NotNil(t, b)
	assert.Nil(t, c)

	require.NoError(t, repo.DeleteExpired(ctx, 100))
	b, _ = repo.FindByHash(ctx, "b")
	assert.Nil(t, b)
}
