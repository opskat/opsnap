package job_svc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	jobapi "github.com/opskat/opsnap/internal/api/job"
	"github.com/opskat/opsnap/internal/model/entity/credential_entity"
	"github.com/opskat/opsnap/internal/model/entity/job_entity"
	"github.com/opskat/opsnap/internal/model/entity/repo_entity"
	"github.com/opskat/opsnap/internal/model/entity/run_entity"
	"github.com/opskat/opsnap/internal/model/entity/server_entity"
	"github.com/opskat/opsnap/internal/model/entity/setting_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/executor"
	"github.com/opskat/opsnap/internal/pkg/secret"
	"github.com/opskat/opsnap/internal/pkg/sshconn"
	"github.com/opskat/opsnap/internal/repository/credential_repo"
	"github.com/opskat/opsnap/internal/repository/job_repo"
	"github.com/opskat/opsnap/internal/repository/repo_repo"
	"github.com/opskat/opsnap/internal/repository/run_repo"
	"github.com/opskat/opsnap/internal/repository/server_repo"
	"github.com/opskat/opsnap/internal/repository/setting_repo"
	"github.com/opskat/opsnap/internal/service/repo_svc"
	"github.com/opskat/opsnap/internal/service/server_svc"
	"github.com/opskat/opsnap/migrations"
)

// fakeProbe 数据库探测替身，记录收到的目标
type fakeProbe struct {
	dbs       []Database
	listErr   error
	diagnosis Diagnosis
	targets   []Target
}

func (f *fakeProbe) ListDatabases(_ context.Context, t Target) ([]Database, error) {
	f.targets = append(f.targets, t)
	return f.dbs, f.listErr
}

func (f *fakeProbe) Diagnose(_ context.Context, t Target) Diagnosis {
	f.targets = append(f.targets, t)
	return f.diagnosis
}

// fakeTunnel SSH 隧道替身
type fakeTunnel struct {
	err     error
	calls   []tunnelCall
	closed  int
	localTo string
}

type tunnelCall struct {
	serverID int64
	addr     string
}

func (f *fakeTunnel) open(_ context.Context, serverID int64, addr string) (string, func(), error) {
	f.calls = append(f.calls, tunnelCall{serverID, addr})
	if f.err != nil {
		return "", nil, f.err
	}
	return f.localTo, func() { f.closed++ }, nil
}

type fixture struct {
	ctx    context.Context
	db     *gorm.DB
	svc    JobSvc
	probe  *fakeProbe
	tunnel *fakeTunnel
	exec   *executor.Fake
	now    time.Time
	srvID  int64
	repoID int64
	mount  string
}

func setupJobTest(t *testing.T) *fixture {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, migrations.RunMigrations(gdb))

	s, err := secret.New("job-test-master-key")
	require.NoError(t, err)
	secret.SetDefault(s)
	t.Cleanup(func() { secret.SetDefault(nil) })

	db.SetDefault(gdb)
	job_repo.RegisterJob(job_repo.NewJob())
	repo_repo.RegisterRepo(repo_repo.NewRepo())
	server_repo.RegisterServer(server_repo.NewServer())
	credential_repo.RegisterCredential(credential_repo.NewCredential())
	setting_repo.RegisterSetting(setting_repo.NewSetting())
	run_repo.RegisterRun(run_repo.NewRun())

	f := &fixture{
		db: gdb, ctx: db.WithContextDB(context.Background(), gdb),
		probe: &fakeProbe{}, tunnel: &fakeTunnel{localTo: "127.0.0.1:45678"},
		exec: executor.NewFake(), now: time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC),
	}
	// 存储位置的 restic 命令一律经替身执行器，用来断言「没有执行任何命令」
	repo_svc.RegisterRepo(repo_svc.NewRepo(f.exec, func() time.Time { return f.now }))
	server_svc.RegisterJobCounter(nil)
	repo_svc.RegisterJobCounter(nil)
	RegisterAttention(nil)
	t.Cleanup(func() {
		server_svc.RegisterJobCounter(nil)
		repo_svc.RegisterJobCounter(nil)
		RegisterAttention(nil)
	})

	cred := &credential_entity.Credential{Kind: credential_entity.KindPassword, Password: "x"}
	require.NoError(t, credential_repo.Credential().Create(f.ctx, cred))
	srv := &server_entity.Server{Name: "web-1", Host: "10.0.0.1", Port: 22, Username: "root", CredentialID: cred.ID, Hops: []server_entity.Hop{}}
	require.NoError(t, server_repo.Server().Create(f.ctx, srv))
	f.srvID = srv.ID

	f.mount = t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(f.mount, repo_entity.MarkerFile), nil, 0o600))
	f.repoID = f.addRepo(t, "nas", f.mount)

	f.svc = NewJob(Deps{Now: func() time.Time { return f.now }, Tunnel: f.tunnel.open, Probes: func(string) (Probe, error) { return f.probe, nil }})
	return f
}

func (f *fixture) addRepo(t *testing.T, name, dir string) int64 {
	t.Helper()
	r := &repo_entity.Repo{
		Name: name, Type: repo_entity.TypeLocal, Params: repo_entity.Params{Path: dir},
		OpsnapKey: "x", Status: repo_entity.StatusOK, FreeSpace: repo_entity.FreeSpaceUnknown,
	}
	require.NoError(t, repo_repo.Repo().Create(f.ctx, r))
	return r.ID
}

func (f *fixture) setTimezone(t *testing.T, tz string) {
	t.Helper()
	st := setting_entity.Default()
	st.Timezone = tz
	require.NoError(t, setting_repo.Setting().Save(f.ctx, st))
}

