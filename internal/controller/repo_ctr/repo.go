// Package repo_ctr 存储位置接口控制器
package repo_ctr

import (
	"context"

	repoapi "github.com/opskat/opsnap/internal/api/repo"
	"github.com/opskat/opsnap/internal/service/repo_svc"
)

type Repo struct{}

func NewRepo() *Repo { return &Repo{} }

// List 列出存储位置
func (r *Repo) List(ctx context.Context, req *repoapi.ListRequest) (*repoapi.ListResponse, error) {
	return repo_svc.Repo().List(ctx)
}

// Get 读取一个存储位置
func (r *Repo) Get(ctx context.Context, req *repoapi.GetRequest) (*repoapi.GetResponse, error) {
	return repo_svc.Repo().Get(ctx, req.ID)
}

// Create 测试访问后添加存储位置
func (r *Repo) Create(ctx context.Context, req *repoapi.CreateRequest) (*repoapi.CreateResponse, error) {
	return repo_svc.Repo().Create(ctx, req)
}

// Update 修改存储位置
func (r *Repo) Update(ctx context.Context, req *repoapi.UpdateRequest) (*repoapi.UpdateResponse, error) {
	return repo_svc.Repo().Update(ctx, req)
}

// Delete 删除存储位置
func (r *Repo) Delete(ctx context.Context, req *repoapi.DeleteRequest) (*repoapi.DeleteResponse, error) {
	if err := repo_svc.Repo().Delete(ctx, req.ID); err != nil {
		return nil, err
	}
	return &repoapi.DeleteResponse{}, nil
}

// SFTPHostKey 取回 SFTP 主机公钥
func (r *Repo) SFTPHostKey(ctx context.Context, req *repoapi.SFTPHostKeyRequest) (*repoapi.SFTPHostKeyResponse, error) {
	return repo_svc.Repo().SFTPHostKey(ctx, req)
}

// NewRecoveryKey 生成一把恢复密钥的值，供保存前展示
func (r *Repo) NewRecoveryKey(ctx context.Context, req *repoapi.NewRecoveryKeyRequest) (*repoapi.NewRecoveryKeyResponse, error) {
	return repo_svc.Repo().NewRecoveryKey(ctx)
}

// Keys 两把密钥的状态
func (r *Repo) Keys(ctx context.Context, req *repoapi.KeysRequest) (*repoapi.KeysResponse, error) {
	return repo_svc.Repo().Keys(ctx, req.ID)
}

// RotateKey 更换 opsnap 密钥
func (r *Repo) RotateKey(ctx context.Context, req *repoapi.RotateKeyRequest) (*repoapi.RotateKeyResponse, error) {
	return repo_svc.Repo().RotateKey(ctx, req.ID)
}

// GenerateRecoveryKey 生成恢复密钥
func (r *Repo) GenerateRecoveryKey(ctx context.Context, req *repoapi.GenerateRecoveryKeyRequest) (*repoapi.GenerateRecoveryKeyResponse, error) {
	return repo_svc.Repo().GenerateRecoveryKey(ctx, req)
}

// RemoveRecoveryKey 移除恢复密钥
func (r *Repo) RemoveRecoveryKey(ctx context.Context, req *repoapi.RemoveRecoveryKeyRequest) (*repoapi.RemoveRecoveryKeyResponse, error) {
	return repo_svc.Repo().RemoveRecoveryKey(ctx, req.ID)
}

// RecoveryBundle 下载恢复包
func (r *Repo) RecoveryBundle(ctx context.Context, req *repoapi.RecoveryBundleRequest) (*repoapi.RecoveryBundleResponse, error) {
	return repo_svc.Repo().RecoveryBundle(ctx, req.ID)
}

// Recheck 重新检查
func (r *Repo) Recheck(ctx context.Context, req *repoapi.RecheckRequest) (*repoapi.RecheckResponse, error) {
	return repo_svc.Repo().Recheck(ctx, req.ID)
}

// Check 校验数据
func (r *Repo) Check(ctx context.Context, req *repoapi.CheckRequest) (*repoapi.CheckResponse, error) {
	return repo_svc.Repo().StartCheck(ctx, req.ID)
}

// Prune 清理空间
func (r *Repo) Prune(ctx context.Context, req *repoapi.PruneRequest) (*repoapi.PruneResponse, error) {
	return repo_svc.Repo().StartPrune(ctx, req.ID)
}

// Unlock 解除仓库锁
func (r *Repo) Unlock(ctx context.Context, req *repoapi.UnlockRequest) (*repoapi.UnlockResponse, error) {
	if err := repo_svc.Repo().Unlock(ctx, req.ID, req.Confirm); err != nil {
		return nil, err
	}
	return &repoapi.UnlockResponse{}, nil
}
