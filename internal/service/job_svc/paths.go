package job_svc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/i18n"

	jobapi "github.com/opskat/opsnap/internal/api/job"
	"github.com/opskat/opsnap/internal/model/entity/job_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/remotefs"
	"github.com/opskat/opsnap/internal/pkg/sshconn"
)

// pathsTimeout 一次列目录或一组路径校验的时限。
//
// 列举本身没有分页入口，一个几十万条的目录层会把传输时间吃满，
// 所以超时是主要的止损手段，不重试。
const pathsTimeout = 20 * time.Second

// sshOpenFS 经服务器的 SSH 连接（含代理链与指纹校验）开一个 SFTP 会话
func sshOpenFS(ctx context.Context, serverID int64) (Browser, func(), error) {
	conn, _, err := sshconn.Dial(ctx, serverID)
	if err != nil {
		return nil, nil, err
	}
	l, closeSFTP, err := remotefs.NewSFTP(conn.Client())
	if err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	return l, func() {
		closeSFTP()
		_ = conn.Close()
	}, nil
}

// Paths 列出服务器上的一层目录。
//
// 只用于文件任务；请求里夹带的路径不作为「连接字段」参与校验（见 prepareConn 的说明），
// 所以路径框还空着时也能浏览——这正是第一次选目录的场景。
func (s *jobSvc) Paths(ctx context.Context, req *jobapi.PathsRequest) (*jobapi.PathsResponse, error) {
	if !job_entity.IsFiles(req.SourceType) {
		return nil, i18n.NewError(ctx, code.JobSourceNotListed)
	}
	if _, err := remotefs.Normalize(req.Path); err != nil {
		var ie *remotefs.InvalidPathError
		if errors.As(err, &ie) {
			return nil, job_entity.FieldErrors{{Field: "path", Message: ie.Reason}}.Err(ctx)
		}
		return nil, err
	}
	j, err := s.prepareConn(ctx, req.ID, req.Input())
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, pathsTimeout)
	defer cancel()

	fs, closeFn, err := s.openFS(ctx, j.ServerID)
	if err != nil {
		reason, label, msg := describe(err)
		return &jobapi.PathsResponse{OK: false, Reason: reason, ReasonLabel: label, Message: msg}, nil
	}
	defer closeFn()

	// 复用 remotefs 的分类：不存在 / 不是目录 / 没权限 / 超时 / 没开 SFTP
	res, err := remotefs.List(ctx, fs, req.Path)
	if err != nil {
		var re *remotefs.Error
		if errors.As(err, &re) {
			return &jobapi.PathsResponse{
				OK: false, Reason: re.Reason, ReasonLabel: pathReasonLabel(re.Reason), Message: re.Message,
			}, nil
		}
		reason, label, msg := describe(err)
		return &jobapi.PathsResponse{OK: false, Reason: reason, ReasonLabel: label, Message: msg}, nil
	}
	list := make([]jobapi.PathEntryItem, 0, len(res.List))
	for _, it := range res.List {
		list = append(list, jobapi.PathEntryItem{
			Name: it.Name, ModTime: it.ModTime, Symlink: it.Symlink, Selectable: it.Selectable,
		})
	}
	return &jobapi.PathsResponse{OK: true, Path: res.Path, List: list, Truncated: res.Truncated}, nil
}

func pathReasonLabel(reason string) string {
	switch reason {
	case remotefs.ReasonNotFound:
		return "不存在"
	case remotefs.ReasonNotDir:
		return "不是目录"
	case remotefs.ReasonPermission:
		return "没有读权限"
	case remotefs.ReasonSFTPUnavailable:
		return "没有开启 SFTP"
	case remotefs.ReasonTimeout:
		return "超时"
	}
	return "读取失败"
}

// pathFieldError 把一条路径的问题写成字段错误文案
func pathFieldError(path string, err error) string {
	var re *remotefs.Error
	if errors.As(err, &re) {
		return re.Message
	}
	return fmt.Sprintf("%s：%v", path, err)
}

// checkPaths 逐条确认路径存在且是目录，返回字段错误。
//
// 服务器连不上时不产生路径错误：一次网络抖动不该让人改不了任务，运行时还会再检查。
func (s *jobSvc) checkPaths(ctx context.Context, serverID int64, paths []string) job_entity.FieldErrors {
	if len(paths) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, pathsTimeout)
	defer cancel()
	fs, closeFn, err := s.openFS(ctx, serverID)
	if err != nil {
		// 连不上服务器（跳失败、超时、没开 SFTP）都放行保存：
		// 一次网络抖动不该让人改不了任务，运行时还会再检查。
		return nil
	}
	defer closeFn()

	var msgs []string
	for _, p := range paths {
		if strings.TrimSpace(p) == "" {
			continue
		}
		if err := fs.Stat(ctx, p); err != nil {
			var invalid *remotefs.InvalidPathError
			if errors.As(err, &invalid) {
				continue // 形状问题已由 ValidateConfig 报过
			}
			msgs = append(msgs, pathFieldError(p, err))
		}
	}
	if len(msgs) > 0 {
		return job_entity.FieldErrors{{Field: "paths", Message: strings.Join(msgs, "；")}}
	}
	return nil
}

// checkFiles 文件任务的「目录可读」检查项
func (s *jobSvc) checkFiles(ctx context.Context, j *job_entity.Job, sshOK bool, b *checkBuilder) {
	const label = "目录可读"
	if !sshOK {
		b.skip(jobapi.CheckPaths, label)
		return
	}
	paths := make([]string, 0, len(j.Paths))
	for _, p := range j.Paths {
		if strings.TrimSpace(p) != "" {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		b.skip(jobapi.CheckPaths, label)
		return
	}
	ctx, cancel := context.WithTimeout(ctx, pathsTimeout)
	defer cancel()
	fs, closeFn, err := s.openFS(ctx, j.ServerID)
	if err != nil {
		_, _, msg := describe(err)
		b.fail(jobapi.CheckPaths, label, msg, "")
		return
	}
	defer closeFn()

	var bad []string
	for _, p := range paths {
		if err := fs.Stat(ctx, p); err != nil {
			var invalid *remotefs.InvalidPathError
			if errors.As(err, &invalid) {
				continue
			}
			bad = append(bad, pathFieldError(p, err))
		}
	}
	if len(bad) > 0 {
		b.fail(jobapi.CheckPaths, label, strings.Join(bad, "；"), "")
		return
	}
	b.ok(jobapi.CheckPaths, label)
}
