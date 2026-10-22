package run_svc

import (
	"context"
	"fmt"
	"io"
	"strconv"

	"github.com/opskat/opsnap/internal/pkg/cmdbuild"
	"github.com/opskat/opsnap/internal/pkg/resticrepo"
)

// jobTag 快照上的任务标签，只带它的快照会被这个任务的保留规则删除
func jobTag(jobID int64) string { return "job:" + strconv.FormatInt(jobID, 10) }

// forgetResult 一次 forget 的结果
type forgetResult struct {
	// removed 删除了几个过期快照
	removed int
	// locked 仓库被其他进程锁住：不是失败，而是这次没做成，稍后补做
	locked bool
	// lockOutput restic 在撞锁时报告的原始输出，只用于写日志排查
	lockOutput string
}

// forgetFailed forget 因为撞锁以外的原因失败
func forgetFailed(code int, output string) error {
	return fmt.Errorf("restic 退出码 %d：%s", code, output)
}

// runForget 按保留规则执行一次 forget，只针对该任务的快照。
//
// run 是执行子进程的方式：正常运行用 execution.exec（命令与凭据会被遮盖后记进日志），补做直接用执行器。
// retry 为真时撞锁先自己等一会儿（正常运行）；补做已持有存储位置的独占锁，本进程内不会有别的备份在写，
// 不必再等，也不该占着位置让新备份排队。
func runForget(
	ctx context.Context,
	run func(context.Context, cmdbuild.Cmd, io.Writer) (int, error),
	repo cmdbuild.Repo,
	jobID int64,
	policy []string,
	retry bool,
) (forgetResult, error) {
	spec := cmdbuild.ForgetSpec{Tags: []string{jobTag(jobID)}, Policy: policy}
	if retry {
		spec.RetryLock = forgetRetryLock
	}
	cmd, err := cmdbuild.ResticForget(repo, spec)
	if err != nil {
		return forgetResult{}, err
	}
	out := newCapBuffer()
	code, err := run(ctx, cmd, out)
	if err != nil {
		return forgetResult{}, err
	}
	if code != 0 {
		if resticrepo.Locked(code, out.tail()) {
			return forgetResult{locked: true, lockOutput: out.tail()}, nil
		}
		return forgetResult{}, forgetFailed(code, out.tail())
	}
	return forgetResult{removed: max(countRemoved(out.buf.Bytes()), 0)}, nil
}
