package job_ctr

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cago-frame/cago/pkg/gogo"
	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/cago-frame/cago/server/mux/muxclient"
	"github.com/cago-frame/cago/server/mux/muxtest"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authapi "github.com/opskat/opsnap/internal/api/auth"
	dsapi "github.com/opskat/opsnap/internal/api/datasource"
	api "github.com/opskat/opsnap/internal/api/job"
	storageapi "github.com/opskat/opsnap/internal/api/storage"
	tokenapi "github.com/opskat/opsnap/internal/api/token"
	"github.com/opskat/opsnap/internal/middleware"
	"github.com/opskat/opsnap/internal/model/entity/datasource_entity"
	"github.com/opskat/opsnap/internal/model/entity/storage_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/dsconn"
	"github.com/opskat/opsnap/internal/pkg/kopiarepo"
	"github.com/opskat/opsnap/internal/pkg/probe"
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
	"github.com/opskat/opsnap/internal/service/secret_svc"
	"github.com/opskat/opsnap/internal/service/storage_svc"
	"github.com/opskat/opsnap/internal/service/token_svc"
)

const (
	adminPassword = "correct-horse-battery"
	repoKey       = "Abcd-Efgh-Ijkl-Mnop-Qrst-Uvwx"
)

type env struct {
	ctx   context.Context
	mux   *muxtest.TestMux
	token string // API 令牌
	bin   string // 假导出工具所在的 PATH
	// 数据源
	mysql, pg, files, broken *datasource_entity.DataSource
	// 存储：primary、secondary 为真实本地 kopia 仓库，down 的状态为“无法连接”
	primary, secondary, down int64
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
	storage_svc.SetDataDir(t.TempDir())
	t.Cleanup(func() { job_svc.SetActiveRunChecker(nil) })
	// 运行在后台执行：先等它们结束，再恢复连接器并释放数据库
	t.Cleanup(func() {
		datasource_svc.SetConnector(nil)
		datasource_svc.SetDatabaseLister(nil)
	})
	t.Cleanup(gogo.Wait)
	e := &env{ctx: ctx, bin: t.TempDir()}
	t.Setenv("PATH", e.bin)
	probe.SetToolsDir("")
	job_svc.SetWorkDir(filepath.Join(t.TempDir(), "runs"))
	datasource_svc.SetConnector(fakeConnector{})
	// 假连接没有真实数据库：运行时列出的库由测试给出
	datasource_svc.SetDatabaseLister(func(context.Context, dsconn.Type, *dsconn.Conn) ([]dsapi.Database, error) {
		return []dsapi.Database{{Name: "app"}, {Name: "reports"}}, nil
	})
	e.tool(pgDumpOK)

	setupCode, _ := auth_svc.Auth().PrepareSetupCode(ctx)
	_, _, err = auth_svc.Auth().Setup(ctx, &authapi.SetupRequest{SetupCode: setupCode, Username: "admin", Password: adminPassword},
		auth_svc.ClientMeta{IP: "192.0.2.1"})
	require.NoError(t, err)
	tok, err := token_svc.Token().Create(ctx, &tokenapi.CreateRequest{Name: "ci"})
	require.NoError(t, err)

	e.token = tok.Token
	encPG, err := secret_svc.Secret().Encrypt(ctx, pgPassword)
	require.NoError(t, err)
	newDS := func(name, kind, status string) *datasource_entity.DataSource {
		ds := &datasource_entity.DataSource{Name: name, Kind: kind, Host: "db.internal", Port: 3306, Status: status,
			Username: "backup", Password: encPG, TLSMode: "disable"}
		require.NoError(t, datasource_repo.DataSource().Create(ctx, ds))
		return ds
	}
	e.mysql = newDS("orders", datasource_entity.KindMySQL, datasource_entity.StatusOK)
	e.pg = newDS("analytics", datasource_entity.KindPostgreSQL, datasource_entity.StatusOK)
	e.files = newDS("web-files", datasource_entity.KindServerFile, datasource_entity.StatusOK)
	e.broken = newDS("legacy", datasource_entity.KindMySQL, datasource_entity.StatusUnreachable)

	newStorage := func(name string) int64 {
		resp, err := storage_svc.Storage().Create(ctx, &storageapi.CreateRequest{Name: name,
			Location: storageapi.Location{Kind: "local", Path: filepath.Join(t.TempDir(), "repo")}, Key: repoKey, ConfirmSaved: true})
		require.NoError(t, err)
		return resp.Item.ID
	}
	e.primary, e.secondary = newStorage("primary"), newStorage("secondary")
	// 状态为“无法连接”的存储不会被打开，不需要真实仓库
	down := &storage_entity.Storage{Name: "down", Kind: "local", Path: "/nonexistent/opsnap", LocationKey: "local:/nonexistent/opsnap",
		Status: storage_entity.StatusUnreachable}
	require.NoError(t, storage_repo.Storage().Create(ctx, down))
	e.down = down.ID

	testMux := muxtest.NewTestMux(muxtest.WithBaseUrl("http://opsnap.test/api/v1"))
	ctr := NewJob()
	authed := testMux.Group("/api/v1", middleware.SameOrigin()).Group("/", middleware.Auth())
	authed.Bind(ctr.List, ctr.Get, ctr.Create, ctr.Update, ctr.Pause, ctr.Enable, ctr.Delete, ctr.SchedulePreview,
		ctr.RunNow, ctr.CancelRun, ctr.Runs, ctr.RunLog, ctr.Stats)
	e.mux = testMux
	return e
}

