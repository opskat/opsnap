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

// 用量只由 SetUsage / SetUsageError 写入：测试连接等操作保存的是读取时的旧记录，不能覆盖期间记录的用量
func TestUsage(t *testing.T) {
	ctx := testdb.New(t)
	r := NewStorage()
	st := &storage_entity.Storage{Name: "a", Kind: "local", Path: "/a", LocationKey: "local:/a"}
	require.NoError(t, r.Create(ctx, st))
	stale, err := r.Find(ctx, st.ID)
	require.NoError(t, err)

	want := storage_entity.Usage{Snapshots: 3, PackedBytes: 1000, OriginalBytes: 5000, Checktime: 100}
	require.NoError(t, r.SetUsage(ctx, st.ID, want))
	got, err := r.Find(ctx, st.ID)
	require.NoError(t, err)
	assert.Equal(t, want, got.Usage)

	stale.Status = storage_entity.StatusUnreachable
	require.NoError(t, r.Save(ctx, stale))
	got, err = r.Find(ctx, st.ID)
	require.NoError(t, err)
	assert.Equal(t, storage_entity.StatusUnreachable, got.Status)
	assert.Equal(t, want, got.Usage, "保存旧记录不覆盖用量")

	require.NoError(t, r.SetUsageError(ctx, st.ID, "打不开", "cannot open", 200))
	got, err = r.Find(ctx, st.ID)
	require.NoError(t, err)
	assert.Equal(t, storage_entity.Usage{Snapshots: 3, PackedBytes: 1000, OriginalBytes: 5000,
		Error: "打不开", ErrorEn: "cannot open", Checktime: 200}, got.Usage, "读取失败保留上一次的数字")

	require.NoError(t, r.SetUsage(ctx, st.ID, storage_entity.Usage{Checktime: 300}))
	got, err = r.Find(ctx, st.ID)
	require.NoError(t, err)
	assert.Equal(t, storage_entity.Usage{Checktime: 300}, got.Usage, "成功读取清除原因，零值也写入")

	require.NoError(t, r.SetUsage(ctx, 999, want), "存储不存在时什么也不做")
	require.NoError(t, r.SetUsageError(ctx, 999, "x", "x", 1))
	got, err = r.Find(ctx, 999)
	require.NoError(t, err)
	assert.Nil(t, got)
}
