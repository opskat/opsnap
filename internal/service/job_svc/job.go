// Package job_svc 备份任务：增删改查、字段校验、库列表、测试连接与下次运行时间
package job_svc

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/i18n"

	jobapi "github.com/opskat/opsnap/internal/api/job"
	"github.com/opskat/opsnap/internal/model/entity/job_entity"
	"github.com/opskat/opsnap/internal/model/entity/run_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/schedule"
	"github.com/opskat/opsnap/internal/pkg/secret"
	"github.com/opskat/opsnap/internal/repository/job_repo"
	"github.com/opskat/opsnap/internal/repository/repo_repo"
	"github.com/opskat/opsnap/internal/repository/run_repo"
	"github.com/opskat/opsnap/internal/repository/server_repo"
	"github.com/opskat/opsnap/internal/service/setting_svc"
)

// AttentionFunc 返回需要处理的任务（键是任务 ID）。运行引擎落地后注册，
// 例如最近一次运行失败、太久没有成功；未注册时没有任务需要处理。
type AttentionFunc func(ctx context.Context) (map[int64]bool, error)

var attention AttentionFunc

// RegisterAttention 注册「需要处理」的判定；传 nil 表示没有
func RegisterAttention(f AttentionFunc) { attention = f }

// CountByServer 每台服务器被多少个任务使用，供 server_svc.RegisterJobCounter 注册
func CountByServer(ctx context.Context) (map[int64]int, error) {
	return job_repo.Job().CountByServer(ctx)
}

// CountByRepo 每个存储位置被多少个任务使用，供 repo_svc.RegisterJobCounter 注册
func CountByRepo(ctx context.Context) (map[int64]int, error) {
	return job_repo.Job().CountByRepo(ctx)
}

// JobSvc 备份任务服务
type JobSvc interface {
	// List 列出任务。filter 为 all | attention | paused；q 按名称、服务器名称、路径搜索
	List(ctx context.Context, filter, q string) (*jobapi.ListResponse, error)
	Get(ctx context.Context, id int64) (*jobapi.GetResponse, error)
	// Create 新建任务；字段错误以 JobFieldInvalid 一次返回
	Create(ctx context.Context, req *jobapi.CreateRequest) (*jobapi.CreateResponse, error)
	// Update 整体保存配置；暂停状态不随表单改变
	Update(ctx context.Context, req *jobapi.UpdateRequest) (*jobapi.UpdateResponse, error)
	// Delete 只删除任务，存储位置里的快照原样保留，不执行任何命令
	Delete(ctx context.Context, id int64) error
	// Duplicate 复制为新任务，副本保持暂停
	Duplicate(ctx context.Context, id int64) (*jobapi.Item, error)
	// SetPaused 暂停或恢复计划
	SetPaused(ctx context.Context, id int64, paused bool) (*jobapi.Item, error)
	// NextRuns 预览一份计划接下来几次的运行时间，按设置的时区计算
	NextRuns(ctx context.Context, req *jobapi.NextRunsRequest) (*jobapi.NextRunsResponse, error)
	// Databases 读取数据库列表及大小；读取失败不是错误，返回 OK=false 与原因
	Databases(ctx context.Context, req *jobapi.DatabasesRequest) (*jobapi.DatabasesResponse, error)
	// Test 逐项检查连接，返回有序的检查项
	Test(ctx context.Context, req *jobapi.TestRequest) (*jobapi.TestResponse, error)
}

// Tunnel 经服务器的 SSH 链路转发到 addr（从该服务器上看的地址），返回 opsnap 本机可连的地址和关闭函数。
// addr 为空只建立连接、不转发，用来检查链路。
type Tunnel func(ctx context.Context, serverID int64, addr string) (local string, closeFn func(), err error)

// Deps 外部依赖，测试里替换
type Deps struct {
	Now    func() time.Time
	Tunnel Tunnel
	Probes ProbeFor
}

type jobSvc struct {
	now    func() time.Time
	tunnel Tunnel
	probes ProbeFor
}

// NewJob 创建任务服务；Deps 中为空的项使用真实实现
func NewJob(d Deps) JobSvc {
	s := &jobSvc{now: d.Now, tunnel: d.Tunnel, probes: d.Probes}
	if s.now == nil {
		s.now = time.Now
	}
	if s.tunnel == nil {
		s.tunnel = sshTunnel
	}
	if s.probes == nil {
		s.probes = defaultProbes
	}
	return s
}

