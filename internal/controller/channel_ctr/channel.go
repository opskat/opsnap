// Package channel_ctr 暴露网络通道管理接口。
package channel_ctr

import (
	"context"

	api "github.com/opskat/opsnap/internal/api/channel"
	"github.com/opskat/opsnap/internal/service/channel_svc"
)

type Channel struct{}

func NewChannel() *Channel {
	return &Channel{}
}

// List 通道列表
func (c *Channel) List(ctx context.Context, req *api.ListRequest) (*api.ListResponse, error) {
	return channel_svc.Channel().List(ctx, req)
}

// Probe 测试连接（新建或编辑时，不保存）
func (c *Channel) Probe(ctx context.Context, req *api.ProbeRequest) (*api.ProbeResponse, error) {
	return channel_svc.Channel().Probe(ctx, req)
}

// Create 新建通道
func (c *Channel) Create(ctx context.Context, req *api.CreateRequest) (*api.CreateResponse, error) {
	return channel_svc.Channel().Create(ctx, req)
}

// Update 编辑通道
func (c *Channel) Update(ctx context.Context, req *api.UpdateRequest) (*api.UpdateResponse, error) {
	return channel_svc.Channel().Update(ctx, req)
}

// Test 测试已保存通道的连接
func (c *Channel) Test(ctx context.Context, req *api.TestRequest) (*api.TestResponse, error) {
	return channel_svc.Channel().Test(ctx, req)
}

// ConfirmHostKey 重新确认主机密钥
func (c *Channel) ConfirmHostKey(ctx context.Context, req *api.ConfirmHostKeyRequest) (*api.ConfirmHostKeyResponse, error) {
	return channel_svc.Channel().ConfirmHostKey(ctx, req)
}

// Delete 删除通道
func (c *Channel) Delete(ctx context.Context, req *api.DeleteRequest) (*api.DeleteResponse, error) {
	return channel_svc.Channel().Delete(ctx, req)
}
