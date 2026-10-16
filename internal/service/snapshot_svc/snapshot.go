// Package snapshot_svc 快照浏览与下载：列出任务的快照（新增量与保留原因）、浏览文件树、
// 把所选路径流式打包成 .tar.gz、下载数据库导出文件并给出恢复命令示例。
//
// 所有操作在持有存储位置共享锁期间进行；下载在发送响应头之前先做预检（挂载、仓库可访问且没有被锁、
// 快照属于这个任务、路径存在），之后才开始写响应；开始后若数据流中断，返回 started=true 的错误，
// 由控制器中止连接。
package snapshot_svc

import (
	"context"
	"io"
	"time"

	"github.com/opskat/opsnap/internal/api/snapshot"
	"github.com/opskat/opsnap/internal/pkg/executor"
)

// Meta 下载响应的描述，在预检通过后交给 Download 的 begin 回调
type Meta struct {
	// Filename 建议的文件名
	Filename    string
	ContentType string
}

// SnapshotSvc 快照服务
type SnapshotSvc interface {
	// List 任务的全部快照，按时间倒序；附新增量与保留原因
	List(ctx context.Context, jobID int64) (*snapshot.ListResponse, error)
	// Files 快照里某个目录的直接子项
	Files(ctx context.Context, req *snapshot.FilesRequest) (*snapshot.FilesResponse, error)
	// Exports 数据库快照里的导出文件与恢复命令示例
	Exports(ctx context.Context, req *snapshot.ExportsRequest) (*snapshot.ExportsResponse, error)
	// Download 流式下载。begin 在确认能读到数据后调用一次，设置响应头并返回正文写入器。
	// 返回 started=false 的错误时还没有写过响应，调用方按普通错误返回；
	// started=true 的错误表示响应头已发出、数据流中途失败，调用方必须中止连接。
	Download(ctx context.Context, req *snapshot.DownloadRequest, begin func(Meta) io.Writer) (started bool, err error)
	// CheckDownload 只做 Download 的预检，不读取数据
	CheckDownload(ctx context.Context, req *snapshot.DownloadRequest) error
}

// Deps 外部依赖，测试里替换；为空的项使用真实实现
type Deps struct {
	// Runner 执行 restic；下载需要把标准输出和标准错误分开
	Runner executor.Streamer
	Now    func() time.Time
}

type snapshotSvc struct {
	runner executor.Streamer
	now    func() time.Time
}

// NewSnapshot 创建快照服务
func NewSnapshot(d Deps) SnapshotSvc {
	if d.Runner == nil {
		d.Runner = executor.New()
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return &snapshotSvc{runner: d.Runner, now: d.Now}
}

var defaultSnapshot = NewSnapshot(Deps{})

// Snapshot 取得快照服务
func Snapshot() SnapshotSvc { return defaultSnapshot }

// RegisterSnapshot 替换服务实现
func RegisterSnapshot(i SnapshotSvc) { defaultSnapshot = i }

// 导出的方法都放在本文件，其余实现分文件放在同一个包里

func (s *snapshotSvc) List(ctx context.Context, jobID int64) (*snapshot.ListResponse, error) {
	return s.list(ctx, jobID)
}

func (s *snapshotSvc) Files(ctx context.Context, req *snapshot.FilesRequest) (*snapshot.FilesResponse, error) {
	return s.files(ctx, req)
}

func (s *snapshotSvc) Exports(ctx context.Context, req *snapshot.ExportsRequest) (*snapshot.ExportsResponse, error) {
	return s.exports(ctx, req)
}

func (s *snapshotSvc) Download(ctx context.Context, req *snapshot.DownloadRequest, begin func(Meta) io.Writer) (bool, error) {
	return s.download(ctx, req, begin)
}

func (s *snapshotSvc) CheckDownload(ctx context.Context, req *snapshot.DownloadRequest) error {
	p, err := s.prepare(ctx, req)
	if err != nil {
		return err
	}
	p.a.close()
	return nil
}
