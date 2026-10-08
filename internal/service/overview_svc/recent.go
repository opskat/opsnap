package overview_svc

import (
	"context"

	api "github.com/opskat/opsnap/internal/api/overview"
	"github.com/opskat/opsnap/internal/model/entity/datasource_entity"
	"github.com/opskat/opsnap/internal/model/entity/job_entity"
	"github.com/opskat/opsnap/internal/repository/job_repo"
	"github.com/opskat/opsnap/internal/service/job_svc"
)

// recent 所有任务中最近 recentRuns 次运行（status 非空时只列该状态的），按触发顺序倒序。
// 运行字段由任务模块给出，与任务页的运行记录相同（运行中的带实时的已导出量与已运行时长）
func recent(ctx context.Context, status string, jobs map[int64]*job_entity.Job,
	dss map[int64]*datasource_entity.DataSource) ([]*api.RecentRun, error) {
	runs, err := job_repo.Run().ListRecent(ctx, status, recentRuns)
	if err != nil {
		return nil, err
	}
	out := make([]*api.RecentRun, 0, len(runs))
	for _, run := range runs {
		row := &api.RecentRun{Run: *job_svc.Job().RunView(ctx, run)}
		if j := jobs[run.JobID]; j != nil {
			row.JobName, row.JobType = j.Name, j.Type
			if ds := dss[j.DataSourceID]; ds != nil {
				row.DataSourceKind, row.DataSourceAddress = ds.Kind, ds.Address()
			}
		}
		out = append(out, row)
	}
	return out, nil
}