// mysqlInput 一份合法的 MySQL 经 SSH 任务
func (f *fixture) mysqlInput() jobapi.JobInput {
	return jobapi.JobInput{
		Name: "orders-db", SourceType: jobapi.SourceMySQL, ServerID: f.srvID, ConnMode: jobapi.ConnSSH,
		DBHost: "172.18.0.5", DBPort: 3306, DBUser: "backup", DBPassword: "pw-plain-123",
		Databases: []string{"orders"},
		RepoID:    f.repoID,
		Schedule:  jobapi.ScheduleInput{Kind: "daily", Time: "03:00"},
		Retention: jobapi.RetentionInput{Preset: "30d"},
	}
}

func (f *fixture) filesInput() jobapi.JobInput {
	return jobapi.JobInput{
		Name: "site-files", SourceType: jobapi.SourceFiles, ServerID: f.srvID,
		Paths: []string{"/var/www"}, Excludes: []string{"*.log"},
		RepoID:   f.repoID,
		Schedule: jobapi.ScheduleInput{Kind: "weekly", Weekday: 0, Time: "04:30"}, Retention: jobapi.RetentionInput{Preset: "long"},
	}
}

func (f *fixture) create(t *testing.T, in jobapi.JobInput) *jobapi.Item {
	t.Helper()
	resp, err := f.svc.Create(f.ctx, jobapi.NewCreateRequest(&in))
	require.NoError(t, err)
	return &resp.Item
}

var fieldLine = regexp.MustCompile(`^\[([\w.]+)\] (.+)$`)

// fieldErrors 解析 JobFieldInvalid 错误，返回字段 -> 原因
func fieldErrors(t *testing.T, err error) map[string]string {
	t.Helper()
	var he *httputils.Error
	require.ErrorAs(t, err, &he, "%v", err)
	require.Equal(t, code.JobFieldInvalid, he.Code, he.Msg)
	out := map[string]string{}
	for _, line := range strings.Split(he.Msg, "\n") {
		m := fieldLine.FindStringSubmatch(line)
		require.NotNil(t, m, "错误行格式不对：%q", line)
		out[m[1]] = m[2]
	}
	return out
}

func codeOf(t *testing.T, err error) int {
	t.Helper()
	var he *httputils.Error
	require.ErrorAs(t, err, &he, "%v", err)
	return he.Code
}

func TestCreateAndGet(t *testing.T) {
	f := setupJobTest(t)
	item := f.create(t, f.mysqlInput())
	assert.NotZero(t, item.ID)
	assert.Equal(t, "web-1", item.ServerName)
	assert.Equal(t, "nas", item.RepoName)
	assert.Equal(t, secret.Masked, item.DBPassword, "密码只返回占位")
	assert.Equal(t, 7200, item.Advanced.TimeoutSec, "超时默认 2 小时")
	assert.Equal(t, "auto", item.Advanced.Compression)
	assert.Equal(t, []string{"orders"}, item.Databases)

	// 库里是密文，不是明文
	var raw string
	require.NoError(t, f.db.Raw(`SELECT db_password FROM jobs WHERE id = ?`, item.ID).Scan(&raw).Error)
	assert.NotEmpty(t, raw)
	assert.NotContains(t, raw, "pw-plain-123")
	plain, err := secret.Default().Decrypt(raw)
	require.NoError(t, err)
	assert.Equal(t, "pw-plain-123", plain)

	got, err := f.svc.Get(f.ctx, item.ID)
	require.NoError(t, err)
	assert.Equal(t, item.Name, got.Name)
	assert.Equal(t, secret.Masked, got.DBPassword)

	_, err = f.svc.Get(f.ctx, 9999)
	assert.Equal(t, code.JobNotFound, codeOf(t, err))
}

func TestNextRunUsesConfiguredTimezone(t *testing.T) {
	f := setupJobTest(t)
	// 默认时区 Asia/Shanghai：每天 03:00 = 前一日 19:00 UTC；现在是 12:00 UTC（20:00 上海）
	item := f.create(t, f.mysqlInput())
	assert.Equal(t, time.Date(2026, 10, 15, 19, 0, 0, 0, time.UTC).Unix(), item.NextRunAt)

	f.setTimezone(t, "America/New_York")
	got, err := f.svc.Get(f.ctx, item.ID)
	require.NoError(t, err)
	// 纽约夏令时 03:00 EDT = 07:00 UTC，次日
	assert.Equal(t, time.Date(2026, 10, 16, 7, 0, 0, 0, time.UTC).Unix(), got.NextRunAt)
}

