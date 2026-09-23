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

type SystemSvc interface {
	// Health 汇总版本号与元数据库状态
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
		Database: status,
	}, nil
}
