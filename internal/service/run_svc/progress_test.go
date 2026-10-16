package run_svc

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opsnap/internal/model/entity/run_entity"
	"github.com/opskat/opsnap/internal/pkg/cmdbuild"
	"github.com/opskat/opsnap/internal/pkg/executor"
	"github.com/opskat/opsnap/internal/repository/run_repo"
)

// stepClock 可手动前进的时钟，用来验证进度写库的节流
type stepClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *stepClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *stepClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// probeRunner 在命令执行的间隙读取已落库的 bytes_processed，模拟侧边栏轮询到的值
type probeRunner struct {
	*executor.Fake
	t     *testing.T
	f     *fixture
	clock *stepClock
	runID func() int64
	// atRestic 每次 restic backup 开始前落库的 bytes_processed
	atRestic []int64
	// midRestic restic 每输出一行进度后立刻读到的落库值
	midRestic []int64
	// restic backup 依次输出的行
	resticLines []string
}

func (p *probeRunner) saved() int64 {
	p.t.Helper()
	runs, err := run_repo.Run().Find(p.f.ctx, p.runID())
	require.NoError(p.t, err)
	return runs.BytesProcessed
}

func (p *probeRunner) Run(ctx context.Context, cmd cmdbuild.Cmd, out io.Writer) (int, error) {
	if !executor.Match("restic", "backup")(cmd) {
		p.clock.advance(10 * time.Second)
		return p.Fake.Run(ctx, cmd, out)
	}
	p.atRestic = append(p.atRestic, p.saved())
	for _, line := range p.resticLines {
		p.clock.advance(10 * time.Second)
		_, _ = io.WriteString(out, line+"\n")
		p.midRestic = append(p.midRestic, p.saved())
	}
	return 0, nil
}

func status(done int64) string {
	return fmt.Sprintf(`{"message_type":"status","percent_done":0.5,"bytes_done":%d,"total_bytes":9000}`, done)
}

func TestLiveProgress(t *testing.T) {
	f := setupRunTest(t)
	_, srvID := f.sshServer(t)
	j := f.filesJob(t, srvID)
	clock := &stepClock{t: time.Now()}
	rsyncOut := "receiving file list ... done\n" +
		"          1,000   5%    1.00MB/s    0:00:01\r" +
		"      1,234  45%    9.50MB/s    0:00:10 (xfr#3, to-chk=10/50)\r" +
		"      2,000 100%    9.50MB/s    0:00:20 (xfr#9, to-chk=0/50)\n"
	f.exec.On(executor.Match("rsync"), executor.Step{Output: rsyncOut})
	var runID int64
	p := &probeRunner{
		Fake: f.exec, t: t, f: f, clock: clock, runID: func() int64 { return runID },
		resticLines: []string{status(3000), status(2500), status(8000), summary("f00df00d", 9000, 100)},
	}
	f.svc.d.Runner = p
	f.svc.d.Now = clock.now

	id, err := f.svc.Trigger(f.ctx, j.ID, run_entity.TriggerManual)
	require.NoError(t, err)
	runID = id
	r := f.finish(t, id)
	require.Equal(t, run_entity.StatusSuccess, r.Status, logText(r))

	require.Len(t, p.atRestic, 1)
	assert.Equal(t, int64(2000), p.atRestic[0], "rsync 阶段结束时落库的是 rsync 报告的字节数")
	// 最后一行 summary 要到 backup 结束才落库；生成快照阶段按 restic 的进度算，之后的 2500 比 3000 小，同一阶段内不倒退
	assert.Equal(t, []int64{3000, 3000, 8000, 8000}, p.midRestic[:4])
	assert.Equal(t, int64(9000), r.BytesProcessed, "成功后等于 restic summary 的处理总量")
	assert.NotContains(t, logText(r), "to-chk", "rsync 进度行不进运行日志")
	assert.Contains(t, logText(r), "receiving file list")
}

func TestLiveProgressResticOnly(t *testing.T) {
	f := setupRunTest(t)
	j := f.mysqlJob(t)
	clock := &stepClock{t: time.Now()}
	var runID int64
	p := &probeRunner{
		Fake: f.exec, t: t, f: f, clock: clock, runID: func() int64 { return runID },
		resticLines: []string{status(3000), status(2500), status(5000), summary("f00df00d", 6000, 100)},
	}
	f.svc.d.Runner = p
	f.svc.d.Now = clock.now

	id, err := f.svc.Trigger(f.ctx, j.ID, run_entity.TriggerManual)
	require.NoError(t, err)
	runID = id
	r := f.finish(t, id)
	require.Equal(t, run_entity.StatusSuccess, r.Status, logText(r))
	// 两个库各一次 backup，第二次从第一次的总量往上加
	require.Len(t, p.midRestic, 8)
	assert.Equal(t, []int64{3000, 3000, 5000, 5000}, p.midRestic[:4])
	assert.Equal(t, []int64{9000, 9000, 11000, 11000}, p.midRestic[4:])
	assert.Equal(t, int64(12000), r.BytesProcessed)
	assert.False(t, strings.Contains(logText(r), "bytes_done"))
}