var defaultJob = NewJob(Deps{})

// Job 取得任务服务
func Job() JobSvc { return defaultJob }

// RegisterJob 替换服务实现
func RegisterJob(i JobSvc) { defaultJob = i }

func (s *jobSvc) find(ctx context.Context, id int64) (*job_entity.Job, error) {
	j, err := job_repo.Job().Find(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := j.Check(ctx); err != nil {
		return nil, err
	}
	return j, nil
}

func (s *jobSvc) List(ctx context.Context, filter, q string) (*jobapi.ListResponse, error) {
	jobs, err := job_repo.Job().List(ctx)
	if err != nil {
		return nil, err
	}
	v, err := s.newViewer(ctx)
	if err != nil {
		return nil, err
	}
	runs, err := s.newRunSummary(ctx)
	if err != nil {
		return nil, err
	}
	q = strings.ToLower(strings.TrimSpace(q))
	resp := &jobapi.ListResponse{List: make([]*jobapi.Item, 0, len(jobs))}
	for _, j := range jobs {
		item := v.item(j)
		runs.fill(item)
		switch filter {
		case jobapi.FilterAttention:
			if !item.Attention {
				continue
			}
		case jobapi.FilterPaused:
			if !item.Paused {
				continue
			}
		}
		if q != "" && !matches(item, q) {
			continue
		}
		resp.List = append(resp.List, item)
	}
	return resp, nil
}

// recentDays 列表运行带覆盖的天数；多取一天，按本地日期切分时最早那天也完整
const recentDays = 14

// runSummary 列表用的运行概况：每个任务最近一次运行和最近 14 天的运行
type runSummary struct {
	latest map[int64]*run_entity.Run
	recent map[int64][]*jobapi.RunBrief
}

func (s *jobSvc) newRunSummary(ctx context.Context) (*runSummary, error) {
	latest, err := run_repo.Run().LatestByJob(ctx)
	if err != nil {
		return nil, err
	}
	since := s.now().Add(-(recentDays + 1) * 24 * time.Hour).Unix()
	list, err := run_repo.Run().ListStartedSince(ctx, since)
	if err != nil {
		return nil, err
	}
	recent := map[int64][]*jobapi.RunBrief{}
	for _, r := range list {
		recent[r.JobID] = append(recent[r.JobID], brief(r))
	}
	return &runSummary{latest: latest, recent: recent}, nil
}

func (rs *runSummary) fill(it *jobapi.Item) {
	if r := rs.latest[it.ID]; r != nil {
		it.LastRun = brief(r)
		it.Running = r.Running()
	}
	it.RecentRuns = rs.recent[it.ID]
}

func brief(r *run_entity.Run) *jobapi.RunBrief {
	return &jobapi.RunBrief{ID: r.ID, Status: r.Status, StartedAt: r.StartedAt, BytesAdded: r.BytesAdded}
}

// matches 名称、服务器名称或任一路径包含关键字（已转小写）
func matches(it *jobapi.Item, q string) bool {
	if strings.Contains(strings.ToLower(it.Name), q) || strings.Contains(strings.ToLower(it.ServerName), q) {
		return true
	}
	return slices.ContainsFunc(it.Paths, func(p string) bool { return strings.Contains(strings.ToLower(p), q) })
}

func (s *jobSvc) Get(ctx context.Context, id int64) (*jobapi.GetResponse, error) {
	j, err := s.find(ctx, id)
	if err != nil {
		return nil, err
	}
	item, err := s.view(ctx, j)
	if err != nil {
		return nil, err
	}
	return &jobapi.GetResponse{Item: *item}, nil
}

// view 单个任务的返回结构
func (s *jobSvc) view(ctx context.Context, j *job_entity.Job) (*jobapi.Item, error) {
	v, err := s.newViewer(ctx)
	if err != nil {
		return nil, err
	}
	return v.item(j), nil
}

func (s *jobSvc) Create(ctx context.Context, req *jobapi.CreateRequest) (*jobapi.CreateResponse, error) {
	// 与轮换主密钥互斥，见 secret.BeginWrite
	endWrite := secret.BeginWrite()
	defer endWrite()
	j, errs, err := s.assemble(ctx, nil, req.Input())
	if err != nil {
		return nil, err
	}
	errs = append(errs, j.ValidateConfig()...)
	more, err := s.checkResources(ctx, j, errs)
	if err != nil {
		return nil, err
	}
	if err := append(errs, more...).Err(ctx); err != nil {
		return nil, err
	}
	j.Paused = req.Paused
	now := s.now().Unix()
	j.Createtime, j.Updatetime = now, now
	if err := job_repo.Job().Create(ctx, j); err != nil {
		return nil, err
	}
	item, err := s.view(ctx, j)
	if err != nil {
		return nil, err
	}
	return &jobapi.CreateResponse{Item: *item}, nil
}

func (s *jobSvc) Update(ctx context.Context, req *jobapi.UpdateRequest) (*jobapi.UpdateResponse, error) {
	// 与轮换主密钥互斥，见 secret.BeginWrite
	endWrite := secret.BeginWrite()
	defer endWrite()
	old, err := s.find(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	j, errs, err := s.assemble(ctx, old, req.Input())
	if err != nil {
		return nil, err
	}
	errs = append(errs, j.ValidateConfig()...)
	more, err := s.checkResources(ctx, j, errs)
	if err != nil {
		return nil, err
	}
	if err := append(errs, more...).Err(ctx); err != nil {
		return nil, err
	}
	j.ID, j.Createtime, j.Paused = old.ID, old.Createtime, old.Paused
	// 待清理是运行状态，不归编辑管：沿用原值，别让「改个名字」把它弄丢
	j.ForgetPendingAt, j.ForgetPendingError = old.ForgetPendingAt, old.ForgetPendingError
	j.Updatetime = s.now().Unix()
	if err := job_repo.Job().Update(ctx, j); err != nil {
		return nil, err
	}
	item, err := s.view(ctx, j)
	if err != nil {
		return nil, err
	}
	return &jobapi.UpdateResponse{Item: *item}, nil
}

func (s *jobSvc) Delete(ctx context.Context, id int64) error {
	if _, err := s.find(ctx, id); err != nil {
		return err
	}
	// 只删任务这一行：不碰存储位置，也不运行任何命令，已有快照原样保留
	return job_repo.Job().Delete(ctx, id)
}

func (s *jobSvc) Duplicate(ctx context.Context, id int64) (*jobapi.Item, error) {
	// 与轮换主密钥互斥，见 secret.BeginWrite
	endWrite := secret.BeginWrite()
	defer endWrite()
	src, err := s.find(ctx, id)
	if err != nil {
		return nil, err
	}
	cp := *src
	cp.ID = 0
	cp.Name = copyName(src.Name)
	cp.Databases = slices.Clone(src.Databases)
	cp.Paths = slices.Clone(src.Paths)
	cp.Excludes = slices.Clone(src.Excludes)
	cp.ExtraArgs = slices.Clone(src.ExtraArgs)
	cp.Paused = true
	// 副本是一个新任务，不该继承原任务欠着的清理
	cp.ForgetPendingAt, cp.ForgetPendingError = 0, ""
	now := s.now().Unix()
	cp.Createtime, cp.Updatetime = now, now
	if err := job_repo.Job().Create(ctx, &cp); err != nil {
		return nil, err
	}
	return s.view(ctx, &cp)
}

const copySuffix = " 副本"

// copyName 名称加「 副本」，总长不超过名称上限 64 个字
func copyName(name string) string {
	r := []rune(name)
	if limit := 64 - len([]rune(copySuffix)); len(r) > limit {
		r = r[:limit]
	}
	return string(r) + copySuffix
}

func (s *jobSvc) SetPaused(ctx context.Context, id int64, paused bool) (*jobapi.Item, error) {
	j, err := s.find(ctx, id)
	if err != nil {
		return nil, err
	}
	now := s.now().Unix()
	if err := job_repo.Job().SetPaused(ctx, id, paused, now); err != nil {
		return nil, err
	}
	j.Paused, j.Updatetime = paused, now
	return s.view(ctx, j)
}

const (
	defaultPreviewCount = 5
	maxPreviewCount     = 20
)

func (s *jobSvc) NextRuns(ctx context.Context, req *jobapi.NextRunsRequest) (*jobapi.NextRunsResponse, error) {
	j := &job_entity.Job{}
	applySchedule(j, req.Schedule)
	if err := j.ValidateSchedule().Err(ctx); err != nil {
		return nil, err
	}
	st, err := setting_svc.Get(ctx)
	if err != nil {
		return nil, err
	}
	n := req.Count
	if n <= 0 {
		n = defaultPreviewCount
	}
	n = min(n, maxPreviewCount)
	loc := st.Location()
	times, err := schedule.NextN(j.Schedule(), s.now(), loc, n)
	if err != nil {
		return nil, i18n.NewError(ctx, code.JobFieldInvalid, "[schedule.kind] "+err.Error())
	}
	resp := &jobapi.NextRunsResponse{Timezone: loc.String(), Times: make([]int64, 0, len(times))}
	for _, t := range times {
		resp.Times = append(resp.Times, t.Unix())
	}
	return resp, nil
}

// viewer 把任务转成返回结构所需的名称、时区、需要处理的集合
type viewer struct {
	now        time.Time
	loc        *time.Location
	servers    map[int64]string
	repos      map[int64]string
	needAttend map[int64]bool
}

func (s *jobSvc) newViewer(ctx context.Context) (*viewer, error) {
	st, err := setting_svc.Get(ctx)
	if err != nil {
		return nil, err
	}
	v := &viewer{now: s.now(), loc: st.Location(), servers: map[int64]string{}, repos: map[int64]string{}}
	servers, err := server_repo.Server().List(ctx)
	if err != nil {
		return nil, err
	}
	for _, sv := range servers {
		v.servers[sv.ID] = sv.Name
	}
	repos, err := repo_repo.Repo().List(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range repos {
		v.repos[r.ID] = r.Name
	}
	if attention != nil {
		if v.needAttend, err = attention(ctx); err != nil {
			return nil, err
		}
	}
	return v, nil
}

func (v *viewer) item(j *job_entity.Job) *jobapi.Item {
	it := &jobapi.Item{
		ID: j.ID, Name: j.Name, SourceType: j.SourceType,
		ServerID: j.ServerID, ServerName: v.servers[j.ServerID], ConnMode: j.ConnMode,
		DBHost: j.DBHost, DBPort: j.DBPort, DBUser: j.DBUser, DBPassword: secret.Mask(j.DBPassword),
		Databases: nonNil(j.Databases), AllDatabases: j.AllDatabases, AuthDB: j.AuthDB,
		Paths: nonNil(j.Paths), Excludes: nonNil(j.Excludes),
		RepoID: j.RepoID, RepoName: v.repos[j.RepoID],
		Schedule: jobapi.ScheduleInput{Kind: j.ScheduleKind, Time: j.ScheduleTime, Weekday: j.ScheduleWeekday, Cron: j.ScheduleCron},
		Retention: jobapi.RetentionInput{
			Preset: j.RetentionPreset, Days: j.RetentionDays, Weeks: j.RetentionWeeks, Months: j.RetentionMonths,
		},
		Advanced: jobapi.AdvancedInput{
			TimeoutSec: j.TimeoutSec, BandwidthKbps: j.BandwidthKbps, Compression: j.Compression, ExtraArgs: nonNil(j.ExtraArgs),
		},
		Paused:    j.Paused,
		Attention: v.needAttend[j.ID],
		// 待清理只是欠着一次空间回收，不影响是否需要处理
		ForgetPendingAt:    j.ForgetPendingAt,
		ForgetPendingError: j.ForgetPendingError,
		CreatedAt:          j.Createtime, UpdatedAt: j.Updatetime,
	}
	if next, ok := j.NextRun(v.now, v.loc); ok {
		it.NextRunAt = next.Unix()
	}
	return it
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

var errNoSecret = errors.New("主密钥未加载，无法处理数据库密码")

// Databases 读取数据库列表及大小；读取失败不是错误，返回 OK=false 与原因。实现见 conn.go
func (s *jobSvc) Databases(ctx context.Context, req *jobapi.DatabasesRequest) (*jobapi.DatabasesResponse, error) {
	return s.listDatabases(ctx, req)
}

// Test 逐项检查连接，返回有序的检查项。实现见 conn.go
func (s *jobSvc) Test(ctx context.Context, req *jobapi.TestRequest) (*jobapi.TestResponse, error) {
	return s.testConnection(ctx, req)
}
