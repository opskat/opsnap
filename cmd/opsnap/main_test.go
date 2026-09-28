package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cago-frame/cago/configs"
	"github.com/cago-frame/cago/pkg/gogo"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dsapi "github.com/opskat/opsnap/internal/api/datasource"
	storageapi "github.com/opskat/opsnap/internal/api/storage"
	"github.com/opskat/opsnap/internal/model/entity/channel_entity"
	"github.com/opskat/opsnap/internal/model/entity/datasource_entity"
	"github.com/opskat/opsnap/internal/model/entity/job_entity"
	"github.com/opskat/opsnap/internal/model/entity/storage_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/testdb"
	"github.com/opskat/opsnap/internal/repository/channel_repo"
	"github.com/opskat/opsnap/internal/repository/datasource_repo"
	"github.com/opskat/opsnap/internal/repository/job_repo"
	"github.com/opskat/opsnap/internal/repository/storage_repo"
	"github.com/opskat/opsnap/internal/service/channel_svc"
	"github.com/opskat/opsnap/internal/service/datasource_svc"
	"github.com/opskat/opsnap/internal/service/job_svc"
	"github.com/opskat/opsnap/internal/service/storage_svc"
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
		datasource_svc.SetJobReferrer(nil)
		storage_svc.SetJobReferrer(nil)
	})
	require.NotNil(t, datasource_repo.DataSource())
	require.NotNil(t, job_repo.Job())

	ch := &channel_entity.Channel{Name: "office-socks", Kind: channel_entity.KindSOCKS5, Host: "proxy", Port: 1080, AuthMethod: channel_entity.AuthNone}
	require.NoError(t, channel_repo.Channel().Create(ctx, ch))
	ds := &datasource_entity.DataSource{Name: "orders", Kind: datasource_entity.KindMySQL, Host: "db", Port: 3306, ChannelID: ch.ID}
	require.NoError(t, datasource_repo.DataSource().Create(ctx, ds))

	used, err := channel_svc.Channel().References(ctx, ch.ID)
	require.NoError(t, err)
	require.Len(t, used.DataSources, 1)
	assert.Equal(t, "orders", used.DataSources[0].Name)

	// 任务对数据源与存储的引用保护同样经 registerHooks 注册（docs/specs/2026-09-27-backup-jobs.md「对已有页面的影响」）
	st := &storage_entity.Storage{Name: "backup-nas", Kind: "local", Path: "/mnt/backup"}
	require.NoError(t, storage_repo.Storage().Create(ctx, st))
	j := &job_entity.Job{Name: "orders-nightly", Type: job_entity.TypeBackup, DataSourceID: ds.ID, StorageID: st.ID,
		Prefix: "orders", DatabaseNames: "[]", ExcludeTables: "[]", ScheduleKind: "daily", ScheduleWeekdays: "[]", Timezone: "UTC"}
	require.NoError(t, job_repo.Job().Create(ctx, j))

	_, dsErr := datasource_svc.DataSource().Delete(ctx, &dsapi.DeleteRequest{ID: ds.ID})
	var dsHerr *httputils.Error
	require.ErrorAs(t, dsErr, &dsHerr)
	assert.Equal(t, code.DataSourceInUse, dsHerr.Code)
	assert.Contains(t, dsHerr.Msg, "orders-nightly")

	_, stErr := storage_svc.Storage().Delete(ctx, &storageapi.DeleteRequest{ID: st.ID})
	var stHerr *httputils.Error
	require.ErrorAs(t, stErr, &stHerr)
	assert.Equal(t, code.StorageInUse, stHerr.Code)
	assert.Contains(t, stHerr.Msg, "orders-nightly")
}

// 调度组件：运行临时目录为 <数据目录>/runs，启动时清扫其中的残留并处理上次退出时未结束的运行；
// 关闭后不再开始新的运行
func TestSchedulerComponent(t *testing.T) {
	ctx := testdb.New(t)
	registerRepositories()
	t.Cleanup(func() { job_svc.SetDispatcher(nil) })
	dir := t.TempDir()
	cfg := newTestConfig(t, "env: test\ndebug: false\nsource: file\ndb:\n  dsn: \""+
		filepath.Join(dir, "opsnap.db")+"?_pragma=busy_timeout(5000)\"\n")
	left := filepath.Join(dir, "runs", "run-left")
	require.NoError(t, os.MkdirAll(left, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(left, "pgpass"), []byte("secret"), 0o600))
	j := &job_entity.Job{Name: "orders", Type: job_entity.TypeBackup, DataSourceID: 1, StorageID: 1, Prefix: "orders",
		DatabaseNames: "[]", ExcludeTables: "[]", ScheduleKind: "daily", ScheduleWeekdays: "[]", Timezone: "UTC"}
	require.NoError(t, job_repo.Job().Create(ctx, j))
	running := &job_entity.Run{JobID: j.ID, Status: job_entity.RunRunning, Trigger: job_entity.TriggerManual, Log: "[]"}
	require.NoError(t, job_repo.Run().Create(ctx, running))

	c := &schedulerComponent{}
	require.NoError(t, c.Start(ctx, cfg))
	c.CloseHandle()
	gogo.Wait()

	_, err := os.Stat(left)
	assert.True(t, os.IsNotExist(err), "启动时清扫 <数据目录>/runs 中的残留")
	got, err := job_repo.Run().Find(ctx, running.ID)
	require.NoError(t, err)
	assert.Equal(t, job_entity.RunFailed, got.Status, "上次退出时运行中的记为失败")
}
