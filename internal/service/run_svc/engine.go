package run_svc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/opskat/opsnap/internal/model/entity/job_entity"
	"github.com/opskat/opsnap/internal/model/entity/notify_entity"
	"github.com/opskat/opsnap/internal/model/entity/run_entity"
	"github.com/opskat/opsnap/internal/model/entity/setting_entity"
	"github.com/opskat/opsnap/internal/pkg/cmdbuild"
	"github.com/opskat/opsnap/internal/pkg/repolock"
	"github.com/opskat/opsnap/internal/pkg/rsh"
	"github.com/opskat/opsnap/internal/pkg/secret"
	"github.com/opskat/opsnap/internal/pkg/sshconn"
	"github.com/opskat/opsnap/internal/repository/alert_repo"
	"github.com/opskat/opsnap/internal/repository/run_repo"
	"github.com/opskat/opsnap/internal/repository/server_repo"
	"github.com/opskat/opsnap/internal/service/notify_svc"
	"github.com/opskat/opsnap/internal/service/repo_svc"
	"github.com/opskat/opsnap/internal/service/setting_svc"
)

const (
	// cleanupTimeout 回滚快照（运行已超时或被停止，不能再用运行的 ctx）的时限
	cleanupTimeout = 10 * time.Minute
	// notifyTimeout 发送通知（含重试）的时限
	notifyTimeout = 2 * time.Minute
	// rsyncVanished rsync 报告「同步期间文件消失」的退出码
	rsyncVanished = 24
	// resticIncomplete restic 报告「快照不完整，部分文件无法读取」的退出码
	resticIncomplete = 3
)

// execution 一次运行的执行过程。只在运行引擎的 goroutine 里使用；例外是命令执行期间的进度回调（见 progress）。
type execution struct {
	s       *runSvc
	job     *job_entity.Job
	run     *run_entity.Run
	log     *liveLog
	setting *setting_entity.Setting
	started time.Time

	repo    cmdbuild.Repo
	conn    *sshconn.Conn
	closers []func()
	// orphans 失败的 restic backup 仍报告了的快照 ID，回滚时删除
	orphans []string
	// mirror 本次成功生成快照的文件镜像目录
	mirror   string
	warnings []string

	// committed 已完成的 restic backup 的处理总量（summary），实时进度在它之上累加。
	// 只在 backup 的命令结束后由引擎主流程修改；执行器返回前输出已全部写完，与进度回调不会同时访问
	committed int64

	// progressMu 保护下面的进度字段：进度由子进程输出的写入方回调，与引擎主流程不在同一个 goroutine
	progressMu sync.Mutex
	// stageMax 当前阶段报告过的最大值。rsync 的传输量和 restic 的处理量不是同一个量，
	// 每次 restic backup 从 committed 重新算起，同一阶段内只增不减
	stageMax int64
	// lastProgressSave 上一次把进度写库的时间
	lastProgressSave time.Time
	// progressDirty 内存里的进度还没写库
	progressDirty bool
}

// progressInterval 实时进度写库的最小间隔，侧边栏每 5 秒轮询一次，不必更频繁
const progressInterval = 3 * time.Second

// progress 报告运行中的实时处理量（rsync 已传输、restic 已处理）。同一阶段内只增不减，写库按 progressInterval 节流；
// 成功后的最终值由 backup 按 restic summary 精确设置。
func (e *execution) progress(live int64) {
	e.progressMu.Lock()
	defer e.progressMu.Unlock()
	if live > e.stageMax {
		e.stageMax = live
		e.run.BytesProcessed = live
		e.progressDirty = true
	}
	if !e.progressDirty {
		return
	}
	if now := e.now(); e.lastProgressSave.IsZero() || now.Sub(e.lastProgressSave) >= progressInterval {
		e.lastProgressSave = now
		e.progressDirty = false
		e.save()
	}
}

func (e *execution) now() time.Time { return e.s.d.Now() }

func (e *execution) jobTag() string { return "job:" + strconv.FormatInt(e.job.ID, 10) }
func (e *execution) runTag() string { return "run:" + strconv.FormatInt(e.run.ID, 10) }
func (e *execution) tags() []string { return []string{e.jobTag(), e.runTag()} }

// save 保存运行记录（含到目前为止的日志）；写库失败只记日志，不影响运行
func (e *execution) save() {
	e.run.Log = e.log.snapshot()
	if err := run_repo.Run().Save(context.Background(), e.run); err != nil {
		e.log.warnf("保存运行记录失败：%v", err)
	}
}

