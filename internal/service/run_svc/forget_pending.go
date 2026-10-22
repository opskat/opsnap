package run_svc

import (
	"context"
	"fmt"
	"strconv"

	"github.com/cago-frame/cago/pkg/logger"

	"github.com/opskat/opsnap/internal/model/entity/job_entity"
	"github.com/opskat/opsnap/internal/pkg/cmdbuild"
	"github.com/opskat/opsnap/internal/pkg/redact"
	"github.com/opskat/opsnap/internal/pkg/repolock"
	"github.com/opskat/opsnap/internal/repository/job_repo"
	"github.com/opskat/opsnap/internal/service/repo_svc"
)

// CleanupForgetPending 补做撞锁时延后的旧快照清理，供调度器在每一轮调用。
//
// 只处理当前在本进程内空闲的存储位置：拿到它的独占锁就说明没有备份在写、也没有维护在进行。
// 补做不是一次备份运行，所以既不写运行记录，也不发通知；成功与否都只体现为任务上的待清理标记与一条服务日志。
func (s *runSvc) CleanupForgetPending(ctx context.Context) error {
	jobs, err := job_repo.Job().ListForgetPending(ctx)
	if err != nil {
		return err
	}
	// 按存储位置分组，保持任务 ID 升序；同一位置只试探一次独占锁
	order := make([]int64, 0, len(jobs))
	byRepo := make(map[int64][]*job_entity.Job, len(jobs))
	for _, j := range jobs {
		if _, ok := byRepo[j.RepoID]; !ok {
			order = append(order, j.RepoID)
		}
		byRepo[j.RepoID] = append(byRepo[j.RepoID], j)
	}
	for _, repoID := range order {
		s.cleanupRepo(ctx, repoID, byRepo[repoID])
	}
	return nil
}

// cleanupRepo 补做一个存储位置上的全部待清理任务；拿不到独占锁就整组跳过，等下一轮
func (s *runSvc) cleanupRepo(ctx context.Context, repoID int64, jobs []*job_entity.Job) {
	release, ok := repolock.TryAcquire(repoID, repolock.Exclusive)
	if !ok {
		return
	}
	defer release()
	repo, releaseEnv, err := repo_svc.Env(ctx, repoID)
	if err != nil {
		// 位置没了或密钥坏了：不是这些任务的错，标记留着，等位置恢复
		s.forgetPendingFailed(ctx, jobs, err.Error())
		return
	}
	defer releaseEnv()
	for _, j := range jobs {
		s.cleanupJob(ctx, repo, j)
	}
}

// cleanupJob 用任务当前的保留规则补做一次 forget，只删除带该任务标签的快照
func (s *runSvc) cleanupJob(ctx context.Context, repo cmdbuild.Repo, j *job_entity.Job) {
	policy, err := j.Retention().ForgetArgs()
	if err != nil {
		s.forgetPendingFailed(ctx, []*job_entity.Job{j}, err.Error())
		return
	}
	cmd, err := cmdbuild.ResticForget(repo, cmdbuild.ForgetSpec{
		Tags: []string{"job:" + strconv.FormatInt(j.ID, 10)}, Policy: policy, RetryLock: forgetRetryLock,
	})
	if err != nil {
		s.forgetPendingFailed(ctx, []*job_entity.Job{j}, err.Error())
		return
	}
	// 失败原因要写进任务的标记里，先把凭据加入遮盖
	rd := redact.New(cmd.Secrets()...)
	out := newCapBuffer()
	code, err := s.d.Runner.Run(ctx, cmd, out)
	switch {
	case err != nil:
		s.forgetPendingFailed(ctx, []*job_entity.Job{j}, rd.String(err.Error()))
		return
	case code != 0 && repoLocked(code, out.tail()):
		// 这一轮又被别的进程抢先了：什么都不改，等下一轮
		return
	case code != 0:
		s.forgetPendingFailed(ctx, []*job_entity.Job{j}, rd.String(fmt.Sprintf("restic 退出码 %d：%s", code, out.tail())))
		return
	}

	if err := job_repo.Job().SetForgetPending(ctx, j.ID, 0, ""); err != nil {
		// 标记没清掉：下一轮会再补做一次，forget 是幂等的，不算错
		logger.Ctx(ctx).Sugar().Warnf("清除任务 %d 的待清理标记失败：%v", j.ID, err)
	}
	removed := countRemoved(out.buf.Bytes())
	logger.Ctx(ctx).Sugar().Infof("补做清理旧快照：任务「%s」（#%d）删除了 %d 个过期快照", j.Name, j.ID, max(removed, 0))
}

// forgetPendingFailed 记下补做失败的原因（保留标记），同一原因只记一条日志，避免每轮重复刷屏
func (s *runSvc) forgetPendingFailed(ctx context.Context, jobs []*job_entity.Job, reason string) {
	for _, j := range jobs {
		sameAsBefore := j.ForgetPendingError == reason
		if err := job_repo.Job().SetForgetPending(ctx, j.ID, j.ForgetPendingAt, reason); err != nil {
			logger.Ctx(ctx).Sugar().Warnf("记录任务 %d 的待清理失败原因失败：%v", j.ID, err)
			continue
		}
		if sameAsBefore {
			continue
		}
		logger.Ctx(ctx).Sugar().Errorf("补做清理旧快照失败：任务「%s」（#%d）：%s", j.Name, j.ID, reason)
	}
}

// CleanupForgetPending 补做撞锁时延后的旧快照清理，供调度器使用
func CleanupForgetPending(ctx context.Context) error { return defaultRun.CleanupForgetPending(ctx) }
