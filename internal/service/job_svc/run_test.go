package job_svc

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cago-frame/cago/pkg/gogo"
	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dsapi "github.com/opskat/opsnap/internal/api/datasource"
	jobapi "github.com/opskat/opsnap/internal/api/job"
	storageapi "github.com/opskat/opsnap/internal/api/storage"
	"github.com/opskat/opsnap/internal/model/entity/channel_entity"
	"github.com/opskat/opsnap/internal/model/entity/datasource_entity"
	"github.com/opskat/opsnap/internal/model/entity/job_entity"
	"github.com/opskat/opsnap/internal/model/entity/storage_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/dsconn"
	"github.com/opskat/opsnap/internal/pkg/fakessh"
	"github.com/opskat/opsnap/internal/pkg/kopiarepo"
	"github.com/opskat/opsnap/internal/pkg/probe"
	"github.com/opskat/opsnap/internal/pkg/testdb"
	"github.com/opskat/opsnap/internal/repository/channel_repo"
	"github.com/opskat/opsnap/internal/repository/datasource_repo"
	"github.com/opskat/opsnap/internal/repository/job_repo"
	"github.com/opskat/opsnap/internal/repository/setting_repo"
	"github.com/opskat/opsnap/internal/repository/storage_repo"
	"github.com/opskat/opsnap/internal/service/datasource_svc"
	"github.com/opskat/opsnap/internal/service/secret_svc"
	"github.com/opskat/opsnap/internal/service/storage_svc"
)

const (
	pgPassword  = "pg-S3cret!pw"   //nolint:gosec // 假数据源的测试密码
	sshPassword = "jump-S3cret#pw" //nolint:gosec // 进程内假 SSH 服务端的测试密码
	repoKey     = "Abcd-Efgh-Ijkl-Mnop-Qrst-Uvwx"
	// pgHeader 假 pg_dump 输出的 custom 格式归档头
	pgHeader = "PGDMP\x01\x0e\x00\x04\x08\x01"
	// globalsOut 假 pg_dumpall 的输出
	globalsOut = "-- globals\n-- PostgreSQL database cluster dump complete\n"
)

// 假工具的脚本主体（$db 为从参数中取出的库名）
const (
	pgDumpOK = `printf 'PGDMP\001\016\000\004\010\001'; printf 'archive %s\n' "$db"`
	// pgDumpFail 错误输出里带着密码，交给导出包去掉
	pgDumpFail = `echo 'FATAL: password authentication failed (pw=` + pgPassword + `)' >&2; exit 1`
	// pgDumpBadHeader 正常退出但不是 custom 格式归档
	pgDumpBadHeader = `printf 'NOT-A-DUMP-AT-ALL'`
	// pgDumpHang 输出归档头后一直不结束
	pgDumpHang = `printf 'PGDMP\001\016\000\004\010\001'; exec sleep 30`
	// pgDumpNoisy 大量错误输出后失败
	pgDumpNoisy = `i=0; while [ $i -lt 5000 ]; do echo x >&2; i=$((i+1)); done; echo last-line >&2; exit 1`
)

// fakeConnector 代替真实的数据库协议：经链路拨通数据源地址（证明走了链路）后返回连接信息
type fakeConnector struct {
	mu      sync.Mutex
	opens   int
	openErr error
}

func (f *fakeConnector) Test(ctx context.Context, d dsconn.Dialer, cfg dsconn.Config) (dsconn.Info, error) {
	c, err := d.Dial(ctx, "tcp", net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)))
	if err != nil {
		return dsconn.Info{}, err
	}
	_ = c.Close()
	return dsconn.Info{Version: "16.4 (Debian 16.4-1)"}, nil
}

func (f *fakeConnector) Open(ctx context.Context, d dsconn.Dialer, cfg dsconn.Config) (*dsconn.Conn, error) {
	f.mu.Lock()
	f.opens++
	openErr := f.openErr
	f.mu.Unlock()
	if openErr != nil {
		return nil, openErr
	}
	info, err := f.Test(ctx, d, cfg)
	if err != nil {
		return nil, err
	}
	return &dsconn.Conn{Info: info}, nil
}

func (f *fakeConnector) openCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.opens
}

type runEnv struct {
	t       *testing.T
	ctx     context.Context
	bin     string // 假工具所在的 PATH
	rec     string // 假工具的调用记录
	work    string // 运行临时目录的父目录
	kopia   string // storage_svc 的数据目录
	storage int64
	ssh     *fakessh.Server
	dbAddr  string
	conn    *fakeConnector
	ds      *datasource_entity.DataSource
	job     *job_entity.Job
}

