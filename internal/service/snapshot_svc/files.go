package snapshot_svc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/opskat/opsnap/internal/api/snapshot"
	"github.com/opskat/opsnap/internal/model/entity/job_entity"
	"github.com/opskat/opsnap/internal/pkg/cmdbuild"
	"github.com/opskat/opsnap/internal/pkg/code"
)

// lsNode restic ls --json 的一个节点（第一行的快照描述没有 path 与 type，会被跳过）
type lsNode struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Path  string `json:"path"`
	Size  int64  `json:"size"`
	MTime string `json:"mtime"`
}

// parseLs 解析 restic ls --json 的逐行输出
func parseLs(out []byte) ([]lsNode, error) {
	dec := json.NewDecoder(bytes.NewReader(out))
	var nodes []lsNode
	for {
		var n lsNode
		if err := dec.Decode(&n); err != nil {
			if errors.Is(err, io.EOF) {
				return nodes, nil
			}
			return nil, err
		}
		if n.Path != "" && n.Type != "" {
			nodes = append(nodes, n)
		}
	}
}

// cleanPath 规整快照内路径：必须是绝对路径且不含 NUL
func cleanPath(p string) (string, bool) {
	if !strings.HasPrefix(p, "/") || strings.ContainsRune(p, 0) {
		return "", false
	}
	return path.Clean(p), true
}

// ls 列出快照内 dir（空表示整个快照）下的节点
func (s *snapshotSvc) ls(ctx context.Context, a *access, id, dir string) ([]lsNode, error) {
	cmd, err := cmdbuild.ResticLs(a.repo, id, dir)
	out, cerr := s.capture(ctx, cmd, err)
	if cerr != nil {
		return nil, cerr
	}
	nodes, perr := parseLs(out)
	if perr != nil {
		return nil, i18n.NewError(ctx, code.SnapshotReadFailed, "无法解析 restic ls 的输出："+perr.Error())
	}
	return nodes, nil
}

func entryType(t string) string {
	switch t {
	case "dir", "file", "symlink":
		return t
	}
	return "other"
}

func mtimeUnix(s string) int64 {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return 0
	}
	return t.Unix()
}

// files 见 SnapshotSvc.Files。restic ls 随版本不同可能把目录自身或更深层的节点一并列出，
// 这里只保留路径的父目录恰好是 dir 的节点，保证只返回直接子项。
func (s *snapshotSvc) files(ctx context.Context, req *snapshot.FilesRequest) (*snapshot.FilesResponse, error) {
	dir := "/"
	if req.Path != "" {
		d, ok := cleanPath(req.Path)
		if !ok {
			return nil, i18n.NewError(ctx, code.SnapshotPathNotFound)
		}
		dir = d
	}
	a, err := s.open(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	defer a.close()
	sn, err := s.resolve(ctx, a, req.SID)
	if err != nil {
		return nil, err
	}
	nodes, err := s.ls(ctx, a, sn.ID, dir)
	if err != nil {
		return nil, err
	}
	resp := &snapshot.FilesResponse{Path: dir, Entries: []*snapshot.Entry{}}
	for _, n := range nodes {
		if n.Path == dir || path.Dir(n.Path) != dir {
			continue
		}
		resp.Entries = append(resp.Entries, &snapshot.Entry{
			Name: path.Base(n.Path), Path: n.Path, Type: entryType(n.Type), Size: n.Size, MTime: mtimeUnix(n.MTime),
		})
	}
	sort.SliceStable(resp.Entries, func(i, j int) bool {
		di, dj := resp.Entries[i].Type == "dir", resp.Entries[j].Type == "dir"
		if di != dj {
			return di
		}
		return resp.Entries[i].Name < resp.Entries[j].Name
	})
	return resp, nil
}

// exports 见 SnapshotSvc.Exports。数据库快照只有几个导出文件，整个快照列一次即可。
func (s *snapshotSvc) exports(ctx context.Context, req *snapshot.ExportsRequest) (*snapshot.ExportsResponse, error) {
	a, err := s.open(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	defer a.close()
	if !job_entity.IsDatabase(a.job.SourceType) {
		return nil, i18n.NewError(ctx, code.SnapshotNotDatabase)
	}
	sn, err := s.resolve(ctx, a, req.SID)
	if err != nil {
		return nil, err
	}
	nodes, err := s.ls(ctx, a, sn.ID, "")
	if err != nil {
		return nil, err
	}
	resp := &snapshot.ExportsResponse{Files: []*snapshot.Export{}}
	for _, n := range nodes {
		if n.Type != "file" {
			continue
		}
		base := path.Base(n.Path)
		gz := gzipByDefault(a.job.SourceType)
		name := base
		if gz {
			name += ".gz"
		}
		resp.Files = append(resp.Files, &snapshot.Export{
			Path: n.Path, Size: n.Size, Gzip: gz, Filename: name, RestoreCommand: restoreCommand(a.job.SourceType, base, gz),
		})
	}
	return resp, nil
}

// gzipByDefault 文本格式的导出默认压缩后下载；其余格式本身已是二进制归档，原样下载
func gzipByDefault(source string) bool {
	return source == job_entity.SourceMySQL
}