func (e *execution) step(key, status, message string) {
	e.run.SetStep(key, status, e.log.redact(message), e.now())
	e.save()
}

// perform 执行全部步骤并记录、通知
func (e *execution) perform(ctx context.Context) {
	defer e.cleanup()
	e.log.infof("开始运行任务「%s」（触发方式：%s）", e.job.Name, run_entity.TriggerLabel(e.run.Trigger))
	st, err := setting_svc.Get(context.Background())
	if err != nil {
		e.log.warnf("读取设置失败，按默认设置运行：%v", err)
		st = setting_entity.Default()
	}
	e.setting = st

	release, err := e.s.limiter.acquire(ctx, e.s.maxConcurrent, func(n int) {
		e.log.infof("同时运行的任务已达上限 %d 个，等待空闲名额", n)
	})
	if err != nil {
		status, reason := e.failed(ctx, run_entity.StepPreflight, err)
		e.finish(status, reason)
		return
	}
	defer release()

	timeout := time.Duration(e.job.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = job_entity.DefaultTimeoutSec * time.Second
	}
	tctx, cancel := context.WithTimeoutCause(ctx, timeout, errTimeout)
	defer cancel()
	status, reason := e.steps(tctx)
	// 备份已经结束：发通知（可能要重试很久）之前就让出名额，不挡住排队的其他任务
	release()
	e.finish(status, reason)
}

// steps 预检 → 取共享仓库锁 → 导出并写入快照 → 按保留规则清理。返回状态与原因。
func (e *execution) steps(ctx context.Context) (string, string) {
	e.step(run_entity.StepPreflight, run_entity.StepRunning, "")
	if err := e.preflight(ctx); err != nil {
		return e.failed(ctx, run_entity.StepPreflight, err)
	}
	e.step(run_entity.StepPreflight, run_entity.StepSuccess, "")

	// 与存储位置的维护（prune/check）互斥：维护持独占锁时在这里等待
	release, err := repolock.Acquire(ctx, e.job.RepoID, repolock.Shared)
	if err != nil {
		return e.failed(ctx, run_entity.StepExport, err)
	}
	defer release()

	e.step(run_entity.StepExport, run_entity.StepRunning, "")
	warn, err := e.export(ctx)
	if err != nil {
		e.rollback()
		return e.failed(ctx, run_entity.StepExport, err)
	}
	if warn != "" {
		e.warnings = append(e.warnings, warn)
		e.step(run_entity.StepExport, run_entity.StepWarning, warn)
	} else {
		e.step(run_entity.StepExport, run_entity.StepSuccess, "")
	}

	e.step(run_entity.StepForget, run_entity.StepRunning, "")
	if err := e.forget(ctx); err != nil {
		if ctx.Err() != nil {
			// 超时或停止：本次运行失败，不留下快照
			e.rollback()
			return e.failed(ctx, run_entity.StepForget, err)
		}
		msg := "清理旧快照失败：" + err.Error()
		e.log.warnf("%s", msg)
		e.warnings = append(e.warnings, msg)
		e.step(run_entity.StepForget, run_entity.StepWarning, msg)
	} else {
		e.step(run_entity.StepForget, run_entity.StepSuccess, "")
	}

	// 存储位置列表的占用、快照数和剩余空间随备份更新；仍持有共享锁。刷新失败不影响本次结果
	if err := repo_svc.RefreshStats(ctx, e.job.RepoID); err != nil {
		e.log.infof("存储位置统计没有刷新：%s", e.log.redact(err.Error()))
	}

	if len(e.warnings) > 0 {
		return run_entity.StatusWarning, strings.Join(e.warnings, "；")
	}
	return run_entity.StatusSuccess, ""
}

// failed 把步骤记为失败并给出原因：超时或手动停止时原因就是「超时」「手动停止」
func (e *execution) failed(ctx context.Context, step string, err error) (string, string) {
	reason := err.Error()
	if ctx.Err() != nil {
		reason = context.Cause(ctx).Error()
	}
	reason = e.log.redact(reason)
	e.log.errorf("%s", reason)
	e.step(step, run_entity.StepFailed, reason)
	return run_entity.StatusFailed, reason
}

func (e *execution) cleanup() {
	for i := len(e.closers) - 1; i >= 0; i-- {
		e.closers[i]()
	}
	e.closers = nil
}

