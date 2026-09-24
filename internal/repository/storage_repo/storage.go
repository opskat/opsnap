// Package storage_repo 读写存储记录。
package storage_repo

import (
	"context"

	"github.com/cago-frame/cago/database/db"

	"github.com/opskat/opsnap/internal/model/entity/storage_entity"
)

//go:generate mockgen -source storage.go -destination mock/storage.go

type StorageRepo interface {
	Create(ctx context.Context, s *storage_entity.Storage) error
	// Save 按 ID 保存全部字段
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
}

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
	return db.Ctx(ctx).Save(s).Error
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
