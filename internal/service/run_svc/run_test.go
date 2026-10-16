package run_svc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	runapi "github.com/opskat/opsnap/internal/api/run"
	"github.com/opskat/opsnap/internal/model/auth"
	"github.com/opskat/opsnap/internal/model/entity/job_entity"
	"github.com/opskat/opsnap/internal/model/entity/notify_entity"
	"github.com/opskat/opsnap/internal/model/entity/repo_entity"
	"github.com/opskat/opsnap/internal/model/entity/run_entity"
	"github.com/opskat/opsnap/internal/model/entity/server_entity"
	"github.com/opskat/opsnap/internal/model/entity/setting_entity"
	"github.com/opskat/opsnap/internal/pkg/cmdbuild"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/executor"
	"github.com/opskat/opsnap/internal/pkg/secret"
	"github.com/opskat/opsnap/internal/pkg/sshconn/sshconntest"
	"github.com/opskat/opsnap/internal/repository/alert_repo"
	"github.com/opskat/opsnap/internal/repository/job_repo"
	"github.com/opskat/opsnap/internal/repository/notify_repo"
	"github.com/opskat/opsnap/internal/repository/repo_repo"
	"github.com/opskat/opsnap/internal/repository/run_repo"
	"github.com/opskat/opsnap/internal/repository/server_repo"
	"github.com/opskat/opsnap/internal/repository/setting_repo"
	"github.com/opskat/opsnap/internal/service/alert_svc"
	"github.com/opskat/opsnap/internal/service/notify_svc"
	"github.com/opskat/opsnap/internal/service/repo_svc"
	"github.com/opskat/opsnap/migrations"
)

// 带标记的假凭据：断言它们不出现在参数、日志与推送里
//
//nolint:gosec // 测试用的假凭据
const (
	dbPassword  = "SECRET-DB-PW"
	repoKey     = "SECRET-REPO-KEY"
	sshPassword = "SECRET-SSH-PW"
)

// fakeNotify 记录收到的通知事件，可脚本化返回错误
type fakeNotify struct {
	mu     sync.Mutex
	events []notify_svc.Event
	err    error
}

func (n *fakeNotify) send(_ context.Context, e notify_svc.Event) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.events = append(n.events, e)
	return n.err
}

func (n *fakeNotify) types() []notify_entity.EventType {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]notify_entity.EventType, 0, len(n.events))
	for _, e := range n.events {
		out = append(out, e.Type)
	}
	return out
}

func (n *fakeNotify) last() notify_svc.Event {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.events[len(n.events)-1]
}

// fakeLister 运行时展开「全部库」的替身
type fakeLister struct {
	names []string
	addrs []string
}

func (l *fakeLister) list(_ context.Context, _ *job_entity.Job, addr string) ([]string, error) {
	l.addrs = append(l.addrs, addr)
	return l.names, nil
}

type fixture struct {
	ctx     context.Context
	db      *gorm.DB
	svc     *runSvc
	exec    *executor.Fake
	notify  *fakeNotify
	lister  *fakeLister
	dataDir string
	mount   string
	repoID  int64
}

func setupRunTest(t *testing.T) *fixture {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, migrations.RunMigrations(gdb))

	s, err := secret.New("run-test-master-key")
	require.NoError(t, err)
	secret.SetDefault(s)
	t.Cleanup(func() { secret.SetDefault(nil) })

	db.SetDefault(gdb)
	job_repo.RegisterJob(job_repo.NewJob())
	repo_repo.RegisterRepo(repo_repo.NewRepo())
	server_repo.RegisterServer(server_repo.NewServer())
	setting_repo.RegisterSetting(setting_repo.NewSetting())
	run_repo.RegisterRun(run_repo.NewRun())
	alert_repo.RegisterAlert(alert_repo.NewAlert())
	notify_repo.RegisterNotify(notify_repo.NewNotify())

	f := &fixture{
		db: gdb, ctx: db.WithContextDB(context.Background(), gdb),
		exec: executor.NewFake(), notify: &fakeNotify{}, lister: &fakeLister{},
		dataDir: t.TempDir(), mount: t.TempDir(),
	}
	repo_svc.RegisterRepo(repo_svc.NewRepo(f.exec, time.Now))
	require.NoError(t, os.WriteFile(filepath.Join(f.mount, repo_entity.MarkerFile), nil, 0o600))
	key, err := secret.Default().Encrypt(repoKey)
	require.NoError(t, err)
	rp := &repo_entity.Repo{
		Name: "nas", Type: repo_entity.TypeLocal, Params: repo_entity.Params{Path: f.mount},
		OpsnapKey: key, Status: repo_entity.StatusOK, FreeSpace: repo_entity.FreeSpaceUnknown,
	}
	require.NoError(t, repo_repo.Repo().Create(f.ctx, rp))
	f.repoID = rp.ID

	f.svc = newRun(Deps{
		Runner: f.exec, Now: time.Now, DataDir: f.dataDir, Self: "/opt/opsnap/opsnap",
		ListDatabases: f.lister.list, Notify: f.notify.send,
	})
	return f
}

func (f *fixture) setting(t *testing.T, mutate func(s *setting_entity.Setting)) {
	t.Helper()
	st := setting_entity.Default()
	mutate(st)
	require.NoError(t, setting_repo.Setting().Save(f.ctx, st))
}

// addJob 保存任务，补齐公共字段；数据库密码明文会被加密
func (f *fixture) addJob(t *testing.T, j *job_entity.Job) *job_entity.Job {
	t.Helper()
	if j.Name == "" {
		j.Name = "job"
	}
	j.RepoID = f.repoID
	j.ScheduleKind, j.ScheduleTime = "daily", "03:00"
	if j.RetentionPreset == "" {
		j.RetentionPreset = "30d"
	}
	if j.TimeoutSec == 0 {
		j.TimeoutSec = job_entity.DefaultTimeoutSec
	}
	j.Compression = job_entity.CompressionAuto
	if j.DBPassword != "" {
		enc, err := secret.Default().Encrypt(j.DBPassword)
		require.NoError(t, err)
		j.DBPassword = enc
	}
	for _, p := range []*[]string{&j.Databases, &j.Paths, &j.Excludes, &j.ExtraArgs} {
		if *p == nil {
			*p = []string{}
		}
	}
	require.NoError(t, job_repo.Job().Create(f.ctx, j))
	return j
}

func (f *fixture) mysqlJob(t *testing.T) *job_entity.Job {
	return f.addJob(t, &job_entity.Job{
		Name: "orders-db", SourceType: job_entity.SourceMySQL, ConnMode: job_entity.ConnDirect,
		DBHost: "10.0.0.5", DBPort: 3306, DBUser: "backup", DBPassword: dbPassword,
		Databases: []string{"orders", "users"},
	})
}

// sshServer 启动进程内 SSH 服务并保存为密码认证的服务器
func (f *fixture) sshServer(t *testing.T) (*sshconntest.SSHServer, int64) {
	t.Helper()
	s := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: sshPassword})
	host, port := sshconntest.HostPort(t, s.Addr)
	enc, err := secret.Default().Encrypt(sshPassword)
	require.NoError(t, err)
	srv := &server_entity.Server{
		Name: "web-1", Host: host, Port: port, Username: "app", AuthType: server_entity.AuthPassword,
		Password: enc, Hops: []server_entity.Hop{}, HostKeyFingerprint: s.Fingerprint,
	}
	require.NoError(t, server_repo.Server().Create(f.ctx, srv))
	return s, srv.ID
}