func TestValidationPerField(t *testing.T) {
	f := setupJobTest(t)
	cases := []struct {
		name   string
		mutate func(in *jobapi.JobInput)
		field  string
	}{
		{"名称为空", func(in *jobapi.JobInput) { in.Name = " " }, "name"},
		{"数据源未知", func(in *jobapi.JobInput) { in.SourceType = "oracle" }, "source_type"},
		{"经 SSH 没选服务器", func(in *jobapi.JobInput) { in.ServerID = 0 }, "server_id"},
		{"服务器不存在", func(in *jobapi.JobInput) { in.ServerID = 4242 }, "server_id"},
		{"连接方式未知", func(in *jobapi.JobInput) { in.ConnMode = "vpn" }, "conn_mode"},
		{"地址为空", func(in *jobapi.JobInput) { in.DBHost = "" }, "db_host"},
		{"端口越界", func(in *jobapi.JobInput) { in.DBPort = 70000 }, "db_port"},
		{"用户为空", func(in *jobapi.JobInput) { in.DBUser = "" }, "db_user"},
		{"没有勾选库", func(in *jobapi.JobInput) { in.Databases = nil }, "databases"},
		{"勾选了系统库", func(in *jobapi.JobInput) { in.Databases = []string{"orders", "mysql"} }, "databases"},
		{"存储位置未选", func(in *jobapi.JobInput) { in.RepoID = 0 }, "repo_id"},
		{"存储位置不存在", func(in *jobapi.JobInput) { in.RepoID = 777 }, "repo_id"},
		{"每天时刻写错", func(in *jobapi.JobInput) { in.Schedule = jobapi.ScheduleInput{Kind: "daily", Time: "3am"} }, "schedule.time"},
		{"每周星期越界", func(in *jobapi.JobInput) {
			in.Schedule = jobapi.ScheduleInput{Kind: "weekly", Weekday: 8, Time: "03:00"}
		}, "schedule.weekday"},
		{"cron 无效", func(in *jobapi.JobInput) { in.Schedule = jobapi.ScheduleInput{Kind: "cron", Cron: "every day"} }, "schedule.cron"},
		{"计划类型未知", func(in *jobapi.JobInput) { in.Schedule = jobapi.ScheduleInput{Kind: "yearly"} }, "schedule.kind"},
		{"保留规则全 0", func(in *jobapi.JobInput) { in.Retention = jobapi.RetentionInput{Preset: "custom"} }, "retention"},
		{"保留预设未知", func(in *jobapi.JobInput) { in.Retention = jobapi.RetentionInput{Preset: "forever"} }, "retention"},
		{"超时太短", func(in *jobapi.JobInput) { in.Advanced.TimeoutSec = 5 }, "advanced.timeout_sec"},
		{"限速为负", func(in *jobapi.JobInput) { in.Advanced.BandwidthKbps = -1 }, "advanced.bandwidth_kbps"},
		{"压缩未知", func(in *jobapi.JobInput) { in.Advanced.Compression = "zstd" }, "advanced.compression"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := f.mysqlInput()
			c.mutate(&in)
			_, err := f.svc.Create(f.ctx, jobapi.NewCreateRequest(&in))
			errs := fieldErrors(t, err)
			assert.Contains(t, errs, c.field)
			assert.NotEmpty(t, errs[c.field])
		})
	}

	t.Run("文件任务", func(t *testing.T) {
		in := f.filesInput()
		in.Paths = []string{"var/www", "/ok", "/a/../etc"}
		_, err := f.svc.Create(f.ctx, jobapi.NewCreateRequest(&in))
		assert.Contains(t, fieldErrors(t, err), "paths")

		in = f.filesInput()
		in.Paths = nil
		_, err = f.svc.Create(f.ctx, jobapi.NewCreateRequest(&in))
		assert.Contains(t, fieldErrors(t, err), "paths")
	})

	t.Run("一次返回所有字段的错误", func(t *testing.T) {
		in := f.mysqlInput()
		in.Name, in.DBHost, in.RepoID = "", "", 0
		_, err := f.svc.Create(f.ctx, jobapi.NewCreateRequest(&in))
		errs := fieldErrors(t, err)
		assert.Contains(t, errs, "name")
		assert.Contains(t, errs, "db_host")
		assert.Contains(t, errs, "repo_id")
	})

	t.Run("校验失败不落库", func(t *testing.T) {
		var n int64
		require.NoError(t, f.db.Table("jobs").Count(&n).Error)
		assert.Zero(t, n)
	})
}

func TestValidSourcesSave(t *testing.T) {
	f := setupJobTest(t)
	for _, c := range []struct {
		name string
		in   func() jobapi.JobInput
	}{
		{"mariadb 全部库", func() jobapi.JobInput {
			in := f.mysqlInput()
			in.SourceType, in.Databases, in.AllDatabases = jobapi.SourceMariaDB, nil, true
			return in
		}},
		{"postgres", func() jobapi.JobInput {
			in := f.mysqlInput()
			in.SourceType, in.DBPort, in.Databases = jobapi.SourcePostgres, 5432, []string{"app", "postgres"}
			return in
		}},
		{"redis 不需要库和用户", func() jobapi.JobInput {
			in := f.mysqlInput()
			in.SourceType, in.DBPort, in.Databases, in.DBUser = jobapi.SourceRedis, 6379, nil, ""
			return in
		}},
		{"mongodb 认证库默认 admin", func() jobapi.JobInput {
			in := f.mysqlInput()
			in.SourceType, in.DBPort, in.Databases = jobapi.SourceMongoDB, 27017, []string{"shop"}
			return in
		}},
		{"直连不需要服务器", func() jobapi.JobInput {
			in := f.mysqlInput()
			in.ConnMode, in.ServerID = jobapi.ConnDirect, 0
			return in
		}},
		{"文件", f.filesInput},
		{"cron", func() jobapi.JobInput {
			in := f.filesInput()
			in.Schedule = jobapi.ScheduleInput{Kind: "cron", Cron: "*/15 * * * *"}
			return in
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			item := f.create(t, c.in())
			assert.NotZero(t, item.NextRunAt)
			if item.SourceType == jobapi.SourceMongoDB {
				assert.Equal(t, "admin", item.AuthDB)
			}
			if item.ConnMode == jobapi.ConnDirect {
				assert.Zero(t, item.ServerID)
			}
		})
	}
}

func TestRepoMustBeMounted(t *testing.T) {
	f := setupJobTest(t)
	unmounted := f.addRepo(t, "nas-2", filepath.Join(t.TempDir(), "gone"))
	in := f.mysqlInput()
	in.RepoID = unmounted
	_, err := f.svc.Create(f.ctx, jobapi.NewCreateRequest(&in))
	errs := fieldErrors(t, err)
	assert.Contains(t, errs["repo_id"], "没有挂载")

	// 修改已有任务时同样拒绝
	item := f.create(t, f.mysqlInput())
	_, err = f.svc.Update(f.ctx, jobapi.NewUpdateRequest(item.ID, &in))
	assert.Contains(t, fieldErrors(t, err), "repo_id")
}

