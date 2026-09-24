package storage_repo

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opsnap/internal/model/entity/storage_entity"
	"github.com/opskat/opsnap/internal/pkg/testdb"
)

func TestSaveDoesNotResurrect(t *testing.T) {
	ctx := testdb.New(t)
	r := NewStorage()
	st := &storage_entity.Storage{Name: "a", Kind: "local", Path: "/a", LocationKey: "local:/a"}
	require.NoError(t, r.Create(ctx, st))

	st.Name = "b"
	require.NoError(t, r.Save(ctx, st))
	got, err := r.Find(ctx, st.ID)
	require.NoError(t, err)
	assert.Equal(t, "b", got.Name)

	// 测试连接等耗时操作期间存储被删除：随后的保存不能把它重新插入
	require.NoError(t, r.Delete(ctx, st.ID))
	assert.ErrorIs(t, r.Save(ctx, st), ErrNotFound)
	got, err = r.Find(ctx, st.ID)
	require.NoError(t, err)
	assert.Nil(t, got)
}
