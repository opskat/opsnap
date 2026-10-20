// Package credential_ctr 密钥库（共享的服务器登录凭据）接口控制器
package credential_ctr

import (
	"context"

	credentialapi "github.com/opskat/opsnap/internal/api/credential"
	"github.com/opskat/opsnap/internal/service/credential_svc"
)

type Credential struct{}

func NewCredential() *Credential { return &Credential{} }

// List 列出共享密钥
func (c *Credential) List(ctx context.Context, req *credentialapi.ListRequest) (*credentialapi.ListResponse, error) {
	return credential_svc.Credential().List(ctx)
}

// Get 读取一条共享密钥
func (c *Credential) Get(ctx context.Context, req *credentialapi.GetRequest) (*credentialapi.GetResponse, error) {
	return credential_svc.Credential().Get(ctx, req.ID)
}

// Create 添加共享密钥
func (c *Credential) Create(ctx context.Context, req *credentialapi.CreateRequest) (*credentialapi.CreateResponse, error) {
	return credential_svc.Credential().Create(ctx, req)
}

// Update 保存共享密钥
func (c *Credential) Update(ctx context.Context, req *credentialapi.UpdateRequest) (*credentialapi.UpdateResponse, error) {
	return credential_svc.Credential().Update(ctx, req)
}

// Delete 删除共享密钥
func (c *Credential) Delete(ctx context.Context, req *credentialapi.DeleteRequest) (*credentialapi.DeleteResponse, error) {
	if err := credential_svc.Credential().Delete(ctx, req.ID); err != nil {
		return nil, err
	}
	return &credentialapi.DeleteResponse{}, nil
}