func TestPasswordMaskedRoundTrip(t *testing.T) {
	f := setupJobTest(t)
	item := f.create(t, f.mysqlInput())
	var before string
	require.NoError(t, f.db.Raw(`SELECT db_password FROM jobs WHERE id = ?`, item.ID).Scan(&before).Error)

	in := f.mysqlInput()
	in.Name = "renamed"
	in.DBPassword = secret.Masked
	got, err := f.svc.Update(f.ctx, jobapi.NewUpdateRequest(item.ID, &in))
	require.NoError(t, err)
	assert.Equal(t, "renamed", got.Name)
	assert.Equal(t, secret.Masked, got.DBPassword)
	var after string
	require.NoError(t, f.db.Raw(`SELECT db_password FROM jobs WHERE id = ?`, item.ID).Scan(&after).Error)
	assert.Equal(t, before, after, "占位表示保留已保存的密文")

	t.Run("换了数据库地址就必须重新填密码", func(t *testing.T) {
		in := f.mysqlInput()
		in.DBHost = "172.18.0.9"
		in.DBPassword = secret.Masked
		_, err := f.svc.Update(f.ctx, jobapi.NewUpdateRequest(item.ID, &in))
		assert.Contains(t, fieldErrors(t, err), "db_password")
	})
	t.Run("新任务不能回传占位", func(t *testing.T) {
		in := f.mysqlInput()
		in.DBPassword = secret.Masked
		_, err := f.svc.Create(f.ctx, jobapi.NewCreateRequest(&in))
		assert.Contains(t, fieldErrors(t, err), "db_password")
	})
	t.Run("清空密码", func(t *testing.T) {
		in := f.mysqlInput()
		in.DBPassword = ""
		got, err := f.svc.Update(f.ctx, jobapi.NewUpdateRequest(item.ID, &in))
		require.NoError(t, err)
		assert.Empty(t, got.DBPassword)
	})
}

func TestUpdateKeepsPausedAndMissingJob(t *testing.T) {
	f := setupJobTest(t)
	item := f.create(t, f.mysqlInput())
	_, err := f.svc.SetPaused(f.ctx, item.ID, true)
	require.NoError(t, err)
	in := f.mysqlInput()
	in.Paused = false // 编辑表单不改暂停状态，暂停由专门的接口控制
	got, err := f.svc.Update(f.ctx, jobapi.NewUpdateRequest(item.ID, &in))
	require.NoError(t, err)
	assert.True(t, got.Paused)

	_, err = f.svc.Update(f.ctx, jobapi.NewUpdateRequest(404, &in))
	assert.Equal(t, code.JobNotFound, codeOf(t, err))
}

func TestPauseExcludesFromNextRun(t *testing.T) {
	f := setupJobTest(t)
	item := f.create(t, f.mysqlInput())
	assert.NotZero(t, item.NextRunAt)

	paused, err := f.svc.SetPaused(f.ctx, item.ID, true)
	require.NoError(t, err)
	assert.True(t, paused.Paused)
	assert.Zero(t, paused.NextRunAt, "暂停的任务没有下次运行时间")

	ent, err := job_repo.Job().Find(f.ctx, item.ID)
	require.NoError(t, err)
	_, ok := ent.NextRun(f.now, time.UTC)
	assert.False(t, ok)

	resumed, err := f.svc.SetPaused(f.ctx, item.ID, false)
	require.NoError(t, err)
	assert.False(t, resumed.Paused)
	assert.Equal(t, item.NextRunAt, resumed.NextRunAt)
	_, ok = ent.NextRun(f.now, time.UTC)
	assert.False(t, ok, "实体是暂停时读出的快照")
}

func TestDuplicate(t *testing.T) {
	f := setupJobTest(t)
	item := f.create(t, f.mysqlInput())
	dup, err := f.svc.Duplicate(f.ctx, item.ID)
	require.NoError(t, err)
	assert.NotEqual(t, item.ID, dup.ID)
	assert.Equal(t, "orders-db 副本", dup.Name)
	assert.True(t, dup.Paused, "副本先暂停，避免没确认就开始按计划运行")
	assert.Equal(t, item.Databases, dup.Databases)
	assert.Equal(t, secret.Masked, dup.DBPassword)

	var a, b string
	require.NoError(t, f.db.Raw(`SELECT db_password FROM jobs WHERE id = ?`, item.ID).Scan(&a).Error)
	require.NoError(t, f.db.Raw(`SELECT db_password FROM jobs WHERE id = ?`, dup.ID).Scan(&b).Error)
	assert.Equal(t, a, b)

	// 两个任务互不影响
	other := f.mysqlInput()
	other.Name = "other"
	other.DBPassword = secret.Masked
	_, err = f.svc.Update(f.ctx, jobapi.NewUpdateRequest(dup.ID, &other))
	require.NoError(t, err)
	orig, err := f.svc.Get(f.ctx, item.ID)
	require.NoError(t, err)
	assert.Equal(t, "orders-db", orig.Name)

	t.Run("长名称不超过上限", func(t *testing.T) {
		in := f.mysqlInput()
		in.Name = strings.Repeat("长", 64)
		long := f.create(t, in)
		d, err := f.svc.Duplicate(f.ctx, long.ID)
		require.NoError(t, err)
		assert.LessOrEqual(t, len([]rune(d.Name)), 64)
		assert.True(t, strings.HasSuffix(d.Name, "副本"))
	})
}

