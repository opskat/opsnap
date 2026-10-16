package snapshot_svc

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"path"
	"strings"

	"github.com/cago-frame/cago/pkg/gogo"
	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/opskat/opsnap/internal/api/snapshot"
	"github.com/opskat/opsnap/internal/model/entity/job_entity"
	"github.com/opskat/opsnap/internal/pkg/cmdbuild"
	"github.com/opskat/opsnap/internal/pkg/code"
)

// gate 在第一次写入时才调用 begin 发送响应头。预检通过后到真正有数据之前，restic 仍可能失败，
// 此时还没有写过任何响应，调用方可以返回普通 JSON 错误。
type gate struct {
	begin   func() io.Writer
	w       io.Writer
	started bool
}

func (g *gate) start() {
	if !g.started {
		g.started = true
		g.w = g.begin()
	}
}

func (g *gate) Write(p []byte) (int, error) {
	g.start()
	return g.w.Write(p)
}

// plan 预检通过后的下载计划；持有共享锁，用完必须 a.close()
type plan struct {
	a     *access
	sn    *resticSnapshot
	paths []string
	files bool
}

// prepare 下载的全部预检：路径参数合法、已挂载且取得共享锁、仓库可读且没有被锁、
// 快照属于这个任务、每个路径都存在（数据库导出必须是文件）。失败时不持有任何锁。
func (s *snapshotSvc) prepare(ctx context.Context, req *snapshot.DownloadRequest) (*plan, error) {
	paths, ok := cleanPaths(req.Path)
	if !ok || len(paths) == 0 {
		return nil, i18n.NewError(ctx, code.SnapshotPathNotFound)
	}
	a, err := s.open(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	p, err := s.verify(ctx, a, req.SID, paths)
	if err != nil {
		a.close()
		return nil, err
	}
	return p, nil
}

func (s *snapshotSvc) verify(ctx context.Context, a *access, sid string, paths []string) (*plan, error) {
	sn, err := s.resolve(ctx, a, sid)
	if err != nil {
		return nil, err
	}
	files := a.job.SourceType == job_entity.SourceFiles
	if !files && len(paths) != 1 {
		return nil, i18n.NewError(ctx, code.SnapshotPathNotFound)
	}
	if files {
		paths = dropNested(paths)
	}
	for _, p := range paths {
		if err := s.checkPath(ctx, a, sn.ID, p, !files); err != nil {
			return nil, err
		}
	}
	return &plan{a: a, sn: sn, paths: paths, files: files}, nil
}

// download 见 SnapshotSvc.Download
func (s *snapshotSvc) download(ctx context.Context, req *snapshot.DownloadRequest, begin func(Meta) io.Writer) (bool, error) {
	p, err := s.prepare(ctx, req)
	if err != nil {
		return false, err
	}
	defer p.a.close()
	if p.files {
		return s.tarGz(ctx, p.a, p.sn, p.paths, begin)
	}
	return s.rawFile(ctx, p.a, p.sn, p.paths[0], req.Gzip, begin)
}

// cleanPaths 规整并去重所选路径
func cleanPaths(in []string) ([]string, bool) {
	seen := map[string]bool{}
	var out []string
	for _, p := range in {
		c, ok := cleanPath(p)
		if !ok {
			return nil, false
		}
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out, true
}

// dropNested 去掉被其他所选目录包含的路径，避免同一个文件在归档里出现两次
func dropNested(paths []string) []string {
	var out []string
	for _, p := range paths {
		nested := false
		for _, q := range paths {
			if q != p && (q == "/" || strings.HasPrefix(p, q+"/")) {
				nested = true
				break
			}
		}
		if !nested {
			out = append(out, p)
		}
	}
	return out
}

// checkPath 用 restic ls 确认路径存在；mustFile 时还要求它是文件
func (s *snapshotSvc) checkPath(ctx context.Context, a *access, id, p string, mustFile bool) error {
	nodes, err := s.ls(ctx, a, id, p)
	if err != nil {
		return err
	}
	for _, n := range nodes {
		if n.Path == p && (!mustFile || n.Type == "file") {
			return nil
		}
	}
	return i18n.NewError(ctx, code.SnapshotPathNotFound)
}

// rawFile 数据库导出：按存储的格式原样输出，gz 为 true 时压缩成 .gz
func (s *snapshotSvc) rawFile(ctx context.Context, a *access, sn *resticSnapshot, p string, gz bool, begin func(Meta) io.Writer) (bool, error) {
	cmd, err := cmdbuild.ResticDump(a.repo, sn.ID, p, "")
	if err != nil {
		return false, i18n.NewError(ctx, code.SnapshotPathNotFound)
	}
	meta := Meta{Filename: path.Base(p), ContentType: "application/octet-stream"}
	if gz {
		meta.Filename += ".gz"
		meta.ContentType = "application/gzip"
	}
	g := &gate{begin: func() io.Writer { return begin(meta) }}
	var sink io.Writer = g
	var zw *gzip.Writer
	if gz {
		zw = gzip.NewWriter(g)
		sink = zw
	}
	var errOut capBuffer
	exit, runErr := s.runner.RunSplit(ctx, cmd, sink, &errOut)
	if runErr != nil || exit != 0 {
		return g.started, failure(ctx, cmd, exit, runErr, errOut.String())
	}
	if zw != nil {
		if err := zw.Close(); err != nil {
			return g.started, err
		}
	}
	// 空文件：restic 没有输出任何字节，这里补发响应头
	g.start()
	return true, nil
}

// tarGz 文件快照：对每个所选路径运行 restic dump --archive tar，把条目转写进同一个 tar.gz 流。
// 数据只在管道和内存缓冲里流动，不落临时文件。
func (s *snapshotSvc) tarGz(ctx context.Context, a *access, sn *resticSnapshot, paths []string, begin func(Meta) io.Writer) (bool, error) {
	meta := Meta{Filename: archiveName(a.job.Name, sn), ContentType: "application/gzip"}
	g := &gate{begin: func() io.Writer { return begin(meta) }}
	zw := gzip.NewWriter(g)
	tw := tar.NewWriter(zw)
	for _, p := range paths {
		if err := s.appendDump(ctx, a, sn.ID, p, tw); err != nil {
			return g.started, err
		}
	}
	// 只有全部路径都完整转写后才写结束标记，中途失败时调用方中止连接，客户端读到的是截断的 gzip
	if err := tw.Close(); err != nil {
		return g.started, err
	}
	if err := zw.Close(); err != nil {
		return g.started, err
	}
	g.start()
	return true, nil
}

// archiveName 下载文件名：<任务名>-<快照短 ID>.tar.gz
func archiveName(jobName string, sn *resticSnapshot) string {
	short := sn.short()
	clean := strings.Map(func(r rune) rune {
		if r < 0x20 || strings.ContainsRune(`/\:*?"<>|`, r) {
			return '_'
		}
		return r
	}, strings.TrimSpace(jobName))
	if clean == "" {
		clean = "snapshot"
	}
	return clean + "-" + short + ".tar.gz"
}

// appendDump 把一个路径的 restic dump --archive tar 输出转写进 tw
func (s *snapshotSvc) appendDump(ctx context.Context, a *access, id, p string, tw *tar.Writer) error {
	cmd, err := cmdbuild.ResticDump(a.repo, id, p, "tar")
	if err != nil {
		return i18n.NewError(ctx, code.SnapshotPathNotFound)
	}
	rctx, cancel := context.WithCancel(ctx)
	defer cancel()
	pr, pw := io.Pipe()
	copied := make(chan error, 1)
	// 读取端不碰请求的 ctx；它失败时通过 cancel 终止 restic，并关闭管道让写入端返回
	gogo.Go(func() error {
		err := copyTar(tar.NewReader(pr), tw)
		if err == nil {
			_, err = io.Copy(io.Discard, pr)
		}
		if err != nil {
			cancel()
			_ = pr.CloseWithError(err)
		}
		copied <- err
		return nil
	})
	var errOut capBuffer
	exit, runErr := s.runner.RunSplit(rctx, cmd, pw, &errOut)
	_ = pw.Close()
	copyErr := <-copied
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	if copyErr != nil && (runErr != nil || exit != 0) {
		// 读取端先失败（归档损坏、写客户端出错）时 restic 是被我们终止的，以读取端的错误为准
		return i18n.NewError(ctx, code.SnapshotReadFailed, copyErr.Error())
	}
	if runErr != nil || exit != 0 {
		return failure(ctx, cmd, exit, runErr, errOut.String())
	}
	if copyErr != nil {
		return i18n.NewError(ctx, code.SnapshotReadFailed, copyErr.Error())
	}
	return nil
}

// copyTar 逐个条目转写，条目名规整成不含 .. 的相对路径（备份内容来自线上服务器，不可信）
func copyTar(tr *tar.Reader, tw *tar.Writer) error {
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := safeName(h.Name)
		if name == "" {
			continue
		}
		if h.Typeflag == tar.TypeDir {
			name += "/"
		}
		h.Name = name
		if h.Typeflag == tar.TypeLink {
			h.Linkname = safeName(h.Linkname)
		}
		h.Format = tar.FormatUnknown
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if _, err := io.Copy(tw, tr); err != nil {
			return err
		}
	}
}

// safeName 去掉前导 / 并消除 ..
func safeName(n string) string {
	return strings.TrimLeft(path.Clean("/"+n), "/")
}
