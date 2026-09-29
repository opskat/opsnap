package overview_ctr

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cago-frame/cago/pkg/gogo"
	"github.com/cago-frame/cago/server/mux/muxclient"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dsapi "github.com/opskat/opsnap/internal/api/datasource"
	jobapi "github.com/opskat/opsnap/internal/api/job"
	api "github.com/opskat/opsnap/internal/api/overview"
	storageapi "github.com/opskat/opsnap/internal/api/storage"
	"github.com/opskat/opsnap/internal/model/entity/datasource_entity"
	"github.com/opskat/opsnap/internal/model/entity/job_entity"
	"github.com/opskat/opsnap/internal/model/entity/storage_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/dsconn"
	"github.com/opskat/opsnap/internal/pkg/kopiarepo"
	"github.com/opskat/opsnap/internal/repository/datasource_repo"
	"github.com/opskat/opsnap/internal/repository/job_repo"
	"github.com/opskat/opsnap/internal/repository/storage_repo"
	"github.com/opskat/opsnap/internal/service/datasource_svc"
	"github.com/opskat/opsnap/internal/service/job_svc"
	"github.com/opskat/opsnap/internal/service/overview_svc"
	"github.com/opskat/opsnap/internal/service/secret_svc"
	"github.com/opskat/opsnap/internal/service/storage_svc"
)

// getLang 用 API 令牌以指定的界面语言读取概览
func (e *env) getLang(t *testing.T, lang string) *api.GetResponse {
	t.Helper()
	resp := &api.GetResponse{}
	require.NoError(t, e.mux.Do(e.ctx, &api.GetRequest{}, resp, muxclient.WithHeader(http.Header{
		"Authorization": {"Bearer " + e.token}, "Accept-Language": {lang}})))
	return resp
}

// localStorage 在临时目录新建一个真实的本地目录存储（建空仓库）
func (e *env) localStorage(t *testing.T, name string) (*storageapi.Item, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "repo")
	resp, err := storage_svc.Storage().Create(e.ctx, &storageapi.CreateRequest{Name: name,
		Location: storageapi.Location{Kind: "local", Path: dir}, Key: repoKey, ConfirmSaved: true})
	require.NoError(t, err)
	return resp.Item, dir
}

// writeSnapshot 绕过运行直接向存储写一份快照（只写仓库，不记录用量），返回写入的字节数
func (e *env) writeSnapshot(t *testing.T, storageID, jobID int64, data []byte) int64 {
	t.Helper()
	w, err := storage_svc.Storage().OpenWriter(e.ctx, storageID)
	require.NoError(t, err)
	defer func() { require.NoError(t, w.Close(e.ctx)) }()
	_, err = w.WriteSnapshot(e.ctx, kopiarepo.SnapshotRequest{Prefix: "manual", Tags: kopiarepo.SnapshotTags{JobID: jobID, Type: "full"},
		Files: []kopiarepo.SnapshotFile{{Name: "data.bin", Reader: bytes.NewReader(data)}}})
	require.NoError(t, err)
	return int64(len(data))
}

func findStorage(t *testing.T, resp *api.GetResponse, id int64) *api.Storage {
	t.Helper()
	for _, s := range resp.Storages {
		if s.ID == id {
			return s
		}
	}
	require.Failf(t, "概览中没有该存储", "id %d", id)
	return nil
}

