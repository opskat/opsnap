package sso_repo

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/opskat/opsnap/internal/model/entity/sso_entity"
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

func TestSSORepo(t *testing.T) {
	ctx := setupDB(t)
	r := NewSSO()

	got, err := r.Get(ctx)
	require.NoError(t, err)
	assert.Nil(t, got, "从未保存时返回 nil")

	s := &sso_entity.SSO{Issuer: "https://git.example.com/", ClientID: "cid", ClientSecret: "cipher", PasswordLogin: false}
	require.NoError(t, r.Save(ctx, s))
	got, err = r.Get(ctx)
	require.NoError(t, err)
	assert.Equal(t, "cid", got.ClientID)
	assert.False(t, got.PasswordLogin, "false 也要落库，不能被列默认值 1 覆盖")

	got.ClientID = "cid2"
	got.PasswordLogin = true
	require.NoError(t, r.Save(ctx, got))
	got, err = r.Get(ctx)
	require.NoError(t, err)
	assert.Equal(t, "cid2", got.ClientID)
	assert.True(t, got.PasswordLogin)

	require.NoError(t, r.Delete(ctx))
	got, err = r.Get(ctx)
	require.NoError(t, err)
	assert.Nil(t, got)
}