// do 用 API 令牌调用
func (e *env) do(req, resp any) error {
	return e.mux.Do(e.ctx, req, resp, muxclient.WithHeader(http.Header{"Authorization": {"Bearer " + e.token}}))
}

func errCode(err error) int {
	var he *httputils.Error
	if errors.As(err, &he) {
		return he.Code
	}
	return 0
}

func errMsg(err error) string {
	var he *httputils.Error
	if errors.As(err, &he) {
		return he.Msg
	}
	return ""
}

// validCreate 一份合法的 MySQL 任务：每天 02:30（上海），保留 7/4/6，失败重试 2 次、间隔 5 分钟、超时 2 小时；等下一次计划
func (e *env) validCreate(name, prefix string) *api.CreateRequest {
	return &api.CreateRequest{
		Type: "backup", DataSourceID: e.mysql.ID, StorageID: e.primary, Prefix: prefix,
		Name: name, Scope: "instance", Method: "full",
		Options:     api.Options{Routines: true, Triggers: true, Events: true},
		Compression: "zstd",
		Schedule:    api.Schedule{Kind: "daily", Hour: 2, Minute: 30, Timezone: "Asia/Shanghai"},
		Retention:   api.Retention{Days: 7, Weeks: 4, Months: 6},
		Failure:     api.Failure{Retries: 2, RetryInterval: 5, Timeout: 120},
	}
}

func (e *env) create(t *testing.T, req *api.CreateRequest) *api.Item {
	t.Helper()
	resp := &api.CreateResponse{}
	require.NoError(t, e.do(req, resp))
	return resp.Item
}

// updateFrom 用新建请求的设置构造编辑请求（不带不可修改的字段）
func updateFrom(id int64, c *api.CreateRequest) *api.UpdateRequest {
	return &api.UpdateRequest{ID: id, Name: c.Name, Scope: c.Scope, Databases: c.Databases, Method: c.Method,
		Options: c.Options, ExcludeTables: c.ExcludeTables, Compression: c.Compression,
		Schedule: c.Schedule, Retention: c.Retention, Failure: c.Failure}
}

