package run_svc

import (
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/opskat/opsnap/internal/model/entity/job_entity"
	"github.com/opskat/opsnap/internal/model/entity/repo_entity"
	"github.com/opskat/opsnap/internal/pkg/executor"
	"github.com/opskat/opsnap/internal/pkg/repolock"
	"github.com/opskat/opsnap/internal/pkg/secret"
	"github.com/opskat/opsnap/internal/repository/job_repo"
	"github.com/opskat/opsnap/internal/repository/repo_repo"
)

// logRecorder 收集日志文本，用来断言「同一个失败原因只记一次」
type logRecorder struct {
	mu    sync.Mutex
	lines []string
}

func (r *logRecorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, string(p))
	return len(p), nil
}

func (r *logRecorder) contains(s string) bool { return r.count(s) > 0 }

func (r *logRecorder) count(s string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, l := range r.lines {
		if strings.Contains(l, s) {
			n++
		}
	}
	return n
}

// captureLog 把全局日志换成收集器；补做清理没有运行记录，靠日志观察
func captureLog(t *testing.T) *logRecorder {
	t.Helper()
	rec := &logRecorder{}
	core := zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), zapcore.AddSync(rec), zapcore.InfoLevel)
	old := logger.Default()
	logger.SetLogger(zap.New(core))
	t.Cleanup(func() { logger.SetLogger(old) })
	return rec
}

// otherRepo 再建一个存储位置，用于验证「一个位置忙不影响另一个」
func (f *fixture) otherRepo(t *testing.T) int64 {
	t.Helper()
	key, err := secret.Default().Encrypt(repoKey)
	require.NoError(t, err)
	rp := &repo_entity.Repo{
		Name: "nas-2", Type: repo_entity.TypeLocal, Params: repo_entity.Params{Path: f.mount},
		OpsnapKey: key, Status: repo_entity.StatusOK, FreeSpace: repo_entity.FreeSpaceUnknown,
	}
	require.NoError(t, repo_repo.Repo().Create(f.ctx, rp))
	return rp.ID
}

// pendingJob 保存一个带待清理标记的任务
func (f *fixture) pendingJob(t *testing.T, repoID int64, name string, at int64) *job_entity.Job {
	t.Helper()
	j := f.addJob(t, &job_entity.Job{
		Name: name, SourceType: job_entity.SourceRedis, ConnMode: job_entity.ConnDirect, DBHost: "h", DBPort: 1,
	})
	if repoID != f.repoID {
		require.NoError(t, f.db.Model(&job_entity.Job{}).Where("id = ?", j.ID).Update("repo_id", repoID).Error)
		j.RepoID = repoID
	}
	require.NoError(t, job_repo.Job().SetForgetPending(f.ctx, j.ID, at, ""))
	j.ForgetPendingAt = at
	return j
}

