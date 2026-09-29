package overview_ctr

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cago-frame/cago/pkg/gogo"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/cago-frame/cago/server/mux/muxclient"
	"github.com/cago-frame/cago/server/mux/muxtest"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authapi "github.com/opskat/opsnap/internal/api/auth"
	dsapi "github.com/opskat/opsnap/internal/api/datasource"
	jobapi "github.com/opskat/opsnap/internal/api/job"
	api "github.com/opskat/opsnap/internal/api/overview"
	storageapi "github.com/opskat/opsnap/internal/api/storage"
	tokenapi "github.com/opskat/opsnap/internal/api/token"
	"github.com/opskat/opsnap/internal/middleware"
	"github.com/opskat/opsnap/internal/model/entity/datasource_entity"
	"github.com/opskat/opsnap/internal/model/entity/job_entity"
	"github.com/opskat/opsnap/internal/model/entity/storage_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/dsconn"
	"github.com/opskat/opsnap/internal/pkg/testdb"
	"github.com/opskat/opsnap/internal/repository/admin_repo"
	"github.com/opskat/opsnap/internal/repository/datasource_repo"
	"github.com/opskat/opsnap/internal/repository/job_repo"
	"github.com/opskat/opsnap/internal/repository/session_repo"
	"github.com/opskat/opsnap/internal/repository/setting_repo"
	"github.com/opskat/opsnap/internal/repository/storage_repo"
	"github.com/opskat/opsnap/internal/repository/token_repo"
	"github.com/opskat/opsnap/internal/service/auth_svc"
	"github.com/opskat/opsnap/internal/service/datasource_svc"
	"github.com/opskat/opsnap/internal/service/job_svc"
	"github.com/opskat/opsnap/internal/service/overview_svc"
	"github.com/opskat/opsnap/internal/service/secret_svc"
	"github.com/opskat/opsnap/internal/service/storage_svc"
	"github.com/opskat/opsnap/internal/service/token_svc"
)

const (
	adminPassword = "correct-horse-battery"
	repoKey       = "Abcd-Efgh-Ijkl-Mnop-Qrst-Uvwx"
	pgPassword    = "pg-S3cret!pw" //nolint:gosec // 假数据源的测试密码
	pgHeader      = "PGDMP\x01\x0e\x00\x04\x08\x01"
	pgDumpHang    = `printf 'PGDMP\001\016\000\004\010\001'; exec sleep 30`
)

// now 测试中概览的当前时间：2026-09-29 01:30 UTC（上海 09:30）
var now = time.Date(2026, 9, 29, 1, 30, 0, 0, time.UTC)

type env struct {
	ctx     context.Context
	mux     *muxtest.TestMux
	token   string // API 令牌
	session string // 浏览器会话
}

func setupTest(t *testing.T) *env {
	ctx := testdb.New(t)
	setting_repo.RegisterSetting(setting_repo.NewSetting())
	admin_repo.RegisterAdmin(admin_repo.NewAdmin())
	session_repo.RegisterSession(session_repo.NewSession())
	token_repo.RegisterToken(token_repo.NewToken())
	storage_repo.RegisterStorage(storage_repo.NewStorage())
	datasource_repo.RegisterDataSource(datasource_repo.NewDataSource())
	job_repo.RegisterJob(job_repo.NewJob())
	_, err := secret_svc.Secret().Init(ctx, secret_svc.InitOptions{DataDir: t.TempDir()})
	require.NoError(t, err)
	overview_svc.SetClock(func() time.Time { return now })
	t.Cleanup(func() { overview_svc.SetClock(nil) })

	setupCode, _ := auth_svc.Auth().PrepareSetupCode(ctx)
	_, issued, err := auth_svc.Auth().Setup(ctx, &authapi.SetupRequest{SetupCode: setupCode, Username: "admin", Password: adminPassword},
		auth_svc.ClientMeta{IP: "192.0.2.1"})
	require.NoError(t, err)
	tok, err := token_svc.Token().Create(ctx, &tokenapi.CreateRequest{Name: "ci"})
	require.NoError(t, err)

	testMux := muxtest.NewTestMux(muxtest.WithBaseUrl("http://opsnap.test/api/v1"))
	ctr := NewOverview()
	authed := testMux.Group("/api/v1", middleware.Language(), middleware.SameOrigin()).Group("/", middleware.Auth())
	authed.Bind(ctr.Get)
	return &env{ctx: ctx, mux: testMux, token: tok.Token, session: issued.Token}
}