func (f *fixture) filesJob(t *testing.T, serverID int64) *job_entity.Job {
	return f.addJob(t, &job_entity.Job{
		Name: "site-files", SourceType: job_entity.SourceFiles, ServerID: serverID,
		Paths: []string{"/var/www"}, Excludes: []string{"*.log"}, RetentionPreset: "long",
	})
}

// run 触发并等到运行结束，返回落库的记录
func (f *fixture) run(t *testing.T, jobID int64, trig run_entity.Trigger) *run_entity.Run {
	t.Helper()
	id, err := f.svc.Trigger(f.ctx, jobID, trig)
	require.NoError(t, err)
	return f.finish(t, id)
}

func (f *fixture) finish(t *testing.T, runID int64) *run_entity.Run {
	t.Helper()
	done := make(chan struct{})
	go func() {
		f.svc.wait(runID)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatalf("运行 %d 没有结束", runID)
	}
	r, err := run_repo.Run().Find(f.ctx, runID)
	require.NoError(t, err)
	require.NotNil(t, r)
	return r
}

func summary(id string, processed, added int64) string {
	return fmt.Sprintf(`{"message_type":"summary","files_new":1,"data_added":%d,"total_bytes_processed":%d,"snapshot_id":"%s"}`+"\n", added, processed, id)
}

// calls 返回匹配的调用
func calls(f *executor.Fake, name string, prefix ...string) []cmdbuild.Cmd {
	var out []cmdbuild.Cmd
	m := executor.Match(name, prefix...)
	for _, c := range f.Calls() {
		if m(c) {
			out = append(out, c)
		}
	}
	return out
}

func logText(r *run_entity.Run) string {
	var b strings.Builder
	for _, l := range r.Log {
		b.WriteString(l.Level + " " + l.Text + "\n")
	}
	return b.String()
}

func stepStatuses(r *run_entity.Run) map[string]string {
	out := map[string]string{}
	for _, s := range r.Steps {
		out[s.Key] = s.Status
	}
	return out
}

func codeOf(t *testing.T, err error) int {
	t.Helper()
	var he *httputils.Error
	require.ErrorAs(t, err, &he, "%v", err)
	return he.Code
}

func TestDatabaseRunSuccess(t *testing.T) {
	f := setupRunTest(t)
	j := f.mysqlJob(t)
	f.exec.On(executor.Match("restic", "backup"),
		executor.Step{Output: summary("a1b2c3d4", 1000, 400)},
		executor.Step{Output: summary("e5f6a7b8", 500, 100)})

	r := f.run(t, j.ID, run_entity.TriggerManual)

	assert.Equal(t, run_entity.StatusSuccess, r.Status, logText(r))
	assert.Equal(t, []string{"a1b2c3d4", "e5f6a7b8"}, r.SnapshotIDs)
	assert.Equal(t, int64(1500), r.BytesProcessed)
	assert.Equal(t, int64(500), r.BytesAdded)
	assert.Equal(t, "manual", r.Trigger)
	assert.Equal(t, "orders-db", r.JobName)
	assert.NotZero(t, r.FinishedAt)
	assert.Equal(t, map[string]string{
		"preflight": run_entity.StepSuccess, "export": run_entity.StepSuccess,
		"forget": run_entity.StepSuccess, "notify": run_entity.StepSuccess,
	}, stepStatuses(r))

	// 每个库一次 restic backup --stdin-from-command，导出命令在 -- 之后，凭据只在环境变量里
	backups := calls(f.exec, "restic", "backup")
	require.Len(t, backups, 2)
	for i, db := range []string{"orders", "users"} {
		args := strings.Join(backups[i].Args, " ")
		assert.Contains(t, args, "--stdin-from-command")
		assert.Contains(t, args, "--stdin-filename mysql/"+db+".sql")
		assert.Contains(t, args, "-- mysqldump")
		assert.Contains(t, args, "--databases "+db)
		assert.Contains(t, args, fmt.Sprintf("--tag job:%d", j.ID))
		assert.Contains(t, backups[i].Env, "MYSQL_PWD="+dbPassword)
		assert.Contains(t, backups[i].Env, "RESTIC_PASSWORD="+repoKey)
	}
	for _, c := range f.exec.Calls() {
		args := strings.Join(c.Args, " ")
		assert.NotContains(t, args, dbPassword)
		assert.NotContains(t, args, repoKey)
	}

	// forget 只针对本任务的标签，保留参数来自保留规则
	forgets := calls(f.exec, "restic", "forget")
	require.Len(t, forgets, 1)
	want, err := j.Retention().ForgetArgs()
	require.NoError(t, err)
	assert.Equal(t, []string{"forget", "--compression", "auto", "--json", "--tag", fmt.Sprintf("job:%d", j.ID)}, forgets[0].Args[:6])
	assert.Equal(t, want, forgets[0].Args[6:])

	// 顺序：预检（snapshots）→ 导出 → forget → 刷新存储位置统计（替身的 stats 没有输出，刷新到此为止）
	order := make([]string, 0, len(f.exec.Calls()))
	for _, c := range f.exec.Calls() {
		order = append(order, c.Name+" "+c.Args[0])
	}
	assert.Equal(t, []string{"restic snapshots", "restic backup", "restic backup", "restic forget", "restic stats"}, order)

	assert.Equal(t, []notify_entity.EventType{notify_entity.EventSuccess}, f.notify.types())
	ev := f.notify.last()
	assert.Equal(t, r.ID, ev.RunID)
	assert.Equal(t, j.ID, ev.JobID)
	assert.Equal(t, "orders-db", ev.JobName)
	assert.Equal(t, fmt.Sprintf("/runs/%d", r.ID), ev.Link)
}

func TestAllDatabasesResolvedAtRunTime(t *testing.T) {
	f := setupRunTest(t)
	f.lister.names = []string{"app", "template1", "shop"}
	j := f.addJob(t, &job_entity.Job{
		Name: "pg", SourceType: job_entity.SourcePostgres, ConnMode: job_entity.ConnDirect,
		DBHost: "10.0.0.6", DBPort: 5432, DBUser: "backup", DBPassword: dbPassword, AllDatabases: true,
	})
	f.exec.On(executor.Match("restic", "backup"), executor.Step{Output: summary("aaaaaaaa", 1, 1)}, executor.Step{Output: summary("bbbbbbbb", 1, 1)})

	r := f.run(t, j.ID, run_entity.TriggerSchedule)
	assert.Equal(t, run_entity.StatusSuccess, r.Status, logText(r))
	assert.Equal(t, []string{"10.0.0.6:5432"}, f.lister.addrs)
	backups := calls(f.exec, "restic", "backup")
	require.Len(t, backups, 2, "系统库不导出")
	assert.Contains(t, strings.Join(backups[0].Args, " "), "--dbname=app")
	assert.Contains(t, strings.Join(backups[1].Args, " "), "--dbname=shop")
	assert.Equal(t, "schedule", r.Trigger)
}

