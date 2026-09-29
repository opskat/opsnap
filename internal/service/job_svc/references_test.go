package job_svc

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dsapi "github.com/opskat/opsnap/internal/api/datasource"
	api "github.com/opskat/opsnap/internal/api/job"
	storageapi "github.com/opskat/opsnap/internal/api/storage"
	"github.com/opskat/opsnap/internal/repository/datasource_repo"
	"github.com/opskat/opsnap/internal/repository/job_repo"
	"github.com/opskat/opsnap/internal/repository/storage_repo"
	"github.com/opskat/opsnap/internal/service/datasource_svc"
	"github.com/opskat/opsnap/internal/service/storage_svc"
)

// 删除数据源或存储时的“没有任务引用它”检查与新建任务不能交错：检查之后、删除之前新建的任务
// 会指向一个已删除的数据源或存储（docs/specs/2026-09-27-backup-jobs.md「对已有页面的影响」）
func TestReferenceCheckAndCreateDoNotInterleave(t *testing.T) {
	for _, target := range []string{"数据源", "存储"} {
		t.Run(target, func(t *testing.T) {
			e := newRunEnv(t)
			require.NoError(t, job_repo.Job().Delete(e.ctx, e.job.ID))
			t.Cleanup(func() {
				datasource_svc.SetJobReferrer(nil)
				storage_svc.SetJobReferrer(nil)
			})
			// 删除一方查完引用后停住，等新建一方有机会插进来
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			hold := func(refs []*api.Ref, err error) ([]*api.Ref, error) {
				once.Do(func() {
					close(entered)
					<-release
				})
				return refs, err
			}
			var del func() error
			if target == "数据源" {
				datasource_svc.SetJobReferrer(func(ctx context.Context, id int64) ([]*api.Ref, error) {
					return hold(Job().ByDataSource(ctx, id))
				})
				del = func() error {
					_, err := datasource_svc.DataSource().Delete(e.ctx, &dsapi.DeleteRequest{ID: e.ds.ID})
					return err
				}
			} else {
				storage_svc.SetJobReferrer(func(ctx context.Context, id int64) ([]*api.Ref, error) {
					return hold(Job().ByStorage(ctx, id))
				})
				del = func() error {
					_, err := storage_svc.Storage().Delete(e.ctx, &storageapi.DeleteRequest{ID: e.storage})
					return err
				}
			}

			delErr := make(chan error, 1)
			go func() { delErr <- del() }()
			<-entered
			createErr := make(chan error, 1)
			go func() {
				_, err := Job().Create(e.ctx, &api.CreateRequest{Type: "backup", DataSourceID: e.ds.ID, StorageID: e.storage,
					Prefix: "pg/race", Name: "race", Scope: "databases", Databases: []string{"app"}, Method: "full",
					Compression: "zstd", Schedule: api.Schedule{Kind: "daily", Hour: 2, Timezone: "UTC"},
					Retention: api.Retention{Days: 7}, Failure: api.Failure{RetryInterval: 5, Timeout: 60}})
				createErr <- err
			}()
			var cErr error
			created := false
			select {
			case cErr = <-createErr:
				created = true
			case <-time.After(300 * time.Millisecond):
			}
			close(release)
			dErr := <-delErr
			if !created {
				cErr = <-createErr
			}
			assert.False(t, cErr == nil && dErr == nil, "新建任务与删除%s不能都成功", target)

			jobs, err := job_repo.Job().List(e.ctx)
			require.NoError(t, err)
			for _, j := range jobs {
				ds, err := datasource_repo.DataSource().Find(e.ctx, j.DataSourceID)
				require.NoError(t, err)
				st, err := storage_repo.Storage().Find(e.ctx, j.StorageID)
				require.NoError(t, err)
				assert.True(t, ds != nil && st != nil, "任务 %q 引用的数据源与存储都存在", j.Name)
			}
		})
	}
}
