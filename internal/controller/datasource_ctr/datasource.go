// Package datasource_ctr 暴露数据源管理接口。
package datasource_ctr

import (
	"context"

	api "github.com/opskat/opsnap/internal/api/datasource"
	"github.com/opskat/opsnap/internal/service/datasource_svc"
)

type DataSource struct{}

func NewDataSource() *DataSource {
	return &DataSource{}
}

// List 数据源列表
func (c *DataSource) List(ctx context.Context, req *api.ListRequest) (*api.ListResponse, error) {
	return datasource_svc.DataSource().List(ctx, req)
}

// Get 按 ID 获取数据源
func (c *DataSource) Get(ctx context.Context, req *api.GetRequest) (*api.GetResponse, error) {
	return datasource_svc.DataSource().Get(ctx, req)
}

// Probe 测试连接（新建或编辑时，不保存）
func (c *DataSource) Probe(ctx context.Context, req *api.ProbeRequest) (*api.ProbeResponse, error) {
	return datasource_svc.DataSource().Probe(ctx, req)
}

// Create 新建数据源
func (c *DataSource) Create(ctx context.Context, req *api.CreateRequest) (*api.CreateResponse, error) {
	return datasource_svc.DataSource().Create(ctx, req)
}

// Update 编辑数据源
func (c *DataSource) Update(ctx context.Context, req *api.UpdateRequest) (*api.UpdateResponse, error) {
	return datasource_svc.DataSource().Update(ctx, req)
}

// Test 测试已保存数据源的连接
func (c *DataSource) Test(ctx context.Context, req *api.TestRequest) (*api.TestResponse, error) {
	return datasource_svc.DataSource().Test(ctx, req)
}

// ConfirmHostKey 重新确认服务器文件目标主机的密钥
func (c *DataSource) ConfirmHostKey(ctx context.Context, req *api.ConfirmHostKeyRequest) (*api.ConfirmHostKeyResponse, error) {
	return datasource_svc.DataSource().ConfirmHostKey(ctx, req)
}

// Delete 删除数据源
func (c *DataSource) Delete(ctx context.Context, req *api.DeleteRequest) (*api.DeleteResponse, error) {
	return datasource_svc.DataSource().Delete(ctx, req)
}