// dialingRunner 在 restic backup 时像导出命令一样连接 redis-cli 的 -h/-p 地址并收发一次数据，
// 用来验证经 SSH 的本地转发确实通到服务器上的数据库地址
type dialingRunner struct {
	*executor.Fake
	mu    sync.Mutex
	reply string
}

func (d *dialingRunner) Run(ctx context.Context, cmd cmdbuild.Cmd, out io.Writer) (int, error) {
	if executor.Match("restic", "backup")(cmd) {
		var host, port string
		for i, a := range cmd.Args {
			switch a {
			case "-h":
				host = cmd.Args[i+1]
			case "-p":
				port = cmd.Args[i+1]
			}
		}
		if c, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), 5*time.Second); err == nil {
			_, _ = c.Write([]byte("PING\n"))
			buf := make([]byte, 5)
			_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
			n, _ := io.ReadFull(c, buf)
			d.mu.Lock()
			d.reply = string(buf[:n])
			d.mu.Unlock()
			_ = c.Close()
		}
	}
	return d.Fake.Run(ctx, cmd, out)
}

func TestSSHModeDumpUsesLocalForward(t *testing.T) {
	f := setupRunTest(t)
	sshd, srvID := f.sshServer(t)
	echo := sshconntest.StartEcho(t)
	host, port := sshconntest.HostPort(t, echo)
	j := f.addJob(t, &job_entity.Job{
		Name: "redis", SourceType: job_entity.SourceRedis, ConnMode: job_entity.ConnSSH, ServerID: srvID,
		DBHost: host, DBPort: port, DBPassword: dbPassword,
	})
	f.exec.On(executor.Match("restic", "backup"), executor.Step{Output: summary("cccccccc", 1, 1)})
	dr := &dialingRunner{Fake: f.exec}
	f.svc.d.Runner = dr

	r := f.run(t, j.ID, run_entity.TriggerManual)
	assert.Equal(t, run_entity.StatusSuccess, r.Status, logText(r))
	backups := calls(f.exec, "restic", "backup")
	require.Len(t, backups, 1)
	args := strings.Join(backups[0].Args, " ")
	assert.Contains(t, args, "-- redis-cli -h 127.0.0.1 -p ")
	assert.NotContains(t, args, fmt.Sprintf("-p %d", port), "经 SSH 时导出命令连本机转发端口")
	assert.Contains(t, backups[0].Env, "REDISCLI_AUTH="+dbPassword)
	assert.Equal(t, "PING\n", dr.reply, "本地转发端口通到数据库")
	assert.Equal(t, []string{echo}, sshd.DirectTCPIP(), "经服务器转发到从服务器上看的数据库地址")
}

func TestPreflightFailureLeavesNoSnapshot(t *testing.T) {
	t.Run("本地目录缺少标记文件", func(t *testing.T) {
		f := setupRunTest(t)
		require.NoError(t, os.Remove(filepath.Join(f.mount, repo_entity.MarkerFile)))
		j := f.mysqlJob(t)
		r := f.run(t, j.ID, run_entity.TriggerManual)
		assert.Equal(t, run_entity.StatusFailed, r.Status)
		assert.Contains(t, r.Reason, "没有挂载")
		assert.Empty(t, calls(f.exec, "restic", "backup"))
		assert.Empty(t, r.SnapshotIDs)
		assert.Equal(t, run_entity.StepFailed, stepStatuses(r)["preflight"])
		assert.Equal(t, run_entity.StepSkipped, stepStatuses(r)["export"])
	})

	t.Run("仓库打不开", func(t *testing.T) {
		f := setupRunTest(t)
		f.exec.On(executor.Match("restic", "snapshots"), executor.Step{ExitCode: 1, Output: "Fatal: wrong password or no key found\n"})
		j := f.mysqlJob(t)
		r := f.run(t, j.ID, run_entity.TriggerManual)
		assert.Equal(t, run_entity.StatusFailed, r.Status)
		assert.Contains(t, r.Reason, "无法访问存储位置")
		assert.Empty(t, calls(f.exec, "restic", "backup"))
	})

	t.Run("服务器指纹不符：拒绝连接", func(t *testing.T) {
		f := setupRunTest(t)
		_, srvID := f.sshServer(t)
		require.NoError(t, f.db.Exec(`UPDATE servers SET host_key_fingerprint = ? WHERE id = ?`, "SHA256:changed", srvID).Error)
		j := f.filesJob(t, srvID)
		r := f.run(t, j.ID, run_entity.TriggerManual)
		assert.Equal(t, run_entity.StatusFailed, r.Status)
		assert.Contains(t, r.Reason, "连接服务器失败")
		assert.Contains(t, r.Reason, "指纹")
		assert.Empty(t, calls(f.exec, "rsync"))
		assert.Empty(t, calls(f.exec, "restic", "backup"))
	})

	t.Run("服务器连不上", func(t *testing.T) {
		f := setupRunTest(t)
		_, srvID := f.sshServer(t)
		require.NoError(t, f.db.Exec(`UPDATE servers SET port = 1 WHERE id = ?`, srvID).Error)
		j := f.addJob(t, &job_entity.Job{
			SourceType: job_entity.SourceMySQL, ConnMode: job_entity.ConnSSH, ServerID: srvID,
			DBHost: "172.18.0.5", DBPort: 3306, DBUser: "u", Databases: []string{"a"},
		})
		r := f.run(t, j.ID, run_entity.TriggerManual)
		assert.Equal(t, run_entity.StatusFailed, r.Status)
		assert.Contains(t, r.Reason, "连接服务器失败")
		assert.Empty(t, calls(f.exec, "restic", "backup"))
	})
}

func TestDumpNonZeroLeavesNoSnapshot(t *testing.T) {
	f := setupRunTest(t)
	j := f.mysqlJob(t)
	// 纵深防御：即使 restic 报告了快照 ID，非零退出也必须失败，并删除这个快照
	f.exec.On(executor.Match("restic", "backup"),
		executor.Step{ExitCode: 1, Output: "mysqldump: Got error: 1045 Access denied\n" + summary("deadbeef", 10, 10)})

	r := f.run(t, j.ID, run_entity.TriggerManual)
	assert.Equal(t, run_entity.StatusFailed, r.Status)
	assert.Contains(t, r.Reason, "orders")
	assert.Empty(t, r.SnapshotIDs)
	assert.Len(t, calls(f.exec, "restic", "backup"), 1, "第一个库失败后不再继续")

	forgets := calls(f.exec, "restic", "forget")
	require.Len(t, forgets, 1, "只删除报告的快照，不按保留规则清理")
	assert.Equal(t, "deadbeef", forgets[0].Args[len(forgets[0].Args)-1])
	assert.NotContains(t, forgets[0].Args, "--keep-within")
	assert.Equal(t, run_entity.StepFailed, stepStatuses(r)["export"])
	assert.Equal(t, []notify_entity.EventType{notify_entity.EventFailed}, f.notify.types())
}

