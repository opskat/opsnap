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
		return nil, i18n.NewError(ctx, code.JobSourceNotBrowsable)
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
		// 没开 SFTP 是浏览专用的失败，要报 sftp_unavailable 而不是笼统的 failed，
		// 面板才能给出「可以直接在输入框里填完整路径」这句。
		if errors.Is(err, remotefs.ErrSFTPUnsupported) {
			return &jobapi.PathsResponse{
				OK: false, Reason: remotefs.ReasonSFTPUnavailable,
				ReasonLabel: pathReasonLabel(remotefs.ReasonSFTPUnavailable),
				Message:     "这台服务器没有开启 SFTP，不能浏览目录；可以直接在输入框里填完整路径",
			}, nil
		}
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

// pathFieldError 把一条路径的问题写成可直接展示的中文文案
func pathFieldError(path string, err error) string {
	return remotefs.Classify(path, err).Message
}

// unverifiable 没能完成路径校验：放行保存，但要在成功提示里说明没确认过。
//
// 两类都算：
//   - 网络/链路问题（暂时性，运行时还会再检查）；
//   - 服务器没开 SFTP：备份本身走 rsync 不需要 SFTP，不能因此挡住保存。
//
// 「服务器不存在」「凭据解不开」不在此列——那是配置错误，必须报出来。
func unverifiable(err error) bool {
	if errors.Is(err, remotefs.ErrSFTPUnsupported) {
		return true
	}
	return transient(err)
}

// transient 临时性错误：网络抖动、链路超时、请求被取消
func transient(err error) bool {
	var he *sshconn.HopError
	if errors.As(err, &he) {
		switch he.Reason {
		case sshconn.ReasonTimeout, sshconn.ReasonUnreachable, sshconn.ReasonCanceled:
			return true
		}
		return false
	}
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}

// pathCheck 一次路径校验的结果
type pathCheck struct {
	// bad 不可读的路径与原因，逐条写成中文
	bad []string
	// unverified 没能连上服务器，路径根本没有被检查
	unverified bool
}

// statPaths 逐条确认路径存在且是目录。
//
// 一次连接、一条路径一次 Stat，不列目录也不读内容。连接失败时区分两类：
// 网络问题只标记 unverified（放行），永久性错误返回 err 让调用方报出来。
func (s *jobSvc) statPaths(ctx context.Context, serverID int64, paths []string) (pathCheck, error) {
	paths = nonEmpty(paths)
	if len(paths) == 0 {
		return pathCheck{}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, pathsTimeout)
	defer cancel()
	fs, closeFn, err := s.openFS(ctx, serverID)
	if err != nil {
		if unverifiable(err) {
			return pathCheck{unverified: true}, nil
		}
		return pathCheck{}, err
	}
	defer closeFn()

	var out pathCheck
	for _, p := range paths {
		if err := fs.Stat(ctx, p); err != nil {
			var invalid *remotefs.InvalidPathError
			if errors.As(err, &invalid) {
				continue // 形状问题已由 ValidateConfig 报过
			}
			if unverifiable(err) {
				return pathCheck{unverified: true}, nil
			}
			out.bad = append(out.bad, pathFieldError(p, err))
		}
	}
	return out, nil
}

func nonEmpty(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return out
}

// prepareSave 保存前的完整校验：字段、引用资源，以及文件任务的目录可读性。
//
// 返回的错误集合里已经包含了全部字段错误；unverified 是没能确认的路径（连不上服务器），
// 交给调用方在成功提示里说明，不算错误。
func (s *jobSvc) prepareSave(ctx context.Context, old *job_entity.Job, in *jobapi.JobInput) (*job_entity.Job, job_entity.FieldErrors, []string, error) {
	j, errs, err := s.assemble(ctx, old, in)
	if err != nil {
		return nil, nil, nil, err
	}
	errs = append(errs, j.ValidateConfig()...)
	more, err := s.checkResources(ctx, j, errs)
	if err != nil {
		return nil, nil, nil, err
	}
	errs = append(errs, more...)
	// 目录可读性是保存的不变量；测试连接另有独立的检查项，浏览本身还要靠这条路径进去。
	if !job_entity.IsFiles(j.SourceType) || j.ServerID <= 0 || errs.Has("server_id") || errs.Has("paths") {
		return j, errs, nil, nil
	}
	check, err := s.statPaths(ctx, j.ServerID, j.Paths)
	if err != nil {
		return nil, nil, nil, err
	}
	if len(check.bad) > 0 {
		errs = append(errs, job_entity.FieldError{Field: "paths", Message: strings.Join(check.bad, "；")})
	}
	var unverified []string
	if check.unverified {
		unverified = nonEmpty(j.Paths)
	}
	return j, errs, unverified, nil
}

// checkFiles 文件任务的「目录可读」检查项
func (s *jobSvc) checkFiles(ctx context.Context, j *job_entity.Job, sshOK bool, b *checkBuilder) {
	const label = "目录可读"
	if !sshOK {
		b.skip(jobapi.CheckPaths, label)
		return
	}
	paths := nonEmpty(j.Paths)
	if len(paths) == 0 {
		b.note(jobapi.CheckPaths, label, "还没有填路径")
		return
	}
	check, err := s.statPaths(ctx, j.ServerID, paths)
	if err != nil {
		_, _, msg := describe(err)
		b.fail(jobapi.CheckPaths, label, msg, "")
		return
	}
	if check.unverified {
		b.fail(jobapi.CheckPaths, label, "连不上服务器，没能确认这些目录："+strings.Join(paths, "、"), "")
		return
	}
	if len(check.bad) > 0 {
		b.fail(jobapi.CheckPaths, label, strings.Join(check.bad, "；"), "")
		return
	}
	b.ok(jobapi.CheckPaths, label, fmt.Sprintf("%d 条路径都可读", len(paths)))
}
