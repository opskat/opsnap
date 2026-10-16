package snapshot_svc

import (
	"bytes"
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/opskat/opsnap/internal/model/entity/job_entity"
	"github.com/opskat/opsnap/internal/pkg/cmdbuild"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/redact"
	"github.com/opskat/opsnap/internal/pkg/repolock"
	"github.com/opskat/opsnap/internal/repository/job_repo"
	"github.com/opskat/opsnap/internal/service/repo_svc"
)

// restic 退出码 11：无法获取仓库锁
const exitRepoLocked = 11

// stderrLimit 保留的 restic 标准错误长度上限
const stderrLimit = 4 << 10

var snapshotIDPattern = regexp.MustCompile(`^[0-9a-f]{4,64}$`)

// capBuffer 只保留前 stderrLimit 字节的缓冲区，超出部分丢弃但不报错
type capBuffer struct{ buf bytes.Buffer }

func (c *capBuffer) Write(p []byte) (int, error) {
	if room := stderrLimit - c.buf.Len(); room > 0 {
		c.buf.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}

func (c *capBuffer) String() string { return strings.TrimSpace(c.buf.String()) }

// access 一次请求对某个任务所在仓库的访问：共享锁与仓库连接
type access struct {
	job     *job_entity.Job
	repo    cmdbuild.Repo
	release func()
}

func (a *access) close() { a.release() }

// open 取得任务、检查挂载、取得共享锁和仓库连接。拿不到共享锁（正在维护）时直接报错，不排队等待。
func (s *snapshotSvc) open(ctx context.Context, jobID int64) (*access, error) {
	j, err := job_repo.Job().Find(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if j == nil {
		return nil, i18n.NewError(ctx, code.JobNotFound)
	}
	unlock, ok := repolock.TryAcquire(j.RepoID, repolock.Shared)
	if !ok {
		return nil, i18n.NewError(ctx, code.RepoBusy)
	}
	if err := repo_svc.Preflight(ctx, j.RepoID); err != nil {
		unlock()
		if errors.Is(err, repo_svc.ErrNotMounted) {
			return nil, i18n.NewError(ctx, code.RepoNotMounted, strings.TrimPrefix(err.Error(), repo_svc.ErrNotMounted.Error()+"："))
		}
		return nil, err
	}
	repo, releaseEnv, err := repo_svc.Env(ctx, j.RepoID)
	if err != nil {
		unlock()
		return nil, err
	}
	return &access{job: j, repo: repo, release: func() { releaseEnv(); unlock() }}, nil
}

// capture 执行命令并收集标准输出；失败时按标准错误归类
func (s *snapshotSvc) capture(ctx context.Context, cmd cmdbuild.Cmd, buildErr error) ([]byte, error) {
	if buildErr != nil {
		return nil, i18n.NewError(ctx, code.SnapshotReadFailed, buildErr.Error())
	}
	var out bytes.Buffer
	var errOut capBuffer
	exit, err := s.runner.RunSplit(ctx, cmd, &out, &errOut)
	if err != nil || exit != 0 {
		return nil, failure(ctx, cmd, exit, err, errOut.String())
	}
	return out.Bytes(), nil
}

// failure 把 restic 的失败归类成对外错误：被锁、快照不存在，其余带上遮盖凭据后的输出
func failure(ctx context.Context, cmd cmdbuild.Cmd, exit int, err error, stderr string) error {
	if err != nil {
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		stderr = err.Error() + " " + stderr
	}
	low := strings.ToLower(stderr)
	switch {
	case exit == exitRepoLocked || strings.Contains(low, "already locked"):
		return i18n.NewError(ctx, code.SnapshotRepoLocked)
	case strings.Contains(low, "no matching id"):
		return i18n.NewError(ctx, code.SnapshotNotFound)
	}
	msg := redact.New(cmd.Secrets()...).String(strings.TrimSpace(stderr))
	if msg == "" {
		msg = "restic 以退出码 " + strconv.Itoa(exit) + " 结束"
	}
	return i18n.NewError(ctx, code.SnapshotReadFailed, msg)
}