func TestLaterFailureRollsBackEarlierSnapshots(t *testing.T) {
	f := setupRunTest(t)
	j := f.mysqlJob(t)
	f.exec.On(executor.Match("restic", "backup"),
		executor.Step{Output: summary("11111111", 1, 1)},
		executor.Step{ExitCode: 1, Output: "mysqldump: error\n"})
	// 本次运行标签下还能查到一个没报告 ID 的快照，也要删除
	f.exec.On(executor.Match("restic", "snapshots"),
		executor.Step{Output: "[]"},
		executor.Step{Output: `[{"id":"22222222aaaa","short_id":"22222222"}]`})

	r := f.run(t, j.ID, run_entity.TriggerManual)
	assert.Equal(t, run_entity.StatusFailed, r.Status)
	assert.Empty(t, r.SnapshotIDs)
	forgets := calls(f.exec, "restic", "forget")
	require.Len(t, forgets, 1)
	assert.Contains(t, forgets[0].Args, "11111111")
	assert.Contains(t, forgets[0].Args, "22222222aaaa")
	snaps := calls(f.exec, "restic", "snapshots")
	require.Len(t, snaps, 2)
	assert.Contains(t, strings.Join(snaps[1].Args, " "), fmt.Sprintf("--tag job:%d,run:%d", j.ID, r.ID))
}

func TestMirrorResetWhenSourceChanges(t *testing.T) {
	f := setupRunTest(t)
	_, srvID := f.sshServer(t)
	j := f.filesJob(t, srvID)
	f.exec.On(executor.Match("restic", "backup"), executor.Step{Output: summary("f00df00d", 1, 1)})
	mirror := filepath.Join(f.dataDir, "mirror", fmt.Sprint(j.ID))
	plant := func(rel string) string {
		p := filepath.Join(mirror, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
		require.NoError(t, os.WriteFile(p, []byte("x"), 0o600))
		return p
	}

	require.Equal(t, run_entity.StatusSuccess, f.run(t, j.ID, run_entity.TriggerManual).Status)
	// 路径不变：镜像保留，rsync 只做增量
	kept := plant("var/www/index.html")
	require.Equal(t, run_entity.StatusSuccess, f.run(t, j.ID, run_entity.TriggerManual).Status)
	assert.FileExists(t, kept)

	// 去掉了 /var/www：rsync --relative --delete 只管这次同步的路径，旧路径的内容会一直留在镜像里进入之后的快照
	require.NoError(t, f.db.Model(&job_entity.Job{}).Where("id = ?", j.ID).Update("paths", `["/srv/app"]`).Error)
	require.Equal(t, run_entity.StatusSuccess, f.run(t, j.ID, run_entity.TriggerManual).Status)
	assert.NoFileExists(t, kept, "来源变了，旧镜像要清空")
}

func TestFilesRun(t *testing.T) {
	t.Run("rsync 退出码 24：有警告，快照照常生成", func(t *testing.T) {
		f := setupRunTest(t)
		_, srvID := f.sshServer(t)
		j := f.filesJob(t, srvID)
		f.exec.On(executor.Match("rsync"), executor.Step{ExitCode: 24, Output: "file has vanished: \"/var/www/tmp/x\"\n"})
		f.exec.On(executor.Match("restic", "backup"), executor.Step{Output: summary("f00df00d", 2048, 512)})

		r := f.run(t, j.ID, run_entity.TriggerManual)
		assert.Equal(t, run_entity.StatusWarning, r.Status, logText(r))
		assert.Equal(t, []string{"f00df00d"}, r.SnapshotIDs)
		assert.Equal(t, run_entity.StepWarning, stepStatuses(r)["export"])
		assert.Contains(t, r.Reason, "文件消失")

		rsyncs := calls(f.exec, "rsync")
		require.Len(t, rsyncs, 1)
		mirror := filepath.Join(f.dataDir, "mirror", fmt.Sprint(j.ID))
		args := rsyncs[0].Args
		assert.Contains(t, args, "/opt/opsnap/opsnap rsh", "rsync 经 opsnap rsh 子命令访问服务器")
		assert.Contains(t, args, "opsnap:/var/www")
		assert.Contains(t, args, "--exclude=*.log")
		assert.Equal(t, mirror+"/", args[len(args)-1])
		envKeys := make([]string, 0, len(rsyncs[0].Env))
		for _, kv := range rsyncs[0].Env {
			k, _, _ := strings.Cut(kv, "=")
			envKeys = append(envKeys, k)
			assert.NotContains(t, kv, sshPassword)
		}
		assert.ElementsMatch(t, []string{"OPSNAP_RSH_SOCKET", "OPSNAP_RSH_TOKEN"}, envKeys)
		assert.NotContains(t, strings.Join(args, " "), sshPassword)

		backups := calls(f.exec, "restic", "backup")
		require.Len(t, backups, 1)
		assert.Equal(t, mirror, backups[0].Args[len(backups[0].Args)-1])
		assert.Contains(t, logText(r), "warn ")
	})

	t.Run("rsync 其他错误：失败，不生成快照", func(t *testing.T) {
		f := setupRunTest(t)
		_, srvID := f.sshServer(t)
		j := f.filesJob(t, srvID)
		f.exec.On(executor.Match("rsync"), executor.Step{ExitCode: 23, Output: "rsync: some files could not be transferred\n"})

		r := f.run(t, j.ID, run_entity.TriggerManual)
		assert.Equal(t, run_entity.StatusFailed, r.Status)
		assert.Contains(t, r.Reason, "23")
		assert.Empty(t, calls(f.exec, "restic", "backup"))
		assert.Empty(t, r.SnapshotIDs)
	})

	t.Run("快照不完整（restic 退出码 3）：失败，删除该快照", func(t *testing.T) {
		f := setupRunTest(t)
		_, srvID := f.sshServer(t)
		j := f.filesJob(t, srvID)
		f.exec.On(executor.Match("restic", "backup"), executor.Step{ExitCode: 3, Output: summary("0badc0de", 1, 1)})
		r := f.run(t, j.ID, run_entity.TriggerManual)
		assert.Equal(t, run_entity.StatusFailed, r.Status)
		assert.Empty(t, r.SnapshotIDs)
		forgets := calls(f.exec, "restic", "forget")
		require.Len(t, forgets, 1)
		assert.Equal(t, "0badc0de", forgets[0].Args[len(forgets[0].Args)-1])
	})
}

func TestClearMirrorSetting(t *testing.T) {
	for _, clear := range []bool{true, false} {
		t.Run(fmt.Sprintf("备份后清空=%v", clear), func(t *testing.T) {
			f := setupRunTest(t)
			f.setting(t, func(s *setting_entity.Setting) { s.ClearMirrorAfterBackup = clear })
			_, srvID := f.sshServer(t)
			j := f.filesJob(t, srvID)
			f.exec.On(executor.Match("restic", "backup"), executor.Step{Output: summary("abcdef01", 1, 1)})
			r := f.run(t, j.ID, run_entity.TriggerManual)
			require.Equal(t, run_entity.StatusSuccess, r.Status, logText(r))
			_, err := os.Stat(filepath.Join(f.dataDir, "mirror", fmt.Sprint(j.ID)))
			assert.Equal(t, clear, os.IsNotExist(err), "err=%v", err)
		})
	}

	t.Run("失败时不清空镜像", func(t *testing.T) {
		f := setupRunTest(t)
		f.setting(t, func(s *setting_entity.Setting) { s.ClearMirrorAfterBackup = true })
		_, srvID := f.sshServer(t)
		j := f.filesJob(t, srvID)
		f.exec.On(executor.Match("restic", "backup"), executor.Step{ExitCode: 1})
		r := f.run(t, j.ID, run_entity.TriggerManual)
		require.Equal(t, run_entity.StatusFailed, r.Status)
		_, err := os.Stat(filepath.Join(f.dataDir, "mirror", fmt.Sprint(j.ID)))
		assert.NoError(t, err)
	})
}

func TestTimeoutLeavesNoSnapshot(t *testing.T) {
	f := setupRunTest(t)
	j := f.addJob(t, &job_entity.Job{
		SourceType: job_entity.SourceRedis, ConnMode: job_entity.ConnDirect, DBHost: "10.0.0.7", DBPort: 6379,
		TimeoutSec: 1,
	})
	// restic 在被终止前已报告了快照 ID：必须删除
	f.exec.On(executor.Match("restic", "backup"), executor.Step{Block: true, Output: summary("ab12cd34", 1, 1)})

	start := time.Now()
	r := f.run(t, j.ID, run_entity.TriggerManual)
	assert.Less(t, time.Since(start), 10*time.Second)
	assert.Equal(t, run_entity.StatusFailed, r.Status)
	assert.Equal(t, "超时", r.Reason)
	assert.Empty(t, r.SnapshotIDs)
	forgets := calls(f.exec, "restic", "forget")
	require.Len(t, forgets, 1)
	assert.Equal(t, "ab12cd34", forgets[0].Args[len(forgets[0].Args)-1])
	assert.Equal(t, notify_entity.EventFailed, f.notify.last().Type)
	assert.Equal(t, "超时", f.notify.last().ErrorSummary)
}

// startBlocked 触发一次会卡在 restic backup 的运行，等到 backup 已开始
func startBlocked(t *testing.T, f *fixture, j *job_entity.Job) int64 {
	t.Helper()
	id, err := f.svc.Trigger(f.ctx, j.ID, run_entity.TriggerManual)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		for _, c := range calls(f.exec, "restic", "backup") {
			if strings.Contains(strings.Join(c.Args, " "), fmt.Sprintf("job:%d", j.ID)) {
				return true
			}
		}
		return false
	}, 10*time.Second, 10*time.Millisecond)
	return id
}