func newRunEnv(t *testing.T) *runEnv {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("导出工具假设类 Unix 环境，与部署目标一致")
	}
	ctx := testdb.New(t)
	setting_repo.RegisterSetting(setting_repo.NewSetting())
	storage_repo.RegisterStorage(storage_repo.NewStorage())
	channel_repo.RegisterChannel(channel_repo.NewChannel())
	datasource_repo.RegisterDataSource(datasource_repo.NewDataSource())
	job_repo.RegisterJob(job_repo.NewJob())
	job_repo.RegisterRun(job_repo.NewRun())
	_, err := secret_svc.Secret().Init(ctx, secret_svc.InitOptions{DataDir: t.TempDir()})
	require.NoError(t, err)

	e := &runEnv{t: t, ctx: ctx, bin: t.TempDir(), rec: t.TempDir(), work: filepath.Join(t.TempDir(), "runs"), kopia: t.TempDir()}
	storage_svc.SetDataDir(e.kopia)
	t.Setenv("PATH", e.bin)
	probe.SetToolsDir("")
	SetWorkDir(e.work)
	// 执行器测试直接调用 Execute，不经后台派发
	SetDispatcher(func(int64) {})
	e.conn = &fakeConnector{}
	datasource_svc.SetConnector(e.conn)
	// 假连接没有真实数据库：库列表由测试给出
	origList := listDatabases
	e.setDatabases("app", "reports", "postgres")
	t.Cleanup(func() {
		listDatabases = origList
		datasource_svc.SetConnector(nil)
		SetDispatcher(nil)
		defaultRunner.setClock(nil)
		timeoutUnit = time.Minute
	})
	t.Cleanup(gogo.Wait)

	st, err := storage_svc.Storage().Create(ctx, &storageapi.CreateRequest{Name: "primary",
		Location: storageapi.Location{Kind: "local", Path: filepath.Join(t.TempDir(), "repo")}, Key: repoKey, ConfirmSaved: true})
	require.NoError(t, err)
	e.storage = st.Item.ID

	// 数据源地址：只接受连接的 TCP 服务，协议由 fakeConnector 代替
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	e.dbAddr = ln.Addr().String()

	// 数据源经 SSH 跳板连接
	e.ssh, err = fakessh.Listen("127.0.0.1:0", fakessh.Config{User: "root", Password: sshPassword})
	require.NoError(t, err)
	t.Cleanup(func() { _ = e.ssh.Close() })
	sshHost, sshPort, _ := net.SplitHostPort(e.ssh.Addr())
	port, _ := strconv.Atoi(sshPort)
	encSSH, err := secret_svc.Secret().Encrypt(ctx, sshPassword)
	require.NoError(t, err)
	ch := &channel_entity.Channel{Name: "jump", Kind: channel_entity.KindSSH, Host: sshHost, Port: port, Username: "root",
		AuthMethod: channel_entity.AuthPassword, Password: encSSH, HostKey: e.ssh.Fingerprint(), Status: "ok"}
	require.NoError(t, channel_repo.Channel().Create(ctx, ch))

	dbHost, dbPort, _ := net.SplitHostPort(e.dbAddr)
	dport, _ := strconv.Atoi(dbPort)
	encPG, err := secret_svc.Secret().Encrypt(ctx, pgPassword)
	require.NoError(t, err)
	e.ds = &datasource_entity.DataSource{Name: "analytics", Kind: datasource_entity.KindPostgreSQL, Host: dbHost, Port: dport,
		Username: "backup", Password: encPG, Database: "postgres", TLSMode: "disable", ChannelID: ch.ID,
		Status: datasource_entity.StatusOK}
	require.NoError(t, datasource_repo.DataSource().Create(ctx, e.ds))

	e.job = &job_entity.Job{Name: "analytics 全量备份", Type: job_entity.TypeBackup, DataSourceID: e.ds.ID, StorageID: e.storage,
		Prefix: "pg/analytics", Scope: job_entity.ScopeDatabases, Method: job_entity.MethodFull, OptGlobals: true,
		Compression: "zstd", ScheduleKind: "daily", ScheduleHour: 2, Timezone: "UTC",
		RetainDays: 7, Retries: 2, RetryInterval: 5, Timeout: 10, Enabled: true}
	e.job.SetDatabases([]string{"app", "reports"})
	e.job.SetExcludeTables(nil)
	e.job.SetWeekdays(nil)
	require.NoError(t, job_repo.Job().Create(ctx, e.job))

	e.tool("pg_dump", "pg_dump (PostgreSQL) 16.4", pgDumpOK)
	e.tool("pg_dumpall", "pg_dumpall (PostgreSQL) 16.4", `printf -- '`+strings.ReplaceAll(globalsOut, "\n", `\n`)+`'`)
	return e
}

// setDatabases 运行时从数据源列出的库
func (e *runEnv) setDatabases(names ...string) {
	listDatabases = func(context.Context, dsconn.Type, *dsconn.Conn) ([]dsapi.Database, error) {
		out := make([]dsapi.Database, 0, len(names))
		for _, n := range names {
			out = append(out, dsapi.Database{Name: n})
		}
		return out, nil
	}
}

// apiReasons 以指定的界面语言经接口读取任务的运行记录，返回 运行 ID → 原因
func apiReasons(t *testing.T, ctx context.Context, jobID int64, lang string) map[int64]string {
	t.Helper()
	resp, err := Job().Runs(i18n.WithLanguage(ctx, lang), &jobapi.RunsRequest{ID: jobID, Page: 1})
	require.NoError(t, err)
	out := map[int64]string{}
	for _, r := range resp.Items {
		out[r.ID] = r.Reason
	}
	return out
}

// tool 写一个假工具：--version 时打印版本；否则记录参数，取出库名到 $db 后执行 body
func (e *runEnv) tool(name, version, body string) {
	e.t.Helper()
	e.toolIn(e.bin, name, version, body)
}