func TestJobCreate(t *testing.T) {
	e := setupTest(t)

	convey.Convey("新建任务", t, func() {
		convey.Convey("合法的 MySQL 任务：保存全部设置并启用，经 API 令牌可以获取与列出", func() {
			req := e.validCreate("orders 全量备份", "mysql/orders")
			req.Options.Globals = true // PostgreSQL 专用项对 MySQL 任务无效，保存为 false
			req.ExcludeTables = []string{" shop.logs ", "", "shop.logs", "shop.audit"}
			item := e.create(t, req)
			assert.NotZero(t, item.ID)
			assert.Equal(t, "orders 全量备份", item.Name)
			assert.Equal(t, "backup", item.Type)
			assert.Equal(t, e.mysql.ID, item.DataSourceID)
			assert.Equal(t, "orders", item.DataSourceName)
			assert.Equal(t, "mysql", item.DataSourceKind)
			assert.Equal(t, "primary", item.StorageName)
			assert.Equal(t, "primary:/mysql/orders", item.Location)
			assert.Equal(t, api.Options{Routines: true, Triggers: true, Events: true}, item.Options)
			assert.Equal(t, []string{"shop.logs", "shop.audit"}, item.ExcludeTables, "去掉空行、首尾空白与重复")
			assert.Equal(t, []string{}, item.Databases)
			assert.Equal(t, api.Schedule{Kind: "daily", Hour: 2, Minute: 30, Weekdays: []int{}, Timezone: "Asia/Shanghai"}, item.Schedule)
			assert.Equal(t, api.Retention{Days: 7, Weeks: 4, Months: 6}, item.Retention)
			assert.Equal(t, api.Failure{Retries: 2, RetryInterval: 5, Timeout: 120}, item.Failure)
			assert.True(t, item.Enabled)
			next := time.Unix(item.NextRunAt, 0).In(time.FixedZone("CST", 8*3600))
			assert.True(t, next.After(time.Now()))
			assert.Equal(t, []int{2, 30}, []int{next.Hour(), next.Minute()}, "下一次执行按任务时区的 02:30")

			saved, err := job_repo.Job().Find(e.ctx, item.ID)
			require.NoError(t, err)
			assert.NotZero(t, saved.EnabledAt)
			assert.Nil(t, item.LastRun, "等下一次计划：没有运行")

			got := &api.GetResponse{}
			require.NoError(t, e.do(&api.GetRequest{ID: item.ID}, got))
			assert.Equal(t, item, got.Item)
			list := &api.ListResponse{}
			require.NoError(t, e.do(&api.ListRequest{}, list))
			require.Len(t, list.Items, 1)
			assert.Equal(t, item, list.Items[0])
		})

		convey.Convey("PostgreSQL 指定数据库：库名去重，MySQL 专用项保存为 false；等下一次计划", func() {
			req := e.validCreate("analytics 全量备份", "postgres/analytics")
			req.DataSourceID = e.pg.ID
			req.Scope, req.Databases = "databases", []string{"app", " app", "reports"}
			req.Options = api.Options{Routines: true, Users: true, Globals: true}
			req.ExcludeTables = []string{"app.public.sessions"}
			req.Schedule = api.Schedule{Kind: "weekly", Hour: 3, Weekdays: []int{5, 1, 5}, Timezone: "UTC"}
			req.RunNow = false
			item := e.create(t, req)
			assert.Equal(t, []string{"app", "reports"}, item.Databases)
			assert.Equal(t, api.Options{Globals: true}, item.Options)
			assert.Equal(t, []int{1, 5}, item.Schedule.Weekdays)
			saved, err := job_repo.Job().Find(e.ctx, item.ID)
			require.NoError(t, err)
			assert.False(t, saved.RunNow)
		})

		convey.Convey("取值范围的边界都能保存", func() {
			lo := e.validCreate("最小值", "b/min")
			lo.Retention = api.Retention{Days: 1, Weeks: 0, Months: 0}
			lo.Failure = api.Failure{Retries: 0, RetryInterval: 1, Timeout: 10}
			lo.Compression = "none"
			lo.Name = strings.Repeat("名", 64)
			lo.Prefix = "A1._-/" + strings.Repeat("x", 122)
			item := e.create(t, lo)
			assert.Equal(t, lo.Failure, item.Failure)
			assert.Len(t, item.Prefix, 128)

			hi := e.validCreate("最大值", "b/max")
			hi.Retention = api.Retention{Days: 365, Weeks: 520, Months: 120}
			hi.Failure = api.Failure{Retries: 5, RetryInterval: 120, Timeout: 48 * 60}
			hi.Compression = "gzip"
			hi.Schedule = api.Schedule{Kind: "cron", Cron: "*/15 9-18 * * 1-5", Timezone: "America/New_York"}
			item = e.create(t, hi)
			assert.Equal(t, hi.Retention, item.Retention)
		})
	})
}