// do 用 API 令牌调用
func (e *env) do(req, resp any) error {
	return e.mux.Do(e.ctx, req, resp, muxclient.WithHeader(http.Header{"Authorization": {"Bearer " + e.token}}))
}

// browser 用浏览器会话调用
func (e *env) browser(req, resp any) error {
	return e.mux.Do(e.ctx, req, resp, muxclient.WithHeader(http.Header{"Cookie": {middleware.SessionCookie + "=" + e.session}}))
}

// get 用 API 令牌读取概览
func (e *env) get(t *testing.T, tz string) *api.GetResponse {
	t.Helper()
	resp := &api.GetResponse{}
	require.NoError(t, e.do(&api.GetRequest{TZ: tz}, resp))
	return resp
}

func errCode(err error) int {
	var he *httputils.Error
	if errors.As(err, &he) {
		return he.Code
	}
	return 0
}

func (e *env) dataSource(t *testing.T, name, kind string) *datasource_entity.DataSource {
	t.Helper()
	port := map[string]int{datasource_entity.KindMySQL: 3306, datasource_entity.KindPostgreSQL: 5432}[kind]
	ds := &datasource_entity.DataSource{Name: name, Kind: kind, Host: name + ".internal", Port: port, Username: "backup",
		Status: datasource_entity.StatusOK, TLSMode: "disable"}
	require.NoError(t, datasource_repo.DataSource().Create(e.ctx, ds))
	return ds
}

// storage 只写记录，不需要真实仓库
func (e *env) storage(t *testing.T, name string) int64 {
	t.Helper()
	st := &storage_entity.Storage{Name: name, Kind: "local", Path: "/srv/" + name, LocationKey: "local:/srv/" + name,
		Status: storage_entity.StatusOK}
	require.NoError(t, storage_repo.Storage().Create(e.ctx, st))
	return st.ID
}

// job 每天 hour:00（UTC）执行的任务，snapshots 为任务上记录的快照数
func (e *env) job(t *testing.T, name string, dsID, storageID int64, enabled bool, hour, snapshots int) *job_entity.Job {
	t.Helper()
	j := &job_entity.Job{Name: name, Type: job_entity.TypeBackup, DataSourceID: dsID, StorageID: storageID, Prefix: "p/" + name,
		Scope: job_entity.ScopeInstance, Method: job_entity.MethodFull, Compression: "zstd",
		ScheduleKind: "daily", ScheduleHour: hour, Timezone: "UTC", RetainDays: 7, Timeout: 60,
		Enabled: enabled, EnabledAt: now.Add(-30 * 24 * time.Hour).Unix()}
	j.SetDatabases(nil)
	j.SetWeekdays(nil)
	j.SetExcludeTables(nil)
	require.NoError(t, job_repo.Job().Create(e.ctx, j))
	require.NoError(t, job_repo.Job().SetSnapshotCount(e.ctx, j.ID, snapshots))
	return j
}

