// Package server_ctr 服务器接口控制器
package server_ctr

import (
	"context"

	serverapi "github.com/opskat/opsnap/internal/api/server"
	"github.com/opskat/opsnap/internal/service/server_svc"
)

type Server struct{}

func NewServer() *Server { return &Server{} }

// List 列出服务器
func (s *Server) List(ctx context.Context, req *serverapi.ListRequest) (*serverapi.ListResponse, error) {
	return server_svc.Server().List(ctx)
}

// Get 读取一台服务器
func (s *Server) Get(ctx context.Context, req *serverapi.GetRequest) (*serverapi.GetResponse, error) {
	return server_svc.Server().Get(ctx, req.ID)
}

// Create 添加服务器
func (s *Server) Create(ctx context.Context, req *serverapi.CreateRequest) (*serverapi.CreateResponse, error) {
	return server_svc.Server().Create(ctx, req)
}

// Update 修改服务器
func (s *Server) Update(ctx context.Context, req *serverapi.UpdateRequest) (*serverapi.UpdateResponse, error) {
	return server_svc.Server().Update(ctx, req)
}

// Delete 删除服务器
func (s *Server) Delete(ctx context.Context, req *serverapi.DeleteRequest) (*serverapi.DeleteResponse, error) {
	if err := server_svc.Server().Delete(ctx, req.ID); err != nil {
		return nil, err
	}
	return &serverapi.DeleteResponse{}, nil
}

// Test 测试未保存的配置
func (s *Server) Test(ctx context.Context, req *serverapi.TestRequest) (*serverapi.TestResponse, error) {
	return server_svc.Server().Test(ctx, req)
}

// TestSaved 测试已保存的服务器
func (s *Server) TestSaved(ctx context.Context, req *serverapi.TestSavedRequest) (*serverapi.TestResponse, error) {
	return server_svc.Server().TestSaved(ctx, req.ID)
}
