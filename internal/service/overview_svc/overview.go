// Package overview_svc 概览页：把任务、运行记录、告警和存储位置统计汇总成一次响应。
package overview_svc

import (
	"context"
	"math"
	"sort"
	"time"

	"github.com/cago-frame/cago/pkg/i18n"

	jobapi "github.com/opskat/opsnap/internal/api/job"
	"github.com/opskat/opsnap/internal/api/overview"
	"github.com/opskat/opsnap/internal/model/entity/job_entity"
	"github.com/opskat/opsnap/internal/model/entity/repo_entity"
	"github.com/opskat/opsnap/internal/model/entity/run_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/repository/job_repo"
	"github.com/opskat/opsnap/internal/repository/repo_repo"
	"github.com/opskat/opsnap/internal/repository/run_repo"
	"github.com/opskat/opsnap/internal/repository/server_repo"
	"github.com/opskat/opsnap/internal/service/alert_svc"
	"github.com/opskat/opsnap/internal/service/setting_svc"
)

// 时间线向后显示的小时数：7 天窗口看一天多一点，30 天窗口看三天
const (
	aheadHours7d  = 30
	aheadHours30d = 72
)

// staleReason 太久没有成功的说明；上次成功的时间由界面按 last_success_at 补充
const staleReason = "太久没有成功运行"

// PlannedRun 窗口内某个任务的一次计划运行
type PlannedRun struct {
	JobID int64
	At    time.Time
}

// OverviewSvc 概览服务
type OverviewSvc interface {
	// Get 汇总概览：计数、总量、需要处理的任务、时间线和存储位置卡片
	Get(ctx context.Context, req *overview.GetRequest) (*overview.GetResponse, error)
}

// Deps 外部依赖，测试里替换；为空的项使用真实实现（Planned 缺省不返回计划，main 注入调度器的 Planned）
type Deps struct {
	Now     func() time.Time
	Stale   func(ctx context.Context) ([]alert_svc.StaleJob, error)
	Planned func(from, to time.Time) ([]PlannedRun, error)
}

type overviewSvc struct{ d Deps }

// NewOverview 创建概览服务
func NewOverview(d Deps) OverviewSvc {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Stale == nil {
		d.Stale = alert_svc.Stale
	}
	if d.Planned == nil {
		d.Planned = func(time.Time, time.Time) ([]PlannedRun, error) { return nil, nil }
	}
	return &overviewSvc{d: d}
}

var defaultOverview OverviewSvc = NewOverview(Deps{})

// Overview 取得概览服务
func Overview() OverviewSvc { return defaultOverview }

// RegisterOverview 替换服务实现（main 注入调度器的计划来源）
func RegisterOverview(i OverviewSvc) { defaultOverview = i }

func (s *overviewSvc) Get(ctx context.Context, req *overview.GetRequest) (*overview.GetResponse, error) {
	days, ahead := 7, aheadHours7d
	window := overview.Window7d
	switch req.Window {
	case "", overview.Window7d:
	case overview.Window30d:
		window, days, ahead = overview.Window30d, 30, aheadHours30d
	default:
		return nil, i18n.NewError(ctx, code.OverviewWindowInvalid)
	}

	now := s.d.Now()
	st, err := setting_svc.Get(ctx)
	if err != nil {
		return nil, err
	}
	loc := st.Location()
	jobs, err := job_repo.Job().List(ctx)
	if err != nil {
		return nil, err
	}
	repos, err := repo_repo.Repo().List(ctx)
	if err != nil {
		return nil, err
	}
	servers, err := server_repo.Server().List(ctx)
	if err != nil {
		return nil, err
	}

	attention, err := s.attention(ctx, jobs, repos)
	if err != nil {
		return nil, err
	}
	local := now.In(loc)
	from := time.Date(local.Year(), local.Month(), local.Day()-(days-1), 0, 0, 0, 0, loc)
	to := now.Add(time.Duration(ahead) * time.Hour)
	tl, err := s.timeline(ctx, jobs, from, now, to, loc)
	if err != nil {
		return nil, err
	}
	tl.Window, tl.AheadHours = window, ahead

	resp := &overview.GetResponse{
		Now:       now.Unix(),
		Attention: attention,
		Timeline:  tl,
		Repos:     repoCards(repos, jobs),
	}
	for _, r := range resp.Repos {
		resp.Totals.RawBytes += r.RawBytes
		resp.Totals.StoredBytes += r.StoredBytes
		resp.Totals.Snapshots += r.Snapshots
	}
	resp.Summary = summary(jobs, attention, len(repos), len(servers))
	return resp, nil
}

func summary(jobs []*job_entity.Job, attention []*overview.AttentionItem, repos, servers int) overview.Summary {
	need := map[int64]bool{}
	for _, a := range attention {
		need[a.JobID] = true
	}
	sum := overview.Summary{Jobs: len(jobs), Attention: len(attention), Servers: servers, Repos: repos}
	for _, j := range jobs {
		switch {
		case need[j.ID]:
		case j.Paused:
			sum.Paused++
		default:
			sum.OK++
		}
	}
	return sum
}

