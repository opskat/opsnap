package channel_repo

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opsnap/internal/model/entity/channel_entity"
	"github.com/opskat/opsnap/internal/pkg/testdb"
)

func TestSaveDoesNotResurrect(t *testing.T) {
	ctx := testdb.New(t)
	r := NewChannel()
	c := &channel_entity.Channel{Name: "a", Kind: channel_entity.KindSOCKS5, Host: "proxy", Port: 1080, AuthMethod: channel_entity.AuthNone}
	require.NoError(t, r.Create(ctx, c))

	c.Name = "b"
	require.NoError(t, r.Save(ctx, c))
	got, err := r.Find(ctx, c.ID)
	require.NoError(t, err)
	assert.Equal(t, "b", got.Name)

	// 测试连接期间通道被删除：随后的保存不能把它重新插入
	require.NoError(t, r.Delete(ctx, c.ID))
	assert.ErrorIs(t, r.Save(ctx, c), ErrNotFound)
	got, err = r.Find(ctx, c.ID)
	require.NoError(t, err)
	assert.Nil(t, got)
}