// toolIn 在目录 dir 下写假工具，行为同 tool
func (e *runEnv) toolIn(dir, name, version, body string) {
	e.t.Helper()
	require.NoError(e.t, os.MkdirAll(dir, 0o755)) //nolint:gosec // 测试临时目录
	script := "#!/bin/sh\nPATH=/usr/bin:/bin\n" +
		"if [ \"$1\" = \"--version\" ]; then printf '%s\\n' '" + version + "'; exit 0; fi\n" +
		"printf '%s\\n' \"$@\" > " + e.rec + "/" + name + ".$$.argv\n" +
		"db=$(printf '%s\\n' \"$@\" | sed -n \"s/.* dbname='\\([^']*\\)'.*/\\1/p\")\n" +
		body + "\n"
	require.NoError(e.t, os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755)) //nolint:gosec // 测试用假可执行文件
}

// argv 假工具全部调用的参数
func (e *runEnv) argv(name string) []string {
	e.t.Helper()
	files, err := filepath.Glob(filepath.Join(e.rec, name+".*.argv"))
	require.NoError(e.t, err)
	out := make([]string, 0, len(files))
	for _, f := range files {
		b, err := os.ReadFile(f) //nolint:gosec // 测试临时目录
		require.NoError(e.t, err)
		out = append(out, string(b))
	}
	return out
}

func (e *runEnv) run(trig Trigger) *job_entity.Run {
	e.t.Helper()
	queued, err := Runs().Enqueue(e.ctx, e.job.ID, trig)
	require.NoError(e.t, err)
	assert.Equal(e.t, job_entity.RunQueued, queued.Status)
	final, err := Runs().Execute(e.ctx, queued.ID)
	require.NoError(e.t, err)
	return final
}

func (e *runEnv) manual() *job_entity.Run { return e.run(Trigger{Kind: job_entity.TriggerManual}) }

// withWriter 打开存储的写入会话，用完即关
func (e *runEnv) withWriter(fn func(w *kopiarepo.Writer)) {
	e.t.Helper()
	w, err := storage_svc.Storage().OpenWriter(e.ctx, e.storage)
	require.NoError(e.t, err)
	defer func() { require.NoError(e.t, w.Close(e.ctx)) }()
	fn(w)
}

func (e *runEnv) snapshots(ref kopiarepo.JobRef) []kopiarepo.SnapshotInfo {
	e.t.Helper()
	var snaps []kopiarepo.SnapshotInfo
	e.withWriter(func(w *kopiarepo.Writer) {
		var err error
		snaps, err = w.ListJobSnapshots(e.ctx, ref)
		require.NoError(e.t, err)
	})
	return snaps
}

// assertClean 运行结束后没有留下运行临时目录（凭据文件）或 kopia 写入会话配置
func (e *runEnv) assertClean() {
	e.t.Helper()
	ents, _ := os.ReadDir(e.work)
	assert.Empty(e.t, ents, "运行临时目录已删除")
	left, _ := filepath.Glob(filepath.Join(e.kopia, "kopia", "tmp-write-*"))
	assert.Empty(e.t, left, "kopia 写入会话配置已删除")
}

func logText(r *job_entity.Run) string {
	var b strings.Builder
	for _, l := range r.LogLines() {
		fmt.Fprintf(&b, "[%s] %s\n", l.Step, l.Message)
	}
	return b.String()
}

func logSteps(r *job_entity.Run) map[string]bool {
	out := map[string]bool{}
	for _, l := range r.LogLines() {
		out[l.Step] = true
	}
	return out
}

func TestRunSuccess(t *testing.T) {
	e := newRunEnv(t)
	before := time.Now()
	run := e.manual()

	require.Equal(t, job_entity.RunSuccess, run.Status, "%s\n%s", run.Reason, logText(run))
	assert.Equal(t, job_entity.TriggerManual, run.Trigger)
	assert.Empty(t, run.FailedStep)
	assert.Empty(t, run.Reason)
	assert.GreaterOrEqual(t, run.StartedAt, before.UnixMilli())
	assert.GreaterOrEqual(t, run.FinishedAt, run.StartedAt)
	wantBytes := int64(len(globalsOut) + len(pgHeader+"archive app\n") + len(pgHeader+"archive reports\n"))
	assert.Equal(t, wantBytes, run.ExportedBytes, "导出体积为导出工具输出的字节数")
	assert.Positive(t, run.UploadedBytes)

	snaps := e.snapshots(e.job.Ref())
	require.Len(t, snaps, 1, "恰好形成一份快照")
	assert.Equal(t, run.SnapshotID, snaps[0].ID)
	assert.Equal(t, kopiarepo.SnapshotTags{JobID: e.job.ID, RunID: run.ID, Type: "full", Kind: "postgres"}, snaps[0].Tags)
	e.withWriter(func(w *kopiarepo.Writer) {
		usage, err := w.Usage(e.ctx, []string{run.SnapshotID})
		require.NoError(t, err)
		assert.Equal(t, wantBytes, usage.ExportBytes, "快照中的文件大小与导出体积一致")
	})

	steps := logSteps(run)
	for _, s := range []string{job_entity.StepPrepare, job_entity.StepConnect, job_entity.StepExport, job_entity.StepVerify, job_entity.StepRetention} {
		assert.True(t, steps[s], "日志带步骤 %s：\n%s", s, logText(run))
	}
	assert.Contains(t, e.ssh.Forwards(), e.dbAddr, "经 SSH 跳板连接数据源")
	for _, a := range e.argv("pg_dump") {
		assert.Contains(t, a, "hostaddr='127.0.0.1'", "导出工具连本机转发端口")
		assert.NotContains(t, a, pgPassword, "命令行参数中没有密码")
	}
	assert.Len(t, e.argv("pg_dump"), 2)
	assert.Len(t, e.argv("pg_dumpall"), 1)
	assert.NotContains(t, logText(run), pgPassword)
	assert.NotContains(t, logText(run), sshPassword)
	e.assertClean()

	saved, err := job_repo.Job().Find(e.ctx, e.job.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, saved.SnapshotCount, "记录当前快照数")

	stored, err := job_repo.Run().Find(e.ctx, run.ID)
	require.NoError(t, err)
	assert.Equal(t, run.Status, stored.Status)
	assert.Equal(t, run.SnapshotID, stored.SnapshotID)
	assert.NotEmpty(t, stored.LogLines())
}