func TestJobCreateValidation(t *testing.T) {
	e := setupTest(t)
	e.create(t, e.validCreate("已有任务", "mysql/orders"))

	cases := []struct {
		name string
		edit func(r *api.CreateRequest)
		code int
	}{
		{"名称为空", func(r *api.CreateRequest) { r.Name = "  " }, code.JobNameInvalid},
		{"名称超过 64 个字符", func(r *api.CreateRequest) { r.Name = strings.Repeat("名", 65) }, code.JobNameInvalid},
		{"名称重复", func(r *api.CreateRequest) { r.Name = " 已有任务 " }, code.JobNameDuplicate},
		{"同步任务尚不支持", func(r *api.CreateRequest) { r.Type = "sync" }, code.JobTypeUnsupported},
		{"数据源不存在", func(r *api.CreateRequest) { r.DataSourceID = 999 }, code.JobDataSourceNotFound},
		{"服务器文件数据源尚不支持", func(r *api.CreateRequest) { r.DataSourceID = e.files.ID }, code.JobDataSourceUnsupported},
		{"数据源状态不是正常", func(r *api.CreateRequest) { r.DataSourceID = e.broken.ID }, code.JobDataSourceNotReady},
		{"存储不存在", func(r *api.CreateRequest) { r.StorageID = 999 }, code.JobStorageNotFound},
		{"存储状态不是正常", func(r *api.CreateRequest) { r.StorageID = e.down }, code.JobStorageNotReady},
		{"备份范围不合法", func(r *api.CreateRequest) { r.Scope = "tables" }, code.JobScopeInvalid},
		{"指定数据库但一个也没选", func(r *api.CreateRequest) { r.Scope, r.Databases = "databases", []string{" "} }, code.JobDatabasesRequired},
		{"增量方式尚不支持", func(r *api.CreateRequest) { r.Method = "binlog" }, code.JobMethodUnsupported},
		{"MySQL 排除表少一段", func(r *api.CreateRequest) { r.ExcludeTables = []string{"logs"} }, code.JobExcludeInvalid},
		{"MySQL 排除表多一段", func(r *api.CreateRequest) { r.ExcludeTables = []string{"shop.public.logs"} }, code.JobExcludeInvalid},
		{"排除表有空段", func(r *api.CreateRequest) { r.ExcludeTables = []string{"shop."} }, code.JobExcludeInvalid},
		{"PostgreSQL 排除表少一段", func(r *api.CreateRequest) {
			r.DataSourceID, r.Options, r.ExcludeTables = e.pg.ID, api.Options{}, []string{"app.sessions"}
		}, code.JobExcludeInvalid},
		{"路径前缀为空", func(r *api.CreateRequest) { r.Prefix = "" }, code.JobPrefixInvalid},
		{"路径前缀以 / 开头", func(r *api.CreateRequest) { r.Prefix = "/x" }, code.JobPrefixInvalid},
		{"路径前缀以 / 结尾", func(r *api.CreateRequest) { r.Prefix = "x/" }, code.JobPrefixInvalid},
		{"路径前缀含 ..", func(r *api.CreateRequest) { r.Prefix = "x/../y" }, code.JobPrefixInvalid},
		{"路径前缀含连续的 /", func(r *api.CreateRequest) { r.Prefix = "x//y" }, code.JobPrefixInvalid},
		{"路径前缀含非法字符", func(r *api.CreateRequest) { r.Prefix = "x y" }, code.JobPrefixInvalid},
		{"路径前缀含非 ASCII 字符", func(r *api.CreateRequest) { r.Prefix = "备份" }, code.JobPrefixInvalid},
		{"路径前缀超过 128 个字符", func(r *api.CreateRequest) { r.Prefix = strings.Repeat("x", 129) }, code.JobPrefixInvalid},
		{"压缩方式不合法", func(r *api.CreateRequest) { r.Compression = "lz4" }, code.JobCompressionInvalid},
		{"Cron 段数不对", func(r *api.CreateRequest) { r.Schedule = api.Schedule{Kind: "cron", Cron: "* * *", Timezone: "UTC"} }, code.JobScheduleInvalid},
		{"Cron 永不触发", func(r *api.CreateRequest) {
			r.Schedule = api.Schedule{Kind: "cron", Cron: "0 0 31 2 *", Timezone: "UTC"}
		}, code.JobScheduleInvalid},
		{"频率未知", func(r *api.CreateRequest) { r.Schedule.Kind = "yearly" }, code.JobScheduleInvalid},
		{"每小时的分钟越界", func(r *api.CreateRequest) { r.Schedule = api.Schedule{Kind: "hourly", Minute: 60, Timezone: "UTC"} }, code.JobScheduleInvalid},
		{"每周未选星期几", func(r *api.CreateRequest) { r.Schedule = api.Schedule{Kind: "weekly", Timezone: "UTC"} }, code.JobScheduleInvalid},
		{"时区为空", func(r *api.CreateRequest) { r.Schedule.Timezone = "" }, code.JobTimezoneInvalid},
		{"时区不存在", func(r *api.CreateRequest) { r.Schedule.Timezone = "Mars/Olympus" }, code.JobTimezoneInvalid},
		{"Local 不是 IANA 时区", func(r *api.CreateRequest) { r.Schedule.Timezone = "Local" }, code.JobTimezoneInvalid},
		{"保留天数为 0", func(r *api.CreateRequest) { r.Retention.Days = 0 }, code.JobRetentionDaysInvalid},
		{"保留天数超过 365", func(r *api.CreateRequest) { r.Retention.Days = 366 }, code.JobRetentionDaysInvalid},
		{"保留周数为负", func(r *api.CreateRequest) { r.Retention.Weeks = -1 }, code.JobRetentionWeeksInvalid},
		{"保留周数超过 520", func(r *api.CreateRequest) { r.Retention.Weeks = 521 }, code.JobRetentionWeeksInvalid},
		{"保留月数为负", func(r *api.CreateRequest) { r.Retention.Months = -1 }, code.JobRetentionMonthsInvalid},
		{"保留月数超过 120", func(r *api.CreateRequest) { r.Retention.Months = 121 }, code.JobRetentionMonthsInvalid},
		{"重试次数为负", func(r *api.CreateRequest) { r.Failure.Retries = -1 }, code.JobRetriesInvalid},
		{"重试次数超过 5", func(r *api.CreateRequest) { r.Failure.Retries = 6 }, code.JobRetriesInvalid},
		{"重试间隔为 0", func(r *api.CreateRequest) { r.Failure.RetryInterval = 0 }, code.JobRetryIntervalInvalid},
		{"重试间隔超过 120 分钟", func(r *api.CreateRequest) { r.Failure.RetryInterval = 121 }, code.JobRetryIntervalInvalid},
		{"超时短于 10 分钟", func(r *api.CreateRequest) { r.Failure.Timeout = 9 }, code.JobTimeoutInvalid},
		{"超时超过 48 小时", func(r *api.CreateRequest) { r.Failure.Timeout = 48*60 + 1 }, code.JobTimeoutInvalid},
	}
	convey.Convey("新建任务的字段校验：不合法时返回对应字段的错误码，不保存", t, func() {
		for i, c := range cases {
			convey.Convey(c.name, func() {
				req := e.validCreate("新任务", "check/"+string(rune('a'+i%26))+strings.Repeat("x", i/26))
				c.edit(req)
				err := e.do(req, &api.CreateResponse{})
				assert.Equal(t, c.code, errCode(err), "%v", err)
				list := &api.ListResponse{}
				require.NoError(t, e.do(&api.ListRequest{}, list))
				assert.Len(t, list.Items, 1)
			})
		}
	})
}

