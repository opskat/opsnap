package repo_ctr

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/server/mux/muxtest"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	repoapi "github.com/opskat/opsnap/internal/api/repo"
	"github.com/opskat/opsnap/internal/pkg/executor"
	"github.com/opskat/opsnap/internal/pkg/secret"
	"github.com/opskat/opsnap/internal/repository/repo_repo"
	"github.com/opskat/opsnap/internal/service/repo_svc"
	"github.com/opskat/opsnap/migrations"
)

func setupRepoTest(t *testing.T) (context.Context, *executor.Fake, *muxtest.TestMux) {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, migrations.RunMigrations(gdb))
	s, err := secret.New("repo-ctr-test-key")
	require.NoError(t, err)
	secret.SetDefault(s)
	t.Cleanup(func() { secret.SetDefault(nil) })

	fake := executor.NewFake()
	repo_repo.RegisterRepo(repo_repo.NewRepo())
	repo_svc.RegisterRepo(repo_svc.NewRepo(fake, time.Now))
	testMux := muxtest.NewTestMux()
	ctr := NewRepo()
	testMux.Group("/api/v1").Bind(ctr.NewRecoveryKey, ctr.List, ctr.Get, ctr.Create, ctr.Update, ctr.Delete, ctr.SFTPHostKey,
		ctr.Keys, ctr.RotateKey, ctr.GenerateRecoveryKey, ctr.RemoveRecoveryKey, ctr.RecoveryBundle,
		ctr.Recheck, ctr.Check, ctr.Prune, ctr.Unlock)
	return db.WithContextDB(context.Background(), gdb), fake, testMux
}

func TestRepoController(t *testing.T) {
	ctx, fake, testMux := setupRepoTest(t)
	dir := t.TempDir()
	fake.On(executor.Match("restic", "snapshots"), executor.Step{ExitCode: 10})
	fake.On(executor.Match("restic", "key", "list"), executor.Step{Output: `[{"current":true,"id":"op1"}]`})

	// 保存前先取一把恢复密钥展示给管理员；不保存
	rk := &repoapi.NewRecoveryKeyResponse{}
	require.NoError(t, testMux.Do(ctx, &repoapi.NewRecoveryKeyRequest{}, rk))
	require.NotEmpty(t, rk.RecoveryKeyValue)

	// 生成恢复密钥（默认）但没有确认已存进密码管理器：拒绝
	err := testMux.Do(ctx, &repoapi.CreateRequest{Name: "nas", Type: repoapi.TypeLocal, Params: repoapi.Params{Path: dir},
		RecoveryKeyValue: rk.RecoveryKeyValue}, &repoapi.CreateResponse{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "密码管理器")
	assert.Empty(t, fake.Calls())

	no := false
	created := &repoapi.CreateResponse{}
	require.NoError(t, testMux.Do(ctx, &repoapi.CreateRequest{
		Name: "nas", Type: repoapi.TypeLocal, Params: repoapi.Params{Path: dir}, RecoveryKey: &no,
	}, created))
	require.NotZero(t, created.ID)
	assert.True(t, created.NoRecoveryKey)

	list := &repoapi.ListResponse{}
	require.NoError(t, testMux.Do(ctx, &repoapi.ListRequest{}, list))
	require.Len(t, list.List, 1)
	assert.True(t, list.List[0].NoRecoveryKey)

	keys := &repoapi.KeysResponse{}
	require.NoError(t, testMux.Do(ctx, &repoapi.KeysRequest{ID: created.ID}, keys))
	assert.True(t, keys.OpsnapKey.Present)
	assert.Equal(t, "op1", keys.OpsnapKey.ID)

	bundle := &repoapi.RecoveryBundleResponse{}
	require.NoError(t, testMux.Do(ctx, &repoapi.RecoveryBundleRequest{ID: created.ID}, bundle))
	assert.Contains(t, bundle.Content, dir)

	// 解除仓库锁必须二次确认
	err = testMux.Do(ctx, &repoapi.UnlockRequest{ID: created.ID}, &repoapi.UnlockResponse{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "二次确认")
	require.NoError(t, testMux.Do(ctx, &repoapi.UnlockRequest{ID: created.ID, Confirm: true}, &repoapi.UnlockResponse{}))

	// 缺少标记文件：重新检查返回「没有挂载」状态
	require.NoError(t, os.Remove(filepath.Join(dir, ".opsnap-repo")))
	rc := &repoapi.RecheckResponse{}
	require.NoError(t, testMux.Do(ctx, &repoapi.RecheckRequest{ID: created.ID}, rc))
	assert.Equal(t, repoapi.StatusNotMounted, rc.Status)

	require.NoError(t, testMux.Do(ctx, &repoapi.DeleteRequest{ID: created.ID}, &repoapi.DeleteResponse{}))
	err = testMux.Do(ctx, &repoapi.GetRequest{ID: created.ID}, &repoapi.GetResponse{})
	require.Error(t, err)
}
