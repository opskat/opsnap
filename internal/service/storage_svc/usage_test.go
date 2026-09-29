package storage_svc

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	api "github.com/opskat/opsnap/internal/api/storage"
	"github.com/opskat/opsnap/internal/pkg/kopiarepo"
	"github.com/opskat/opsnap/internal/pkg/testdb"
	"github.com/opskat/opsnap/internal/repository/setting_repo"
	"github.com/opskat/opsnap/internal/repository/storage_repo"
	"github.com/opskat/opsnap/internal/service/secret_svc"
)

// 新建存储（空位置建库或接入已有仓库）与重新解锁时都校验了仓库，同时记录用量
func TestCreateAndUnlockRecordUsage(t *testing.T) {
	ctx := testdb.New(t)
	setting_repo.RegisterSetting(setting_repo.NewSetting())
	storage_repo.RegisterStorage(storage_repo.NewStorage())
	_, err := secret_svc.Secret().Init(ctx, secret_svc.InitOptions{DataDir: t.TempDir()})
	require.NoError(t, err)
	SetDataDir(t.TempDir())
	const key = "Abcd-Efgh-Ijkl-Mnop-Qrst-Uvwx"
	dir := filepath.Join(t.TempDir(), "repo")

	first, err := Storage().Create(ctx, &api.CreateRequest{Name: "first", Location: api.Location{Kind: "local", Path: dir},
		Key: key, ConfirmSaved: true})
	require.NoError(t, err)
	st, err := storage_repo.Storage().Find(ctx, first.Item.ID)
	require.NoError(t, err)
	assert.Positive(t, st.Usage.Checktime, "空位置建库：用量为 0，也算读取过")
	assert.Zero(t, st.Usage.Snapshots)

	w, err := Storage().OpenWriter(ctx, first.Item.ID)
	require.NoError(t, err)
	_, err = w.WriteSnapshot(ctx, kopiarepo.SnapshotRequest{Prefix: "x", Tags: kopiarepo.SnapshotTags{JobID: 1},
		Files: []kopiarepo.SnapshotFile{{Name: "a", Reader: bytes.NewReader(make([]byte, 4096))}}})
	require.NoError(t, err)
	require.NoError(t, w.Close(ctx))
	_, err = Storage().Delete(ctx, &api.DeleteRequest{ID: first.Item.ID})
	require.NoError(t, err)

	second, err := Storage().Create(ctx, &api.CreateRequest{Name: "second", Location: api.Location{Kind: "local", Path: dir}, Key: key})
	require.NoError(t, err)
	st, err = storage_repo.Storage().Find(ctx, second.Item.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, st.Usage.Snapshots, "接入已有仓库时读取用量")
	assert.Equal(t, int64(4096), st.Usage.OriginalBytes)

	require.NoError(t, storage_repo.Storage().SetUsageError(ctx, st.ID, "x", "x", 1))
	_, err = Storage().Unlock(ctx, &api.UnlockRequest{ID: st.ID, Key: key})
	require.NoError(t, err)
	st, err = storage_repo.Storage().Find(ctx, second.Item.ID)
	require.NoError(t, err)
	assert.Empty(t, st.Usage.Error, "重新解锁后重新读取")
	assert.Equal(t, 1, st.Usage.Snapshots)
}
