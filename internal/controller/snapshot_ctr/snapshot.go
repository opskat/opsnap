// Package snapshot_ctr 快照接口控制器
package snapshot_ctr

import (
	"context"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/opskat/opsnap/internal/api/snapshot"
	"github.com/opskat/opsnap/internal/service/snapshot_svc"
)

type Snapshot struct{}

func NewSnapshot() *Snapshot { return &Snapshot{} }

// List 任务的快照列表
func (c *Snapshot) List(ctx context.Context, req *snapshot.ListRequest) (*snapshot.ListResponse, error) {
	return snapshot_svc.Snapshot().List(ctx, req.ID)
}

// Files 快照里一个目录的直接子项
func (c *Snapshot) Files(ctx context.Context, req *snapshot.FilesRequest) (*snapshot.FilesResponse, error) {
	return snapshot_svc.Snapshot().Files(ctx, req)
}

// Exports 数据库快照的导出文件与恢复命令
func (c *Snapshot) Exports(ctx context.Context, req *snapshot.ExportsRequest) (*snapshot.ExportsResponse, error) {
	return snapshot_svc.Snapshot().Exports(ctx, req)
}

// CheckDownload 下载预检，不读取数据
func (c *Snapshot) CheckDownload(ctx context.Context, req *snapshot.CheckDownloadRequest) (*snapshot.CheckDownloadResponse, error) {
	err := snapshot_svc.Snapshot().CheckDownload(ctx, &snapshot.DownloadRequest{ID: req.ID, SID: req.SID, Path: req.Path, Gzip: req.Gzip})
	if err != nil {
		return nil, err
	}
	return &snapshot.CheckDownloadResponse{}, nil
}

// Download 流式下载。服务在预检通过、确有数据可发之前不会调用 begin，所以失败时还没有写过响应，
// 按普通 JSON 错误返回；响应头发出后失败则中止连接，让客户端看到下载失败而不是一个看似完整的文件。
func (c *Snapshot) Download(ctx *gin.Context, req *snapshot.DownloadRequest) error {
	w := ctx.Writer
	// 连接可能被复用（keep-alive）：下载结束后取消下载期间设置的写超时
	defer func() { _ = http.NewResponseController(w).SetWriteDeadline(time.Time{}) }()
	started, err := snapshot_svc.Snapshot().Download(ctx.Request.Context(), req, func(m snapshot_svc.Meta) io.Writer {
		h := w.Header()
		h.Set("Content-Type", m.ContentType)
		h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": m.Filename}))
		h.Set("Cache-Control", "no-store")
		h.Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		return idleWriter(w, downloadIdleTimeout)
	})
	if err == nil {
		return nil
	}
	if started {
		logger.Ctx(ctx.Request.Context()).Error("快照下载中途失败，已中止连接", zap.Error(err))
		abortConnection(w)
		return nil
	}
	return err
}

// abortConnection 响应已经开始后中止：直接关闭底层连接，不写分块结束标记，客户端会读到意外的 EOF。
// 不能用普通返回或 gin 的恢复中间件，它们会把响应正常收尾；gin 的 Hijack 在写过正文后拒绝执行，所以取底层的 ResponseWriter。
func abortConnection(w http.ResponseWriter) {
	for {
		u, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			break
		}
		w = u.Unwrap()
	}
	if hj, ok := w.(http.Hijacker); ok {
		if conn, _, err := hj.Hijack(); err == nil {
			_ = conn.Close()
			return
		}
	}
	// 无法接管连接（如 HTTP/2）时用 net/http 约定的中止方式
	panic(http.ErrAbortHandler)
}
