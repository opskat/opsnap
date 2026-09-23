// Package system_ctr 暴露系统级 HTTP 接口。
package system_ctr

import (
	"context"

	api "github.com/opskat/opsnap/internal/api/system"
	"github.com/opskat/opsnap/internal/service/system_svc"
)

type System struct{}

func NewSystem() *System {
	return &System{}
}

// Health 健康检查
func (c *System) Health(ctx context.Context, req *api.HealthRequest) (*api.HealthResponse, error) {
	return system_svc.System().Health(ctx, req)
}