func TestOverviewStorageUsage(t *testing.T) {
	e := setupTest(t)
	storage_svc.SetDataDir(t.TempDir())
	item, dir := e.localStorage(t, "primary")

	convey.Convey("存储目标的仓库用量取最近一次记录的值，本地目录的磁盘用量每次读取", t, func() {
		row := findStorage(t, e.get(t, ""), item.ID)
		assert.Equal(t, "primary", row.Name)
		assert.Equal(t, "local", row.Kind)
		assert.Equal(t, dir, row.Path)
		assert.Equal(t, item.Location, row.Location, "位置与存储列表相同")
		assert.Equal(t, "ok", row.Status)
		assert.True(t, row.Readable, "新建存储时已读取用量：%s", row.Reason)
		assert.Empty(t, row.Reason)
		assert.Zero(t, row.Snapshots)
		assert.Zero(t, row.PackedBytes)
		assert.Zero(t, row.OriginalBytes)
		assert.Positive(t, row.UsageRecordedAt)
		require.NotNil(t, row.Disk, "本地目录显示所在磁盘的用量")
		assert.Positive(t, row.Disk.TotalBytes)
		assert.Positive(t, row.Disk.UsedBytes)
		assert.Positive(t, row.Disk.FreeBytes)
		assert.LessOrEqual(t, row.Disk.UsedBytes+row.Disk.FreeBytes, row.Disk.TotalBytes)

		// 两份内容相同的快照：原始大小按份累加，占用只计一次
		data := bytes.Repeat([]byte("opsnap overview usage "), 64<<10)
		original := e.writeSnapshot(t, item.ID, 1, data) + e.writeSnapshot(t, item.ID, 2, data)
		row = findStorage(t, e.get(t, ""), item.ID)
		assert.Zero(t, row.Snapshots, "刷新概览不打开仓库，仍是记录的值")
		assert.Zero(t, row.OriginalBytes)

		before := row.UsageRecordedAt
		tested, err := storage_svc.Storage().Test(e.ctx, &storageapi.TestRequest{ID: item.ID})
		require.NoError(t, err)
		resp := e.get(t, "")
		row = findStorage(t, resp, item.ID)
		assert.True(t, row.Readable, row.Reason)
		assert.Equal(t, 2, row.Snapshots, "测试连接之后更新")
		assert.Equal(t, tested.Snapshots, row.Snapshots, "与测试连接返回的快照数一致")
		assert.Equal(t, original, row.OriginalBytes)
		assert.Positive(t, row.PackedBytes)
		assert.Less(t, row.PackedBytes, row.OriginalBytes/10, "去重、压缩之后的占用")
		assert.GreaterOrEqual(t, row.UsageRecordedAt, before)
		assert.InDelta(t, 1-float64(row.PackedBytes)/float64(original), resp.StorageUsage.Savings, 1e-9)
		resp.StorageUsage.Savings = 0
		assert.Equal(t, api.StorageUsage{PackedBytes: row.PackedBytes, OriginalBytes: original}, resp.StorageUsage)

		// 仓库被移走：刷新概览仍显示记录的值；测试连接后状态变为无法连接，给出原因并不计入总和
		require.NoError(t, os.Rename(dir, dir+".moved"))
		row = findStorage(t, e.get(t, ""), item.ID)
		assert.True(t, row.Readable)
		assert.Equal(t, 2, row.Snapshots)
		_, err = storage_svc.Storage().Test(e.ctx, &storageapi.TestRequest{ID: item.ID})
		require.NoError(t, err)
		for lang, reason := range map[string]string{
			code.LangZhCN: "无法连接：目标位置不是 kopia 仓库",
			code.LangEn:   "Could not connect: The location is not a kopia repository",
		} {
			resp := e.getLang(t, lang)
			row := findStorage(t, resp, item.ID)
			assert.Equal(t, "unreachable", row.Status)
			assert.False(t, row.Readable)
			assert.Equal(t, reason, row.Reason, lang)
			assert.Zero(t, row.Snapshots)
			assert.Zero(t, row.PackedBytes)
			assert.Zero(t, row.OriginalBytes)
			assert.Nil(t, row.Disk, "目录不存在时读不到磁盘用量")
			assert.Equal(t, api.StorageUsage{Unreadable: 1}, resp.StorageUsage)
		}
	})
}

// s3Storage 只写记录的 S3 存储
func (e *env) s3Storage(t *testing.T, name string) *storage_entity.Storage {
	t.Helper()
	st := &storage_entity.Storage{Name: name, Kind: "s3", Endpoint: "s3.internal:9000", Bucket: "backups", Prefix: name + "/",
		AccessKey: "AK", LocationKey: "s3:" + name, Status: storage_entity.StatusOK}
	require.NoError(t, storage_repo.Storage().Create(e.ctx, st))
	return st
}

