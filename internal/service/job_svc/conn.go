package job_svc

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/i18n"
	jobapi "github.com/opskat/opsnap/internal/api/job"
	"github.com/opskat/opsnap/internal/model/entity/job_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/sshconn"
)

// probeTimeout 一次读取库列表或测试连接的总时限
const probeTimeout = 45 * time.Second

// connectionFields 测试连接和读取库列表只关心的字段；其余字段（名称、计划、保留……）允许还没填好。
//
// paths 不在这里：它决定的是「备份什么」，不是「能不能连上」。留在里面会让路径框还空着时
// 测试连接直接报「请至少填写一个要备份的路径」，也就永远跑不到目录可读那一项。
var connectionFields = map[string]bool{
	"source_type": true, "server_id": true, "conn_mode": true, "db_host": true, "db_port": true,
	"db_user": true, "db_password": true, "auth_db": true,
}

// prepareConn 整理未保存的配置并只校验连接相关字段。id 非 0 时表示编辑中的已保存任务，占位密码取它的值。
func (s *jobSvc) prepareConn(ctx context.Context, id int64, in *jobapi.JobInput) (*job_entity.Job, error) {
	var old *job_entity.Job
	if id != 0 {
		var err error
		if old, err = s.find(ctx, id); err != nil {
			return nil, err
		}
	}
	j, errs, err := s.assemble(ctx, old, in)
	if err != nil {
		return nil, err
	}
	for _, fe := range j.ValidateConfig() {
		if connectionFields[fe.Field] {
			errs = append(errs, fe)
		}
	}
	more, err := s.checkServer(ctx, j, errs)
	if err != nil {
		return nil, err
	}
	if err := append(errs, more...).Err(ctx); err != nil {
		return nil, err
	}
	return j, nil
}

func (s *jobSvc) checkServer(ctx context.Context, j *job_entity.Job, prior job_entity.FieldErrors) (job_entity.FieldErrors, error) {
	repoless := *j
	repoless.RepoID = 0
	return s.checkResources(ctx, &repoless, prior)
}

// connect 返回 opsnap 本机可连的数据库地址：直连是原地址，经 SSH 是本地转发端口
func (s *jobSvc) connect(ctx context.Context, j *job_entity.Job) (string, func(), error) {
	if j.ConnMode != job_entity.ConnSSH {
		return j.DBAddr(), func() {}, nil
	}
	return s.tunnel(ctx, j.ServerID, j.DBAddr())
}

func (s *jobSvc) target(j *job_entity.Job, addr string) (Target, error) {
	plain, err := plainPassword(j.DBPassword)
	if err != nil {
		return Target{}, err
	}
	return Target{Type: j.SourceType, Addr: addr, User: j.DBUser, Password: plain, AuthDB: j.AuthDB}, nil
}

// describe 把连接过程的错误整理成面板展示用的原因、标签和消息
func describe(err error) (reason, label, message string) {
	var he *sshconn.HopError
	var pe *ProbeError
	switch {
	case errors.As(err, &he):
		return string(he.Reason), he.Reason.Label(), he.Error()
	case errors.As(err, &pe):
		return pe.Reason, probeLabel(pe.Reason), pe.Message
	}
	return ReasonFailed, probeLabel(ReasonFailed), err.Error()
}

func probeLabel(reason string) string {
	switch reason {
	case ReasonUnreachable:
		return "连不上"
	case ReasonAuthFailed:
		return "认证失败"
	case ReasonTimeout:
		return "超时"
	}
	return "失败"
}

func (s *jobSvc) listDatabases(ctx context.Context, req *jobapi.DatabasesRequest) (*jobapi.DatabasesResponse, error) {
	if !job_entity.HasDatabaseList(req.SourceType) {
		return nil, i18n.NewError(ctx, code.JobSourceNotListed)
	}
	j, err := s.prepareConn(ctx, req.ID, req.Input())
	if err != nil {
		return nil, err
	}
	probe, err := s.probes(j.SourceType)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	fail := func(err error) *jobapi.DatabasesResponse {
		reason, label, msg := describe(err)
		return &jobapi.DatabasesResponse{List: []*jobapi.Database{}, Reason: reason, ReasonLabel: label, Message: msg}
	}
	addr, closeFn, err := s.connect(ctx, j)
	if err != nil {
		return fail(err), nil
	}
	defer closeFn()
	tgt, err := s.target(j, addr)
	if err != nil {
		return nil, err
	}
	dbs, err := probe.ListDatabases(ctx, tgt)
	if err != nil {
		return fail(asProbeError(ctx, err)), nil
	}
	sort.Slice(dbs, func(a, b int) bool { return dbs[a].Name < dbs[b].Name })
	resp := &jobapi.DatabasesResponse{OK: true, List: make([]*jobapi.Database, 0, len(dbs))}
	for _, d := range dbs {
		sys := job_entity.IsSystemDatabase(j.SourceType, d.Name)
		resp.List = append(resp.List, &jobapi.Database{Name: d.Name, SizeBytes: d.SizeBytes, System: sys, Selectable: !sys})
	}
	return resp, nil
}