func TestManualStop(t *testing.T) {
	f := setupRunTest(t)
	j := f.mysqlJob(t)
	f.exec.On(executor.Match("restic", "backup"), executor.Step{Block: true})
	id := startBlocked(t, f, j)

	_, err := f.svc.Stop(f.ctx, &runapi.StopRequest{ID: id, Confirm: false})
	assert.Equal(t, code.RunStopNotConfirmed, codeOf(t, err))
	got, err := f.svc.Get(f.ctx, id)
	require.NoError(t, err)
	assert.Equal(t, run_entity.StatusRunning, got.Status, "未确认时不停止")

	_, err = f.svc.Stop(f.ctx, &runapi.StopRequest{ID: id, Confirm: true})
	require.NoError(t, err)
	r := f.finish(t, id)
	assert.Equal(t, run_entity.StatusFailed, r.Status)
	assert.Equal(t, "手动停止", r.Reason)
	assert.Empty(t, r.SnapshotIDs)
	assert.Len(t, calls(f.exec, "restic", "backup"), 1, "停止后不再导出后面的库")
	assert.Empty(t, calls(f.exec, "restic", "forget", "--compression", "auto", "--json", "--tag"), "不按保留规则清理")

	_, err = f.svc.Stop(f.ctx, &runapi.StopRequest{ID: id, Confirm: true})
	assert.Equal(t, code.RunNotRunning, codeOf(t, err))
	_, err = f.svc.Stop(f.ctx, &runapi.StopRequest{ID: 9999, Confirm: true})
	assert.Equal(t, code.RunNotFound, codeOf(t, err))
}

func TestSameJobRejected(t *testing.T) {
	f := setupRunTest(t)
	j := f.mysqlJob(t)
	f.exec.On(executor.Match("restic", "backup"), executor.Step{Block: true})
	id := startBlocked(t, f, j)

	_, err := f.svc.Trigger(f.ctx, j.ID, run_entity.TriggerSchedule)
	assert.ErrorIs(t, err, ErrAlreadyRunning)
	_, err = f.svc.RunJob(f.ctx, &runapi.RunJobRequest{ID: j.ID})
	assert.Equal(t, code.RunAlreadyRunning, codeOf(t, err))

	list, err := f.svc.List(f.ctx, &runapi.ListRequest{JobID: j.ID})
	require.NoError(t, err)
	assert.Equal(t, int64(1), list.Total, "被拒绝的触发不产生记录")

	_, err = f.svc.Stop(f.ctx, &runapi.StopRequest{ID: id, Confirm: true})
	require.NoError(t, err)
	f.finish(t, id)

	// 结束后可以再次运行
	f.exec.On(executor.Match("restic"), executor.Step{})
	_, err = f.svc.Trigger(f.ctx, j.ID, run_entity.TriggerManual)
	assert.NoError(t, err)
}

func TestMaxConcurrentRuns(t *testing.T) {
	f := setupRunTest(t)
	f.setting(t, func(s *setting_entity.Setting) { s.MaxConcurrentRuns = 1 })
	a := f.mysqlJob(t)
	b := f.addJob(t, &job_entity.Job{Name: "cache", SourceType: job_entity.SourceRedis, ConnMode: job_entity.ConnDirect, DBHost: "10.0.0.8", DBPort: 6379})
	f.exec.On(executor.Match("restic", "backup"), executor.Step{Block: true})
	idA := startBlocked(t, f, a)

	idB, err := f.svc.Trigger(f.ctx, b.ID, run_entity.TriggerManual)
	require.NoError(t, err)
	time.Sleep(200 * time.Millisecond)
	assert.Len(t, calls(f.exec, "restic", "backup"), 1, "名额用完时第二个任务等待")
	gotB, err := f.svc.Get(f.ctx, idB)
	require.NoError(t, err)
	assert.Equal(t, run_entity.StatusRunning, gotB.Status)

	_, err = f.svc.Stop(f.ctx, &runapi.StopRequest{ID: idA, Confirm: true})
	require.NoError(t, err)
	f.finish(t, idA)
	require.Eventually(t, func() bool { return len(calls(f.exec, "restic", "backup")) == 2 }, 10*time.Second, 10*time.Millisecond)
	_, err = f.svc.Stop(f.ctx, &runapi.StopRequest{ID: idB, Confirm: true})
	require.NoError(t, err)
	f.finish(t, idB)
}