func TestRunStepFailures(t *testing.T) {
	cases := []struct {
		name  string
		setup func(e *runEnv)
		// restore 检查仓库前恢复环境（可为 nil）
		restore func(e *runEnv)
		step    string
		reason  string
		// connects 是否发起了数据源连接
		connects bool
	}{
		{name: "存储状态不是正常", step: job_entity.StepPrepare, reason: "存储", setup: func(e *runEnv) {
			e.setStorageStatus(storage_entity.StatusUnreachable)
		}, restore: func(e *runEnv) { e.setStorageStatus(storage_entity.StatusOK) }},
		{name: "数据源主机密钥已变化", step: job_entity.StepPrepare, reason: "主机密钥", setup: func(e *runEnv) {
			e.ds.Status = datasource_entity.StatusHostKeyChanged
			require.NoError(e.t, datasource_repo.DataSource().Save(e.ctx, e.ds))
		}},
		{name: "找不到 pg_dumpall", step: job_entity.StepPrepare, reason: "pg_dumpall", setup: func(e *runEnv) {
			require.NoError(e.t, os.Remove(filepath.Join(e.bin, "pg_dumpall")))
		}},
		{name: "连接数据源失败（错误中带密码）", step: job_entity.StepConnect, reason: "authentication failed", connects: true,
			setup: func(e *runEnv) {
				e.conn.openErr = fmt.Errorf("password authentication failed for user backup (%s)", pgPassword)
			}},
		{name: "导出工具失败（错误输出中带密码）", step: job_entity.StepExport, reason: "pg_dump", connects: true,
			setup: func(e *runEnv) { e.tool("pg_dump", "pg_dump (PostgreSQL) 16.4", pgDumpFail) }},
		{name: "归档头不完整", step: job_entity.StepVerify, reason: "归档头", connects: true,
			setup: func(e *runEnv) { e.tool("pg_dump", "pg_dump (PostgreSQL) 16.4", pgDumpBadHeader) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newRunEnv(t)
			c.setup(e)
			run := e.manual()
			assert.Equal(t, job_entity.RunFailed, run.Status)
			assert.Equal(t, c.step, run.FailedStep, "%s\n%s", run.Reason, logText(run))
			assert.Contains(t, run.Reason, c.reason)
			assert.NotContains(t, run.Reason, pgPassword)
			assert.NotContains(t, logText(run), pgPassword)
			assert.NotContains(t, logText(run), sshPassword)
			assert.Empty(t, run.SnapshotID)
			assert.True(t, logSteps(run)[c.step], "失败的步骤写进日志")
			assert.False(t, logSteps(run)[job_entity.StepRetention], "失败后不应用保留策略")
			if !c.connects {
				assert.Zero(t, e.conn.openCount(), "准备失败时不发起连接")
				assert.Zero(t, e.ssh.AuthAttempts(), "准备失败时不连接跳板")
			}
			for _, a := range e.argv("pg_dump") {
				assert.NotContains(t, a, pgPassword)
			}
			e.assertClean()
			if c.restore != nil {
				c.restore(e)
			}
			assert.Empty(t, e.snapshots(e.job.Ref()), "失败不形成快照")
		})
	}
}

func (e *runEnv) setStorageStatus(status string) {
	e.t.Helper()
	st, err := storage_repo.Storage().Find(e.ctx, e.storage)
	require.NoError(e.t, err)
	st.Status = status
	require.NoError(e.t, storage_repo.Storage().Save(e.ctx, st))
}

func TestRunLogCapped(t *testing.T) {
	e := newRunEnv(t)
	e.tool("pg_dump", "pg_dump (PostgreSQL) 16.4", pgDumpNoisy)
	run := e.manual()
	require.Equal(t, job_entity.RunFailed, run.Status)

	lines := run.LogLines()
	assert.Len(t, lines, job_entity.MaxLogLines, "每次运行最多 1000 行")
	assert.Equal(t, job_entity.StepPrepare, lines[0].Step, "保留开头")
	assert.Contains(t, logText(run), "last-line", "保留结尾")
	markers := 0
	for _, l := range lines {
		if l.Omitted > 0 {
			markers++
			assert.Greater(t, l.Omitted, 3000, "注明省略了多少行")
		}
	}
	assert.Equal(t, 1, markers)
	assert.LessOrEqual(t, len(run.Reason), 4096, "失败原因不照搬全部错误输出")
}

