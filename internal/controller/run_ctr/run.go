// Package run_ctr 运行记录接口控制器
package run_ctr

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	runapi "github.com/opskat/opsnap/internal/api/run"
	"github.com/opskat/opsnap/internal/service/run_svc"
)

type Run struct{}

func NewRun() *Run { return &Run{} }

// RunJob 立即运行一次任务
func (c *Run) RunJob(ctx context.Context, req *runapi.RunJobRequest) (*runapi.RunJobResponse, error) {
	return run_svc.Run().RunJob(ctx, req)
}

// List 运行列表
func (c *Run) List(ctx context.Context, req *runapi.ListRequest) (*runapi.ListResponse, error) {
	return run_svc.Run().List(ctx, req)
}

// Get 运行详情
func (c *Run) Get(ctx context.Context, req *runapi.GetRequest) (*runapi.GetResponse, error) {
	return run_svc.Run().Get(ctx, req.ID)
}

// Stop 停止运行
func (c *Run) Stop(ctx context.Context, req *runapi.StopRequest) (*runapi.StopResponse, error) {
	return run_svc.Run().Stop(ctx, req)
}

// Log 完整日志
func (c *Run) Log(ctx context.Context, req *runapi.LogRequest) (*runapi.LogResponse, error) {
	return run_svc.Run().Log(ctx, req)
}

// StreamLog 实时日志（SSE）。在写出第一个事件前出错（如运行不存在）时按普通错误返回 JSON。
func (c *Run) StreamLog(ctx *gin.Context, req *runapi.StreamLogRequest) error {
	started := false
	w := ctx.Writer
	return run_svc.Run().StreamLog(ctx.Request.Context(), req.ID, req.WarningsOnly, func(event string, data any) error {
		if !started {
			started = true
			h := w.Header()
			h.Set("Content-Type", "text/event-stream")
			h.Set("Cache-Control", "no-cache")
			h.Set("X-Accel-Buffering", "no")
			w.WriteHeader(http.StatusOK)
		}
		if event == run_svc.EventPing {
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return err
			}
		} else {
			b, err := json.Marshal(data)
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b); err != nil {
				return err
			}
		}
		w.Flush()
		return ctx.Request.Context().Err()
	})
}