func repoCards(repos []*repo_entity.Repo, jobs []*job_entity.Job) []*overview.RepoCard {
	perRepo := map[int64]int{}
	for _, j := range jobs {
		perRepo[j.RepoID]++
	}
	cards := make([]*overview.RepoCard, 0, len(repos))
	for _, r := range repos {
		c := &overview.RepoCard{
			ID: r.ID, Name: r.Name, Type: r.Type, Status: r.Status,
			StoredBytes: r.StoredSize, RawBytes: r.RawSize, Snapshots: r.SnapshotCount,
			Jobs: perRepo[r.ID], FreeSpace: r.FreeSpace, Ratio: r.DedupRatio(),
		}
		if c.Status == "" {
			c.Status = repo_entity.StatusUnknown
		}
		cards = append(cards, c)
	}
	return cards
}

// attention 失败、有警告和太久没有成功的任务；同一任务只列一次，失败优先。
// 顺序：失败、太久没有成功、有警告，同类里开始得早的在前。
func (s *overviewSvc) attention(ctx context.Context, jobs []*job_entity.Job, repos []*repo_entity.Repo) ([]*overview.AttentionItem, error) {
	latest, err := run_repo.Run().LatestFinishedStatus(ctx)
	if err != nil {
		return nil, err
	}
	stale, err := s.d.Stale(ctx)
	if err != nil {
		return nil, err
	}
	staleOf := map[int64]alert_svc.StaleJob{}
	for _, sj := range stale {
		staleOf[sj.JobID] = sj
	}
	repoOf := map[int64]*repo_entity.Repo{}
	for _, r := range repos {
		repoOf[r.ID] = r
	}

	items := make([]*overview.AttentionItem, 0)
	for _, j := range jobs {
		status := latest[j.ID]
		sj, isStale := staleOf[j.ID]
		var kind string
		switch {
		case status == run_entity.StatusFailed:
			kind = overview.KindFailed
		case isStale:
			kind = overview.KindStale
		case status == run_entity.StatusWarning:
			kind = overview.KindWarning
		default:
			continue
		}
		it := &overview.AttentionItem{
			JobID: j.ID, JobName: j.Name, SourceType: j.SourceType, Kind: kind, RepoID: j.RepoID,
		}
		if r := repoOf[j.RepoID]; r != nil {
			it.RepoNotMounted = r.Status == repo_entity.StatusNotMounted
		}
		last, err := run_repo.Run().PreviousFinished(ctx, j.ID, math.MaxInt64)
		if err != nil {
			return nil, err
		}
		if last != nil {
			it.RunID = last.ID
		}
		if ok, err := run_repo.Run().LastSucceeded(ctx, j.ID); err != nil {
			return nil, err
		} else if ok != nil {
			it.LastSuccessAt = ok.FinishedAt
		}
		if kind == overview.KindStale {
			it.Reason, it.Since = staleReason, sj.Since.Unix()
		} else {
			// 两次查询之间记录可能刚被清理，last 为空时只少了原因
			if last != nil {
				it.Reason = last.Reason
			}
			if it.Since, err = run_repo.Run().StreakStart(ctx, j.ID, status); err != nil {
				return nil, err
			}
			if it.Since == 0 && last != nil {
				it.Since = last.StartedAt
			}
		}
		items = append(items, it)
	}
	rank := map[string]int{overview.KindFailed: 0, overview.KindStale: 1, overview.KindWarning: 2}
	sort.SliceStable(items, func(a, b int) bool {
		if rank[items[a].Kind] != rank[items[b].Kind] {
			return rank[items[a].Kind] < rank[items[b].Kind]
		}
		return items[a].Since < items[b].Since
	})
	return items, nil
}

func (s *overviewSvc) timeline(ctx context.Context, jobs []*job_entity.Job, from, now, to time.Time, loc *time.Location) (*overview.Timeline, error) {
	runs, err := run_repo.Run().ListStartedSince(ctx, from.Unix())
	if err != nil {
		return nil, err
	}
	planned, err := s.d.Planned(now, to)
	if err != nil {
		return nil, err
	}
	latest, err := run_repo.Run().LatestFinishedStatus(ctx)
	if err != nil {
		return nil, err
	}

	tl := &overview.Timeline{From: from.Unix(), To: to.Unix(), Jobs: make([]*overview.TimelineJob, 0, len(jobs))}
	byJob := map[int64]*overview.TimelineJob{}
	for _, j := range jobs {
		tj := &overview.TimelineJob{
			JobID: j.ID, Name: j.Name, SourceType: j.SourceType, Paused: j.Paused,
			Schedule:   jobapi.ScheduleInput{Kind: j.ScheduleKind, Time: j.ScheduleTime, Weekday: j.ScheduleWeekday, Cron: j.ScheduleCron},
			LastStatus: latest[j.ID],
			Runs:       []*overview.TimelineRun{}, Planned: []int64{},
		}
		if next, ok := j.NextRun(now, loc); ok {
			tj.NextRunAt = next.Unix()
		}
		byJob[j.ID] = tj
		tl.Jobs = append(tl.Jobs, tj)
	}
	for _, r := range runs {
		if tj := byJob[r.JobID]; tj != nil {
			tj.Runs = append(tj.Runs, &overview.TimelineRun{ID: r.ID, StartedAt: r.StartedAt, Status: r.Status})
		}
	}
	for _, p := range planned {
		if tj := byJob[p.JobID]; tj != nil {
			tj.Planned = append(tj.Planned, p.At.Unix())
		}
	}
	return tl, nil
}