func TestRunCancel(t *testing.T) {
	t.Run("运行中：终止导出工具，不形成快照", func(t *testing.T) {
		e := newRunEnv(t)
		e.tool("pg_dump", "pg_dump (PostgreSQL) 16.4", pgDumpHang)
		queued, err := Runs().Enqueue(e.ctx, e.job.ID, Trigger{Kind: job_entity.TriggerManual})
		require.NoError(t, err)
		done := make(chan *job_entity.Run, 1)
		go func() {
			final, err := Runs().Execute(context.Background(), queued.ID)
			assert.NoError(t, err)
			done <- final
		}()
		require.Eventually(t, func() bool { return len(e.argv("pg_dump")) > 0 }, 30*time.Second, 20*time.Millisecond)
		active, err := Runs().HasActive(e.ctx, e.job.ID)
		require.NoError(t, err)
		assert.True(t, active)

		canceled, err := Runs().Cancel(e.ctx, queued.ID)
		require.NoError(t, err)
		assert.Equal(t, job_entity.RunCanceled, canceled.Status)
		final := <-done
		assert.Equal(t, job_entity.RunCanceled, final.Status)
		assert.Empty(t, final.FailedStep)
		assert.Empty(t, final.SnapshotID)
		assert.Positive(t, final.ExportedBytes, "记录已导出的字节数")
		e.assertClean()
		assert.Empty(t, e.snapshots(e.job.Ref()), "取消不形成快照")
		active, err = Runs().HasActive(e.ctx, e.job.ID)
		require.NoError(t, err)
		assert.False(t, active)

		_, err = Runs().Cancel(e.ctx, queued.ID)
		assert.ErrorIs(t, err, ErrRunFinished)
	})

	t.Run("排队中：直接取消，之后不再执行", func(t *testing.T) {
		e := newRunEnv(t)
		queued, err := Runs().Enqueue(e.ctx, e.job.ID, Trigger{Kind: job_entity.TriggerSchedule, ScheduledAt: 1700000000})
		require.NoError(t, err)
		canceled, err := Runs().Cancel(e.ctx, queued.ID)
		require.NoError(t, err)
		assert.Equal(t, job_entity.RunCanceled, canceled.Status)
		final, err := Runs().Execute(e.ctx, queued.ID)
		require.NoError(t, err)
		assert.Equal(t, job_entity.RunCanceled, final.Status)
		assert.Empty(t, e.argv("pg_dump"), "已取消的运行不再执行")
		assert.Zero(t, e.conn.openCount())

		_, err = Runs().Cancel(e.ctx, 999)
		assert.ErrorIs(t, err, ErrRunNotFound)
	})
}

func TestRunTimeout(t *testing.T) {
	e := newRunEnv(t)
	e.tool("pg_dump", "pg_dump (PostgreSQL) 16.4", pgDumpHang)
	// 任务超时 10 分钟，测试中每“分钟”为 500ms（-race 下打开仓库也要数百毫秒）
	timeoutUnit = 500 * time.Millisecond
	start := time.Now()
	run := e.manual()
	assert.Less(t, time.Since(start), 20*time.Second, "超时后立即终止导出工具")
	assert.Equal(t, job_entity.RunFailed, run.Status)
	assert.Equal(t, "超时（超过 10 分钟）", run.Reason)
	assert.Equal(t, job_entity.StepExport, run.FailedStep)
	assert.Equal(t, "超时（超过 10 分钟）", apiReasons(t, e.ctx, e.job.ID, code.LangZhCN)[run.ID])
	assert.Equal(t, "Timed out (exceeded 10 min)", apiReasons(t, e.ctx, e.job.ID, code.LangEn)[run.ID], "英文界面的原因为英文")
	e.assertClean()
	assert.Empty(t, e.snapshots(e.job.Ref()), "超时不形成快照")
}