func TestJobPrefixConflict(t *testing.T) {
	e := setupTest(t)
	e.create(t, e.validCreate("订单库", "mysql/orders"))

	convey.Convey("同一存储中路径前缀不能相同，也不能互为上下级", t, func() {
		for _, prefix := range []string{"mysql/orders", "mysql", "mysql/orders/daily"} {
			convey.Convey("冲突："+prefix, func() {
				err := e.do(e.validCreate("另一个", prefix), &api.CreateResponse{})
				assert.Equal(t, code.JobPrefixConflict, errCode(err))
				assert.Contains(t, errMsg(err), "订单库", "提示与哪个任务冲突")
			})
		}
		convey.Convey("只是字符串前缀相同、不是上下级时不冲突", func() {
			e.create(t, e.validCreate("订单库 2", "mysql/orders2"))
		})
		convey.Convey("另一个存储中可以使用相同前缀", func() {
			req := e.validCreate("订单库异地", "mysql/orders")
			req.StorageID = e.secondary
			e.create(t, req)
		})
	})
}

func TestJobUpdate(t *testing.T) {
	e := setupTest(t)
	orig := e.validCreate("订单库", "mysql/orders")
	item := e.create(t, orig)
	e.create(t, e.validCreate("其他任务", "mysql/other"))

	convey.Convey("编辑任务", t, func() {
		convey.Convey("修改名称、范围、计划、保留与失败处理，立即生效", func() {
			req := updateFrom(item.ID, orig)
			req.Name = "订单库（新）"
			req.Scope, req.Databases = "databases", []string{"shop"}
			req.Compression = "gzip"
			req.Schedule = api.Schedule{Kind: "hourly", Minute: 15, Timezone: "Europe/Berlin"}
			req.Retention = api.Retention{Days: 30, Weeks: 8, Months: 12}
			req.Failure = api.Failure{Retries: 0, RetryInterval: 10, Timeout: 60}
			resp := &api.UpdateResponse{}
			require.NoError(t, e.do(req, resp))
			assert.Equal(t, "订单库（新）", resp.Item.Name)
			assert.Equal(t, []string{"shop"}, resp.Item.Databases)
			assert.Equal(t, req.Retention, resp.Item.Retention)
			assert.Equal(t, req.Failure, resp.Item.Failure)
			assert.Equal(t, 15, time.Unix(resp.Item.NextRunAt, 0).UTC().Minute())
			got := &api.GetResponse{}
			require.NoError(t, e.do(&api.GetRequest{ID: item.ID}, got))
			assert.Equal(t, resp.Item, got.Item)
			assert.Equal(t, item.CreatedAt, got.Item.CreatedAt)
		})

		convey.Convey("带上与保存值相同的数据源、存储、前缀可以保存", func() {
			req := updateFrom(item.ID, orig)
			req.DataSourceID, req.StorageID, req.Prefix = e.mysql.ID, e.primary, "mysql/orders"
			require.NoError(t, e.do(req, &api.UpdateResponse{}))
		})

		convey.Convey("数据源、存储、路径前缀创建后不能修改", func() {
			for _, edit := range []func(r *api.UpdateRequest){
				func(r *api.UpdateRequest) { r.DataSourceID = e.pg.ID },
				func(r *api.UpdateRequest) { r.StorageID = e.secondary },
				func(r *api.UpdateRequest) { r.Prefix = "mysql/orders-v2" },
			} {
				req := updateFrom(item.ID, orig)
				edit(req)
				err := e.do(req, &api.UpdateResponse{})
				assert.Equal(t, code.JobImmutableField, errCode(err))
			}
			saved, err := job_repo.Job().Find(e.ctx, item.ID)
			require.NoError(t, err)
			assert.Equal(t, []any{e.mysql.ID, e.primary, "mysql/orders"}, []any{saved.DataSourceID, saved.StorageID, saved.Prefix})
		})

		convey.Convey("校验与新建相同：名称不能与其他任务重复，字段不合法时不保存", func() {
			req := updateFrom(item.ID, orig)
			req.Name = "其他任务"
			assert.Equal(t, code.JobNameDuplicate, errCode(e.do(req, &api.UpdateResponse{})))
			req = updateFrom(item.ID, orig)
			req.Retention.Days = 0
			assert.Equal(t, code.JobRetentionDaysInvalid, errCode(e.do(req, &api.UpdateResponse{})))
			req = updateFrom(item.ID, orig)
			req.ExcludeTables = []string{"shop.public.logs"} // MySQL 任务按 库.表 校验
			assert.Equal(t, code.JobExcludeInvalid, errCode(e.do(req, &api.UpdateResponse{})))
		})

		convey.Convey("数据源或存储后来状态异常时仍可编辑计划", func() {
			st, err := storage_repo.Storage().Find(e.ctx, e.primary)
			require.NoError(t, err)
			st.Status = storage_entity.StatusWrongKey
			require.NoError(t, storage_repo.Storage().Save(e.ctx, st))
			defer func() {
				st.Status = storage_entity.StatusOK
				require.NoError(t, storage_repo.Storage().Save(e.ctx, st))
			}()
			require.NoError(t, e.do(updateFrom(item.ID, orig), &api.UpdateResponse{}))
		})

		convey.Convey("任务不存在", func() {
			err := e.do(updateFrom(999, orig), &api.UpdateResponse{})
			assert.Equal(t, code.JobNotFound, errCode(err))
		})
	})
}