// exec 执行一条命令：先把它环境变量里的凭据加入遮盖，再记下命令行（参数里没有凭据）
func (e *execution) exec(ctx context.Context, cmd cmdbuild.Cmd, w io.Writer) (int, error) {
	e.log.secret(cmd.Secrets()...)
	e.log.infof("$ %s", cmd.String())
	return e.s.d.Runner.Run(ctx, cmd, w)
}

// needsSSH 文件任务与经 SSH 的数据库任务需要连接服务器
func (e *execution) needsSSH() bool {
	return e.job.SourceType == job_entity.SourceFiles || e.job.ConnMode == job_entity.ConnSSH
}

// preflight 存储位置可访问且已初始化（本地目录还要有挂载标记），服务器连接（含代理链与指纹校验）能建立
func (e *execution) preflight(ctx context.Context) error {
	if err := repo_svc.Preflight(ctx, e.job.RepoID); err != nil {
		return err
	}
	repo, release, err := repo_svc.Env(ctx, e.job.RepoID)
	if err != nil {
		return fmt.Errorf("无法访问存储位置：%w", err)
	}
	e.closers = append(e.closers, release)
	e.log.secret(repo.Password)
	repo.Compression = cmdbuild.Compression(e.job.Compression)
	if e.job.SourceType != job_entity.SourceFiles {
		// 文件任务的限速作用在 rsync 上；数据库任务只能限制写入存储位置的速度
		repo.LimitUploadKiB = e.job.BandwidthKbps
	}
	e.repo = repo

	cmd, err := cmdbuild.ResticSnapshots(repo, []string{e.jobTag()})
	if err != nil {
		return err
	}
	out := newCapBuffer()
	code, err := e.exec(ctx, cmd, out)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("无法访问存储位置：restic 退出码 %d：%s", code, out.tail())
	}
	e.log.infof("存储位置可以访问")

	if !e.needsSSH() {
		return nil
	}
	if err := e.serverSecrets(ctx); err != nil {
		return fmt.Errorf("连接服务器失败：%w", err)
	}
	conn, _, err := sshconn.Dial(ctx, e.job.ServerID)
	if err != nil {
		return fmt.Errorf("连接服务器失败：%w", err)
	}
	e.conn = conn
	e.closers = append(e.closers, func() { _ = conn.Close() })
	e.log.infof("已连接服务器（主机指纹已核对）")
	return nil
}

// serverSecrets 把服务器及代理链上各跳的密码、私钥、口令加入遮盖
func (e *execution) serverSecrets(ctx context.Context) error {
	srv, err := server_repo.Server().Find(ctx, e.job.ServerID)
	if err != nil {
		return err
	}
	if srv == nil {
		return sshconn.ErrServerNotFound
	}
	cfg, err := sshconn.ServerConfig(ctx, srv)
	if err != nil {
		return err
	}
	for _, h := range append(cfg.Hops, cfg.Target) {
		e.log.secret(h.Password, h.PrivateKey, h.Passphrase)
	}
	return nil
}

func (e *execution) export(ctx context.Context) (string, error) {
	if e.job.SourceType == job_entity.SourceFiles {
		return e.exportFiles(ctx)
	}
	return "", e.exportDatabases(ctx)
}

// exportDatabases 每个库一次 restic backup --stdin-from-command：导出数据经管道直接进入 restic，本机不落盘
func (e *execution) exportDatabases(ctx context.Context) error {
	addr := e.job.DBAddr()
	if e.job.ConnMode == job_entity.ConnSSH {
		local, fwd, err := e.conn.Forward(addr)
		if err != nil {
			return fmt.Errorf("经服务器转发到数据库 %s 失败：%w", addr, err)
		}
		e.closers = append(e.closers, func() { _ = fwd.Close() })
		e.log.infof("已建立到数据库 %s 的转发（本机 %s）", addr, local)
		addr = local
	}
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return err
	}
	password, err := decrypt(e.job.DBPassword)
	if err != nil {
		return fmt.Errorf("解密数据库密码失败：%w", err)
	}
	e.log.secret(password)
	names, err := e.databases(ctx, addr)
	if err != nil {
		return err
	}
	for _, name := range names {
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		spec := cmdbuild.DumpSpec{
			Kind:         cmdbuild.DBKind(e.job.SourceType),
			Conn:         cmdbuild.DBConn{Host: host, Port: port, User: e.job.DBUser, Password: password},
			AuthDatabase: e.job.AuthDB, ExtraArgs: e.job.ExtraArgs,
		}
		label := "导出"
		if name != "" {
			spec.Databases = []string{name}
			label = "导出库 " + name
		}
		dump, err := cmdbuild.Dump(spec)
		if err != nil {
			return fmt.Errorf("%s失败：%w", label, err)
		}
		cmd, err := cmdbuild.ResticBackupStdin(e.repo, cmdbuild.StdinBackup{
			Filename: dumpFilename(spec, name), Tags: e.tags(), Command: dump,
		})
		if err != nil {
			return fmt.Errorf("%s失败：%w", label, err)
		}
		e.log.infof("%s", label)
		if err := e.backup(ctx, cmd, label); err != nil {
			return err
		}
	}
	return nil
}