// run 一条在 at 触发的运行：跳过与等待中的没有开始时间，其余在 at 开始；已结束的耗时 1 分钟
func (e *env) run(t *testing.T, jobID int64, status string, at time.Time) *job_entity.Run {
	t.Helper()
	r := &job_entity.Run{JobID: jobID, Status: status, Trigger: job_entity.TriggerSchedule, Log: "[]",
		Createtime: at.Unix(), Updatetime: at.Unix()}
	if status != job_entity.RunSkipped && status != job_entity.RunQueued {
		r.StartedAt = at.UnixMilli()
	}
	if status != job_entity.RunQueued && status != job_entity.RunRunning {
		r.FinishedAt = at.Add(time.Minute).UnixMilli()
	}
	if status == job_entity.RunSuccess {
		r.ExportedBytes, r.SnapshotID = 4096, "k"+at.Format("150405")
	}
	require.NoError(t, job_repo.Run().Create(e.ctx, r))
	return r
}

func runIDs(rows []*api.RecentRun) []int64 {
	ids := make([]int64, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	return ids
}

func TestOverviewAuthAndEmpty(t *testing.T) {
	e := setupTest(t)

	convey.Convey("空实例的概览", t, func() {
		convey.Convey("浏览器会话与 API 令牌都可以读取，未登录返回 401", func() {
			byToken, bySession := &api.GetResponse{}, &api.GetResponse{}
			require.NoError(t, e.do(&api.GetRequest{}, byToken))
			require.NoError(t, e.browser(&api.GetRequest{}, bySession))
			assert.Equal(t, byToken, bySession)
			err := e.mux.Do(e.ctx, &api.GetRequest{}, &api.GetResponse{})
			assert.Equal(t, code.Unauthorized, errCode(err))
		})

		convey.Convey("统计为 0 或空，14 天为 14 个空的日子，列表为空数组而不是 null", func() {
			resp := e.get(t, "")
			assert.Equal(t, api.Counts{}, resp.Counts)
			assert.Zero(t, resp.Protected.Count)
			assert.NotNil(t, resp.Protected.ByKind)
			assert.Empty(t, resp.Protected.ByKind)
			assert.Equal(t, api.SuccessRate{}, resp.Success24h)
			assert.Nil(t, resp.NextRun)
			assert.NotNil(t, resp.Recent.Items)
			assert.NotNil(t, resp.Recent.Failed)
			assert.Empty(t, resp.Recent.Items)
			assert.Empty(t, resp.Recent.Failed)
			assert.Zero(t, resp.Recent.Failed24h)
			assert.Equal(t, "UTC", resp.Timezone)
			require.Len(t, resp.Daily, 14)
			assert.Equal(t, "2026-09-16", resp.Daily[0].Date)
			assert.Equal(t, "2026-09-29", resp.Daily[13].Date)
			for _, d := range resp.Daily {
				assert.Zero(t, d.Success+d.Failed, d.Date)
			}
		})
	})
}

func TestOverviewCounts(t *testing.T) {
	e := setupTest(t)
	ds := e.dataSource(t, "orders", datasource_entity.KindMySQL)
	e.dataSource(t, "web", datasource_entity.KindServerFile)
	st := e.storage(t, "primary")

	convey.Convey("引导所需的数量：全部数据源（含服务器文件）、存储与任务", t, func() {
		assert.Equal(t, api.Counts{DataSources: 2, Storages: 1, Jobs: 0}, e.get(t, "").Counts)
		e.job(t, "paused", ds.ID, st, false, 3, 0)
		assert.Equal(t, api.Counts{DataSources: 2, Storages: 1, Jobs: 1}, e.get(t, "").Counts, "暂停的任务也算")
	})
}

func TestOverviewProtected(t *testing.T) {
	e := setupTest(t)
	st := e.storage(t, "primary")
	at := now.Add(-48 * time.Hour)
	// 已启用、成功过、有快照：受保护
	a := e.dataSource(t, "a", datasource_entity.KindMySQL)
	e.run(t, e.job(t, "a1", a.ID, st, true, 3, 1).ID, job_entity.RunSuccess, at)
	// 只失败过、没有快照
	b := e.dataSource(t, "b", datasource_entity.KindPostgreSQL)
	e.run(t, e.job(t, "b1", b.ID, st, true, 3, 0).ID, job_entity.RunFailed, at)
	// 任务已暂停
	c := e.dataSource(t, "c", datasource_entity.KindPostgreSQL)
	e.run(t, e.job(t, "c1", c.ID, st, false, 3, 1).ID, job_entity.RunSuccess, at)
	// 两个任务都符合，只算一次
	d := e.dataSource(t, "d", datasource_entity.KindPostgreSQL)
	e.run(t, e.job(t, "d1", d.ID, st, true, 3, 2).ID, job_entity.RunSuccess, at)
	e.run(t, e.job(t, "d2", d.ID, st, true, 4, 1).ID, job_entity.RunSuccess, at)
	// 成功过，但仓库中已没有本任务的快照
	f := e.dataSource(t, "f", datasource_entity.KindMySQL)
	e.run(t, e.job(t, "f1", f.ID, st, true, 3, 0).ID, job_entity.RunSuccess, at)
	// g1 已启用、记录有快照但只失败过；成功的那次是另一个暂停任务 g2 的
	g := e.dataSource(t, "g", datasource_entity.KindMySQL)
	e.run(t, e.job(t, "g1", g.ID, st, true, 3, 1).ID, job_entity.RunFailed, at)
	e.run(t, e.job(t, "g2", g.ID, st, false, 3, 1).ID, job_entity.RunSuccess, at)
	// 没有被任务引用
	e.dataSource(t, "h", datasource_entity.KindMySQL)

	convey.Convey("受保护的数据源：被已启用、成功过且有快照的任务引用，按类型分布", t, func() {
		p := e.get(t, "").Protected
		assert.Equal(t, 2, p.Count)
		assert.Equal(t, []*api.KindCount{{Kind: "mysql", Count: 1}, {Kind: "postgres", Count: 1}}, p.ByKind)
	})
}

func TestOverviewSuccessRate(t *testing.T) {
	e := setupTest(t)
	ds := e.dataSource(t, "orders", datasource_entity.KindMySQL)
	j := e.job(t, "orders", ds.ID, e.storage(t, "primary"), true, 3, 1).ID
	e.run(t, j, job_entity.RunSuccess, now.Add(-24*time.Hour-time.Minute)) // 开始于 24 小时之前
	e.run(t, j, job_entity.RunFailed, now.Add(-24*time.Hour-time.Minute))
	e.run(t, j, job_entity.RunSuccess, now.Add(-23*time.Hour-59*time.Minute))
	e.run(t, j, job_entity.RunFailed, now.Add(-2*time.Hour))
	e.run(t, j, job_entity.RunSkipped, now.Add(-90*time.Minute))
	e.run(t, j, job_entity.RunCanceled, now.Add(-80*time.Minute))
	e.run(t, j, job_entity.RunSuccess, now.Add(-time.Hour))
	// 触发于 24 小时之前、排队后在 24 小时之内开始：按开始时间计入
	queuedLong := e.run(t, j, job_entity.RunFailed, now.Add(-25*time.Hour))
	queuedLong.StartedAt, queuedLong.FinishedAt = now.Add(-23*time.Hour).UnixMilli(), now.Add(-22*time.Hour).UnixMilli()
	ok, err := job_repo.Run().SaveIf(e.ctx, queuedLong, job_entity.RunFailed)
	require.True(t, ok && err == nil, err)
	e.run(t, j, job_entity.RunRunning, now.Add(-10*time.Minute))
	e.run(t, j, job_entity.RunQueued, now.Add(-5*time.Minute))

	convey.Convey("24h 成功率：开始于最近 24 小时、已结束的运行，不计跳过与取消", t, func() {
		resp := e.get(t, "")
		assert.Equal(t, 4, resp.Success24h.Runs)
		assert.Equal(t, 2, resp.Success24h.Success)
		assert.Equal(t, 2, resp.Success24h.Failed)
		assert.InDelta(t, 0.5, resp.Success24h.SuccessRate, 1e-9)
		assert.Equal(t, 2, resp.Recent.Failed24h, "失败筛选的次数与成功率同一口径")
	})
}

func TestOverviewNextRun(t *testing.T) {
	e := setupTest(t)
	ds := e.dataSource(t, "orders", datasource_entity.KindMySQL)
	st := e.storage(t, "primary")
	e.job(t, "暂停的", ds.ID, st, false, 2, 0)

	convey.Convey("下一次运行", t, func() {
		convey.Convey("没有已启用的任务时为 null", func() {
			assert.Nil(t, e.get(t, "").NextRun)
		})
		convey.Convey("已启用任务中最早的下一次计划时间与任务名，暂停的不算", func() {
			e.job(t, "午间", ds.ID, st, true, 12, 0)
			night := e.job(t, "凌晨", ds.ID, st, true, 3, 0)
			next := e.get(t, "").NextRun
			require.NotNil(t, next)
			assert.Equal(t, api.NextRun{At: time.Date(2026, 9, 29, 3, 0, 0, 0, time.UTC).Unix(), JobID: night.ID, JobName: "凌晨"}, *next)
		})
	})
}

func TestOverviewRecent(t *testing.T) {
	e := setupTest(t)
	st := e.storage(t, "primary")
	orders := e.job(t, "orders", e.dataSource(t, "orders", datasource_entity.KindMySQL).ID, st, true, 3, 1)
	analytics := e.job(t, "analytics", e.dataSource(t, "analytics", datasource_entity.KindPostgreSQL).ID, st, true, 3, 1)
	// 22 次开始于 24 小时之前的失败，两个任务交替
	failed := make([]int64, 0, 22)
	for i := range 22 {
		jobID := orders.ID
		if i%2 == 1 {
			jobID = analytics.ID
		}
		failed = append(failed, e.run(t, jobID, job_entity.RunFailed, now.Add(-30*time.Hour+time.Duration(i)*time.Minute)).ID)
	}
	success := e.run(t, orders.ID, job_entity.RunSuccess, now.Add(-2*time.Hour))
	recentFailed := e.run(t, analytics.ID, job_entity.RunFailed, now.Add(-90*time.Minute))
	skipped := e.run(t, orders.ID, job_entity.RunSkipped, now.Add(-time.Hour))
	canceled := e.run(t, analytics.ID, job_entity.RunCanceled, now.Add(-50*time.Minute))
	queued := e.run(t, orders.ID, job_entity.RunQueued, now.Add(-10*time.Minute))
	running := e.run(t, analytics.ID, job_entity.RunRunning, now.Add(-5*time.Minute))

	convey.Convey("最近运行", t, func() {
		resp := e.get(t, "")

		convey.Convey("所有任务中最近 20 条，按触发顺序倒序，含等待中、运行中、跳过与取消", func() {
			want := []int64{running.ID, queued.ID, canceled.ID, skipped.ID, recentFailed.ID, success.ID}
			for i := 21; len(want) < 20; i-- {
				want = append(want, failed[i])
			}
			assert.Equal(t, want, runIDs(resp.Recent.Items))
			statuses := map[string]bool{}
			for _, r := range resp.Recent.Items {
				statuses[r.Status] = true
			}
			for _, s := range []string{"queued", "running", "canceled", "skipped", "failed", "success"} {
				assert.True(t, statuses[s], s)
			}
		})

		convey.Convey("每行带任务名、类型、数据源类型与地址；运行字段与任务页相同", func() {
			top := resp.Recent.Items[0]
			assert.Equal(t, analytics.ID, top.JobID)
			assert.Equal(t, "analytics", top.JobName)
			assert.Equal(t, "backup", top.JobType)
			assert.Equal(t, "postgres", top.DataSourceKind)
			assert.Equal(t, "postgres://analytics.internal:5432", top.DataSourceAddress)
			assert.Equal(t, "running", top.Status)

			var got *api.RecentRun
			for _, r := range resp.Recent.Items {
				if r.ID == success.ID {
					got = r
				}
			}
			require.NotNil(t, got)
			assert.Equal(t, "orders", got.JobName)
			assert.Equal(t, "mysql", got.DataSourceKind)
			assert.Equal(t, "mysql://orders.internal:3306", got.DataSourceAddress)
			page, err := job_svc.Job().Runs(e.ctx, &jobapi.RunsRequest{ID: orders.ID})
			require.NoError(t, err)
			var fromJobPage *jobapi.Run
			for _, r := range page.Items {
				if r.ID == success.ID {
					fromJobPage = r
				}
			}
			require.NotNil(t, fromJobPage)
			assert.Equal(t, *fromJobPage, got.Run, "与任务页的运行记录一致")
			assert.EqualValues(t, 4096, got.ExportedBytes)
			assert.EqualValues(t, time.Minute.Milliseconds(), got.DurationMs)
		})

		convey.Convey("失败筛选：最近 20 条失败的运行；次数只算最近 24 小时", func() {
			want := []int64{recentFailed.ID}
			for i := 21; len(want) < 20; i-- {
				want = append(want, failed[i])
			}
			assert.Equal(t, want, runIDs(resp.Recent.Failed))
			for _, r := range resp.Recent.Failed {
				assert.Equal(t, "failed", r.Status)
			}
			assert.Equal(t, 1, resp.Recent.Failed24h)
		})
	})
}

func TestOverviewDaily(t *testing.T) {
	e := setupTest(t)
	ds := e.dataSource(t, "orders", datasource_entity.KindMySQL)
	j := e.job(t, "orders", ds.ID, e.storage(t, "primary"), true, 3, 1).ID
	e.run(t, j, job_entity.RunSuccess, time.Date(2026, 9, 28, 17, 0, 0, 0, time.UTC))  // 上海 09-29 01:00
	e.run(t, j, job_entity.RunFailed, time.Date(2026, 9, 28, 15, 0, 0, 0, time.UTC))   // 上海 09-28 23:00
	e.run(t, j, job_entity.RunFailed, time.Date(2026, 9, 29, 1, 0, 0, 0, time.UTC))    // 上海 09-29 09:00
	e.run(t, j, job_entity.RunSuccess, time.Date(2026, 9, 15, 16, 30, 0, 0, time.UTC)) // 上海 09-16 00:30，UTC 09-15
	e.run(t, j, job_entity.RunSuccess, time.Date(2026, 9, 15, 15, 30, 0, 0, time.UTC)) // 上海 09-15 23:30
	e.run(t, j, job_entity.RunSkipped, time.Date(2026, 9, 29, 1, 10, 0, 0, time.UTC))
	e.run(t, j, job_entity.RunCanceled, time.Date(2026, 9, 29, 1, 20, 0, 0, time.UTC))

	days := func(resp *api.GetResponse) map[string][2]int {
		out := map[string][2]int{}
		for _, d := range resp.Daily {
			if d.Success+d.Failed > 0 {
				out[d.Date] = [2]int{d.Success, d.Failed}
			}
		}
		return out
	}

	convey.Convey("14 天运行按客户端时区分天，只计成功与失败", t, func() {
		convey.Convey("上海", func() {
			resp := e.get(t, "Asia/Shanghai")
			assert.Equal(t, "Asia/Shanghai", resp.Timezone)
			require.Len(t, resp.Daily, 14)
			assert.Equal(t, "2026-09-16", resp.Daily[0].Date)
			assert.Equal(t, "2026-09-29", resp.Daily[13].Date)
			assert.Equal(t, map[string][2]int{"2026-09-16": {1, 0}, "2026-09-28": {0, 1}, "2026-09-29": {1, 1}}, days(resp))
		})

		convey.Convey("无法识别的时区按 UTC", func() {
			for _, tz := range []string{"Mars/Olympus", "Local", ""} {
				resp := e.get(t, tz)
				assert.Equal(t, "UTC", resp.Timezone, tz)
				require.Len(t, resp.Daily, 14)
				assert.Equal(t, "2026-09-16", resp.Daily[0].Date)
				assert.Equal(t, map[string][2]int{"2026-09-28": {1, 1}, "2026-09-29": {0, 1}}, days(resp), tz)
			}
		})
	})
}

// fakeConnector 代替数据库协议：连接“成功”并返回服务端版本，不需要真实数据库
type fakeConnector struct{}

func (fakeConnector) Test(context.Context, dsconn.Dialer, dsconn.Config) (dsconn.Info, error) {
	return dsconn.Info{Version: "16.4"}, nil
}

func (fakeConnector) Open(context.Context, dsconn.Dialer, dsconn.Config) (*dsconn.Conn, error) {
	return &dsconn.Conn{Info: dsconn.Info{Version: "16.4"}}, nil
}

func TestOverviewRunningRun(t *testing.T) {
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
	datasource_svc.SetDatabaseLister(func(context.Context, dsconn.Type, *dsconn.Conn) ([]dsapi.Database, error) {
		return []dsapi.Database{{Name: "app"}}, nil
	})
	script := "#!/bin/sh\nPATH=/usr/bin:/bin\n" +
		"if [ \"$1\" = \"--version\" ]; then echo 'pg_dump (PostgreSQL) 16.4'; exit 0; fi\n" + pgDumpHang + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(bin, "pg_dump"), []byte(script), 0o755)) //nolint:gosec // 测试用假可执行文件

	pw, err := secret_svc.Secret().Encrypt(e.ctx, pgPassword)
	require.NoError(t, err)
	ds := &datasource_entity.DataSource{Name: "analytics", Kind: datasource_entity.KindPostgreSQL, Host: "db.internal", Port: 5432,
		Username: "backup", Password: pw, TLSMode: "disable", Status: datasource_entity.StatusOK}
	require.NoError(t, datasource_repo.DataSource().Create(e.ctx, ds))
	st, err := storage_svc.Storage().Create(e.ctx, &storageapi.CreateRequest{Name: "primary",
		Location: storageapi.Location{Kind: "local", Path: filepath.Join(t.TempDir(), "repo")}, Key: repoKey, ConfirmSaved: true})
	require.NoError(t, err)
	created, err := job_svc.Job().Create(e.ctx, &jobapi.CreateRequest{Type: "backup", DataSourceID: ds.ID, StorageID: st.Item.ID,
		Prefix: "pg/analytics", Name: "analytics", Scope: "databases", Databases: []string{"app"}, Method: "full",
		Compression: "zstd", Schedule: jobapi.Schedule{Kind: "daily", Hour: 2, Minute: 30, Timezone: "UTC"},
		Retention: jobapi.Retention{Days: 7}, Failure: jobapi.Failure{RetryInterval: 5, Timeout: 120}})
	require.NoError(t, err)
	started, err := job_svc.Job().RunNow(e.ctx, &jobapi.RunNowRequest{ID: created.Item.ID})
	require.NoError(t, err)

	convey.Convey("运行中的运行显示实时的已导出量与已运行时长", t, func() {
		var top *api.RecentRun
		require.Eventually(t, func() bool {
			items := e.get(t, "").Recent.Items
			if len(items) == 0 {
				return false
			}
			top = items[0]
			return top.Status == "running" && top.ExportedBytes > 0
		}, 30*time.Second, 20*time.Millisecond)
		assert.Equal(t, started.Run.ID, top.ID)
		assert.EqualValues(t, len(pgHeader), top.ExportedBytes)
		assert.GreaterOrEqual(t, top.DurationMs, int64(0))

		_, err := job_svc.Job().CancelRun(e.ctx, &jobapi.CancelRunRequest{ID: created.Item.ID, RunID: started.Run.ID})
		require.NoError(t, err)
	})
}
