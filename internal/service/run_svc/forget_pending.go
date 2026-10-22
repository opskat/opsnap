package run_svc

import (
	"context"

	"github.com/cago-frame/cago/pkg/logger"

	"github.com/opskat/opsnap/internal/model/entity/job_entity"
	"github.com/opskat/opsnap/internal/pkg/cmdbuild"
	"github.com/opskat/opsnap/internal/pkg/redact"
	"github.com/opskat/opsnap/internal/pkg/repolock"
	"github.com/opskat/opsnap/internal/repository/job_repo"
	"github.com/opskat/opsnap/internal/service/repo_svc"
)

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
		for _, j := range jobs {
			s.forgetPendingFailed(ctx, j, err.Error())
		}
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
		s.forgetPendingFailed(ctx, j, err.Error())
		return
	}
	res, err := runForget(ctx, s.d.Runner.Run, repo, j.ID, policy, false)
	switch {
	case err != nil:
		// 失败原因要写进任务的标记里，先把凭据遮盖掉
		s.forgetPendingFailed(ctx, j, redact.New(repo.Password).String(err.Error()))
		return
	case res.locked:
		// 这一轮又被别的进程抢先了：什么都不改，等下一轮
		return
	}

	if err := job_repo.Job().SetForgetPending(ctx, j.ID, 0, ""); err != nil {
		// 标记没清掉：下一轮会再补做一次，forget 是幂等的，不算错
		logger.Ctx(ctx).Sugar().Warnf("清除任务 %d 的待清理标记失败：%v", j.ID, err)
	}
	logger.Ctx(ctx).Sugar().Infof("补做清理旧快照：任务「%s」（#%d）删除了 %d 个过期快照", j.Name, j.ID, res.removed)
}

// forgetPendingFailed 记下补做失败的原因（保留标记），同一原因只记一条日志，避免每轮重复刷屏
func (s *runSvc) forgetPendingFailed(ctx context.Context, j *job_entity.Job, reason string) {
	sameAsBefore := j.ForgetPendingError == reason
	if err := job_repo.Job().SetForgetPending(ctx, j.ID, j.ForgetPendingAt, reason); err != nil {
		logger.Ctx(ctx).Sugar().Warnf("记录任务 %d 的待清理失败原因失败：%v", j.ID, err)
		return
	}
	if sameAsBefore {
		return
	}
	logger.Ctx(ctx).Sugar().Errorf("补做清理旧快照失败：任务「%s」（#%d）：%s", j.Name, j.ID, reason)
}
