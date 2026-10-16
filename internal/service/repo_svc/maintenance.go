package repo_svc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/cago-frame/cago/pkg/gogo"
	"github.com/cago-frame/cago/pkg/i18n"

	repoapi "github.com/opskat/opsnap/internal/api/repo"
	"github.com/opskat/opsnap/internal/model/entity/repo_entity"
	"github.com/opskat/opsnap/internal/pkg/cmdbuild"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/repolock"
	"github.com/opskat/opsnap/internal/repository/repo_repo"
)

// ErrNotMounted 本地目录缺少 .opsnap-repo，判定 NAS 没有挂载。运行记录的失败原因写「没有挂载」。
var ErrNotMounted = errors.New("没有挂载")

// preflight 本地目录检查标记文件：找不到就把存储位置标为没有挂载并返回包裹 ErrNotMounted 的错误，
// 不向该路径写入任何数据；找到时把之前的「没有挂载」恢复为正常。其他类型直接通过。
func (s *repoSvc) preflight(ctx context.Context, r *repo_entity.Repo) error {
	if r.Type != repo_entity.TypeLocal {
		return nil
	}
	marker := r.MarkerPath()
	if fi, err := os.Stat(marker); err == nil && fi.Mode().IsRegular() {
		if r.Status == repo_entity.StatusNotMounted {
			r.SetStatus(repo_entity.StatusOK, "", s.now())
			_ = repo_repo.Repo().UpdateState(context.WithoutCancel(ctx), r)
		}
		return nil
	}
	reason := fmt.Sprintf("找不到标记文件 %s，NAS 可能没有挂载到这个目录", marker)
	r.SetStatus(repo_entity.StatusNotMounted, ErrNotMounted.Error()+"："+reason, s.now())
	// 状态是附带记录，写入失败不影响判定
	_ = repo_repo.Repo().UpdateState(context.WithoutCancel(ctx), r)
	return fmt.Errorf("%w：%s", ErrNotMounted, reason)
}

// preflightErr 同 preflight，把没有挂载转换成对外的错误码
func (s *repoSvc) preflightErr(ctx context.Context, r *repo_entity.Repo) error {
	err := s.preflight(ctx, r)
	if errors.Is(err, ErrNotMounted) {
		return i18n.NewError(ctx, code.RepoNotMounted, strings.TrimPrefix(err.Error(), ErrNotMounted.Error()+"："))
	}
	return err
}

// statsOutput restic stats --json 的输出
type statsOutput struct {
	TotalSize      int64 `json:"total_size"`
	SnapshotsCount int   `json:"snapshots_count"`
}

func (s *repoSvc) stats(ctx context.Context, r cmdbuild.Repo, mode string) (*statsOutput, result) {
	res := s.exec(ctx)(cmdbuild.ResticStats(r, mode))
	if !res.ok() {
		return nil, res
	}
	out := &statsOutput{}
	if err := json.Unmarshal([]byte(lastLine(res.stdout)), out); err != nil {
		return nil, result{code: -1, err: fmt.Errorf("无法解析 restic stats 的输出：%w", err)}
	}
	return out, res
}

// lastLine 输出的最后一个非空行（restic 可能在 JSON 前打印提示）
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// refresh 刷新统计并记录状态。restic 失败记为状态 error（不是接口错误）；只有数据库出错才返回错误。
func (s *repoSvc) refresh(ctx context.Context, r *repo_entity.Repo) error {
	reason, err := s.collectStats(ctx, r)
	if err != nil {
		return err
	}
	if reason != "" {
		r.SetStatus(repo_entity.StatusError, reason, s.now())
	} else {
		r.SetStatus(repo_entity.StatusOK, "", s.now())
	}
	return repo_repo.Repo().UpdateState(ctx, r)
}

// refreshStats 备份或维护之后刷新统计：成功时写入统计，restic 失败只返回错误，不改动存储位置的状态
func (s *repoSvc) refreshStats(ctx context.Context, r *repo_entity.Repo) error {
	reason, err := s.collectStats(ctx, r)
	if err != nil {
		return err
	}
	if reason != "" {
		return errors.New(reason)
	}
	r.SetStatus(repo_entity.StatusOK, "", s.now())
	return repo_repo.Repo().UpdateState(ctx, r)
}

