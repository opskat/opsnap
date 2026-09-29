// Package overview_ctr 暴露概览接口。
package overview_ctr

import (
	"context"

	api "github.com/opskat/opsnap/internal/api/overview"
	"github.com/opskat/opsnap/internal/service/overview_svc"
)

type Overview struct{}

func NewOverview() *Overview {
	return &Overview{}
}

// Get 概览数据
func (c *Overview) Get(ctx context.Context, req *api.GetRequest) (*api.GetResponse, error) {
	return overview_svc.Overview().Get(ctx, req)
}
