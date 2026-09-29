package kopiarepo

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"

	kfs "github.com/kopia/kopia/fs"
	"github.com/kopia/kopia/fs/virtualfs"
	"github.com/kopia/kopia/repo"
	"github.com/kopia/kopia/repo/compression"
	"github.com/kopia/kopia/repo/manifest"
	"github.com/kopia/kopia/snapshot"
	"github.com/kopia/kopia/snapshot/policy"
	"github.com/kopia/kopia/snapshot/snapshotfs"
	"github.com/kopia/kopia/snapshot/upload"

	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/l10n"
)

// Compression 快照中文件内容的压缩方式
type Compression string

const (
	// CompressionNone 不压缩
	CompressionNone Compression = "none"
	// CompressionGzip gzip
	CompressionGzip Compression = "gzip"
	// CompressionZstd zstd，空值也按 zstd
	CompressionZstd Compression = "zstd"
)

// SnapshotTags 快照标签，用于保留、删除和快照页按任务查找
type SnapshotTags struct {
	JobID int64
	RunID int64
	// Type 备份类型，如 "full"
	Type string
	// Kind 数据源类型，如 "mysql"、"postgresql"
	Kind string
}

// SnapshotFile 快照中的一个文件，内容从 Reader 流式读到 EOF
type SnapshotFile struct {
	Name   string
	Reader io.Reader
}

// SnapshotRequest 一次写快照的内容
type SnapshotRequest struct {
	// Prefix 任务的路径前缀，快照来源为 opsnap@opsnap:/<Prefix>
	Prefix      string
	Tags        SnapshotTags
	Compression Compression
	Files       []SnapshotFile
}

// FileResult 快照中一个文件读回后的大小
type FileResult struct {
	Name string
	Size int64
}

// SnapshotResult 写入并读回校验成功的快照
type SnapshotResult struct {
	// ID kopia 快照清单 ID
	ID    string
	Files []FileResult
	// UploadedBytes 本次新增上传到仓库的字节数（去重、压缩后）
	UploadedBytes int64
}

// tmpWritePrefix 写入会话一次性配置目录的前缀
const tmpWritePrefix = "tmp-write-"

// 快照来源 opsnap@opsnap:/<前缀> 中的主机名与用户名
const (
	sourceHost = "opsnap"
	sourceUser = "opsnap"
)

// ErrInvalidSnapshot 写快照的请求不完整或不合法，未写入任何数据
var ErrInvalidSnapshot error = l10n.Errorf(code.KopiaErrInvalidSnapshot)

// ErrVerify 快照清单已保存但读回校验没有通过，清单随即被删除（删除也失败时错误中一并说明）；调用方据此把失败归入“校验”一步
var ErrVerify error = l10n.Errorf(code.KopiaErrVerify)

func (c Compression) compressor() (compression.Name, error) {
	switch c {
	case CompressionNone:
		return "none", nil
	case CompressionGzip:
		return "gzip", nil
	case CompressionZstd, "":
		return "zstd", nil
	}
	return "", l10n.Errorf(code.KopiaUnsupportedCompression, ErrInvalidSnapshot, string(c))
}

// labels 标签按 kopia 命令行的约定加 "tag:" 前缀，`kopia snapshot list --tags job:1` 可直接筛选
func (t SnapshotTags) labels() map[string]string {
	return map[string]string{
		"tag:job":  strconv.FormatInt(t.JobID, 10),
		"tag:run":  strconv.FormatInt(t.RunID, 10),
		"tag:type": t.Type,
		"tag:kind": t.Kind,
	}
}

func (r SnapshotRequest) validate() error {
	if strings.Trim(r.Prefix, "/") == "" {
		return l10n.Errorf(code.KopiaNoPrefix, ErrInvalidSnapshot)
	}
	if len(r.Files) == 0 {
		return l10n.Errorf(code.KopiaNoFiles, ErrInvalidSnapshot)
	}
	seen := map[string]bool{}
	for _, f := range r.Files {
		if f.Name == "" || f.Name == "." || f.Name == ".." || strings.ContainsAny(f.Name, "/\\") {
			return l10n.Errorf(code.KopiaBadFileName, ErrInvalidSnapshot, f.Name)
		}
		if seen[f.Name] {
			return l10n.Errorf(code.KopiaDupFileName, ErrInvalidSnapshot, f.Name)
		}
		seen[f.Name] = true
		if f.Reader == nil {
			return l10n.Errorf(code.KopiaNoReader, ErrInvalidSnapshot, f.Name)
		}
	}
	return nil
}