// collectStats 读取占用、原始数据量、快照数、密钥数和剩余空间写入 r（不落库）。
// restic 失败时返回原因，r 不变；解密等内部错误返回 err。
func (s *repoSvc) collectStats(ctx context.Context, r *repo_entity.Repo) (string, error) {
	key, err := decrypt(r.OpsnapKey)
	if err != nil {
		return "", err
	}
	sess, err := s.open(ctx, r)
	if err != nil {
		return err.Error(), nil
	}
	defer sess.close()
	repo := sess.repo(key)
	stored, res := s.stats(ctx, repo, "raw-data")
	if !res.ok() {
		return res.reason(), nil
	}
	restore, res := s.stats(ctx, repo, "restore-size")
	if !res.ok() {
		return res.reason(), nil
	}
	keys, res := s.listKeys(ctx, repo)
	if !res.ok() {
		return res.reason(), nil
	}
	r.StoredSize, r.RawSize = stored.TotalSize, restore.TotalSize
	r.SnapshotCount, r.KeyCount = restore.SnapshotsCount, len(keys)
	r.FreeSpace = s.freeSpaceOf(ctx, r, sess)
	r.StatsAt = s.now().Unix()
	return "", nil
}

// freeSpaceOf NAS 剩余空间：本地目录取所在文件系统，SFTP/WebDAV 经 rclone about 查询；取不到时为未知
func (s *repoSvc) freeSpaceOf(ctx context.Context, r *repo_entity.Repo, sess *session) int64 {
	switch r.Type {
	case repo_entity.TypeLocal:
		if free, err := freeSpace(r.Params.Path); err == nil {
			return free
		}
	case repo_entity.TypeSFTP, repo_entity.TypeWebDAV:
		res := s.exec(ctx)(cmdbuild.RcloneAbout(sess.backend))
		if !res.ok() {
			return repo_entity.FreeSpaceUnknown
		}
		var about struct {
			Free *int64 `json:"free"`
		}
		// rclone about --json 输出多行 JSON，前面可能有提示
		out := res.stdout
		if i := strings.Index(out, "{"); i >= 0 {
			out = out[i:]
		}
		if err := json.Unmarshal([]byte(out), &about); err == nil && about.Free != nil && *about.Free >= 0 {
			return *about.Free
		}
	}
	return repo_entity.FreeSpaceUnknown
}

// start 取得独占锁后在后台执行维护；拿不到锁立即返回「正在备份或维护」
func (s *repoSvc) start(ctx context.Context, id int64, action string) (*repoapi.Item, error) {
	r, err := s.find(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.preflightErr(ctx, r); err != nil {
		return nil, err
	}
	release, ok := repolock.TryAcquire(id, repolock.Exclusive)
	if !ok {
		return nil, i18n.NewError(ctx, code.RepoBusy)
	}
	s.setRunning(id, action)
	s.wg.Add(1)
	// 维护可能持续很久，不随请求结束而取消
	gogo.Go(func() error {
		defer s.wg.Done()
		defer release()
		defer s.setRunning(id, "")
		err := s.maintain(context.Background(), r, action)
		// 清理空间会改变占用，统计随之刷新；刷新失败不影响维护结果
		_ = s.refreshStats(context.Background(), r)
		return err
	})
	return s.view(ctx, id)
}

// maintain 执行一项维护并记录结果；调用方已持有独占锁
func (s *repoSvc) maintain(ctx context.Context, r *repo_entity.Repo, action string) error {
	res := s.runMaintenance(ctx, r, action)
	msg := strings.TrimSpace(res.out)
	if !res.ok() {
		msg = res.reason()
	}
	now := s.now()
	label := "清理空间"
	if action == repoapi.MaintenanceCheck {
		label = "校验数据"
		r.RecordCheck(res.ok(), msg, now)
	} else {
		r.RecordPrune(res.ok(), msg, now)
	}
	if err := repo_repo.Repo().UpdateMaintenance(context.WithoutCancel(ctx), r); err != nil {
		return err
	}
	if !res.ok() {
		return fmt.Errorf("%s失败：%s", label, res.reason())
	}
	return nil
}

func (s *repoSvc) runMaintenance(ctx context.Context, r *repo_entity.Repo, action string) result {
	key, err := decrypt(r.OpsnapKey)
	if err != nil {
		return result{code: -1, err: err}
	}
	sess, err := s.open(ctx, r)
	if err != nil {
		return result{code: -1, err: err}
	}
	defer sess.close()
	if action == repoapi.MaintenanceCheck {
		return s.exec(ctx)(cmdbuild.ResticCheck(sess.repo(key), CheckSubset))
	}
	return s.exec(ctx)(cmdbuild.ResticPrune(sess.repo(key)))
}
