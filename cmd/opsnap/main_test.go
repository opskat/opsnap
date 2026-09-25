package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cago-frame/cago/configs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opsnap/internal/model/entity/channel_entity"
	"github.com/opskat/opsnap/internal/model/entity/datasource_entity"
	"github.com/opskat/opsnap/internal/pkg/testdb"
	"github.com/opskat/opsnap/internal/repository/channel_repo"
	"github.com/opskat/opsnap/internal/repository/datasource_repo"
	"github.com/opskat/opsnap/internal/service/channel_svc"
)

// newTestConfig 用一份最小配置文件构造 *configs.Config，供只读取个别配置项的单元测试使用
func newTestConfig(t *testing.T, yaml string) *configs.Config {
	t.Helper()
	file := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(file, []byte(yaml), 0o600))
	cfg, err := configs.NewConfig("opsnap", configs.WithConfigFile(file))
	require.NoError(t, err)
	return cfg
}

// TestToolsDir 配置项 tools.dir：能力探测中主控端工具在 PATH 之外的备用查找目录；
// main() 用它的返回值调用 probe.SetToolsDir（与 storage_svc.SetDataDir 的接入方式一致）
func TestToolsDir(t *testing.T) {
	ctx := context.Background()

	cfg := newTestConfig(t, "env: test\ndebug: false\nsource: file\ntools:\n  dir: /opt/opsnap/tools\n")
	assert.Equal(t, "/opt/opsnap/tools", toolsDir(ctx, cfg))

	cfg = newTestConfig(t, "env: test\ndebug: false\nsource: file\n")
	assert.Equal(t, "", toolsDir(ctx, cfg), "未配置时只在 PATH 中查找")
}

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