func TestDeleteKeepsRepoUntouched(t *testing.T) {
	f := setupJobTest(t)
	item := f.create(t, f.mysqlInput())
	server_svc.RegisterJobCounter(CountByServer)
	repo_svc.RegisterJobCounter(CountByRepo)

	// 有任务时服务器与存储位置拒绝删除
	err := server_svc.Server().Delete(f.ctx, f.srvID)
	assert.Equal(t, code.ServerInUse, codeOf(t, err))
	err = repo_svc.Repo().Delete(f.ctx, f.repoID)
	assert.Equal(t, code.RepoInUse, codeOf(t, err))

	require.NoError(t, f.svc.Delete(f.ctx, item.ID))
	_, err = f.svc.Get(f.ctx, item.ID)
	assert.Equal(t, code.JobNotFound, codeOf(t, err))
	assert.Empty(t, f.exec.Calls(), "删除任务不执行任何 restic/rsync 命令，已有快照原样保留")
	r, err := repo_repo.Repo().Find(f.ctx, f.repoID)
	require.NoError(t, err)
	assert.NotNil(t, r, "存储位置记录不动")
	_, statErr := os.Stat(filepath.Join(f.mount, repo_entity.MarkerFile))
	assert.NoError(t, statErr)

	// 任务删光后才能删服务器
	require.NoError(t, server_svc.Server().Delete(f.ctx, f.srvID))

	assert.Equal(t, code.JobNotFound, codeOf(t, f.svc.Delete(f.ctx, item.ID)))
}

func TestCounters(t *testing.T) {
	f := setupJobTest(t)
	f.create(t, f.mysqlInput())
	f.create(t, f.filesInput())
	direct := f.mysqlInput()
	direct.Name, direct.ConnMode, direct.ServerID = "direct", jobapi.ConnDirect, 0
	f.create(t, direct)

	byServer, err := CountByServer(f.ctx)
	require.NoError(t, err)
	assert.Equal(t, map[int64]int{f.srvID: 2}, byServer, "直连任务不占用服务器")
	byRepo, err := CountByRepo(f.ctx)
	require.NoError(t, err)
	assert.Equal(t, map[int64]int{f.repoID: 3}, byRepo)
}

func TestListFilterAndSearch(t *testing.T) {
	f := setupJobTest(t)
	a := f.create(t, f.mysqlInput())
	b := f.create(t, f.filesInput())
	_, err := f.svc.SetPaused(f.ctx, b.ID, true)
	require.NoError(t, err)

	names := func(resp *jobapi.ListResponse, err error) []string {
		require.NoError(t, err)
		out := make([]string, 0, len(resp.List))
		for _, it := range resp.List {
			out = append(out, it.Name)
		}
		return out
	}
	assert.Equal(t, []string{"orders-db", "site-files"}, names(f.svc.List(f.ctx, "", "")))
	assert.Equal(t, []string{"orders-db", "site-files"}, names(f.svc.List(f.ctx, jobapi.FilterAll, "")))
	assert.Equal(t, []string{"site-files"}, names(f.svc.List(f.ctx, jobapi.FilterPaused, "")))
	assert.Empty(t, names(f.svc.List(f.ctx, jobapi.FilterAttention, "")), "运行引擎落地前没有需要处理的任务")

	assert.Equal(t, []string{"orders-db"}, names(f.svc.List(f.ctx, "", "ORDERS")), "按名称，不区分大小写")
	assert.Equal(t, []string{"orders-db", "site-files"}, names(f.svc.List(f.ctx, "", "web-1")), "按服务器名称")
	assert.Equal(t, []string{"site-files"}, names(f.svc.List(f.ctx, "", "/var/www")), "按路径")
	assert.Empty(t, names(f.svc.List(f.ctx, "", "nope")))

	t.Run("需要处理由钩子决定", func(t *testing.T) {
		RegisterAttention(func(context.Context) (map[int64]bool, error) { return map[int64]bool{a.ID: true}, nil })
		resp, err := f.svc.List(f.ctx, jobapi.FilterAttention, "")
		require.NoError(t, err)
		require.Len(t, resp.List, 1)
		assert.Equal(t, a.ID, resp.List[0].ID)
		assert.True(t, resp.List[0].Attention)

		RegisterAttention(func(context.Context) (map[int64]bool, error) { return nil, errors.New("boom") })
		_, err = f.svc.List(f.ctx, jobapi.FilterAll, "")
		assert.Error(t, err)
	})
}

func TestListRunSummary(t *testing.T) {
	f := setupJobTest(t)
	a := f.create(t, f.mysqlInput())
	b := f.create(t, f.filesInput())
	day := int64(86400)
	now := f.now.Unix()
	add := func(jobID int64, status string, startedAt, added int64) *run_entity.Run {
		r := run_entity.New(jobID, "x", run_entity.TriggerSchedule, time.Unix(startedAt, 0))
		r.Status, r.BytesAdded = status, added
		require.NoError(t, run_repo.Run().Create(f.ctx, r))
		return r
	}
	// a：30 天前成功过一次，之后没有运行——上次结果不受 14 天窗口限制
	old := add(a.ID, run_entity.StatusSuccess, now-30*day, 2048)
	// b：最近有失败、有跳过，现在正在运行
	add(b.ID, run_entity.StatusFailed, now-3*day, 0)
	add(b.ID, run_entity.StatusSkipped, now-2*day, 0)
	running := add(b.ID, run_entity.StatusRunning, now-60, 0)

	resp, err := f.svc.List(f.ctx, jobapi.FilterAll, "")
	require.NoError(t, err)
	byID := map[int64]*jobapi.Item{}
	for _, it := range resp.List {
		byID[it.ID] = it
	}
	ia, ib := byID[a.ID], byID[b.ID]
	require.NotNil(t, ia.LastRun)
	assert.Equal(t, old.ID, ia.LastRun.ID)
	assert.Equal(t, run_entity.StatusSuccess, ia.LastRun.Status)
	assert.Equal(t, int64(2048), ia.LastRun.BytesAdded)
	assert.Empty(t, ia.RecentRuns, "14 天外的运行不进运行带")
	assert.False(t, ia.Running)

	require.NotNil(t, ib.LastRun)
	assert.Equal(t, running.ID, ib.LastRun.ID, "上次结果不取跳过的运行")
	assert.True(t, ib.Running)
	statuses := make([]string, 0, len(ib.RecentRuns))
	for _, r := range ib.RecentRuns {
		statuses = append(statuses, r.Status)
	}
	assert.Equal(t, []string{run_entity.StatusFailed, run_entity.StatusRunning}, statuses, "运行带不含跳过，按时间升序")
}