// Writer 对一个仓库的可写会话。连接配置（S3 时含明文凭据）放在 <root>/tmp-write-*/ 下，Close 时删除；
// 进程中途退出留下的由 NewManager 清理。同一 Writer 可依次写多份快照，不要并发调用 WriteSnapshot。
type Writer struct {
	rep repo.Repository
	dir string
	m   *Manager
	// key 仓库位置的标识，同一位置的维护依次进行
	key string
}

// OpenWriter 用密钥以可写方式连接并打开仓库（ErrInvalidPassword / ErrNotRepository / *LocationError）。
// 每个会话一个 0700 一次性配置目录，多个会话（即使是同一存储）互不影响；调用方用完必须 Close。
func (m *Manager) OpenWriter(ctx context.Context, loc Location, password string) (*Writer, error) {
	if err := os.MkdirAll(m.root, 0o700); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(m.root, tmpWritePrefix)
	if err != nil {
		return nil, err
	}
	rep, err := connectAndOpen(ctx, filepath.Join(dir, configName), loc, password, false)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	key := loc
	_ = key.Normalize()
	return &Writer{rep: rep, dir: dir, m: m, key: key.Key()}, nil
}

// Close 断开仓库并删除本会话的连接配置
func (w *Writer) Close(ctx context.Context) error {
	err := w.rep.Close(ctx)
	if rerr := os.RemoveAll(w.dir); rerr != nil && err == nil {
		err = rerr
	}
	return err
}

