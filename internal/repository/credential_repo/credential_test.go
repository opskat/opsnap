// Package credential_repo 密钥库（服务器登录用的密码与私钥）的数据访问测试
package credential_repo

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/opskat/opsnap/internal/model/entity/credential_entity"
	"github.com/opskat/opsnap/internal/model/entity/server_entity"
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

func TestCredentialRepo(t *testing.T) {
	ctx := setupDB(t)
	repo := NewCredential()

	private := &credential_entity.Credential{Kind: credential_entity.KindPassword, Password: "SECRET-private"}
	require.NoError(t, repo.Create(ctx, private))
	assert.NotZero(t, private.ID)

	b := &credential_entity.Credential{Name: "b-root", Kind: credential_entity.KindPassword, Password: "SECRET-b", Username: "root", Shared: true}
	require.NoError(t, repo.Create(ctx, b))
	a := &credential_entity.Credential{Name: "a-admin", Kind: credential_entity.KindSSHKey, PrivateKey: "SECRET-key", Shared: true}
	require.NoError(t, repo.Create(ctx, a))

	got, err := repo.Find(ctx, a.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "a-admin", got.Name)
	assert.Equal(t, credential_entity.KindSSHKey, got.Kind)

	missing, err := repo.Find(ctx, 999)
	require.NoError(t, err)
	assert.Nil(t, missing, "不存在时返回 nil")

	t.Run("只列共享密钥，按名称排序", func(t *testing.T) {
		list, err := repo.ListShared(ctx)
		require.NoError(t, err)
		names := make([]string, 0, len(list))
		for _, c := range list {
			names = append(names, c.Name)
			assert.True(t, c.Shared)
		}
		assert.Equal(t, []string{"a-admin", "b-root"}, names)
	})

	t.Run("共享名称唯一，私有凭据不参与", func(t *testing.T) {
		dup := &credential_entity.Credential{Name: "a-admin", Kind: credential_entity.KindPassword, Password: "SECRET-x", Shared: true}
		assert.Error(t, repo.Create(ctx, dup), "共享密钥名称不能重复")
		for i := range 2 {
			assert.NoError(t, repo.Create(ctx, &credential_entity.Credential{
				Name: "", Kind: credential_entity.KindPassword, Password: "SECRET-p", Shared: false,
			}), "私有凭据的名字为空串，可以有多条：%d", i)
		}
	})

	t.Run("更新保存内容与名称", func(t *testing.T) {
		a.Name, a.Password, a.PrivateKey, a.Note = "a-renamed", "SECRET-new", "", "改了"
		require.NoError(t, repo.Update(ctx, a))
		again, err := repo.Find(ctx, a.ID)
		require.NoError(t, err)
		assert.Equal(t, "a-renamed", again.Name)
		assert.Equal(t, "SECRET-new", again.Password)
		assert.Empty(t, again.PrivateKey)
		assert.Equal(t, "改了", again.Note)
	})

	t.Run("按服务器取它的私有凭据", func(t *testing.T) {
		srv := &server_entity.Server{
			Name: "web", Host: "h", Port: 22, Username: "root", CredentialID: private.ID,
			Hops: []server_entity.Hop{}, HostKeyFingerprint: "SHA256:x",
		}
		require.NoError(t, db.Ctx(ctx).Create(srv).Error)

		p, err := repo.FindPrivate(ctx, srv.ID)
		require.NoError(t, err)
		require.NotNil(t, p)
		assert.Equal(t, private.ID, p.ID)

		shared := &server_entity.Server{
			Name: "web2", Host: "h", Port: 22, Username: "root", CredentialID: b.ID,
			Hops: []server_entity.Hop{}, HostKeyFingerprint: "SHA256:x",
		}
		require.NoError(t, db.Ctx(ctx).Create(shared).Error)
		none, err := repo.FindPrivate(ctx, shared.ID)
		require.NoError(t, err)
		assert.Nil(t, none, "共享凭据不是这台服务器的私有凭据")

		n, err := repo.CountServersByCredentialID(ctx, private.ID)
		require.NoError(t, err)
		assert.Equal(t, int64(1), n)
		n, err = repo.CountServersByCredentialID(ctx, b.ID)
		require.NoError(t, err)
		assert.Equal(t, int64(1), n, "按凭据统计使用它的服务器数")
		n, err = repo.CountServersByCredentialID(ctx, 999)
		require.NoError(t, err)
		assert.Zero(t, n)
	})

	t.Run("删除", func(t *testing.T) {
		require.NoError(t, repo.Delete(ctx, a.ID))
		gone, err := repo.Find(ctx, a.ID)
		require.NoError(t, err)
		assert.Nil(t, gone)
	})
}