func TestRunRetention(t *testing.T) {
	e := newRunEnv(t)
	var other, elsewhere string
	e.withWriter(func(w *kopiarepo.Writer) {
		write := func(jobID int64, prefix string) string {
			res, err := w.WriteSnapshot(e.ctx, kopiarepo.SnapshotRequest{Prefix: prefix,
				Tags:  kopiarepo.SnapshotTags{JobID: jobID, RunID: 1, Type: "full", Kind: "postgres"},
				Files: []kopiarepo.SnapshotFile{{Name: "app.dump", Reader: strings.NewReader(pgHeader + "old")}}})
			require.NoError(t, err)
			return res.ID
		}
		write(e.job.ID, e.job.Prefix)
		write(e.job.ID, e.job.Prefix)
		other = write(e.job.ID+100, "pg/other")
		elsewhere = write(e.job.ID, "pg/elsewhere") // 同一任务 ID 但不在本任务的前缀下，不是本任务的快照
	})

	t.Run("都在保留天数内：不删除", func(t *testing.T) {
		run := e.manual()
		require.Equal(t, job_entity.RunSuccess, run.Status, run.Reason)
		assert.Len(t, e.snapshots(e.job.Ref()), 3)
		saved, err := job_repo.Job().Find(e.ctx, e.job.ID)
		require.NoError(t, err)
		assert.Equal(t, 3, saved.SnapshotCount)
	})

	t.Run("超出保留天数：只删本任务更早的快照，保留最新一份", func(t *testing.T) {
		// 30 天后运行，保留 1 天：本任务此前的快照都已过期
		defaultRunner.setClock(func() time.Time { return time.Now().Add(30 * 24 * time.Hour) })
		e.job.RetainDays = 1
		require.NoError(t, job_repo.Job().Save(e.ctx, e.job))
		run := e.manual()
		require.Equal(t, job_entity.RunSuccess, run.Status, run.Reason)
		snaps := e.snapshots(e.job.Ref())
		require.Len(t, snaps, 1)
		assert.Equal(t, run.SnapshotID, snaps[0].ID, "本次成功的快照保留")
		assert.Equal(t, []string{other}, ids(e.snapshots(kopiarepo.JobRef{JobID: e.job.ID + 100, Prefix: "pg/other"})), "其他任务的快照不受影响")
		assert.Equal(t, []string{elsewhere}, ids(e.snapshots(kopiarepo.JobRef{JobID: e.job.ID, Prefix: "pg/elsewhere"})), "其他前缀下的快照不受影响")
		assert.Contains(t, logText(run), "删除 3 份")
		saved, err := job_repo.Job().Find(e.ctx, e.job.ID)
		require.NoError(t, err)
		assert.Equal(t, 1, saved.SnapshotCount)
		e.assertClean()
	})
}

func ids(snaps []kopiarepo.SnapshotInfo) []string {
	out := make([]string, 0, len(snaps))
	for _, s := range snaps {
		out = append(out, s.ID)
	}
	return out
}

func TestRunEnqueue(t *testing.T) {
	e := newRunEnv(t)
	first, err := Runs().Enqueue(e.ctx, e.job.ID, Trigger{Kind: job_entity.TriggerRetry, Attempt: 1, Total: 2})
	require.NoError(t, err)
	assert.Equal(t, job_entity.TriggerRetry, first.Trigger)
	assert.Equal(t, []int{1, 2}, []int{first.RetryAttempt, first.RetryTotal}, "重试 i/N")

	_, err = Runs().Enqueue(e.ctx, e.job.ID, Trigger{Kind: job_entity.TriggerManual})
	assert.ErrorIs(t, err, ErrRunActive, "同一任务最多一个运行或排队中的运行")
	_, err = Runs().Enqueue(e.ctx, 999, Trigger{Kind: job_entity.TriggerManual})
	assert.ErrorIs(t, err, job_repo.ErrNotFound)

	final, err := Runs().Execute(e.ctx, first.ID)
	require.NoError(t, err)
	assert.Equal(t, job_entity.RunSuccess, final.Status, final.Reason)
	_, err = Runs().Enqueue(e.ctx, e.job.ID, Trigger{Kind: job_entity.TriggerManual})
	assert.NoError(t, err, "上一次结束后可以再次触发")
}

func TestRunKeepsLatestRecords(t *testing.T) {
	e := newRunEnv(t)
	require.NoError(t, os.Remove(filepath.Join(e.bin, "pg_dump"))) // 准备即失败，运行很快
	var firstID int64
	for i := 0; i < job_entity.MaxRunsPerJob; i++ {
		r := &job_entity.Run{JobID: e.job.ID, Status: job_entity.RunFailed, Trigger: job_entity.TriggerSchedule, Log: "[]"}
		require.NoError(t, job_repo.Run().Create(e.ctx, r))
		if i == 0 {
			firstID = r.ID
		}
	}
	run := e.manual()
	_, total, err := job_repo.Run().Page(e.ctx, e.job.ID, 0, 1)
	require.NoError(t, err)
	assert.EqualValues(t, job_entity.MaxRunsPerJob, total, "每个任务最多保留 1000 条")
	oldest, err := job_repo.Run().Find(e.ctx, firstID)
	require.NoError(t, err)
	assert.Nil(t, oldest, "删除最早的记录")
	kept, err := job_repo.Run().Find(e.ctx, run.ID)
	require.NoError(t, err)
	assert.NotNil(t, kept)
}

// 只保留最近 1000 条时不删除仍在排队或运行的记录：每分钟触发的任务在一次长时间运行期间会记下上千条“跳过”，
// 删掉进行中的记录会让它的结果无处保存，并让任务在它还没结束时再次入队
func TestRunTrimKeepsActiveRun(t *testing.T) {
	e := newRunEnv(t)
	active, err := Runs().Enqueue(e.ctx, e.job.ID, Trigger{Kind: job_entity.TriggerManual})
	require.NoError(t, err)
	for i := 0; i < job_entity.MaxRunsPerJob; i++ {
		_, err := defaultRunner.skip(e.ctx, e.job.ID, Trigger{Kind: job_entity.TriggerSchedule, ScheduledAt: int64(i + 1)},
			job_entity.ReasonStillRunning)
		require.NoError(t, err)
	}
	kept, err := job_repo.Run().Find(e.ctx, active.ID)
	require.NoError(t, err)
	require.NotNil(t, kept, "进行中的记录不被清理")
	_, err = Runs().Enqueue(e.ctx, e.job.ID, Trigger{Kind: job_entity.TriggerManual})
	assert.ErrorIs(t, err, ErrRunActive, "任务仍有排队中的运行")
	final, err := Runs().Execute(e.ctx, active.ID)
	require.NoError(t, err)
	assert.Equal(t, job_entity.RunSuccess, final.Status, final.Reason)
}

