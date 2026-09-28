package storage_svc

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	api "github.com/opskat/opsnap/internal/api/storage"
	"github.com/opskat/opsnap/internal/model/entity/storage_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/kopiarepo"
	"github.com/opskat/opsnap/internal/pkg/testdb"
	"github.com/opskat/opsnap/internal/repository/setting_repo"
	"github.com/opskat/opsnap/internal/repository/storage_repo"
	"github.com/opskat/opsnap/internal/service/secret_svc"
)

// OpenWriter 按存储 ID 用保存的位置与托管密钥打开写入会话；状态不是“正常”时不连接
func TestOpenWriter(t *testing.T) {
	ctx := testdb.New(t)
	setting_repo.RegisterSetting(setting_repo.NewSetting())
	storage_repo.RegisterStorage(storage_repo.NewStorage())
	_, err := secret_svc.Secret().Init(ctx, secret_svc.InitOptions{DataDir: t.TempDir()})
	require.NoError(t, err)
	dataDir := t.TempDir()
	SetDataDir(dataDir)

	resp, err := Storage().Create(ctx, &api.CreateRequest{Name: "primary",
		Location: api.Location{Kind: "local", Path: filepath.Join(t.TempDir(), "repo")},
		Key:      "Abcd-Efgh-Ijkl-Mnop-Qrst-Uvwx", ConfirmSaved: true})
	require.NoError(t, err)
	id := resp.Item.ID

	w, err := Storage().OpenWriter(ctx, id)
	require.NoError(t, err)
	snaps, err := w.ListJobSnapshots(ctx, kopiarepo.JobRef{JobID: 1, Prefix: "x"})
	require.NoError(t, err)
	assert.Empty(t, snaps)
	require.NoError(t, w.Close(ctx))

	_, err = Storage().OpenWriter(ctx, 999)
	var he *httputils.Error
	require.ErrorAs(t, err, &he)
	assert.Equal(t, code.StorageNotFound, he.Code)

	for _, status := range []string{storage_entity.StatusWrongKey, storage_entity.StatusUnreachable} {
		st, err := storage_repo.Storage().Find(ctx, id)
		require.NoError(t, err)
		st.Status = status
		require.NoError(t, storage_repo.Storage().Save(ctx, st))
		_, err = Storage().OpenWriter(ctx, id)
		assert.True(t, errors.Is(err, ErrNotReady), "状态 %s：%v", status, err)
	}
}