func TestJobPauseEnable(t *testing.T) {
	e := setupTest(t)
	item := e.create(t, e.validCreate("订单库", "mysql/orders"))

	convey.Convey("暂停与启用", t, func() {
		paused := &api.PauseResponse{}
		require.NoError(t, e.do(&api.PauseRequest{ID: item.ID}, paused))
		assert.False(t, paused.Item.Enabled)
		assert.Zero(t, paused.Item.NextRunAt, "暂停后计划不再触发")
		require.NoError(t, e.do(&api.PauseRequest{ID: item.ID}, paused), "重复暂停不报错")

		before, err := job_repo.Job().Find(e.ctx, item.ID)
		require.NoError(t, err)
		before.EnabledAt = 1
		require.NoError(t, job_repo.Job().Save(e.ctx, before))

		enabled := &api.EnableResponse{}
		require.NoError(t, e.do(&api.EnableRequest{ID: item.ID}, enabled))
		assert.True(t, enabled.Item.Enabled)
		assert.Equal(t, item.NextRunAt, enabled.Item.NextRunAt)
		after, err := job_repo.Job().Find(e.ctx, item.ID)
		require.NoError(t, err)
		assert.Greater(t, after.EnabledAt, int64(1), "启用时间更新：暂停期间的计划不算错过")

		require.NoError(t, e.do(&api.EnableRequest{ID: item.ID}, enabled))
		again, err := job_repo.Job().Find(e.ctx, item.ID)
		require.NoError(t, err)
		assert.Equal(t, after.EnabledAt, again.EnabledAt, "已启用时再次启用不改变启用时间")

		assert.Equal(t, code.JobNotFound, errCode(e.do(&api.PauseRequest{ID: 999}, &api.PauseResponse{})))
		assert.Equal(t, code.JobNotFound, errCode(e.do(&api.EnableRequest{ID: 999}, &api.EnableResponse{})))
	})
}

// writeSnapshot 以任务的身份往存储写一份快照
func (e *env) writeSnapshot(t *testing.T, storageID, jobID int64, prefix string) {
	t.Helper()
	w, err := storage_svc.Storage().OpenWriter(e.ctx, storageID)
	require.NoError(t, err)
	defer func() { require.NoError(t, w.Close(e.ctx)) }()
	_, err = w.WriteSnapshot(e.ctx, kopiarepo.SnapshotRequest{Prefix: prefix,
		Tags:  kopiarepo.SnapshotTags{JobID: jobID, RunID: 1, Type: "mysql", Kind: "full"},
		Files: []kopiarepo.SnapshotFile{{Name: "dump.sql", Reader: strings.NewReader("CREATE TABLE t (id int);")}}})
	require.NoError(t, err)
}

