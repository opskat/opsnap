// Package repo_repo 存储位置的数据访问
package repo_repo

import (
	"context"

	"github.com/cago-frame/cago/database/db"

	"github.com/opskat/opsnap/internal/model/entity/repo_entity"
)

// RepoRepo 存储位置仓储。配置、密钥、状态统计、维护结果分别保存，互不覆盖。
//
//go:generate mockgen -source repo.go -destination mock_repo_repo/repo.go -package mock_repo_repo
type RepoRepo interface {
	// Find 按 ID 查找，不存在返回 nil
	Find(ctx context.Context, id int64) (*repo_entity.Repo, error)
	// List 列出全部存储位置，按 ID 升序
	List(ctx context.Context) ([]*repo_entity.Repo, error)
	Create(ctx context.Context, r *repo_entity.Repo) error
	Delete(ctx context.Context, id int64) error
	// UpdateConfig 只保存名称、地址参数和凭据
	UpdateConfig(ctx context.Context, r *repo_entity.Repo) error
	// UpdateKeys 只保存 opsnap 密钥与恢复密钥的记录和密钥数量
	UpdateKeys(ctx context.Context, r *repo_entity.Repo) error
	// UpdateState 只保存状态与统计
	UpdateState(ctx context.Context, r *repo_entity.Repo) error
	// UpdateMaintenance 只保存校验与清理的结果
	UpdateMaintenance(ctx context.Context, r *repo_entity.Repo) error
}

var defaultRepo RepoRepo

// Repo 取得已注册的仓储
func Repo() RepoRepo { return defaultRepo }

// RegisterRepo 注册仓储实现
func RegisterRepo(i RepoRepo) { defaultRepo = i }

type repoRepo struct{}

// NewRepo 创建基于 GORM 的实现
func NewRepo() RepoRepo { return &repoRepo{} }

func (r *repoRepo) Find(ctx context.Context, id int64) (*repo_entity.Repo, error) {
	ret := &repo_entity.Repo{}
	if err := db.Ctx(ctx).Where("id = ?", id).First(ret).Error; err != nil {
		if db.RecordNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return ret, nil
}

func (r *repoRepo) List(ctx context.Context) ([]*repo_entity.Repo, error) {
	var list []*repo_entity.Repo
	if err := db.Ctx(ctx).Order("id ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (r *repoRepo) Create(ctx context.Context, e *repo_entity.Repo) error {
	return db.Ctx(ctx).Create(e).Error
}

func (r *repoRepo) Delete(ctx context.Context, id int64) error {
	return db.Ctx(ctx).Where("id = ?", id).Delete(&repo_entity.Repo{}).Error
}

func (r *repoRepo) update(ctx context.Context, e *repo_entity.Repo, cols ...string) error {
	return db.Ctx(ctx).Model(&repo_entity.Repo{}).Select(cols).Where("id = ?", e.ID).Updates(e).Error
}

func (r *repoRepo) UpdateConfig(ctx context.Context, e *repo_entity.Repo) error {
	return r.update(ctx, e, "name", "params", "credential", "private_key", "updatetime")
}

func (r *repoRepo) UpdateKeys(ctx context.Context, e *repo_entity.Repo) error {
	return r.update(ctx, e, "opsnap_key", "opsnap_key_id", "opsnap_key_created_at",
		"recovery_key_id", "recovery_key_created_at", "key_count")
}

func (r *repoRepo) UpdateState(ctx context.Context, e *repo_entity.Repo) error {
	return r.update(ctx, e, "status", "status_message", "status_at", "stored_size", "raw_size",
		"snapshot_count", "free_space", "key_count", "stats_at")
}

func (r *repoRepo) UpdateMaintenance(ctx context.Context, e *repo_entity.Repo) error {
	return r.update(ctx, e, "last_check_at", "last_check_ok", "last_check_message",
		"last_prune_at", "last_prune_ok", "last_prune_message")
}