// databases 要导出的库：Redis 整个实例导出；「全部，含以后新建」在运行时列出，去掉系统库
func (e *execution) databases(ctx context.Context, addr string) ([]string, error) {
	if !job_entity.HasDatabaseList(e.job.SourceType) {
		return []string{""}, nil
	}
	if !e.job.AllDatabases {
		return e.job.Databases, nil
	}
	all, err := e.s.d.ListDatabases(ctx, e.job, addr)
	if err != nil {
		return nil, fmt.Errorf("读取库列表失败：%w", err)
	}
	var names []string
	for _, n := range all {
		if !job_entity.IsSystemDatabase(e.job.SourceType, n) {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return nil, errors.New("没有可导出的库")
	}
	e.log.infof("本次导出 %d 个库：%s", len(names), strings.Join(names, "、"))
	return names, nil
}

// dumpFilename 快照里的文件名：MySQL/MariaDB 每个库单独一个快照，文件按库命名
func dumpFilename(spec cmdbuild.DumpSpec, name string) string {
	if name != "" && (spec.Kind == cmdbuild.DBMySQL || spec.Kind == cmdbuild.DBMariaDB) {
		return string(spec.Kind) + "/" + strings.NewReplacer("/", "_", `\`, "_").Replace(name) + ".sql"
	}
	return spec.Filename()
}

// exportFiles rsync 经 opsnap rsh 桥接增量同步到本机镜像目录，再对镜像生成快照
func (e *execution) exportFiles(ctx context.Context) (string, error) {
	mirror := filepath.Join(e.s.d.DataDir, "mirror", strconv.FormatInt(e.job.ID, 10))
	if err := e.prepareMirror(mirror); err != nil {
		return "", err
	}
	remoteErr := e.log.textWriter()
	bridge, err := rsh.Serve(e.conn.Client(), remoteErr)
	if err != nil {
		return "", fmt.Errorf("启动 rsync 转发失败：%w", err)
	}
	e.log.secret(bridge.Token())
	cmd, err := rsh.Rsync(rsh.RsyncSpec{
		Self: e.s.d.Self, Paths: e.job.Paths, Excludes: e.job.Excludes, Dest: mirror,
		BwLimitKiB: e.job.BandwidthKbps, ExtraArgs: e.job.ExtraArgs,
	}, bridge.Env())
	if err != nil {
		_ = bridge.Close()
		return "", err
	}
	out := e.log.rsyncWriter(e.progress)
	code, err := e.exec(ctx, cmd, out)
	out.Flush()
	e.save() // 阶段结束时把最后一次进度落库（节流可能漏掉）
	_ = bridge.Close()
	remoteErr.Flush()
	if err != nil {
		return "", err
	}
	warn := ""
	switch code {
	case 0:
		e.log.infof("同步完成")
	case rsyncVanished:
		warn = "同步期间有文件消失（rsync 退出码 24）"
		e.log.warnf("%s，快照照常生成", warn)
	default:
		return "", fmt.Errorf("rsync 同步失败（退出码 %d）", code)
	}
	backup, err := cmdbuild.ResticBackupDir(e.repo, cmdbuild.DirBackup{Path: mirror, Tags: e.tags()})
	if err != nil {
		return "", err
	}
	if err := e.backup(ctx, backup, "生成快照"); err != nil {
		return "", err
	}
	e.mirror = mirror
	return warn, nil
}

// prepareMirror 准备镜像目录。rsync --relative --delete 只清理本次同步的路径，任务去掉的路径或换掉的服务器
// 留下的内容会一直进入之后的快照，所以来源（服务器和路径）变了就先清空镜像。来源记在镜像目录旁边，不进快照。
func (e *execution) prepareMirror(mirror string) error {
	source, err := json.Marshal(struct {
		ServerID int64    `json:"server_id"`
		Paths    []string `json:"paths"`
	}{e.job.ServerID, e.job.Paths})
	if err != nil {
		return err
	}
	sourceFile := mirror + ".source"
	if prev, err := os.ReadFile(sourceFile); err != nil || !bytes.Equal(prev, source) { // #nosec G304 -- 数据目录下由本程序生成的文件
		if _, statErr := os.Stat(mirror); statErr == nil {
			e.log.infof("任务的来源变了，清空旧的文件镜像 %s", mirror)
		}
		if err := os.RemoveAll(mirror); err != nil {
			return fmt.Errorf("清空旧的文件镜像失败：%w", err)
		}
	}
	if err := os.MkdirAll(mirror, 0o700); err != nil {
		return fmt.Errorf("创建镜像目录失败：%w", err)
	}
	if err := os.WriteFile(sourceFile, source, 0o600); err != nil {
		return fmt.Errorf("记录镜像来源失败：%w", err)
	}
	return nil
}

// backup 执行一次 restic backup。任何非零退出都算失败：restic ≥0.17 在导出命令非零退出时不会生成快照，
// 这里再做一层防御——即使报告了快照 ID 也记下来，回滚时删除。
func (e *execution) backup(ctx context.Context, cmd cmdbuild.Cmd, label string) error {
	res := &resticResult{}
	// 生成快照阶段从已完成的总量重新算起：文件任务的 restic 要读一遍整个镜像，不能停在 rsync 的传输量上
	e.progressMu.Lock()
	e.stageMax = e.committed
	e.progressMu.Unlock()
	w := e.log.resticWriter(res, func(done int64) { e.progress(e.committed + done) })
	code, err := e.exec(ctx, cmd, w)
	w.Flush()
	if res.snapshotID != "" && (err != nil || code != 0) {
		e.orphans = append(e.orphans, res.snapshotID)
	}
	if err != nil {
		return err
	}
	switch {
	case code == resticIncomplete:
		return fmt.Errorf("%s失败：快照不完整，部分文件无法读取（restic 退出码 3）", label)
	case code != 0:
		return fmt.Errorf("%s失败：导出命令或 restic 以退出码 %d 结束", label, code)
	case res.snapshotID == "":
		return fmt.Errorf("%s失败：restic 没有报告快照 ID", label)
	}
	e.run.SnapshotIDs = append(e.run.SnapshotIDs, res.snapshotID)
	e.committed += res.processed
	e.run.BytesProcessed = e.committed // 精确值取代实时进度
	e.run.BytesAdded += res.added
	e.save()
	return nil
}

// forget 按任务的保留规则删除过期快照（只限本任务的标签；空间回收交给每周维护）
func (e *execution) forget(ctx context.Context) error {
	policy, err := e.job.Retention().ForgetArgs()
	if err != nil {
		return err
	}
	cmd, err := cmdbuild.ResticForget(e.repo, cmdbuild.ForgetSpec{Tags: []string{e.jobTag()}, Policy: policy})
	if err != nil {
		return err
	}
	out := newCapBuffer()
	code, err := e.exec(ctx, cmd, out)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("restic 退出码 %d：%s", code, out.tail())
	}
	if n := countRemoved(out.buf.Bytes()); n >= 0 {
		e.log.infof("按保留规则删除了 %d 个过期快照", n)
	}
	return nil
}

// rollback 失败的运行不能留下快照：删除本次已生成或已报告的快照，
// 并按本次运行的标签再查一遍，捕获已写入但没来得及报告 ID 的快照。运行的 ctx 可能已结束，使用独立的时限。
func (e *execution) rollback() {
	if e.repo.Backend == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	ids := append(append([]string{}, e.run.SnapshotIDs...), e.orphans...)
	if cmd, err := cmdbuild.ResticSnapshots(e.repo, []string{e.jobTag(), e.runTag()}); err == nil {
		out := newCapBuffer()
		if code, err := e.exec(ctx, cmd, out); err == nil && code == 0 {
			ids = append(ids, parseSnapshotIDs(out.buf.Bytes())...)
		} else {
			e.log.warnf("查询本次运行留下的快照失败：%s", out.tail())
		}
	}
	e.run.SnapshotIDs = []string{}
	e.mirror = ""
	ids = uniqueIDs(ids)
	if len(ids) == 0 {
		return
	}
	cmd, err := forgetIDs(e.repo, ids)
	if err != nil {
		e.log.errorf("无法删除本次运行留下的快照 %s：%v", strings.Join(ids, " "), err)
		return
	}
	out := newCapBuffer()
	code, err := e.exec(ctx, cmd, out)
	if err != nil || code != 0 {
		e.log.errorf("删除本次运行留下的快照 %s 失败，请在存储位置手动删除：%s", strings.Join(ids, " "), out.tail())
		return
	}
	e.log.warnf("本次运行失败，已删除它留下的快照 %s", strings.Join(ids, " "))
}

// finish 清空镜像（按设置）→ 记录 → 通知；通知失败时在记录里注明
func (e *execution) finish(status, reason string) {
	succeeded := status == run_entity.StatusSuccess || status == run_entity.StatusWarning
	if succeeded && e.mirror != "" && e.setting != nil && e.setting.ClearMirrorAfterBackup {
		if err := os.RemoveAll(e.mirror); err != nil {
			e.log.warnf("清空文件镜像失败：%v", err)
		} else {
			e.log.infof("已按设置清空文件镜像 %s", e.mirror)
		}
	}
	switch status {
	case run_entity.StatusSuccess:
		e.log.infof("运行成功，快照 %s", strings.Join(e.run.SnapshotIDs, " "))
	case run_entity.StatusWarning:
		e.log.warnf("运行完成但有警告：%s", reason)
	}
	e.run.SetStep(run_entity.StepNotify, run_entity.StepRunning, "", e.now())
	e.run.Finish(status, e.log.redact(reason), e.now(), time.Since(e.started))
	e.save()

	if err := e.notify(); err != nil {
		msg := "通知发送失败：" + err.Error()
		e.run.NotifyFailed = true
		e.log.warnf("%s", msg)
		e.run.SetStep(run_entity.StepNotify, run_entity.StepFailed, e.log.redact(msg), e.now())
	} else {
		e.run.SetStep(run_entity.StepNotify, run_entity.StepSuccess, "", e.now())
	}
	e.save()
	e.log.close(e.run.Status, e.run.Reason)
}

// notify 失败发「备份失败」；成功时上一次失败或处于「太久没有成功」告警中则发「恢复正常」，并发「每次成功」。是否真正发送由通知设置决定。
func (e *execution) notify() error {
	ctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
	defer cancel()
	base := notify_svc.Event{
		JobID: e.job.ID, JobName: e.job.Name, RunID: e.run.ID,
		StartedAt: e.started, Duration: time.Duration(e.run.DurationMs) * time.Millisecond,
		Link: fmt.Sprintf("/runs/%d", e.run.ID),
	}
	if e.run.Status == run_entity.StatusFailed {
		ev := base
		ev.Type = notify_entity.EventFailed
		ev.ErrorSummary = e.run.Reason
		// 上次成功时间只是附带信息，查不到也照常发送
		last, err := run_repo.Run().LastSucceeded(ctx, e.job.ID)
		if err != nil {
			e.log.warnf("读取上次成功的时间失败，通知里不带这一项：%v", err)
		}
		if last != nil {
			ev.LastSuccessAt = time.Unix(last.FinishedAt, 0)
		}
		return e.s.d.Notify(ctx, ev)
	}
	var errs []error
	prev, err := run_repo.Run().PreviousFinished(ctx, e.job.ID, e.run.ID)
	if err != nil {
		// 判断不了是否从失败中恢复，「每次成功」仍然照常发送
		e.log.warnf("读取上一次运行失败，无法判断是否要发「恢复正常」：%v", err)
	}
	// 「太久没有成功」的告警状态在成功时清除；上一次失败或曾告警都算从异常中恢复，合起来只发一条
	wasStale, err := alert_repo.Alert().Clear(ctx, e.job.ID)
	if err != nil {
		errs = append(errs, err)
	}
	if wasStale || (prev != nil && prev.Status == run_entity.StatusFailed) {
		ev := base
		ev.Type = notify_entity.EventRecovered
		ev.ErrorSummary = e.run.Reason
		ev.LastSuccessAt = time.Unix(e.run.FinishedAt, 0)
		errs = append(errs, e.s.d.Notify(ctx, ev))
	}
	ev := base
	ev.Type = notify_entity.EventSuccess
	ev.ErrorSummary = e.run.Reason
	ev.LastSuccessAt = time.Unix(e.run.FinishedAt, 0)
	errs = append(errs, e.s.d.Notify(ctx, ev))
	return errors.Join(errs...)
}

// decrypt 解密主密钥加密的值
func decrypt(cipher string) (string, error) {
	if cipher == "" {
		return "", nil
	}
	sec := secret.Default()
	if sec == nil {
		return "", errors.New("主密钥未加载")
	}
	return sec.Decrypt(cipher)
}