func TestCleanupForgetPending(t *testing.T) {
	t.Run("存储位置被占用时一条 forget 都不执行", func(t *testing.T) {
		f := setupRunTest(t)
		j := f.pendingJob(t, f.repoID, "待清理", 1000)
		busy, ok := repolock.TryAcquire(f.repoID, repolock.Shared)
		require.True(t, ok)
		defer busy()

		require.NoError(t, f.svc.CleanupForgetPending(f.ctx))

		assert.Empty(t, calls(f.exec, "restic", "forget"), "有备份在写时不碰仓库")
		got, err := job_repo.Job().Find(f.ctx, j.ID)
		require.NoError(t, err)
		assert.Equal(t, int64(1000), got.ForgetPendingAt, "标记留着等下一轮")
	})

	t.Run("空闲时按各任务当下的保留规则补做并清标记", func(t *testing.T) {
		f := setupRunTest(t)
		week := f.pendingJob(t, f.repoID, "七天", 1000)
		long := f.pendingJob(t, f.repoID, "长期", 2000)
		require.NoError(t, f.db.Model(&job_entity.Job{}).Where("id = ?", long.ID).
			Update("retention_preset", "long").Error)
		// 已暂停的任务也要补做
		require.NoError(t, job_repo.Job().SetPaused(f.ctx, week.ID, true, 1))

		require.NoError(t, f.svc.CleanupForgetPending(f.ctx))

		forgets := calls(f.exec, "restic", "forget")
		require.Len(t, forgets, 2)
		// 按任务 ID 升序，各用自己的标签与保留参数
		want7d, err := week.Retention().ForgetArgs()
		require.NoError(t, err)
		argOf := func(i int) string { return strings.Join(forgets[i].Args, " ") }
		assert.Contains(t, argOf(0), "job:"+strconv.FormatInt(week.ID, 10))
		assert.Contains(t, argOf(0), strings.Join(want7d, " "))
		assert.Contains(t, argOf(1), "job:"+strconv.FormatInt(long.ID, 10))
		assert.Contains(t, argOf(1), "--keep-daily 7")

		for _, j := range []*job_entity.Job{week, long} {
			got, ferr := job_repo.Job().Find(f.ctx, j.ID)
			require.NoError(t, ferr)
			assert.Zero(t, got.ForgetPendingAt, "补做成功后清标记")
			assert.Empty(t, got.ForgetPendingError)
		}
		pending, err := job_repo.Job().ListForgetPending(f.ctx)
		require.NoError(t, err)
		assert.Empty(t, pending)
	})

	t.Run("一个存储位置忙不影响另一个空闲的", func(t *testing.T) {
		f := setupRunTest(t)
		free := f.pendingJob(t, f.repoID, "空闲位置上的任务", 1000)
		busyRepo := f.otherRepo(t)
		f.pendingJob(t, busyRepo, "忙位置上的任务", 2000)
		busy, ok := repolock.TryAcquire(busyRepo, repolock.Shared)
		require.True(t, ok)
		defer busy()

		require.NoError(t, f.svc.CleanupForgetPending(f.ctx))

		forgets := calls(f.exec, "restic", "forget")
		require.Len(t, forgets, 1)
		assert.Contains(t, strings.Join(forgets[0].Args, " "), "job:"+strconv.FormatInt(free.ID, 10))
	})

	t.Run("补做时又撞锁：标记留着，不算失败", func(t *testing.T) {
		f := setupRunTest(t)
		j := f.pendingJob(t, f.repoID, "待清理", 1000)
		f.exec.On(executor.Match("restic", "forget"), executor.Step{ExitCode: 11, Output: lockedOutput})

		rec := captureLog(t)
		require.NoError(t, f.svc.CleanupForgetPending(f.ctx))

		got, err := job_repo.Job().Find(f.ctx, j.ID)
		require.NoError(t, err)
		assert.Equal(t, int64(1000), got.ForgetPendingAt)
		assert.Empty(t, got.ForgetPendingError, "撞锁不是失败，不记原因")
		assert.False(t, rec.contains("补做清理旧快照失败"))
	})

	t.Run("被锁以外的失败：保留标记与原因，同一原因只记一次日志", func(t *testing.T) {
		f := setupRunTest(t)
		j := f.pendingJob(t, f.repoID, "待清理", 1000)
		// 同一个失败原因两次，然后换一个原因
		f.exec.On(executor.Match("restic", "forget"),
			executor.Step{ExitCode: 1, Output: `{"message_type":"exit_error","code":1,"message":"wrong password"}`},
			executor.Step{ExitCode: 1, Output: `{"message_type":"exit_error","code":1,"message":"wrong password"}`},
			executor.Step{ExitCode: 1, Output: `{"message_type":"exit_error","code":1,"message":"no such host"}`})

		rec := captureLog(t)
		require.NoError(t, f.svc.CleanupForgetPending(f.ctx))
		got, err := job_repo.Job().Find(f.ctx, j.ID)
		require.NoError(t, err)
		assert.Equal(t, int64(1000), got.ForgetPendingAt, "还欠着这次清理")
		assert.Contains(t, got.ForgetPendingError, "wrong password")
		assert.Equal(t, 1, rec.count("补做清理旧快照失败"), "第一次失败记一条")

		// 同一个原因再来一次：不重复刷日志
		require.NoError(t, f.svc.CleanupForgetPending(f.ctx))
		assert.Equal(t, 1, rec.count("补做清理旧快照失败"))

		// 换了个原因：再记一条
		require.NoError(t, f.svc.CleanupForgetPending(f.ctx))
		assert.Equal(t, 2, rec.count("补做清理旧快照失败"))
	})

	t.Run("成功时写一条日志，说明删了几个过期快照", func(t *testing.T) {
		f := setupRunTest(t)
		f.pendingJob(t, f.repoID, "待清理", 1000)
		f.exec.On(executor.Match("restic", "forget"), executor.Step{Output: `[{"remove":[{"id":"aaaa"},{"id":"bbbb"}]}]`})

		rec := captureLog(t)
		require.NoError(t, f.svc.CleanupForgetPending(f.ctx))
		assert.True(t, rec.contains("删除了 2 个过期快照"), strings.Join(rec.lines, ""))
	})

	t.Run("存储位置读不出来：记一条日志，其他任务照做，标记留着", func(t *testing.T) {
		f := setupRunTest(t)
		gone := f.pendingJob(t, f.repoID, "位置没了", 1000)
		fine := f.pendingJob(t, f.repoID, "正常", 2000)
		// 让第一个任务指向一个不存在的存储位置
		require.NoError(t, f.db.Model(&job_entity.Job{}).Where("id = ?", gone.ID).Update("repo_id", 9999).Error)

		rec := captureLog(t)
		require.NoError(t, f.svc.CleanupForgetPending(f.ctx))

		forgets := calls(f.exec, "restic", "forget")
		require.Len(t, forgets, 1)
		assert.Contains(t, strings.Join(forgets[0].Args, " "), "job:"+strconv.FormatInt(fine.ID, 10))
		assert.True(t, rec.contains("补做清理旧快照失败"))
		got, err := job_repo.Job().Find(f.ctx, gone.ID)
		require.NoError(t, err)
		assert.Equal(t, int64(1000), got.ForgetPendingAt, "位置没了不算这个任务的错，标记留着")
	})
}
