// Package overview_ctr 概览接口控制器
package overview_ctr

import (
	"context"

	"github.com/opskat/opsnap/internal/api/overview"
	"github.com/opskat/opsnap/internal/service/overview_svc"
)

type Overview struct{}

func NewOverview() *Overview { return &Overview{} }

// Get 概览：状态句计数、总量、需要处理的任务、运行时间线和存储位置卡片
func (c *Overview) Get(ctx context.Context, req *overview.GetRequest) (*overview.GetResponse, error) {
	return overview_svc.Overview().Get(ctx, req)
}