func (s *jobSvc) testConnection(ctx context.Context, req *jobapi.TestRequest) (*jobapi.TestResponse, error) {
	j, err := s.prepareConn(ctx, req.ID, req.Input())
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	b := &checkBuilder{}

	isDB := job_entity.IsDatabase(j.SourceType)
	usesSSH := !isDB || j.ConnMode == job_entity.ConnSSH
	addr := j.DBAddr()
	if !isDB {
		addr = "" // 文件任务只检查链路
	}

	sshOK := true
	if usesSSH {
		local, closeFn, err := s.tunnel(ctx, j.ServerID, addr)
		if err != nil {
			_, _, msg := describe(err)
			b.fail(jobapi.CheckSSH, "SSH 链路可达", msg, "")
			sshOK = false
		} else {
			defer closeFn()
			b.ok(jobapi.CheckSSH, "SSH 链路可达", "")
			if isDB {
				addr = local
			}
		}
	}
	if isDB {
		if err := s.checkDatabase(ctx, j, addr, sshOK, b); err != nil {
			return nil, err
		}
	} else {
		s.checkFiles(ctx, j, sshOK, b)
	}
	b.add(s.checkRepo(ctx, j.RepoID))
	return b.response(), nil
}

// checkDatabase 数据库的可达、认证、权限三项；SSH 不通时全部跳过
func (s *jobSvc) checkDatabase(ctx context.Context, j *job_entity.Job, addr string, sshOK bool, b *checkBuilder) error {
	const (
		reachLabel = "数据库可达"
		authLabel  = "认证通过"
		privsLabel = "权限足够导出"
	)
	if !sshOK {
		b.skip(jobapi.CheckDBReach, reachLabel)
		b.skip(jobapi.CheckDBAuth, authLabel)
		b.skip(jobapi.CheckDBPrivs, privsLabel)
		return nil
	}
	probe, err := s.probes(j.SourceType)
	if err != nil {
		return err
	}
	tgt, err := s.target(j, addr)
	if err != nil {
		return err
	}
	d := probe.Diagnose(ctx, tgt)
	switch {
	case d.Reach != nil:
		b.fail(jobapi.CheckDBReach, reachLabel, d.Reach.Message, "")
		b.skip(jobapi.CheckDBAuth, authLabel)
		b.skip(jobapi.CheckDBPrivs, privsLabel)
	case d.Auth != nil:
		b.ok(jobapi.CheckDBReach, reachLabel, "")
		b.fail(jobapi.CheckDBAuth, authLabel, d.Auth.Message, "")
		b.skip(jobapi.CheckDBPrivs, privsLabel)
	case len(d.Missing) > 0:
		b.ok(jobapi.CheckDBReach, reachLabel, "")
		b.ok(jobapi.CheckDBAuth, authLabel, "")
		b.fail(jobapi.CheckDBPrivs, privsLabel, "缺少权限："+strings.Join(d.Missing, "、"), fixCommand(j.SourceType, j.DBUser, d.Missing))
	default:
		b.ok(jobapi.CheckDBReach, reachLabel, "")
		b.ok(jobapi.CheckDBAuth, authLabel, "")
		b.ok(jobapi.CheckDBPrivs, privsLabel, "")
	}
	return nil
}

// checkRepo 目标存储位置存在且已挂载
func (s *jobSvc) checkRepo(ctx context.Context, repoID int64) *jobapi.CheckItem {
	const label = "存储位置可达、已挂载"
	if repoID <= 0 {
		return &jobapi.CheckItem{Key: jobapi.CheckRepo, Label: label, Reason: "请选择存到哪个存储位置"}
	}
	msg, err := repoProblem(ctx, repoID)
	if err != nil {
		msg = err.Error()
	}
	if msg != "" {
		return &jobapi.CheckItem{Key: jobapi.CheckRepo, Label: label, Reason: msg}
	}
	return &jobapi.CheckItem{Key: jobapi.CheckRepo, Label: label, OK: true}
}

type checkBuilder struct{ items []*jobapi.CheckItem }

func (b *checkBuilder) add(c *jobapi.CheckItem) { b.items = append(b.items, c) }
func (b *checkBuilder) ok(key, label, detail string) {
	b.add(&jobapi.CheckItem{Key: key, Label: label, OK: true, Reason: detail})
}
func (b *checkBuilder) skip(key, label string) {
	b.add(&jobapi.CheckItem{Key: key, Label: label, Skipped: true, Reason: "前面的检查没有通过，已跳过"})
}

// note 这一项没有失败，但也没有真正检查（例如还没填路径）
func (b *checkBuilder) note(key, label, reason string) {
	b.add(&jobapi.CheckItem{Key: key, Label: label, Skipped: true, Reason: reason})
}
func (b *checkBuilder) fail(key, label, reason, fix string) {
	b.add(&jobapi.CheckItem{Key: key, Label: label, Reason: reason, Fix: fix})
}

func (b *checkBuilder) response() *jobapi.TestResponse {
	resp := &jobapi.TestResponse{OK: true, Checks: b.items}
	for _, c := range b.items {
		if !c.OK {
			resp.OK = false
		}
	}
	return resp
}

// ListDatabases 运行时展开「全部，含以后新建」：在 addr（opsnap 本机可连的地址：直连是数据库地址，
// 经 SSH 是已建立的本地转发）上列出全部库，去掉系统库，按名称排序。
func ListDatabases(ctx context.Context, j *job_entity.Job, addr string) ([]string, error) {
	probe, err := defaultProbes(j.SourceType)
	if err != nil {
		return nil, err
	}
	plain, err := plainPassword(j.DBPassword)
	if err != nil {
		return nil, err
	}
	dbs, err := probe.ListDatabases(ctx, Target{Type: j.SourceType, Addr: addr, User: j.DBUser, Password: plain, AuthDB: j.AuthDB})
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(dbs))
	for _, d := range dbs {
		if !job_entity.IsSystemDatabase(j.SourceType, d.Name) {
			names = append(names, d.Name)
		}
	}
	sort.Strings(names)
	return names, nil
}