func TestOverviewStorageRows(t *testing.T) {
	e := setupTest(t)
	at := now.Add(-time.Hour).Unix()
	s3 := e.s3Storage(t, "offsite")
	require.NoError(t, storage_repo.Storage().SetUsage(e.ctx, s3.ID,
		storage_entity.Usage{Snapshots: 12, PackedBytes: 3000, OriginalBytes: 10000, Checktime: at}))
	local := e.storage(t, "local")
	require.NoError(t, storage_repo.Storage().SetUsage(e.ctx, local, storage_entity.Usage{Snapshots: 3, PackedBytes: 1000, OriginalBytes: 2000, Checktime: at}))
	never := e.storage(t, "never")
	failed := e.storage(t, "failed")
	require.NoError(t, storage_repo.Storage().SetUsage(e.ctx, failed, storage_entity.Usage{Snapshots: 5, PackedBytes: 500, OriginalBytes: 900, Checktime: at}))
	require.NoError(t, storage_repo.Storage().SetUsageError(e.ctx, failed, "网络中断", "network down", at+60))
	wrongKey := e.storage(t, "wrong-key")
	require.NoError(t, storage_repo.Storage().SetUsage(e.ctx, wrongKey, storage_entity.Usage{Snapshots: 7, PackedBytes: 700, OriginalBytes: 800, Checktime: at}))
	st, err := storage_repo.Storage().Find(e.ctx, wrongKey)
	require.NoError(t, err)
	st.Status, st.StatusCode = storage_entity.StatusWrongKey, code.StorageManagedKeyInvalid
	require.NoError(t, storage_repo.Storage().Save(e.ctx, st))

	convey.Convey("各存储一行：S3 没有磁盘用量；状态不是正常或读不到用量的给出原因，不计入总和", t, func() {
		resp := e.get(t, "")
		ids := make([]int64, 0, len(resp.Storages))
		for _, s := range resp.Storages {
			ids = append(ids, s.ID)
		}
		assert.Equal(t, []int64{s3.ID, local, never, failed, wrongKey}, ids, "按创建顺序")

		row := findStorage(t, resp, s3.ID)
		assert.Equal(t, api.Storage{ID: s3.ID, Name: "offsite", Kind: "s3", Location: "s3://backups/offsite/", Status: "ok",
			Readable: true, PackedBytes: 3000, OriginalBytes: 10000, Snapshots: 12, UsageRecordedAt: at}, *row, "S3 不读磁盘用量")

		row = findStorage(t, resp, local)
		assert.True(t, row.Readable)
		assert.Equal(t, 3, row.Snapshots)
		assert.Nil(t, row.Disk, "目录不存在时读不到磁盘用量")

		row = findStorage(t, resp, never)
		assert.False(t, row.Readable)
		assert.Equal(t, "还没有读取过用量，测试连接后显示", row.Reason)
		assert.Zero(t, row.UsageRecordedAt)

		row = findStorage(t, resp, failed)
		assert.False(t, row.Readable)
		assert.Equal(t, "无法读取用量：网络中断", row.Reason)
		assert.Zero(t, row.Snapshots, "读不到时不显示上一次的数字")
		assert.Zero(t, row.PackedBytes)
		assert.Equal(t, at+60, row.UsageRecordedAt)

		row = findStorage(t, resp, wrongKey)
		assert.Equal(t, "wrong_key", row.Status)
		assert.False(t, row.Readable)
		assert.Equal(t, "保存的密钥无法打开该位置的仓库，请用“重新解锁”提供正确的密钥", row.Reason)
		assert.Zero(t, row.PackedBytes, "状态不是正常时不显示记录的用量")

		assert.InDelta(t, 2.0/3, resp.StorageUsage.Savings, 1e-9, "节省 = 1 - 占用 ÷ 原始总大小")
		resp.StorageUsage.Savings = 0
		assert.Equal(t, api.StorageUsage{PackedBytes: 4000, OriginalBytes: 12000, Unreadable: 3}, resp.StorageUsage,
			"只计入能读到用量的存储")

		en := e.getLang(t, code.LangEn)
		assert.Equal(t, "Usage has not been read yet. Test the connection to read it", findStorage(t, en, never).Reason)
		assert.Equal(t, "Could not read usage: network down", findStorage(t, en, failed).Reason)
	})
}