func TestSlotFreedBeforeNotifying(t *testing.T) {
	f := setupRunTest(t)
	f.setting(t, func(s *setting_entity.Setting) { s.MaxConcurrentRuns = 1 })
	a := f.addJob(t, &job_entity.Job{Name: "a", SourceType: job_entity.SourceRedis, ConnMode: job_entity.ConnDirect, DBHost: "10.0.0.7", DBPort: 6379})
	b := f.addJob(t, &job_entity.Job{Name: "b", SourceType: job_entity.SourceRedis, ConnMode: job_entity.ConnDirect, DBHost: "10.0.0.8", DBPort: 6379})
	f.exec.On(executor.Match("restic", "backup"), executor.Step{Output: summary("aaaaaaaa", 1, 1)})
	// a 的通知卡住（Webhook 对端无响应，带重试可能要几十秒）
	unblock := make(chan struct{})
	var once sync.Once
	f.svc.d.Notify = func(ctx context.Context, e notify_svc.Event) error {
		if e.JobID == a.ID {
			once.Do(func() { <-unblock })
		}
		return nil
	}
	idA, err := f.svc.Trigger(f.ctx, a.ID, run_entity.TriggerManual)
	require.NoError(t, err)
	defer func() { close(unblock); f.finish(t, idA) }()
	require.Eventually(t, func() bool {
		r, err := run_repo.Run().Find(f.ctx, idA)
		return err == nil && r.Status == run_entity.StatusSuccess
	}, 10*time.Second, 10*time.Millisecond)

	// a 已经有结果，只是还在发通知：同时运行上限不应被它占着
	rb := f.run(t, b.ID, run_entity.TriggerManual)
	assert.Equal(t, run_entity.StatusSuccess, rb.Status)
}

// failingLastSucceeded 查询最近一次成功时出错的运行记录仓储
type failingLastSucceeded struct{ run_repo.RunRepo }

func (failingLastSucceeded) LastSucceeded(context.Context, int64) (*run_entity.Run, error) {
	return nil, errors.New("database is locked")
}

func TestFailureNotifiedEvenIfLastSuccessUnknown(t *testing.T) {
	f := setupRunTest(t)
	j := f.mysqlJob(t)
	f.exec.On(executor.Match("restic", "backup"), executor.Step{ExitCode: 1, Output: "mysqldump: error\n"})
	run_repo.RegisterRun(failingLastSucceeded{run_repo.NewRun()})
	t.Cleanup(func() { run_repo.RegisterRun(run_repo.NewRun()) })

	r := f.run(t, j.ID, run_entity.TriggerManual)
	assert.Equal(t, run_entity.StatusFailed, r.Status)
	require.Equal(t, []notify_entity.EventType{notify_entity.EventFailed}, f.notify.types(), "查不到上次成功时间也要发「备份失败」")
	assert.True(t, f.notify.last().LastSuccessAt.IsZero())
	assert.False(t, r.NotifyFailed, "通知已经发出")
	assert.Contains(t, logText(r), "读取上次成功的时间失败")
}

func TestTriggerRecorded(t *testing.T) {
	f := setupRunTest(t)
	j := f.addJob(t, &job_entity.Job{SourceType: job_entity.SourceRedis, ConnMode: job_entity.ConnDirect, DBHost: "10.0.0.9", DBPort: 6379})
	f.exec.On(executor.Match("restic", "backup"), executor.Step{Output: summary("aaaaaaaa", 1, 1)})

	cases := []struct {
		ctx  context.Context
		want string
	}{
		{auth.WithActor(f.ctx, auth.Actor{Kind: auth.KindAdmin, ID: 1, Name: "admin"}), "manual"},
		// 名称可能重名，带上列表里显示的密钥前缀才能确定是哪把密钥
		{auth.WithActor(f.ctx, auth.Actor{Kind: auth.KindAPIKey, ID: 7, Name: "ci-deploy", Prefix: "osk_1a2b3c4d"}), "api_key:ci-deploy（osk_1a2b3c4d…）"},
	}
	for _, c := range cases {
		resp, err := f.svc.RunJob(c.ctx, &runapi.RunJobRequest{ID: j.ID})
		require.NoError(t, err)
		r := f.finish(t, resp.RunID)
		assert.Equal(t, c.want, r.Trigger)
	}
	r := f.run(t, j.ID, run_entity.TriggerSchedule)
	assert.Equal(t, "schedule", r.Trigger)

	_, err := f.svc.RunJob(f.ctx, &runapi.RunJobRequest{ID: 4242})
	assert.Equal(t, code.JobNotFound, codeOf(t, err))
}

func TestSecretsNeverLogged(t *testing.T) {
	f := setupRunTest(t)
	j := f.mysqlJob(t)
	// 子进程把凭据打印出来（例如错误信息里带了连接串）
	f.exec.On(executor.Match("restic", "backup"),
		executor.Step{Output: "warning: connecting with password " + dbPassword + "\n" +
			`{"message_type":"error","error":{"message":"repo key ` + repoKey + `"},"during":"archival","item":"x"}` + "\n" +
			summary("aaaaaaaa", 1, 1)},
		executor.Step{ExitCode: 1, Output: "fatal: " + repoKey + " / " + dbPassword + "\n"})

	var streamed strings.Builder
	var mu sync.Mutex
	id, err := f.svc.Trigger(f.ctx, j.ID, run_entity.TriggerManual)
	require.NoError(t, err)
	streamDone := make(chan error, 1)
	go func() {
		streamDone <- f.svc.StreamLog(context.Background(), id, false, func(event string, data any) error {
			mu.Lock()
			defer mu.Unlock()
			streamed.WriteString(event + fmt.Sprintf(" %+v\n", data))
			return nil
		})
	}()
	r := f.finish(t, id)
	require.NoError(t, <-streamDone)

	assert.Equal(t, run_entity.StatusFailed, r.Status)
	persisted := logText(r) + r.Reason
	for _, s := range []string{dbPassword, repoKey} {
		assert.NotContains(t, persisted, s)
		assert.NotContains(t, streamed.String(), s)
	}
	assert.Contains(t, persisted, "******")
	assert.Contains(t, streamed.String(), "end ")

	// 同时验证「只看警告」
	resp, err := f.svc.Log(f.ctx, &runapi.LogRequest{ID: id, WarningsOnly: true})
	require.NoError(t, err)
	require.NotEmpty(t, resp.Lines)
	for _, l := range resp.Lines {
		assert.NotEqual(t, run_entity.LevelInfo, l.Level, l.Text)
	}
	all, err := f.svc.Log(f.ctx, &runapi.LogRequest{ID: id})
	require.NoError(t, err)
	assert.Greater(t, len(all.Lines), len(resp.Lines))
}

func TestNotifyEvents(t *testing.T) {
	t.Run("失败后恢复：发送恢复正常与成功；失败事件带上次成功时间", func(t *testing.T) {
		f := setupRunTest(t)
		j := f.addJob(t, &job_entity.Job{SourceType: job_entity.SourceRedis, ConnMode: job_entity.ConnDirect, DBHost: "10.0.0.9", DBPort: 6379})
		f.exec.On(executor.Match("restic", "backup"),
			executor.Step{Output: summary("aaaaaaaa", 1, 1)},
			executor.Step{ExitCode: 1},
			executor.Step{Output: summary("bbbbbbbb", 1, 1)})

		ok1 := f.run(t, j.ID, run_entity.TriggerManual)
		require.Equal(t, run_entity.StatusSuccess, ok1.Status)
		failed := f.run(t, j.ID, run_entity.TriggerManual)
		require.Equal(t, run_entity.StatusFailed, failed.Status)
		ev := f.notify.last()
		assert.Equal(t, notify_entity.EventFailed, ev.Type)
		assert.Equal(t, failed.Reason, ev.ErrorSummary)
		assert.Equal(t, ok1.FinishedAt, ev.LastSuccessAt.Unix())

		f.run(t, j.ID, run_entity.TriggerManual)
		assert.Equal(t, []notify_entity.EventType{
			notify_entity.EventSuccess, notify_entity.EventFailed, notify_entity.EventRecovered, notify_entity.EventSuccess,
		}, f.notify.types())
	})

	t.Run("太久没有成功后恢复：只发一条恢复正常", func(t *testing.T) {
		staleThenSuccess(t, false)
	})

	t.Run("失败、太久没有成功、再成功：恢复正常也只发一条", func(t *testing.T) {
		staleThenSuccess(t, true)
	})

	t.Run("通知发送失败：运行记录注明", func(t *testing.T) {
		f := setupRunTest(t)
		f.notify.err = errors.New("webhook 返回 502")
		j := f.addJob(t, &job_entity.Job{SourceType: job_entity.SourceRedis, ConnMode: job_entity.ConnDirect, DBHost: "10.0.0.9", DBPort: 6379})
		f.exec.On(executor.Match("restic", "backup"), executor.Step{ExitCode: 1})
		r := f.run(t, j.ID, run_entity.TriggerManual)
		assert.Equal(t, run_entity.StatusFailed, r.Status, "通知失败不改变运行结果")
		assert.True(t, r.NotifyFailed)
		assert.Equal(t, run_entity.StepFailed, stepStatuses(r)["notify"])
		assert.Contains(t, logText(r), "通知发送失败")
		got, err := f.svc.Get(f.ctx, r.ID)
		require.NoError(t, err)
		assert.True(t, got.NotifyFailed)
	})
}

