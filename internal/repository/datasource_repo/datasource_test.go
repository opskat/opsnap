package datasource_repo

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opsnap/internal/model/entity/datasource_entity"
	"github.com/opskat/opsnap/internal/pkg/testdb"
)

func TestSaveDoesNotResurrect(t *testing.T) {
	ctx := testdb.New(t)
	r := NewDataSource()
	d := &datasource_entity.DataSource{Name: "a", Kind: datasource_entity.KindMySQL, Host: "db", Port: 3306, AuthMethod: datasource_entity.AuthPassword}
	require.NoError(t, r.Create(ctx, d))

	d.Name, d.TLSVerified = "b", true
	require.NoError(t, r.Save(ctx, d))
	got, err := r.Find(ctx, d.ID)
	require.NoError(t, err)
	assert.Equal(t, "b", got.Name)
	assert.True(t, got.TLSVerified)
	byName, err := r.FindByName(ctx, "b")
	require.NoError(t, err)
	assert.Equal(t, d.ID, byName.ID)

	// 测试连接期间数据源被删除：随后的保存不能把它重新插入
	require.NoError(t, r.Delete(ctx, d.ID))
	assert.ErrorIs(t, r.Save(ctx, d), ErrNotFound)
	got, err = r.Find(ctx, d.ID)
	require.NoError(t, err)
	assert.Nil(t, got)
}