func TestProgressThrottle(t *testing.T) {
	f := setupRunTest(t)
	clock := &stepClock{t: time.Now()}
	f.svc.d.Now = clock.now
	r := &run_entity.Run{JobID: 1, JobName: "x", Status: run_entity.StatusRunning, StartedAt: clock.now().Unix()}
	require.NoError(t, run_repo.Run().Create(f.ctx, r))
	e := &execution{s: f.svc, run: r, log: newLiveLog(clock.now)}
	saved := func() int64 {
		got, err := run_repo.Run().Find(f.ctx, r.ID)
		require.NoError(t, err)
		return got.BytesProcessed
	}
	e.progress(100)
	assert.Equal(t, int64(100), saved(), "第一次立即写库")
	clock.advance(time.Second)
	e.progress(200)
	assert.Equal(t, int64(100), saved(), "间隔内不写库")
	assert.Equal(t, int64(200), r.BytesProcessed, "内存里照常更新")
	clock.advance(progressInterval)
	e.progress(150)
	assert.Equal(t, int64(200), saved(), "到点写下积压的值，且不倒退")
}

// 文件任务首次运行（或开启「备份后清空文件镜像」）时 rsync 传完整份数据，restic 再读一遍整个镜像：
// 生成快照阶段的已处理量要从 restic 自己的进度算起，不能停在 rsync 的总量上，否则这一阶段速度一直是 0
func TestLiveProgressSnapshotStageAfterFullSync(t *testing.T) {
	f := setupRunTest(t)
	_, srvID := f.sshServer(t)
	j := f.filesJob(t, srvID)
	clock := &stepClock{t: time.Now()}
	f.exec.On(executor.Match("rsync"), executor.Step{Output: "      5,000 100%    9.50MB/s    0:00:20 (xfr#9, to-chk=0/50)\n"})
	var runID int64
	p := &probeRunner{
		Fake: f.exec, t: t, f: f, clock: clock, runID: func() int64 { return runID },
		resticLines: []string{status(1000), status(4000), status(3500), status(7000), summary("f00df00d", 7000, 100)},
	}
	f.svc.d.Runner = p
	f.svc.d.Now = clock.now

	id, err := f.svc.Trigger(f.ctx, j.ID, run_entity.TriggerManual)
	require.NoError(t, err)
	runID = id
	r := f.finish(t, id)
	require.Equal(t, run_entity.StatusSuccess, r.Status, logText(r))

	require.Len(t, p.atRestic, 1)
	assert.Equal(t, int64(5000), p.atRestic[0], "同步阶段报告 rsync 的传输量")
	assert.Equal(t, []int64{1000, 4000, 4000, 7000}, p.midRestic[:4], "生成快照阶段按 restic 的进度增长，同一阶段内不倒退")
	assert.Equal(t, int64(7000), r.BytesProcessed)
}

// 附加参数带 -h 时 rsync 把字节数写成 1.23M，本地化环境下千位分隔符可能是句点：这些进度行同样不进日志，
// 能精确读出的字节数照常报告（带单位的只是近似值，不报告）
func TestRsyncWriterNumberFormats(t *testing.T) {
	l := newLiveLog(time.Now)
	var got []int64
	w := l.rsyncWriter(func(n int64) { got = append(got, n) })
	_, _ = io.WriteString(w, "\r          1.23M  12%    1.00MB/s    0:00:05 (xfr#1, to-chk=1/3)"+
		"\r      1.234.567  45%    9,50MB/s    0:00:10 (xfr#3, ir-chk=10/50)"+
		"\r            999 100%    0.00kB/s    0:00:00 (xfr#9, to-chk=0/50)\n")
	w.Flush()
	assert.Equal(t, []int64{1234567, 999}, got)
	assert.Empty(t, l.snapshot(), "进度行不进日志")
}

// 进度行后面紧跟着的其他输出（没有换行隔开）不能随进度行一起丢掉
func TestRsyncWriterKeepsTextAfterProgress(t *testing.T) {
	l := newLiveLog(time.Now)
	var got []int64
	w := l.rsyncWriter(func(n int64) { got = append(got, n) })
	_, _ = io.WriteString(w, "\r      1,234  45%    9.50MB/s    0:00:10 (xfr#3, to-chk=10/50)"+
		"rsync: [sender] send_files failed to open \"/srv/a\": Permission denied (13)\n")
	w.Flush()
	assert.Equal(t, []int64{1234}, got)
	lines := l.snapshot()
	require.Len(t, lines, 1)
	assert.Equal(t, `rsync: [sender] send_files failed to open "/srv/a": Permission denied (13)`, lines[0].Text)
	assert.Equal(t, run_entity.LevelError, lines[0].Level)
}

// rsync 算不出剩余时间时把时间一栏写成 ??:??:??，这样的进度行也要读出字节数，并且不进日志
func TestRsyncWriterUnknownRemaining(t *testing.T) {
	l := newLiveLog(time.Now)
	var got []int64
	w := l.rsyncWriter(func(n int64) { got = append(got, n) })
	_, _ = io.WriteString(w, "\r      1,048,576   3%    0.00kB/s      ??:??:??  \r      2,097,152   6%    1.00MB/s    0:01:30  \n")
	w.Flush()
	assert.Equal(t, []int64{1048576, 2097152}, got)
	for _, line := range l.snapshot() {
		assert.NotContains(t, line.Text, "??:??:??", "进度行不进日志")
	}
}