func TestAttentionAndRecover(t *testing.T) {
	f := setupRunTest(t)
	ok := f.addJob(t, &job_entity.Job{Name: "ok", SourceType: job_entity.SourceRedis, ConnMode: job_entity.ConnDirect, DBHost: "h", DBPort: 1})
	bad := f.addJob(t, &job_entity.Job{Name: "bad", SourceType: job_entity.SourceRedis, ConnMode: job_entity.ConnDirect, DBHost: "h", DBPort: 2})
	f.exec.On(func(c cmdbuild.Cmd) bool {
		return c.Name == "restic" && c.Args[0] == "backup" && strings.Contains(strings.Join(c.Args, " "), fmt.Sprintf("job:%d", bad.ID))
	}, executor.Step{ExitCode: 1})
	f.exec.On(executor.Match("restic", "backup"), executor.Step{Output: summary("aaaaaaaa", 1, 1)})
	f.run(t, ok.ID, run_entity.TriggerManual)
	f.run(t, bad.ID, run_entity.TriggerManual)
	// 有警告：快照已生成，清理旧快照失败
	warn := f.addJob(t, &job_entity.Job{Name: "warn", SourceType: job_entity.SourceRedis, ConnMode: job_entity.ConnDirect, DBHost: "h", DBPort: 3})
	f.exec.On(func(c cmdbuild.Cmd) bool {
		return c.Name == "restic" && c.Args[0] == "forget" && strings.Contains(strings.Join(c.Args, " "), fmt.Sprintf("job:%d", warn.ID))
	}, executor.Step{ExitCode: 1})
	require.Equal(t, run_entity.StatusWarning, f.run(t, warn.ID, run_entity.TriggerManual).Status)
	// 太久没有成功：从未运行过，创建时间是 1970 年
	stale := f.addJob(t, &job_entity.Job{Name: "stale", SourceType: job_entity.SourceRedis, ConnMode: job_entity.ConnDirect, DBHost: "h", DBPort: 4})
	// 刚创建、从未运行过：还没到阈值，不需要处理
	fresh := &job_entity.Job{Name: "fresh", SourceType: job_entity.SourceRedis, ConnMode: job_entity.ConnDirect, DBHost: "h", DBPort: 5, Createtime: time.Now().Unix()}
	f.addJob(t, fresh)

	// 「需要处理」与概览一致：失败、有警告、太久没有成功
	att, err := f.svc.Attention(f.ctx)
	require.NoError(t, err)
	assert.Equal(t, map[int64]bool{bad.ID: true, warn.ID: true, stale.ID: true}, att)

	// 重启时把残留的运行中记录改为失败
	interrupted := run_entity.New(ok.ID, "ok", run_entity.TriggerSchedule, time.Now())
	interrupted.SetStep(run_entity.StepPreflight, run_entity.StepRunning, "", time.Now())
	require.NoError(t, run_repo.Run().Create(f.ctx, interrupted))
	require.NoError(t, f.svc.Recover(f.ctx))
	f.svc.waitBackground()
	got, err := run_repo.Run().Find(f.ctx, interrupted.ID)
	require.NoError(t, err)
	assert.Equal(t, run_entity.StatusFailed, got.Status)
	assert.Contains(t, got.Reason, "中断")
	assert.Equal(t, run_entity.StepFailed, got.StepStatus(run_entity.StepPreflight), "中断时进行中的步骤记为失败")
	assert.Equal(t, run_entity.StepSkipped, got.StepStatus(run_entity.StepExport))
	// 被中断的运行同样发「备份失败」通知
	ev := f.notify.last()
	assert.Equal(t, notify_entity.EventFailed, ev.Type)
	assert.Equal(t, interrupted.ID, ev.RunID)
	assert.Equal(t, "ok", ev.JobName)
	assert.Contains(t, ev.ErrorSummary, "中断")
}

func TestRecoverRemovesInterruptedSnapshots(t *testing.T) {
	f := setupRunTest(t)
	j := f.mysqlJob(t)
	// 进程在写第二个库时退出：第一个库的快照已写入并记在运行记录里
	interrupted := run_entity.New(j.ID, j.Name, run_entity.TriggerSchedule, time.Now())
	interrupted.SnapshotIDs = []string{"11111111"}
	interrupted.SetStep(run_entity.StepExport, run_entity.StepRunning, "", time.Now())
	require.NoError(t, run_repo.Run().Create(f.ctx, interrupted))
	// 按本次运行的标签还能查到一个没来得及记下的快照
	f.exec.On(executor.Match("restic", "snapshots"), executor.Step{Output: `[{"id":"22222222aaaa","short_id":"22222222"}]`})

	require.NoError(t, f.svc.Recover(f.ctx))
	f.svc.waitBackground()

	got, err := run_repo.Run().Find(f.ctx, interrupted.ID)
	require.NoError(t, err)
	assert.Equal(t, run_entity.StatusFailed, got.Status)
	assert.Empty(t, got.SnapshotIDs, "被中断的运行不能留下快照")
	snaps := calls(f.exec, "restic", "snapshots")
	require.Len(t, snaps, 1)
	assert.Contains(t, strings.Join(snaps[0].Args, " "), fmt.Sprintf("--tag job:%d,run:%d", j.ID, interrupted.ID))
	forgets := calls(f.exec, "restic", "forget")
	require.Len(t, forgets, 1)
	assert.Contains(t, forgets[0].Args, "11111111")
	assert.Contains(t, forgets[0].Args, "22222222aaaa")
	assert.NotContains(t, forgets[0].Args, "--keep-within", "只删除这次运行的快照")
	assert.Contains(t, logText(got), "已删除它留下的快照")
	assert.NotContains(t, logText(got), repoKey)
	assert.Equal(t, notify_entity.EventFailed, f.notify.last().Type)
}