// 运行之后记录存储用量；与任务页读到的快照数、占用来自同一个仓库（spec 硬约束 1）
func TestOverviewStorageUsageAfterRun(t *testing.T) {
	e := setupTest(t)
	overview_svc.SetClock(nil)
	storage_svc.SetDataDir(t.TempDir())
	t.Cleanup(func() {
		datasource_svc.SetConnector(nil)
		datasource_svc.SetDatabaseLister(nil)
	})
	t.Cleanup(gogo.Wait)
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	job_svc.SetWorkDir(filepath.Join(t.TempDir(), "runs"))
	datasource_svc.SetConnector(fakeConnector{})
	datasource_svc.SetDatabaseLister(func(_ context.Context, _ dsconn.Type, _ *dsconn.Conn) ([]dsapi.Database, error) {
		return []dsapi.Database{{Name: "app"}}, nil
	})
	script := "#!/bin/sh\nPATH=/usr/bin:/bin\n" +
		"if [ \"$1\" = \"--version\" ]; then echo 'pg_dump (PostgreSQL) 16.4'; exit 0; fi\n" +
		"printf 'PGDMP\\001\\016\\000\\004\\010\\001'; printf 'archive app\\n'\n"
	require.NoError(t, os.WriteFile(filepath.Join(bin, "pg_dump"), []byte(script), 0o755)) //nolint:gosec // 测试用假可执行文件

	pw, err := secret_svc.Secret().Encrypt(e.ctx, pgPassword)
	require.NoError(t, err)
	ds := &datasource_entity.DataSource{Name: "analytics", Kind: datasource_entity.KindPostgreSQL, Host: "db.internal", Port: 5432,
		Username: "backup", Password: pw, TLSMode: "disable", Status: datasource_entity.StatusOK}
	require.NoError(t, datasource_repo.DataSource().Create(e.ctx, ds))
	item, _ := e.localStorage(t, "primary")
	created, err := job_svc.Job().Create(e.ctx, &jobapi.CreateRequest{Type: "backup", DataSourceID: ds.ID, StorageID: item.ID,
		Prefix: "pg/analytics", Name: "analytics", Scope: "databases", Databases: []string{"app"}, Method: "full",
		Compression: "zstd", Schedule: jobapi.Schedule{Kind: "daily", Hour: 2, Minute: 30, Timezone: "UTC"},
		Retention: jobapi.Retention{Days: 7}, Failure: jobapi.Failure{RetryInterval: 5, Timeout: 120}})
	require.NoError(t, err)
	started, err := job_svc.Job().RunNow(e.ctx, &jobapi.RunNowRequest{ID: created.Item.ID})
	require.NoError(t, err)
	var run *job_entity.Run
	require.Eventually(t, func() bool {
		run, err = job_repo.Run().Find(e.ctx, started.Run.ID)
		return err == nil && run != nil && run.FinishedAt > 0
	}, 30*time.Second, 20*time.Millisecond)
	require.Equal(t, job_entity.RunSuccess, run.Status, run.Reason)

	convey.Convey("运行之后概览显示新的用量，与任务页的快照数和占用一致", t, func() {
		row := findStorage(t, e.get(t, ""), item.ID)
		assert.True(t, row.Readable, row.Reason)
		assert.Equal(t, 1, row.Snapshots)
		assert.Equal(t, run.ExportedBytes, row.OriginalBytes)
		assert.Positive(t, row.PackedBytes)

		stats, err := job_svc.Job().Stats(e.ctx, &jobapi.StatsRequest{ID: created.Item.ID})
		require.NoError(t, err)
		assert.Equal(t, stats.SnapshotCount, row.Snapshots, "只有这个任务时，与任务页的快照数一致")
		assert.Equal(t, stats.PackedBytes, row.PackedBytes, "与任务页的占用一致")
		assert.Equal(t, stats.ExportBytes, row.OriginalBytes)
	})
}