func (e *env) countSnapshots(t *testing.T, storageID, jobID int64, prefix string) int {
	t.Helper()
	w, err := storage_svc.Storage().OpenWriter(e.ctx, storageID)
	require.NoError(t, err)
	defer func() { require.NoError(t, w.Close(e.ctx)) }()
	snaps, err := w.ListJobSnapshots(e.ctx, kopiarepo.JobRef{JobID: jobID, Prefix: prefix})
	require.NoError(t, err)
	return len(snaps)
}

func TestJobDelete(t *testing.T) {
	e := setupTest(t)

	convey.Convey("删除任务", t, func() {
		convey.Convey("默认保留快照", func() {
			item := e.create(t, e.validCreate("保留快照", "keep/me"))
			e.writeSnapshot(t, e.primary, item.ID, "keep/me")
			resp := &api.DeleteResponse{}
			require.NoError(t, e.do(&api.DeleteRequest{ID: item.ID}, resp))
			assert.Equal(t, api.DeleteResponse{}, *resp)
			assert.Equal(t, code.JobNotFound, errCode(e.do(&api.GetRequest{ID: item.ID}, &api.GetResponse{})))
			assert.Equal(t, 1, e.countSnapshots(t, e.primary, item.ID, "keep/me"))
		})

		convey.Convey("勾选同时删除快照：只删除本任务的快照", func() {
			item := e.create(t, e.validCreate("删快照", "drop/me"))
			other := e.create(t, e.validCreate("邻居", "drop/other"))
			e.writeSnapshot(t, e.primary, item.ID, "drop/me")
			e.writeSnapshot(t, e.primary, item.ID, "drop/me")
			e.writeSnapshot(t, e.primary, other.ID, "drop/other")

			resp := &api.DeleteResponse{}
			require.NoError(t, e.do(&api.DeleteRequest{ID: item.ID, DeleteSnapshots: true}, resp))
			assert.Equal(t, api.DeleteResponse{SnapshotsDeleted: 2}, *resp)
			assert.Equal(t, 0, e.countSnapshots(t, e.primary, item.ID, "drop/me"))
			assert.Equal(t, 1, e.countSnapshots(t, e.primary, other.ID, "drop/other"), "其他任务的快照不受影响")
		})

		convey.Convey("无法打开存储时任务仍然删除，并提示快照未能删除", func() {
			item := e.create(t, e.validCreate("存储出错", "broken/store"))
			st, err := storage_repo.Storage().Find(e.ctx, e.primary)
			require.NoError(t, err)
			st.Status = storage_entity.StatusUnreachable
			require.NoError(t, storage_repo.Storage().Save(e.ctx, st))
			defer func() {
				st.Status = storage_entity.StatusOK
				require.NoError(t, storage_repo.Storage().Save(e.ctx, st))
			}()

			resp := &api.DeleteResponse{}
			require.NoError(t, e.do(&api.DeleteRequest{ID: item.ID, DeleteSnapshots: true}, resp))
			assert.Equal(t, i18n.T(e.ctx, code.JobSnapshotsUnreachable), resp.SnapshotsMessage)
			assert.Zero(t, resp.SnapshotsDeleted)
			assert.Equal(t, code.JobNotFound, errCode(e.do(&api.GetRequest{ID: item.ID}, &api.GetResponse{})))
		})

		convey.Convey("任务正在运行或排队时不能删除", func() {
			item := e.create(t, e.validCreate("运行中", "busy/job"))
			var asked []int64
			job_svc.SetActiveRunChecker(func(_ context.Context, jobID int64) (bool, error) {
				asked = append(asked, jobID)
				return jobID == item.ID, nil
			})
			defer job_svc.SetActiveRunChecker(nil)
			err := e.do(&api.DeleteRequest{ID: item.ID, DeleteSnapshots: true}, &api.DeleteResponse{})
			assert.Equal(t, code.JobRunActive, errCode(err))
			assert.Equal(t, []int64{item.ID}, asked)
			require.NoError(t, e.do(&api.GetRequest{ID: item.ID}, &api.GetResponse{}), "任务仍在")

			job_svc.SetActiveRunChecker(func(context.Context, int64) (bool, error) { return false, errors.New("boom") })
			require.Error(t, e.do(&api.DeleteRequest{ID: item.ID}, &api.DeleteResponse{}))
			require.NoError(t, e.do(&api.GetRequest{ID: item.ID}, &api.GetResponse{}), "查询运行状态失败时不删除")

			job_svc.SetActiveRunChecker(nil)
			require.NoError(t, e.do(&api.DeleteRequest{ID: item.ID}, &api.DeleteResponse{}), "默认没有进行中的运行")
		})

		convey.Convey("任务不存在", func() {
			assert.Equal(t, code.JobNotFound, errCode(e.do(&api.DeleteRequest{ID: 999}, &api.DeleteResponse{})))
		})
	})
}