func TestNextRunsPreview(t *testing.T) {
	f := setupJobTest(t)
	resp, err := f.svc.NextRuns(f.ctx, &jobapi.NextRunsRequest{Schedule: jobapi.ScheduleInput{Kind: "daily", Time: "03:00"}})
	require.NoError(t, err)
	assert.Equal(t, "Asia/Shanghai", resp.Timezone)
	require.Len(t, resp.Times, 5, "默认 5 次")
	assert.Equal(t, time.Date(2026, 10, 15, 19, 0, 0, 0, time.UTC).Unix(), resp.Times[0])
	assert.Equal(t, resp.Times[0]+86400, resp.Times[1])

	resp, err = f.svc.NextRuns(f.ctx, &jobapi.NextRunsRequest{Schedule: jobapi.ScheduleInput{Kind: "hourly"}, Count: 100})
	require.NoError(t, err)
	assert.Len(t, resp.Times, 20, "最多 20 次")

	_, err = f.svc.NextRuns(f.ctx, &jobapi.NextRunsRequest{Schedule: jobapi.ScheduleInput{Kind: "cron", Cron: "bad"}})
	assert.Contains(t, fieldErrors(t, err), "schedule.cron")
}

func TestListDatabases(t *testing.T) {
	f := setupJobTest(t)
	f.probe.dbs = []Database{{"information_schema", 0}, {"mysql", 1000}, {"orders", 123456}, {"sys", 0}, {"performance_schema", 0}, {"shop", -1}}

	t.Run("经 SSH 隧道，系统库不可选", func(t *testing.T) {
		in := f.mysqlInput()
		resp, err := f.svc.Databases(f.ctx, jobapi.NewDatabasesRequest(0, &in))
		require.NoError(t, err)
		require.True(t, resp.OK)
		assert.Equal(t, []tunnelCall{{f.srvID, "172.18.0.5:3306"}}, f.tunnel.calls, "按「从服务器上看」的地址转发")
		require.Len(t, f.probe.targets, 1)
		assert.Equal(t, "127.0.0.1:45678", f.probe.targets[0].Addr, "探测连的是本地转发端口")
		assert.Equal(t, "pw-plain-123", f.probe.targets[0].Password)
		assert.Equal(t, 1, f.tunnel.closed, "用完关闭隧道")

		by := map[string]*jobapi.Database{}
		for _, d := range resp.List {
			by[d.Name] = d
		}
		for _, sys := range []string{"information_schema", "performance_schema", "mysql", "sys"} {
			assert.True(t, by[sys].System, sys)
			assert.False(t, by[sys].Selectable, sys)
		}
		assert.False(t, by["orders"].System)
		assert.True(t, by["orders"].Selectable)
		assert.Equal(t, int64(123456), by["orders"].SizeBytes)
		assert.Equal(t, int64(-1), by["shop"].SizeBytes)
	})

	t.Run("直连不开隧道", func(t *testing.T) {
		f.tunnel.calls, f.probe.targets = nil, nil
		in := f.mysqlInput()
		in.ConnMode, in.ServerID, in.DBHost, in.DBPort = jobapi.ConnDirect, 0, "db.example.com", 3307
		resp, err := f.svc.Databases(f.ctx, jobapi.NewDatabasesRequest(0, &in))
		require.NoError(t, err)
		assert.True(t, resp.OK)
		assert.Empty(t, f.tunnel.calls)
		assert.Equal(t, "db.example.com:3307", f.probe.targets[0].Addr)
	})

	t.Run("postgres 和 mongodb 的系统库", func(t *testing.T) {
		f.probe.dbs = []Database{{"template0", 1}, {"template1", 1}, {"postgres", 1}, {"app", 1}, {"admin", 1}, {"local", 1}, {"config", 1}}
		in := f.mysqlInput()
		in.SourceType, in.DBPort = jobapi.SourcePostgres, 5432
		resp, err := f.svc.Databases(f.ctx, jobapi.NewDatabasesRequest(0, &in))
		require.NoError(t, err)
		sys := map[string]bool{}
		for _, d := range resp.List {
			sys[d.Name] = d.System
		}
		assert.True(t, sys["template0"])
		assert.True(t, sys["template1"])
		assert.False(t, sys["app"])

		in.SourceType, in.DBPort = jobapi.SourceMongoDB, 27017
		resp, err = f.svc.Databases(f.ctx, jobapi.NewDatabasesRequest(0, &in))
		require.NoError(t, err)
		sys = map[string]bool{}
		for _, d := range resp.List {
			sys[d.Name] = d.System
		}
		assert.True(t, sys["admin"] && sys["local"] && sys["config"])
		assert.False(t, sys["app"])
	})

	t.Run("编辑中的任务占位密码取已保存的值", func(t *testing.T) {
		item := f.create(t, f.mysqlInput())
		f.probe.targets = nil
		in := f.mysqlInput()
		in.DBPassword = secret.Masked
		_, err := f.svc.Databases(f.ctx, jobapi.NewDatabasesRequest(item.ID, &in))
		require.NoError(t, err)
		assert.Equal(t, "pw-plain-123", f.probe.targets[0].Password)
	})

	t.Run("读取失败返回原因，可重试", func(t *testing.T) {
		f.probe.listErr = &ProbeError{Reason: ReasonAuthFailed, Message: "Access denied for user 'backup'"}
		resp, err := f.svc.Databases(f.ctx, jobapi.NewDatabasesRequest(0, ptr(f.mysqlInput())))
		require.NoError(t, err, "失败是正常结果，不是接口错误")
		assert.False(t, resp.OK)
		assert.Equal(t, ReasonAuthFailed, resp.Reason)
		assert.Contains(t, resp.Message, "Access denied")
		assert.NotEmpty(t, resp.ReasonLabel)
		assert.Empty(t, resp.List)
		assert.NotContains(t, resp.Message, "pw-plain-123")

		// 重试：恢复后同样的请求成功
		f.probe.listErr = nil
		resp, err = f.svc.Databases(f.ctx, jobapi.NewDatabasesRequest(0, ptr(f.mysqlInput())))
		require.NoError(t, err)
		assert.True(t, resp.OK)
	})

	t.Run("SSH 链路失败", func(t *testing.T) {
		f.tunnel.err = &sshconn.HopError{Index: 1, Kind: sshconn.KindSSH, Addr: "10.0.0.1:22", Reason: sshconn.ReasonTimeout, Message: "连接超时"}
		defer func() { f.tunnel.err = nil }()
		resp, err := f.svc.Databases(f.ctx, jobapi.NewDatabasesRequest(0, ptr(f.mysqlInput())))
		require.NoError(t, err)
		assert.False(t, resp.OK)
		assert.Equal(t, "timeout", resp.Reason)
		assert.Equal(t, "超时", resp.ReasonLabel)
		assert.Contains(t, resp.Message, "第 1 跳")
	})

	t.Run("连接字段不合法按字段报错", func(t *testing.T) {
		in := f.mysqlInput()
		in.DBHost = ""
		_, err := f.svc.Databases(f.ctx, jobapi.NewDatabasesRequest(0, &in))
		assert.Contains(t, fieldErrors(t, err), "db_host")
	})

	t.Run("redis 和文件没有库列表", func(t *testing.T) {
		in := f.mysqlInput()
		in.SourceType, in.DBPort, in.Databases = jobapi.SourceRedis, 6379, nil
		_, err := f.svc.Databases(f.ctx, jobapi.NewDatabasesRequest(0, &in))
		assert.Equal(t, code.JobSourceNotListed, codeOf(t, err))
		_, err = f.svc.Databases(f.ctx, jobapi.NewDatabasesRequest(0, ptr(f.filesInput())))
		assert.Equal(t, code.JobSourceNotListed, codeOf(t, err))
	})
}

