// Package overview_svc 汇总概览页的数据（docs/specs/2026-09-29-overview-docker.md「概览」）。
// 数字都取自任务页、数据源页与存储页读取的同一份记录（任务、运行记录、数据源、存储），不另行保存。
package overview_svc

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	api "github.com/opskat/opsnap/internal/api/overview"
	"github.com/opskat/opsnap/internal/model/entity/datasource_entity"
	"github.com/opskat/opsnap/internal/model/entity/job_entity"
	"github.com/opskat/opsnap/internal/repository/datasource_repo"
	"github.com/opskat/opsnap/internal/repository/job_repo"
	"github.com/opskat/opsnap/internal/repository/storage_repo"
)

const (
	// recentRuns 最近运行与失败筛选各列出的条数
	recentRuns = 20
	// dailyDays 运行柱状图的天数（含今天）
	dailyDays = 14
	// successWindow 成功率统计的时间范围
	successWindow = 24 * time.Hour
)

// finishedStatuses 计入成功率与 14 天运行的状态；跳过与取消的不计入
var finishedStatuses = []string{job_entity.RunSuccess, job_entity.RunFailed}

type OverviewSvc interface {
	// Get 四个统计（存储占用除外）、最近运行、14 天运行（按 req.TZ 分天）与引导所需的数量
	Get(ctx context.Context, req *api.GetRequest) (*api.GetResponse, error)
}

type overviewSvc struct {
	mu  sync.RWMutex
	now func() time.Time
}

var defaultOverview = &overviewSvc{now: time.Now}

func Overview() OverviewSvc {
	return defaultOverview
}

// SetClock 替换当前时间（测试用）；nil 恢复为 time.Now
func SetClock(fn func() time.Time) {
	if fn == nil {
		fn = time.Now
	}
	defaultOverview.mu.Lock()
	defer defaultOverview.mu.Unlock()
	defaultOverview.now = fn
}

func (s *overviewSvc) clock() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.now()
}

// location 浏览器的时区；缺省、无法识别或为服务端本地时区时按 UTC
func location(tz string) *time.Location {
	if tz == "" || strings.EqualFold(tz, "Local") {
		return time.UTC
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.UTC
	}
	return loc
}

func (s *overviewSvc) Get(ctx context.Context, req *api.GetRequest) (*api.GetResponse, error) {
	now := s.clock()
	loc := location(req.TZ)
	jobs, err := job_repo.Job().List(ctx)
	if err != nil {
		return nil, err
	}
	dsList, err := datasource_repo.DataSource().List(ctx)
	if err != nil {
		return nil, err
	}
	stList, err := storage_repo.Storage().List(ctx)
	if err != nil {
		return nil, err
	}
	dss := make(map[int64]*datasource_entity.DataSource, len(dsList))
	for _, ds := range dsList {
		dss[ds.ID] = ds
	}
	byID := make(map[int64]*job_entity.Job, len(jobs))
	for _, j := range jobs {
		byID[j.ID] = j
	}
	resp := &api.GetResponse{
		Timezone: loc.String(),
		NextRun:  nextRun(jobs, now),
		Counts:   api.Counts{DataSources: len(dsList), Storages: len(stList), Jobs: len(jobs)},
	}
	if resp.Protected, err = protected(ctx, jobs, dss); err != nil {
		return nil, err
	}
	if err := runStats(ctx, now, loc, resp); err != nil {
		return nil, err
	}
	if resp.Recent.Items, err = recent(ctx, "", byID, dss); err != nil {
		return nil, err
	}
	if resp.Recent.Failed, err = recent(ctx, job_entity.RunFailed, byID, dss); err != nil {
		return nil, err
	}
	return resp, nil
}

// protected 受保护的数据源（spec 决策 4）：至少被一个已启用的任务引用，且该任务至少成功过一次、
// 有可恢复的快照（任务上记录的快照数，与任务页显示的相同）
func protected(ctx context.Context, jobs []*job_entity.Job, dss map[int64]*datasource_entity.DataSource) (api.Protected, error) {
	out := api.Protected{ByKind: []*api.KindCount{}}
	succeeded, err := job_repo.Run().SucceededJobs(ctx)
	if err != nil {
		return out, err
	}
	seen := map[int64]bool{}
	byKind := map[string]int{}
	for _, j := range jobs {
		ds := dss[j.DataSourceID]
		if ds == nil || seen[ds.ID] || !j.Enabled || j.SnapshotCount == 0 || !succeeded[j.ID] {
			continue
		}
		seen[ds.ID] = true
		byKind[ds.Kind]++
	}
	for kind, n := range byKind {
		out.ByKind = append(out.ByKind, &api.KindCount{Kind: kind, Count: n})
		out.Count += n
	}
	slices.SortFunc(out.ByKind, func(a, b *api.KindCount) int { return strings.Compare(a.Kind, b.Kind) })
	return out, nil
}

// nextRun 已启用任务中最早的下一次计划时间；同一时间取 ID 最小的任务
func nextRun(jobs []*job_entity.Job, now time.Time) *api.NextRun {
	var out *api.NextRun
	for _, j := range jobs {
		next, ok := j.NextRun(now)
		if !ok || out != nil && next.Unix() >= out.At {
			continue
		}
		out = &api.NextRun{At: next.Unix(), JobID: j.ID, JobName: j.Name}
	}
	return out
}

// runStats 24h 成功率、最近 24 小时的失败次数与 14 天运行，按运行的开始时间统计
func runStats(ctx context.Context, now time.Time, loc *time.Location, resp *api.GetResponse) error {
	local := now.In(loc)
	first := time.Date(local.Year(), local.Month(), local.Day()-(dailyDays-1), 0, 0, 0, 0, loc)
	since := now.Add(-successWindow)
	resp.Daily = make([]*api.DayRuns, 0, dailyDays)
	days := make(map[string]*api.DayRuns, dailyDays)
	for i := range dailyDays {
		d := &api.DayRuns{Date: time.Date(first.Year(), first.Month(), first.Day()+i, 0, 0, 0, 0, loc).Format(time.DateOnly)}
		resp.Daily = append(resp.Daily, d)
		days[d.Date] = d
	}
	runs, err := job_repo.Run().ListStartedSince(ctx, min(first.UnixMilli(), since.UnixMilli()), finishedStatuses)
	if err != nil {
		return err
	}
	rate := &resp.Success24h
	for _, run := range runs {
		success := run.Status == job_entity.RunSuccess
		if run.StartedAt >= since.UnixMilli() {
			if success {
				rate.Success++
			} else {
				rate.Failed++
			}
		}
		if d := days[time.UnixMilli(run.StartedAt).In(loc).Format(time.DateOnly)]; d != nil {
			if success {
				d.Success++
			} else {
				d.Failed++
			}
		}
	}
	rate.Runs = rate.Success + rate.Failed
	if rate.Runs > 0 {
		rate.SuccessRate = float64(rate.Success) / float64(rate.Runs)
	}
	resp.Recent.Failed24h = rate.Failed
	return nil
}