func TestRecoverNotifyFailed(t *testing.T) {
	f := setupRunTest(t)
	j := f.addJob(t, &job_entity.Job{Name: "j", SourceType: job_entity.SourceRedis, ConnMode: job_entity.ConnDirect, DBHost: "h", DBPort: 1})
	interrupted := run_entity.New(j.ID, "j", run_entity.TriggerSchedule, time.Now())
	require.NoError(t, run_repo.Run().Create(f.ctx, interrupted))
	f.notify.err = errors.New("对方返回状态码 500")
	require.NoError(t, f.svc.Recover(f.ctx))
	f.svc.waitBackground()
	got, err := run_repo.Run().Find(f.ctx, interrupted.ID)
	require.NoError(t, err)
	assert.True(t, got.NotifyFailed, "通知发送失败时在运行记录里注明")
	assert.Equal(t, run_entity.StepFailed, got.StepStatus(run_entity.StepNotify))
}

func TestRepoStatsRefreshedAfterRun(t *testing.T) {
	f := setupRunTest(t)
	j := f.addJob(t, &job_entity.Job{SourceType: job_entity.SourceRedis, ConnMode: job_entity.ConnDirect, DBHost: "10.0.0.9", DBPort: 6379})
	f.exec.On(executor.Match("restic", "backup"), executor.Step{Output: summary("aaaaaaaa", 1, 1)}, executor.Step{ExitCode: 1})
	f.exec.On(executor.Match("restic", "stats", "--json", "--mode", "raw-data"), executor.Step{Output: `{"total_size":1000,"snapshots_count":4}`})
	f.exec.On(executor.Match("restic", "stats", "--json", "--mode", "restore-size"), executor.Step{Output: `{"total_size":5000,"snapshots_count":4}`})
	f.exec.On(executor.Match("restic", "key", "list"), executor.Step{Output: `[{"current":true,"id":"op1"},{"current":false,"id":"rc1"}]`})

	require.Equal(t, run_entity.StatusSuccess, f.run(t, j.ID, run_entity.TriggerManual).Status)
	rp, err := repo_repo.Repo().Find(f.ctx, f.repoID)
	require.NoError(t, err)
	assert.Equal(t, 4, rp.SnapshotCount, "存储位置列表的快照数随备份更新")
	assert.Equal(t, int64(1000), rp.StoredSize)
	assert.Equal(t, int64(5000), rp.RawSize)
	assert.Equal(t, 2, rp.KeyCount)

	// 失败的运行不刷新
	n := len(calls(f.exec, "restic", "stats"))
	require.Equal(t, run_entity.StatusFailed, f.run(t, j.ID, run_entity.TriggerManual).Status)
	assert.Len(t, calls(f.exec, "restic", "stats"), n)
}

func TestListAndGet(t *testing.T) {
	f := setupRunTest(t)
	a := f.addJob(t, &job_entity.Job{Name: "a", SourceType: job_entity.SourceRedis, ConnMode: job_entity.ConnDirect, DBHost: "h", DBPort: 1})
	b := f.addJob(t, &job_entity.Job{Name: "b", SourceType: job_entity.SourceRedis, ConnMode: job_entity.ConnDirect, DBHost: "h", DBPort: 2})
	f.exec.On(executor.Match("restic", "backup"), executor.Step{Output: summary("aaaaaaaa", 1, 1)}, executor.Step{ExitCode: 1}, executor.Step{Output: summary("bbbbbbbb", 1, 1)})
	r1 := f.run(t, a.ID, run_entity.TriggerManual)
	r2 := f.run(t, b.ID, run_entity.TriggerManual)
	r3 := f.run(t, a.ID, run_entity.TriggerManual)

	all, err := f.svc.List(f.ctx, &runapi.ListRequest{})
	require.NoError(t, err)
	assert.Equal(t, int64(3), all.Total)
	require.Len(t, all.List, 3)
	assert.Equal(t, []int64{r3.ID, r2.ID, r1.ID}, []int64{all.List[0].ID, all.List[1].ID, all.List[2].ID}, "新的在前")

	byJob, err := f.svc.List(f.ctx, &runapi.ListRequest{JobID: a.ID})
	require.NoError(t, err)
	assert.Equal(t, int64(2), byJob.Total)
	failed, err := f.svc.List(f.ctx, &runapi.ListRequest{Status: run_entity.StatusFailed})
	require.NoError(t, err)
	require.Len(t, failed.List, 1)
	assert.Equal(t, r2.ID, failed.List[0].ID)

	page := &runapi.ListRequest{}
	page.Page, page.Size = 2, 2
	paged, err := f.svc.List(f.ctx, page)
	require.NoError(t, err)
	assert.Equal(t, int64(3), paged.Total)
	require.Len(t, paged.List, 1)
	assert.Equal(t, r1.ID, paged.List[0].ID)

	got, err := f.svc.Get(f.ctx, r1.ID)
	require.NoError(t, err)
	assert.Equal(t, "手动", got.TriggerLabel)
	assert.Equal(t, []string{"aaaaaaaa"}, got.SnapshotIDs)
	require.Len(t, got.Steps, 4)
	assert.Equal(t, "预检", got.Steps[0].Label)
	_, err = f.svc.Get(f.ctx, 9999)
	assert.Equal(t, code.RunNotFound, codeOf(t, err))
}

// staleThenSuccess 让任务进入「太久没有成功」后成功一次，断言恢复正常只发了一条。afterFailure 为真时先有一次失败的运行。
func staleThenSuccess(t *testing.T, afterFailure bool) {
	t.Helper()
	f := setupRunTest(t)
	j := f.addJob(t, &job_entity.Job{SourceType: job_entity.SourceRedis, ConnMode: job_entity.ConnDirect, DBHost: "10.0.0.9", DBPort: 6379})
	if afterFailure {
		f.exec.On(executor.Match("restic", "backup"), executor.Step{ExitCode: 1}, executor.Step{Output: summary("cccccccc", 1, 1)})
		require.Equal(t, run_entity.StatusFailed, f.run(t, j.ID, run_entity.TriggerManual).Status)
	} else {
		f.exec.On(executor.Match("restic", "backup"), executor.Step{Output: summary("cccccccc", 1, 1)})
	}
	// 任务从未成功过且创建时间是 1970 年：必然超过阈值
	require.NoError(t, alert_svc.NewAlert(alert_svc.Deps{Notify: f.notify.send}).Check(f.ctx))
	require.Equal(t, notify_entity.EventStale, f.notify.last().Type)

	require.Equal(t, run_entity.StatusSuccess, f.run(t, j.ID, run_entity.TriggerManual).Status)
	recovered := 0
	for _, ty := range f.notify.types() {
		if ty == notify_entity.EventRecovered {
			recovered++
		}
	}
	assert.Equal(t, 1, recovered, "%v", f.notify.types())
	was, err := alert_repo.Alert().Clear(f.ctx, j.ID)
	require.NoError(t, err)
	assert.False(t, was, "恢复成功后告警状态已清除，下次再失败/超时会重新告警")

	f.exec.On(executor.Match("restic", "backup"), executor.Step{Output: summary("dddddddd", 1, 1)})
	f.run(t, j.ID, run_entity.TriggerManual)
	recovered = 0
	for _, ty := range f.notify.types() {
		if ty == notify_entity.EventRecovered {
			recovered++
		}
	}
	assert.Equal(t, 1, recovered, "后续成功不再重复恢复正常")
}