func TestJobSchedulePreview(t *testing.T) {
	e := setupTest(t)

	convey.Convey("计划预览：接下来三次执行时间（任务时区）与最多保留份数", t, func() {
		convey.Convey("每天 02:30 上海", func() {
			resp := &api.SchedulePreviewResponse{}
			require.NoError(t, e.do(&api.SchedulePreviewRequest{
				Schedule:  api.Schedule{Kind: "daily", Hour: 2, Minute: 30, Timezone: "Asia/Shanghai"},
				Retention: api.Retention{Days: 7, Weeks: 4, Months: 6},
			}, resp))
			require.Len(t, resp.NextRuns, 3)
			var prev time.Time
			for i, s := range resp.NextRuns {
				assert.True(t, strings.HasSuffix(s, "T02:30:00+08:00"), "按任务时区表示：%s", s)
				ts, err := time.Parse(time.RFC3339, s)
				require.NoError(t, err)
				if i == 0 {
					assert.True(t, ts.After(time.Now()))
				} else {
					assert.Equal(t, 24*time.Hour, ts.Sub(prev))
				}
				prev = ts
			}
			assert.Equal(t, 7+4+6+1, resp.MaxSnapshots)
		})

		convey.Convey("每小时：K 计入 N 天内的全部次数", func() {
			resp := &api.SchedulePreviewResponse{}
			require.NoError(t, e.do(&api.SchedulePreviewRequest{
				Schedule:  api.Schedule{Kind: "hourly", Minute: 5, Timezone: "UTC"},
				Retention: api.Retention{Days: 7, Weeks: 4, Months: 6},
			}, resp))
			assert.Equal(t, 7*24+4+6+1, resp.MaxSnapshots)
			assert.True(t, strings.HasSuffix(resp.NextRuns[0], ":05:00Z"), resp.NextRuns[0])
		})

		convey.Convey("不合法的计划或保留策略给出字段错误", func() {
			err := e.do(&api.SchedulePreviewRequest{
				Schedule:  api.Schedule{Kind: "cron", Cron: "61 * * * *", Timezone: "UTC"},
				Retention: api.Retention{Days: 7},
			}, &api.SchedulePreviewResponse{})
			assert.Equal(t, code.JobScheduleInvalid, errCode(err))
			assert.Contains(t, errMsg(err), "分钟", "附上原因")
			err = e.do(&api.SchedulePreviewRequest{
				Schedule:  api.Schedule{Kind: "daily", Timezone: "Nowhere/City"},
				Retention: api.Retention{Days: 7},
			}, &api.SchedulePreviewResponse{})
			assert.Equal(t, code.JobTimezoneInvalid, errCode(err))
			err = e.do(&api.SchedulePreviewRequest{
				Schedule:  api.Schedule{Kind: "daily", Timezone: "UTC"},
				Retention: api.Retention{Days: 7, Months: 121},
			}, &api.SchedulePreviewResponse{})
			assert.Equal(t, code.JobRetentionMonthsInvalid, errCode(err))
		})
	})
}

func TestJobReferences(t *testing.T) {
	e := setupTest(t)
	a := e.create(t, e.validCreate("任务 A", "ref/a"))
	req := e.validCreate("任务 B", "ref/b")
	req.StorageID = e.secondary
	b := e.create(t, req)
	req = e.validCreate("任务 C", "ref/c")
	req.DataSourceID = e.pg.ID
	c := e.create(t, req)

	convey.Convey("查询引用某个数据源或存储的任务（供引用保护）", t, func() {
		refs, err := job_svc.Job().ByDataSource(e.ctx, e.mysql.ID)
		require.NoError(t, err)
		assert.Equal(t, []*api.Ref{{ID: a.ID, Name: "任务 A"}, {ID: b.ID, Name: "任务 B"}}, refs)
		refs, err = job_svc.Job().ByStorage(e.ctx, e.primary)
		require.NoError(t, err)
		assert.Equal(t, []*api.Ref{{ID: a.ID, Name: "任务 A"}, {ID: c.ID, Name: "任务 C"}}, refs)
		refs, err = job_svc.Job().ByStorage(e.ctx, e.down)
		require.NoError(t, err)
		assert.Empty(t, refs)
	})
}
