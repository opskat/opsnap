// Package alert_svc 「太久没有成功」告警：找出超过设定时长没有成功运行的任务，每段无成功期只通知一次。
//
// 告警状态存在 stale_alerts 表里，重启后不重发。任务再次成功时由运行引擎清除状态并发出「恢复正常」
// （见 run_svc 的通知步骤），所以无论之前是连续失败还是根本没有运行，一次恢复只通知一次。
package alert_svc

import (
	"context"
	"fmt"
	"time"

	"github.com/opskat/opsnap/internal/model/entity/job_entity"
	"github.com/opskat/opsnap/internal/model/entity/notify_entity"
	"github.com/opskat/opsnap/internal/repository/alert_repo"
	"github.com/opskat/opsnap/internal/repository/job_repo"
	"github.com/opskat/opsnap/internal/repository/notify_repo"
	"github.com/opskat/opsnap/internal/repository/run_repo"
	"github.com/opskat/opsnap/internal/service/notify_svc"
)

// StaleJob 一个太久没有成功的任务
type StaleJob struct {
	JobID   int64
	JobName string
	// Since 从这一刻起算作「太久没有成功」：上次成功（从未成功则任务创建）加阈值
	Since time.Time
	// LastSuccessAt 上次成功的时间；从未成功时为零值
	LastSuccessAt time.Time
}

// AlertSvc 告警服务
type AlertSvc interface {
	// Stale 当前所有太久没有成功的未暂停任务，按任务 ID 升序；概览页用它，不依赖通知设置
	Stale(ctx context.Context) ([]StaleJob, error)
	// Check 对尚未通知过的太久没有成功的任务各发一次「太久没有成功」并记下；顺带清理已删除任务的状态。
	// 先记录再发送：发送失败（通知服务已重试）也不会在下一轮重发。
	Check(ctx context.Context) error
}

// Deps 外部依赖，测试里替换；为空的项使用真实实现
type Deps struct {
	Now    func() time.Time
	Notify func(ctx context.Context, e notify_svc.Event) error
}

type alertSvc struct{ d Deps }

// NewAlert 创建告警服务
func NewAlert(d Deps) AlertSvc {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Notify == nil {
		d.Notify = notify_svc.Send
	}
	return &alertSvc{d: d}
}

var defaultAlert AlertSvc = NewAlert(Deps{})

// Alert 取得告警服务
func Alert() AlertSvc { return defaultAlert }

// RegisterAlert 替换服务实现
func RegisterAlert(i AlertSvc) { defaultAlert = i }

// Stale 见 AlertSvc.Stale
func Stale(ctx context.Context) ([]StaleJob, error) { return defaultAlert.Stale(ctx) }

// Check 见 AlertSvc.Check
func Check(ctx context.Context) error { return defaultAlert.Check(ctx) }

// threshold 通知设置里的阈值，没有保存过设置时用默认值
func threshold(ctx context.Context) (time.Duration, error) {
	n, err := notify_repo.Notify().Get(ctx)
	if err != nil {
		return 0, err
	}
	if n == nil {
		n = notify_entity.Default()
	}
	return time.Duration(n.StaleHours) * time.Hour, nil
}

func (s *alertSvc) jobs(ctx context.Context) ([]*job_entity.Job, error) {
	return job_repo.Job().List(ctx)
}

func (s *alertSvc) Stale(ctx context.Context) ([]StaleJob, error) {
	jobs, err := s.jobs(ctx)
	if err != nil {
		return nil, err
	}
	return s.stale(ctx, jobs)
}

func (s *alertSvc) stale(ctx context.Context, jobs []*job_entity.Job) ([]StaleJob, error) {
	limit, err := threshold(ctx)
	if err != nil {
		return nil, err
	}
	now := s.d.Now()
	var out []StaleJob
	for _, j := range jobs {
		if j.Paused {
			continue
		}
		base := time.Unix(j.Createtime, 0)
		var last time.Time
		r, err := run_repo.Run().LastSucceeded(ctx, j.ID)
		if err != nil {
			return nil, err
		}
		if r != nil {
			last = time.Unix(r.FinishedAt, 0)
			base = last
		}
		if now.Sub(base) > limit {
			out = append(out, StaleJob{JobID: j.ID, JobName: j.Name, Since: base.Add(limit), LastSuccessAt: last})
		}
	}
	return out, nil
}

func (s *alertSvc) Check(ctx context.Context) error {
	jobs, err := s.jobs(ctx)
	if err != nil {
		return err
	}
	if err := s.dropDeleted(ctx, jobs); err != nil {
		return err
	}
	stale, err := s.stale(ctx, jobs)
	if err != nil {
		return err
	}
	var firstErr error
	for _, st := range stale {
		created, err := alert_repo.Alert().Create(ctx, st.JobID, s.d.Now())
		if err != nil {
			return err
		}
		if !created {
			continue
		}
		ev := notify_svc.Event{
			Type: notify_entity.EventStale, JobID: st.JobID, JobName: st.JobName,
			LastSuccessAt: st.LastSuccessAt, Link: fmt.Sprintf("/jobs/%d/edit", st.JobID),
		}
		if err := s.d.Notify(ctx, ev); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// dropDeleted 清掉已删除任务的告警状态
func (s *alertSvc) dropDeleted(ctx context.Context, jobs []*job_entity.Job) error {
	alive := make(map[int64]bool, len(jobs))
	for _, j := range jobs {
		alive[j.ID] = true
	}
	list, err := alert_repo.Alert().List(ctx)
	if err != nil {
		return err
	}
	for _, a := range list {
		if !alive[a.JobID] {
			if _, err := alert_repo.Alert().Clear(ctx, a.JobID); err != nil {
				return err
			}
		}
	}
	return nil
}
