// Package storage_ctr 暴露存储管理接口。
package storage_ctr

import (
	"context"

	api "github.com/opskat/opsnap/internal/api/storage"
	"github.com/opskat/opsnap/internal/service/storage_svc"
)

type Storage struct{}

func NewStorage() *Storage {
	return &Storage{}
}

// List 存储列表
func (s *Storage) List(ctx context.Context, req *api.ListRequest) (*api.ListResponse, error) {
	return storage_svc.Storage().List(ctx, req)
}

// Probe 测试连接（新建或编辑时）
func (s *Storage) Probe(ctx context.Context, req *api.ProbeRequest) (*api.ProbeResponse, error) {
	return storage_svc.Storage().Probe(ctx, req)
}

// Create 新建存储
func (s *Storage) Create(ctx context.Context, req *api.CreateRequest) (*api.CreateResponse, error) {
	return storage_svc.Storage().Create(ctx, req)
}

// Update 编辑存储
func (s *Storage) Update(ctx context.Context, req *api.UpdateRequest) (*api.UpdateResponse, error) {
	return storage_svc.Storage().Update(ctx, req)
}

// Test 测试已保存存储的连接
func (s *Storage) Test(ctx context.Context, req *api.TestRequest) (*api.TestResponse, error) {
	return storage_svc.Storage().Test(ctx, req)
}

// Unlock 重新解锁
func (s *Storage) Unlock(ctx context.Context, req *api.UnlockRequest) (*api.UnlockResponse, error) {
	return storage_svc.Storage().Unlock(ctx, req)
}

// Delete 删除存储（数据保留）
func (s *Storage) Delete(ctx context.Context, req *api.DeleteRequest) (*api.DeleteResponse, error) {
	return storage_svc.Storage().Delete(ctx, req)
}

// Key 生成密钥或计算指纹
func (s *Storage) Key(ctx context.Context, req *api.KeyRequest) (*api.KeyResponse, error) {
	return storage_svc.Storage().Key(ctx, req)
}
