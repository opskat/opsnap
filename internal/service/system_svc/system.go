// Package system_svc 实现系统级业务逻辑。
package system_svc

import (
	"context"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/cago-frame/cago/configs"

	api "github.com/opskat/opsnap/internal/api/system"
	"github.com/opskat/opsnap/internal/repository/system_repo"
)

// Commit 构建时写入的提交短号（Makefile 的 COMMIT，经 -ldflags -X 注入）；未写入时为空
var Commit string

type SystemSvc interface {
	// Health 汇总版本号、提交短号与元数据库状态
	Health(ctx context.Context, req *api.HealthRequest) (*api.HealthResponse, error)
}

type systemSvc struct{}

var defaultSystem = &systemSvc{}

func System() SystemSvc {
	return defaultSystem
}

// Health 元数据库不可用不视为接口错误：探活方需要拿到明确的状态，而不是 500
func (s *systemSvc) Health(ctx context.Context, req *api.HealthRequest) (*api.HealthResponse, error) {
	status := api.DatabaseOK
	if err := system_repo.System().Ping(ctx); err != nil {
		logger.Ctx(ctx).Error("元数据库不可用", zap.Error(err))
		status = api.DatabaseError
	}
	return &api.HealthResponse{
		Version:  configs.Version,
		Commit:   Commit,
		Database: status,
	}, nil
}
