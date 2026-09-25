package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opsnap/internal/model/entity/channel_entity"
	"github.com/opskat/opsnap/internal/model/entity/datasource_entity"
	"github.com/opskat/opsnap/internal/pkg/testdb"
	"github.com/opskat/opsnap/internal/repository/channel_repo"
	"github.com/opskat/opsnap/internal/repository/datasource_repo"
	"github.com/opskat/opsnap/internal/service/channel_svc"
)

// 服务启动时注册全部仓库与模块间的钩子：通道的引用计数与删除保护要计入数据源
func TestRegisterRepositoriesAndHooks(t *testing.T) {
	ctx := testdb.New(t)
	registerRepositories()
	registerHooks()
	t.Cleanup(func() {
		channel_svc.SetDataSourceReferrer(nil)
		channel_svc.SetHostKeyConfirmedHook(nil)
		channel_svc.SetHostKeyChangedHook(nil)
	})
	require.NotNil(t, datasource_repo.DataSource())

	ch := &channel_entity.Channel{Name: "office-socks", Kind: channel_entity.KindSOCKS5, Host: "proxy", Port: 1080, AuthMethod: channel_entity.AuthNone}
	require.NoError(t, channel_repo.Channel().Create(ctx, ch))
	ds := &datasource_entity.DataSource{Name: "orders", Kind: datasource_entity.KindMySQL, Host: "db", Port: 3306, ChannelID: ch.ID}
	require.NoError(t, datasource_repo.DataSource().Create(ctx, ds))

	used, err := channel_svc.Channel().References(ctx, ch.ID)
	require.NoError(t, err)
	require.Len(t, used.DataSources, 1)
	assert.Equal(t, "orders", used.DataSources[0].Name)
}
