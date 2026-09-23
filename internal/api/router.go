// Package api 注册所有 HTTP 路由。
package api

import (
	"context"

	"github.com/cago-frame/cago/server/mux"

	"github.com/opskat/opsnap/internal/controller/system_ctr"
)

// Router 所有业务接口统一挂在 /api/v1 下；其余路径交给前端单页应用（见 internal/web）
func Router(ctx context.Context, root *mux.Router) error {
	r := root.Group("/api/v1")

	system := system_ctr.NewSystem()
	r.Group("/").Bind(system.Health)
	return nil
}
