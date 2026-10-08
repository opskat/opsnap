// Package storage_repo 读写存储记录。
package storage_repo

import (
	"context"
	"errors"

	"github.com/cago-frame/cago/database/db"

	"github.com/opskat/opsnap/internal/model/entity/storage_entity"
)

//go:generate mockgen -source storage.go -destination mock/storage.go

type StorageRepo interface {
	Create(ctx context.Context, s *storage_entity.Storage) error
	// Save 按 ID 更新全部字段；记录已被删除时返回 ErrNotFound，不会重新插入
	Save(ctx context.Context, s *storage_entity.Storage) error
	// List 按创建时间正序
	List(ctx context.Context) ([]*storage_entity.Storage, error)
	// Find 不存在时返回 nil, nil
	Find(ctx context.Context, id int64) (*storage_entity.Storage, error)
	// FindByName 不存在时返回 nil, nil
	FindByName(ctx context.Context, name string) (*storage_entity.Storage, error)
	// FindByLocationKey 不存在时返回 nil, nil
	FindByLocationKey(ctx context.Context, key string) (*storage_entity.Storage, error)
	Delete(ctx context.Context, id int64) error
	// SetUsage 记录读到的仓库用量（清除读取失败的原因）；存储不存在时什么也不做
	SetUsage(ctx context.Context, id int64, u storage_entity.Usage) error
	// SetUsageError 记录读取用量失败的原因与时间，保留上一次读到的数字；存储不存在时什么也不做
	SetUsageError(ctx context.Context, id int64, reason, reasonEn string, checktime int64) error
}

// usageColumns 仓库用量的列，Save 不改动
var usageColumns = []string{"usage_snapshots", "usage_packed_bytes", "usage_original_bytes", "usage_error", "usage_error_en", "usage_checktime"}

// ErrNotFound 保存时记录已不存在（已被删除）
var ErrNotFound = errors.New("storage not found")

var defaultStorage StorageRepo

func Storage() StorageRepo {
	return defaultStorage
}

func RegisterStorage(i StorageRepo) {
	defaultStorage = i
}

type storageRepo struct{}

func NewStorage() StorageRepo {
	return &storageRepo{}
}

func (r *storageRepo) Create(ctx context.Context, s *storage_entity.Storage) error {
	return db.Ctx(ctx).Create(s).Error
}

func (r *storageRepo) Save(ctx context.Context, s *storage_entity.Storage) error {
	// 不用 gorm 的 Save：它在没有匹配行时会改为插入，把刚删除的存储重新写回。
	// 用量只由 SetUsage / SetUsageError 写入：测试连接等耗时操作保存的是读取时的记录，不能覆盖期间记录的用量
	res := db.Ctx(ctx).Model(s).Select("*").Omit(usageColumns...).Updates(s)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *storageRepo) List(ctx context.Context) ([]*storage_entity.Storage, error) {
	var rows []*storage_entity.Storage
	err := db.Ctx(ctx).Order("createtime ASC, id ASC").Find(&rows).Error
	return rows, err
}

func (r *storageRepo) first(ctx context.Context, query string, arg any) (*storage_entity.Storage, error) {
	var rows []*storage_entity.Storage
	if err := db.Ctx(ctx).Where(query, arg).Limit(1).Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

func (r *storageRepo) Find(ctx context.Context, id int64) (*storage_entity.Storage, error) {
	return r.first(ctx, "id = ?", id)
}

func (r *storageRepo) FindByName(ctx context.Context, name string) (*storage_entity.Storage, error) {
	return r.first(ctx, "name = ?", name)
}

func (r *storageRepo) FindByLocationKey(ctx context.Context, key string) (*storage_entity.Storage, error) {
	return r.first(ctx, "location_key = ?", key)
}

func (r *storageRepo) Delete(ctx context.Context, id int64) error {
	return db.Ctx(ctx).Delete(&storage_entity.Storage{}, id).Error
}

func (r *storageRepo) SetUsage(ctx context.Context, id int64, u storage_entity.Usage) error {
	return db.Ctx(ctx).Model(&storage_entity.Storage{}).Where("id = ?", id).Updates(map[string]any{
		"usage_snapshots":      u.Snapshots,
		"usage_packed_bytes":   u.PackedBytes,
		"usage_original_bytes": u.OriginalBytes,
		"usage_error":          "",
		"usage_error_en":       "",
		"usage_checktime":      u.Checktime,
	}).Error
}

func (r *storageRepo) SetUsageError(ctx context.Context, id int64, reason, reasonEn string, checktime int64) error {
	return db.Ctx(ctx).Model(&storage_entity.Storage{}).Where("id = ?", id).Updates(map[string]any{
		"usage_error":     reason,
		"usage_error_en":  reasonEn,
		"usage_checktime": checktime,
	}).Error
}