func TestRunRecover(t *testing.T) {
	e := newRunEnv(t)
	mk := func(status string) *job_entity.Run {
		r := &job_entity.Run{JobID: e.job.ID, Status: status, Trigger: job_entity.TriggerSchedule, Log: "[]"}
		require.NoError(t, job_repo.Run().Create(e.ctx, r))
		return r
	}
	running, queued, done := mk(job_entity.RunRunning), mk(job_entity.RunQueued), mk(job_entity.RunSuccess)
	require.NoError(t, os.MkdirAll(filepath.Join(e.work, "run-left"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(e.work, "run-left", "pgpass"), []byte(pgPassword), 0o600))

	require.NoError(t, Runs().Recover(e.ctx))
	get := func(id int64) *job_entity.Run {
		r, err := job_repo.Run().Find(e.ctx, id)
		require.NoError(t, err)
		return r
	}
	assert.Equal(t, job_entity.RunFailed, get(running.ID).Status)
	assert.Equal(t, "OpsNap 重启，运行中断", get(running.ID).Reason)
	assert.Positive(t, get(running.ID).FinishedAt)
	assert.Equal(t, job_entity.RunCanceled, get(queued.ID).Status)
	assert.Equal(t, "OpsNap 重启", get(queued.ID).Reason)
	assert.Equal(t, job_entity.RunSuccess, get(done.ID).Status)
	e.assertClean()

	zh, en := apiReasons(t, e.ctx, e.job.ID, code.LangZhCN), apiReasons(t, e.ctx, e.job.ID, code.LangEn)
	assert.Equal(t, "OpsNap 重启，运行中断", zh[running.ID])
	assert.Equal(t, "OpsNap 重启", zh[queued.ID])
	assert.Equal(t, "OpsNap restarted; the run was interrupted", en[running.ID], "英文界面的原因为英文")
	assert.Equal(t, "OpsNap restarted", en[queued.ID], "英文界面的原因为英文")
}

// 准备步骤在连接数据源之前确认导出工具可用：大版本低于数据源（最近一次测试读到的版本）的 pg_dump、
// 无法保证 TLS 模式的 MariaDB mysqldump 都在这一步失败（docs/specs/2026-09-27-backup-jobs.md「执行」第 1 步、「第 2 步」）
func TestRunPrepareChecksToolVersion(t *testing.T) {
	t.Run("pg_dump 大版本低于服务端", func(t *testing.T) {
		e := newRunEnv(t)
		e.ds.Version = "16.4 (Debian 16.4-1)"
		require.NoError(t, datasource_repo.DataSource().Save(e.ctx, e.ds))
		e.tool("pg_dump", "pg_dump (PostgreSQL) 15.2", pgDumpOK)
		run := e.manual()
		assert.Equal(t, job_entity.RunFailed, run.Status)
		assert.Equal(t, job_entity.StepPrepare, run.FailedStep, "%s\n%s", run.Reason, logText(run))
		assert.Contains(t, run.Reason, "15")
		assert.Contains(t, run.Reason, "修复")
		assert.Zero(t, e.conn.openCount(), "准备失败时不发起连接")
		assert.Zero(t, e.ssh.AuthAttempts(), "准备失败时不连接跳板")
		assert.Empty(t, e.argv("pg_dump"))
		e.assertClean()
	})

	t.Run("MariaDB 的 mysqldump 无法保证数据源的 TLS 模式", func(t *testing.T) {
		e := newRunEnv(t)
		e.ds.Kind, e.ds.TLSMode, e.ds.Version = datasource_entity.KindMySQL, "require", "8.0.40"
		require.NoError(t, datasource_repo.DataSource().Save(e.ctx, e.ds))
		e.job.OptGlobals = false
		require.NoError(t, job_repo.Job().Save(e.ctx, e.job))
		e.tool("mysqldump", "mysqldump  Ver 10.19 Distrib 10.11.14-MariaDB, for debian-linux-gnu (x86_64)", "exit 0")
		run := e.manual()
		assert.Equal(t, job_entity.RunFailed, run.Status)
		assert.Equal(t, job_entity.StepPrepare, run.FailedStep, "%s\n%s", run.Reason, logText(run))
		assert.Contains(t, run.Reason, "TLS")
		assert.Zero(t, e.conn.openCount(), "准备失败时不发起连接")
		assert.Empty(t, e.argv("mysqldump"))
		e.assertClean()
	})
}

// 运行日志的“导出工具”行写出按服务端版本实际选用的工具路径与版本
// （docs/specs/2026-09-29-overview-docker.md「按服务端版本选择导出工具」）
func TestRunLogShowsChosenTool(t *testing.T) {
	e := newRunEnv(t)
	toolsDir := t.TempDir()
	probe.SetToolsDir(toolsDir)
	t.Cleanup(func() { probe.SetToolsDir("") })
	for _, v := range []string{"16", "17"} {
		bin := filepath.Join(toolsDir, "postgresql-"+v, "bin")
		e.toolIn(bin, "pg_dump", "pg_dump (PostgreSQL) "+v+".10", pgDumpOK)
		e.toolIn(bin, "pg_dumpall", "pg_dumpall (PostgreSQL) "+v+".10", `printf -- '`+strings.ReplaceAll(globalsOut, "\n", `\n`)+`'`)
	}

	run := e.manual()
	require.Equal(t, job_entity.RunSuccess, run.Status, "%s\n%s", run.Reason, logText(run))
	pgDump := filepath.Join(toolsDir, "postgresql-16", "bin", "pg_dump")
	zh := apiLog(t, e, run.ID, code.LangZhCN)
	assert.Contains(t, zh, "导出工具 pg_dump："+pgDump+"（16.10）")
	assert.Contains(t, zh, "导出工具 pg_dumpall："+filepath.Join(toolsDir, "postgresql-16", "bin", "pg_dumpall")+"（16.10）")
	assert.Contains(t, apiLog(t, e, run.ID, code.LangEn), "Export tool pg_dump: "+pgDump+" (16.10)")
	assert.NotContains(t, zh, "postgresql-17", "服务端为 16.4，选不低于它的最低版本")
}

// “指定数据库”中的库在运行时不存在：本次运行失败并指出是哪个库，不启动导出工具
// （docs/specs/2026-09-27-backup-jobs.md「第 2 步」）
func TestRunMissingDatabase(t *testing.T) {
	e := newRunEnv(t)
	e.setDatabases("app", "postgres")
	run := e.manual()
	assert.Equal(t, job_entity.RunFailed, run.Status)
	assert.Equal(t, job_entity.StepConnect, run.FailedStep, "%s\n%s", run.Reason, logText(run))
	assert.Contains(t, run.Reason, "reports")
	assert.NotContains(t, run.Reason, "app")
	assert.Empty(t, e.argv("pg_dump"), "不启动导出工具")
	assert.Empty(t, e.snapshots(e.job.Ref()))
	e.assertClean()
}

// 运行刚登记为运行中、还没开始执行各步骤时收到取消：取消照常生效，不会因为还没有取消函数而崩溃
func TestRunCancelRightAfterStart(t *testing.T) {
	e := newRunEnv(t)
	queued, err := Runs().Enqueue(e.ctx, e.job.ID, Trigger{Kind: job_entity.TriggerManual})
	require.NoError(t, err)
	_, _, ar, err := defaultRunner.begin(e.ctx, queued.ID)
	require.NoError(t, err)
	require.NotNil(t, ar)

	var panicked any
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { panicked = recover() }()
		_, _ = Runs().Cancel(e.ctx, queued.ID)
	}()
	require.Eventually(t, func() bool { return ar.ctx.Err() != nil }, 5*time.Second, time.Millisecond, "取消作用到运行")
	assert.ErrorIs(t, context.Cause(ar.ctx), errCanceled)
	// 模拟执行随即结束
	defaultRunner.mu.Lock()
	delete(defaultRunner.active, queued.ID)
	defaultRunner.mu.Unlock()
	close(ar.done)
	<-done
	assert.Nil(t, panicked)
}