func keys(resp *jobapi.TestResponse) []string {
	out := make([]string, 0, len(resp.Checks))
	for _, c := range resp.Checks {
		out = append(out, c.Key)
	}
	return out
}

func checkOf(t *testing.T, resp *jobapi.TestResponse, key string) *jobapi.CheckItem {
	t.Helper()
	for _, c := range resp.Checks {
		if c.Key == key {
			return c
		}
	}
	require.Failf(t, "缺少检查项", key)
	return nil
}

func TestConnection(t *testing.T) {
	f := setupJobTest(t)

	t.Run("全部通过", func(t *testing.T) {
		resp, err := f.svc.Test(f.ctx, jobapi.NewTestRequest(0, ptr(f.mysqlInput())))
		require.NoError(t, err)
		assert.True(t, resp.OK)
		assert.Equal(t, []string{"ssh", "db_reach", "db_auth", "db_privs", "repo"}, keys(resp))
		for _, c := range resp.Checks {
			assert.True(t, c.OK, c.Key)
			assert.Empty(t, c.Reason)
			assert.NotEmpty(t, c.Label)
		}
	})

	t.Run("直连没有 SSH 检查项", func(t *testing.T) {
		in := f.mysqlInput()
		in.ConnMode, in.ServerID = jobapi.ConnDirect, 0
		resp, err := f.svc.Test(f.ctx, jobapi.NewTestRequest(0, &in))
		require.NoError(t, err)
		assert.Equal(t, []string{"db_reach", "db_auth", "db_privs", "repo"}, keys(resp))
	})

	t.Run("缺少权限给出 GRANT 修复命令", func(t *testing.T) {
		f.probe.diagnosis = Diagnosis{Missing: []string{"SELECT", "LOCK TABLES"}}
		defer func() { f.probe.diagnosis = Diagnosis{} }()
		resp, err := f.svc.Test(f.ctx, jobapi.NewTestRequest(0, ptr(f.mysqlInput())))
		require.NoError(t, err)
		assert.False(t, resp.OK)
		privs := checkOf(t, resp, "db_privs")
		assert.False(t, privs.OK)
		assert.Contains(t, privs.Reason, "SELECT")
		assert.Equal(t, "GRANT SELECT, LOCK TABLES ON *.* TO 'backup'@'%';", privs.Fix)
		assert.True(t, checkOf(t, resp, "db_reach").OK)
		assert.True(t, checkOf(t, resp, "db_auth").OK)
		assert.True(t, checkOf(t, resp, "repo").OK)
	})

	t.Run("用户名里的引号被转义", func(t *testing.T) {
		f.probe.diagnosis = Diagnosis{Missing: []string{"SELECT"}}
		defer func() { f.probe.diagnosis = Diagnosis{} }()
		in := f.mysqlInput()
		in.DBUser = "o'brien"
		resp, err := f.svc.Test(f.ctx, jobapi.NewTestRequest(0, &in))
		require.NoError(t, err)
		assert.Equal(t, "GRANT SELECT ON *.* TO 'o''brien'@'%';", checkOf(t, resp, "db_privs").Fix)
	})

	t.Run("postgres 缺权限", func(t *testing.T) {
		f.probe.diagnosis = Diagnosis{Missing: []string{"pg_read_all_data"}}
		defer func() { f.probe.diagnosis = Diagnosis{} }()
		in := f.mysqlInput()
		in.SourceType, in.DBPort, in.DBUser = jobapi.SourcePostgres, 5432, "bk"
		resp, err := f.svc.Test(f.ctx, jobapi.NewTestRequest(0, &in))
		require.NoError(t, err)
		assert.Equal(t, `GRANT pg_read_all_data TO "bk";`, checkOf(t, resp, "db_privs").Fix)
	})

	t.Run("认证失败：后面的检查跳过", func(t *testing.T) {
		f.probe.diagnosis = Diagnosis{Auth: &ProbeError{Reason: ReasonAuthFailed, Message: "Access denied"}}
		defer func() { f.probe.diagnosis = Diagnosis{} }()
		resp, err := f.svc.Test(f.ctx, jobapi.NewTestRequest(0, ptr(f.mysqlInput())))
		require.NoError(t, err)
		assert.False(t, resp.OK)
		assert.True(t, checkOf(t, resp, "db_reach").OK)
		auth := checkOf(t, resp, "db_auth")
		assert.False(t, auth.OK)
		assert.Contains(t, auth.Reason, "Access denied")
		privs := checkOf(t, resp, "db_privs")
		assert.False(t, privs.OK)
		assert.True(t, privs.Skipped)
	})

	t.Run("连不上数据库", func(t *testing.T) {
		f.probe.diagnosis = Diagnosis{Reach: &ProbeError{Reason: ReasonUnreachable, Message: "connection refused"}}
		defer func() { f.probe.diagnosis = Diagnosis{} }()
		resp, err := f.svc.Test(f.ctx, jobapi.NewTestRequest(0, ptr(f.mysqlInput())))
		require.NoError(t, err)
		assert.False(t, checkOf(t, resp, "db_reach").OK)
		assert.True(t, checkOf(t, resp, "db_auth").Skipped)
		assert.True(t, checkOf(t, resp, "db_privs").Skipped)
	})

	t.Run("SSH 不通：数据库检查跳过，存储位置照常检查", func(t *testing.T) {
		f.tunnel.err = &sshconn.HopError{Index: 1, Kind: sshconn.KindSSH, Addr: "10.0.0.1:22", Reason: sshconn.ReasonAuthFailed, Message: "认证失败"}
		defer func() { f.tunnel.err = nil }()
		f.probe.targets = nil
		resp, err := f.svc.Test(f.ctx, jobapi.NewTestRequest(0, ptr(f.mysqlInput())))
		require.NoError(t, err)
		assert.False(t, resp.OK)
		assert.False(t, checkOf(t, resp, "ssh").OK)
		assert.Contains(t, checkOf(t, resp, "ssh").Reason, "第 1 跳")
		for _, k := range []string{"db_reach", "db_auth", "db_privs"} {
			assert.True(t, checkOf(t, resp, k).Skipped, k)
		}
		assert.True(t, checkOf(t, resp, "repo").OK)
		assert.Empty(t, f.probe.targets)
	})

	t.Run("存储位置没有挂载", func(t *testing.T) {
		in := f.mysqlInput()
		in.RepoID = f.addRepo(t, "gone", filepath.Join(t.TempDir(), "nope"))
		resp, err := f.svc.Test(f.ctx, jobapi.NewTestRequest(0, &in))
		require.NoError(t, err)
		assert.False(t, resp.OK)
		repo := checkOf(t, resp, "repo")
		assert.False(t, repo.OK)
		assert.Contains(t, repo.Reason, "没有挂载")
		assert.True(t, checkOf(t, resp, "db_privs").OK)
	})

	t.Run("文件任务只检查 SSH 与存储位置", func(t *testing.T) {
		resp, err := f.svc.Test(f.ctx, jobapi.NewTestRequest(0, ptr(f.filesInput())))
		require.NoError(t, err)
		assert.True(t, resp.OK)
		assert.Equal(t, []string{"ssh", "repo"}, keys(resp))
	})

	t.Run("连接字段不合法按字段报错，不做任何网络访问", func(t *testing.T) {
		f.tunnel.calls = nil
		in := f.mysqlInput()
		in.DBPort = 0
		in.DBHost = ""
		_, err := f.svc.Test(f.ctx, jobapi.NewTestRequest(0, &in))
		assert.Contains(t, fieldErrors(t, err), "db_host")
		assert.Empty(t, f.tunnel.calls)
	})

	t.Run("未保存的草稿允许计划等字段还没填好", func(t *testing.T) {
		in := f.mysqlInput()
		in.Schedule = jobapi.ScheduleInput{}
		in.Name = ""
		resp, err := f.svc.Test(f.ctx, jobapi.NewTestRequest(0, &in))
		require.NoError(t, err)
		assert.True(t, resp.OK)
	})
}

func TestFixCommand(t *testing.T) {
	assert.Empty(t, fixCommand(job_entity.SourceMySQL, "u", nil))
	assert.Equal(t, `db.getSiblingDB("admin").grantRolesToUser("u", [{ role: "backup", db: "admin" }])`,
		fixCommand(job_entity.SourceMongoDB, "u", []string{"backup"}))
	assert.Empty(t, fixCommand(job_entity.SourceRedis, "u", []string{"x"}))
}

func TestMissingMySQL(t *testing.T) {
	assert.Empty(t, missingMySQL([]string{"GRANT ALL PRIVILEGES ON *.* TO `root`@`%` WITH GRANT OPTION"}))
	assert.Equal(t, []string{"SELECT", "SHOW VIEW", "TRIGGER", "LOCK TABLES"}, missingMySQL([]string{"GRANT USAGE ON *.* TO `u`@`%`"}))
	assert.Equal(t, []string{"TRIGGER"}, missingMySQL([]string{
		"GRANT SELECT, SHOW VIEW, LOCK TABLES ON *.* TO `u`@`%`",
		"GRANT SELECT ON `app`.* TO `u`@`%`",
	}))
}

func ptr[T any](v T) *T { return &v }