// WriteSnapshot 把 Files 依次（按给定顺序，一个读到 EOF 后才读下一个）流式写成一份快照，
// 来源 opsnap@opsnap:/<Prefix>，带 Tags 标签，文件内容按 Compression 压缩。
// 清单保存后再读回，逐个文件核对大小并确认内容全部在仓库索引中，全部通过才返回结果。
// 读取出错（返回该错误）、ctx 取消（context.Canceled）或读回校验失败时不留下清单；
// 已上传的数据块成为孤立数据，由仓库维护回收。
func (w *Writer) WriteSnapshot(ctx context.Context, req SnapshotRequest) (*SnapshotResult, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}
	comp, err := req.Compression.compressor()
	if err != nil {
		return nil, err
	}
	src := snapshot.SourceInfo{Host: sourceHost, UserName: sourceUser, Path: "/" + strings.Trim(req.Prefix, "/")}

	readers := make([]*streamReader, len(req.Files))
	entries := make([]kfs.Entry, len(req.Files))
	for i, f := range req.Files {
		readers[i] = &streamReader{ctx: ctx, r: f.Reader}
		entries[i] = virtualfs.StreamingFileFromReader(f.Name, io.NopCloser(readers[i]))
	}
	root := virtualfs.NewStaticDirectory("/", entries)

	pol := *policy.DefaultPolicy
	pol.CompressionPolicy = policy.CompressionPolicy{CompressorName: comp}
	tree := policy.BuildTree(map[string]*policy.Policy{".": &pol}, policy.DefaultPolicy)

	var uploaded atomic.Int64
	var id manifest.ID
	err = repo.WriteSession(ctx, w.rep, repo.WriteSessionOptions{
		Purpose:  "opsnap:backup",
		OnUpload: func(n int64) { uploaded.Add(n) },
	}, func(ctx context.Context, rw repo.RepositoryWriter) error {
		u := upload.NewUploader(rw)
		// 按顺序逐个读取文件
		u.ParallelUploads = 1
		u.FailFast = true
		u.DisableIgnoreRules = true
		// 不做中途检查点：检查点会保存未完成的清单并套用 kopia 自己的保留策略
		u.CheckpointInterval = 0
		man, err := u.Upload(ctx, root, tree, src)
		if rerr := firstReadError(readers); rerr != nil {
			return rerr
		}
		if err != nil {
			return l10n.Errorf(code.KopiaWriteSnapshot, err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if man.IncompleteReason != "" {
			return l10n.Errorf(code.KopiaWriteIncomplete, man.IncompleteReason)
		}
		if ds := man.RootEntry.DirSummary; ds != nil && ds.FatalErrorCount > 0 {
			return l10n.Errorf(code.KopiaWriteFatal, ds.FatalErrorCount)
		}
		man.Tags = req.Tags.labels()
		id, err = snapshot.SaveSnapshot(ctx, rw, man)
		if err != nil {
			return l10n.Errorf(code.KopiaSaveManifest, err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	files := make([]FileResult, len(req.Files))
	for i, f := range req.Files {
		files[i] = FileResult{Name: f.Name, Size: readers[i].n}
	}
	if err := w.confirm(ctx, id, files); err != nil {
		return nil, err
	}
	return &SnapshotResult{ID: string(id), Files: files, UploadedBytes: uploaded.Load()}, nil
}

// confirm 从存储重新加载清单并读回校验；失败时删除清单，不让它成为一份快照
func (w *Writer) confirm(ctx context.Context, id manifest.ID, files []FileResult) error {
	err := w.rep.Refresh(ctx)
	if err == nil {
		err = readBack(ctx, w.rep, id, files)
	}
	if err == nil {
		return nil
	}
	// 用不受取消影响的 ctx 删除，取消发生在保存之后也要删掉
	dctx := context.WithoutCancel(ctx)
	if derr := repo.WriteSession(dctx, w.rep, repo.WriteSessionOptions{Purpose: "opsnap:discard"},
		func(ctx context.Context, rw repo.RepositoryWriter) error {
			return rw.DeleteManifest(ctx, id)
		}); derr != nil {
		return l10n.Errorf(code.KopiaVerifyDeleteFailed, ErrVerify, err, derr)
	}
	return l10n.Errorf(code.WrapColon, ErrVerify, err)
}

// readBack 读回快照清单，核对文件集合、每个文件的大小，并确认其内容全部在仓库索引中。测试可替换
var readBack = func(ctx context.Context, rep repo.Repository, id manifest.ID, want []FileResult) error {
	man, err := snapshot.LoadSnapshot(ctx, rep, id)
	if err != nil {
		return err
	}
	root, err := snapshotfs.SnapshotRoot(rep, man)
	if err != nil {
		return err
	}
	dir, ok := root.(kfs.Directory)
	if !ok {
		return l10n.Errorf(code.KopiaRootNotDir)
	}
	entries, err := kfs.GetAllEntries(ctx, dir)
	if err != nil {
		return l10n.Errorf(code.KopiaReadDir, err)
	}
	got := make(map[string]kfs.Entry, len(entries))
	for _, e := range entries {
		got[e.Name()] = e
	}
	if len(got) != len(want) {
		return l10n.Errorf(code.KopiaFileCount, len(got), len(want))
	}
	for _, f := range want {
		e, ok := got[f.Name]
		if !ok {
			return l10n.Errorf(code.KopiaFileMissing, f.Name)
		}
		if e.Size() != f.Size {
			return l10n.Errorf(code.KopiaFileSize, f.Name, e.Size(), f.Size)
		}
		de, ok := e.(snapshot.HasDirEntry)
		if !ok {
			return l10n.Errorf(code.KopiaFileNoObject, f.Name)
		}
		if _, err := rep.VerifyObject(ctx, de.DirEntry().ObjectID); err != nil {
			return l10n.Errorf(code.KopiaFileIncomplete, f.Name, err)
		}
	}
	return nil
}

// streamReader 统计读到的字节数，记住第一个读取错误，并在 ctx 取消后停止读取
type streamReader struct {
	ctx context.Context //nolint:containedctx // kopia 的流式文件只接受 io.Reader，取消只能在读取时检查
	r   io.Reader
	n   int64
	err error
}

func (s *streamReader) Read(p []byte) (int, error) {
	if s.err != nil {
		return 0, s.err
	}
	if err := s.ctx.Err(); err != nil {
		s.err = err
		return 0, err
	}
	n, err := s.r.Read(p)
	s.n += int64(n)
	if err != nil && !errors.Is(err, io.EOF) {
		s.err = err
	}
	return n, err
}

func firstReadError(rs []*streamReader) error {
	for _, r := range rs {
		if r.err != nil {
			return r.err
		}
	}
	return nil
}
