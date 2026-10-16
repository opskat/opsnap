package snapshot_svc

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/opskat/opsnap/internal/api/snapshot"
	"github.com/opskat/opsnap/internal/pkg/cmdbuild"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/repository/run_repo"
)

// maxRunsLookup 为取新增量最多查看的最近运行记录条数
const maxRunsLookup = 500

// resticSnapshot restic snapshots --json 的一项
type resticSnapshot struct {
	ID      string   `json:"id"`
	ShortID string   `json:"short_id"`
	Time    string   `json:"time"`
	Paths   []string `json:"paths"`
	Tags    []string `json:"tags"`
	Summary *struct {
		DataAdded int64 `json:"data_added"`
	} `json:"summary"`
}

func jobTag(jobID int64) string { return "job:" + strconv.FormatInt(jobID, 10) }

// runID 快照标签 run:<id> 里的运行 ID，没有时为 0
func (r *resticSnapshot) runID() int64 {
	for _, t := range r.Tags {
		if v, ok := strings.CutPrefix(t, "run:"); ok {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				return n
			}
		}
	}
	return 0
}

// short 短 ID；老版本 restic 不输出 short_id 时取完整 ID 的前 8 位
func (r *resticSnapshot) short() string {
	if r.ShortID == "" && len(r.ID) >= 8 {
		return r.ID[:8]
	}
	return r.ShortID
}

func (r *resticSnapshot) unix() int64 {
	t, err := time.Parse(time.RFC3339Nano, r.Time)
	if err != nil {
		return 0
	}
	return t.Unix()
}

// listSnapshots 列出任务的全部快照，按时间倒序
func (s *snapshotSvc) listSnapshots(ctx context.Context, a *access) ([]*resticSnapshot, error) {
	cmd, err := cmdbuild.ResticSnapshots(a.repo, []string{jobTag(a.job.ID)})
	out, cerr := s.capture(ctx, cmd, err)
	if cerr != nil {
		return nil, cerr
	}
	var list []*resticSnapshot
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, i18n.NewError(ctx, code.SnapshotReadFailed, "无法解析 restic snapshots 的输出："+err.Error())
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].unix() > list[j].unix() })
	return list, nil
}

// resolve 按完整 ID 或前缀在任务的快照里找到快照；找不到（含不属于这个任务）返回 SnapshotNotFound
func (s *snapshotSvc) resolve(ctx context.Context, a *access, sid string) (*resticSnapshot, error) {
	if !snapshotIDPattern.MatchString(sid) {
		return nil, i18n.NewError(ctx, code.SnapshotNotFound)
	}
	list, err := s.listSnapshots(ctx, a)
	if err != nil {
		return nil, err
	}
	// 前缀对上多个快照时不猜（restic 自己也会报歧义），按找不到处理
	var found *resticSnapshot
	for _, sn := range list {
		if sn.ID == sid {
			return sn, nil
		}
		if strings.HasPrefix(sn.ID, sid) {
			if found != nil {
				return nil, i18n.NewError(ctx, code.SnapshotNotFound)
			}
			found = sn
		}
	}
	if found == nil {
		return nil, i18n.NewError(ctx, code.SnapshotNotFound)
	}
	return found, nil
}

// list 见 SnapshotSvc.List
func (s *snapshotSvc) list(ctx context.Context, jobID int64) (*snapshot.ListResponse, error) {
	a, err := s.open(ctx, jobID)
	if err != nil {
		return nil, err
	}
	defer a.close()
	list, err := s.listSnapshots(ctx, a)
	if err != nil {
		return nil, err
	}
	runs, _, err := run_repo.Run().List(ctx, run_repo.ListFilter{JobID: jobID, Limit: maxRunsLookup})
	if err != nil {
		return nil, err
	}
	// 一次运行产生多个快照（MySQL 每个库一个）时，运行记录里只有总量，无法拆分到各快照，不采用
	added := map[int64]int64{}
	for _, r := range runs {
		if len(r.SnapshotIDs) == 1 {
			added[r.ID] = r.BytesAdded
		}
	}
	reasons := s.reasons(ctx, a)

	resp := &snapshot.ListResponse{JobName: a.job.Name, SourceType: a.job.SourceType, Snapshots: make([]*snapshot.Item, 0, len(list))}
	for _, sn := range list {
		it := &snapshot.Item{
			ID: sn.ID, ShortID: sn.short(), Time: sn.unix(), Paths: sn.Paths, RunID: sn.runID(),
			BytesAdded: -1, Reasons: reasons[sn.ID],
		}
		if it.Paths == nil {
			it.Paths = []string{}
		}
		if it.Reasons == nil {
			it.Reasons = []string{}
		}
		if n, ok := added[it.RunID]; sn.Summary != nil {
			it.BytesAdded = sn.Summary.DataAdded
		} else if ok {
			it.BytesAdded = n
		}
		resp.Snapshots = append(resp.Snapshots, it)
	}
	return resp, nil
}

// forgetGroup restic forget --dry-run --json 的一组
type forgetGroup struct {
	Reasons []struct {
		Snapshot struct {
			ID string `json:"id"`
		} `json:"snapshot"`
		Matches []string `json:"matches"`
	} `json:"reasons"`
}

// reasons 用任务的保留规则演算 restic forget --dry-run，读出每个被保留快照的保留原因（快照完整 ID → 原因）。
// 选择让 restic 自己演算而不是在 Go 里重写规则：分组方式、ISO 周的边界、时区都与真正的清理一致。
// 演算失败只丢掉保留原因，不影响列表本身。
func (s *snapshotSvc) reasons(ctx context.Context, a *access) map[string][]string {
	policy, err := a.job.Retention().ForgetArgs()
	if err != nil {
		return nil
	}
	cmd, err := cmdbuild.ResticForget(a.repo, cmdbuild.ForgetSpec{Tags: []string{jobTag(a.job.ID)}, Policy: policy, DryRun: true})
	out, cerr := s.capture(ctx, cmd, err)
	if cerr != nil {
		return nil
	}
	var groups []forgetGroup
	if json.Unmarshal(out, &groups) != nil {
		return nil
	}
	ret := map[string][]string{}
	for _, g := range groups {
		for _, r := range g.Reasons {
			ret[r.Snapshot.ID] = reasonsFromMatches(r.Matches)
		}
	}
	return ret
}

// reasonsFromMatches 把 restic 的保留说明（"last snapshot"、"daily snapshot"、"within 7d" 等）
// 归并成 last | daily | weekly | monthly，顺序固定；时间窗口也算「最近」，小时和年不在规则里，忽略。
func reasonsFromMatches(matches []string) []string {
	has := map[string]bool{}
	for _, m := range matches {
		// 按词判断：restic ≥0.17 还会写「oldest daily snapshot」
		words := map[string]bool{}
		for _, w := range strings.Fields(m) {
			words[w] = true
		}
		switch {
		case words["daily"]:
			has[snapshot.ReasonDaily] = true
		case words["weekly"]:
			has[snapshot.ReasonWeekly] = true
		case words["monthly"]:
			has[snapshot.ReasonMonthly] = true
		case words["last"], words["within"]:
			has[snapshot.ReasonLast] = true
		}
	}
	var out []string
	for _, r := range []string{snapshot.ReasonLast, snapshot.ReasonDaily, snapshot.ReasonWeekly, snapshot.ReasonMonthly} {
		if has[r] {
			out = append(out, r)
		}
	}
	return out
}