func TestRunNowFlag(t *testing.T) {
	e := newRunEnv(t)
	var dispatched []int64
	var mu sync.Mutex
	SetDispatcher(func(id int64) {
		mu.Lock()
		defer mu.Unlock()
		dispatched = append(dispatched, id)
	})
	e.job.RunNow = true
	require.NoError(t, job_repo.Job().Create(e.ctx, &job_entity.Job{Name: "未标记", Type: job_entity.TypeBackup,
		DataSourceID: e.ds.ID, StorageID: e.storage, Prefix: "pg/unmarked", Scope: job_entity.ScopeInstance,
		DatabaseNames: "[]", ExcludeTables: "[]", ScheduleWeekdays: "[]", Timezone: "UTC", Enabled: true}))
	require.NoError(t, jobRunNow(e))

	require.NoError(t, Runs().RunPending(e.ctx))
	require.NoError(t, Runs().RunPending(e.ctx))
	page, total, err := job_repo.Run().Page(e.ctx, e.job.ID, 0, 10)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total, "立即执行一次只执行一次")
	assert.Equal(t, job_entity.TriggerManual, page[0].Trigger)
	mu.Lock()
	assert.Equal(t, []int64{page[0].ID}, dispatched)
	mu.Unlock()
	saved, err := job_repo.Job().Find(e.ctx, e.job.ID)
	require.NoError(t, err)
	assert.False(t, saved.RunNow, "执行后清除标记")
}

// jobRunNow 直接在库中给任务打上“立即执行一次”标记（Save 不写 run_now）
func jobRunNow(e *runEnv) error {
	if err := job_repo.Job().Delete(e.ctx, e.job.ID); err != nil {
		return err
	}
	return job_repo.Job().Create(e.ctx, e.job)
}
