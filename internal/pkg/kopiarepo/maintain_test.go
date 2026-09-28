package kopiarepo

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kopia/kopia/repo"
	"github.com/kopia/kopia/repo/maintenance"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func maintenanceSchedule(t *testing.T, loc Location) *maintenance.Schedule {
	t.Helper()
	dr, ok := inspectRepo(t, loc).(repo.DirectRepository)
	require.True(t, ok)
	s, err := maintenance.GetSchedule(context.Background(), dr)
	require.NoError(t, err)
	return s
}

// setMaintenanceOwner 模拟仓库的维护已被别的 kopia 客户端认领
func setMaintenanceOwner(t *testing.T, w *Writer, owner string) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, repo.WriteSession(ctx, w.rep, repo.WriteSessionOptions{Purpose: "test"},
		func(ctx context.Context, rw repo.RepositoryWriter) error {
			p := maintenance.DefaultParams()
			p.Owner = owner
			return maintenance.SetParams(ctx, rw, &p)
		}))
}

func TestMaintain(t *testing.T) {
	ctx := context.Background()

	for _, owner := range []string{"", "alice@laptop"} {
		t.Run("快速与完整维护都会执行，维护者为 "+owner, func(t *testing.T) {
			loc := newTestRepo(t)
			root := t.TempDir()
			w := openTestWriter(t, NewManager(root), loc)
			writeJobSnapshot(t, w, "mysql/a", 1, 1, randomBytes(t, 64<<10))
			if owner != "" {
				setMaintenanceOwner(t, w, owner)
			}

			require.NoError(t, w.Maintain(ctx, MaintenanceQuick))
			s := maintenanceSchedule(t, loc)
			assert.False(t, s.NextQuickMaintenanceTime.IsZero(), "快速维护已执行")
			assert.True(t, s.NextFullMaintenanceTime.IsZero(), "快速维护不做完整维护")

			require.NoError(t, w.Maintain(ctx, MaintenanceFull))
			s = maintenanceSchedule(t, loc)
			assert.False(t, s.NextFullMaintenanceTime.IsZero(), "完整维护已执行")

			// 维护锁文件放在会话配置旁边，关闭后与配置一起删除
			require.NoError(t, w.Close(ctx))
			assert.Empty(t, leftFiles(t, root))
			assert.Equal(t, 1, snapshotCount(t, loc), "维护不删除现存快照")
		})
	}

	t.Run("不认识的维护方式报错", func(t *testing.T) {
		loc := newTestRepo(t)
		w := openTestWriter(t, NewManager(t.TempDir()), loc)
		require.Error(t, w.Maintain(ctx, "auto"))
	})

	t.Run("同一存储的维护在本进程内依次进行", func(t *testing.T) {
		loc := newTestRepo(t)
		m := NewManager(t.TempDir())
		w1 := openTestWriter(t, m, loc)
		w2 := openTestWriter(t, m, loc)

		orig := runMaintenance
		t.Cleanup(func() { runMaintenance = orig })
		entered := make(chan struct{}, 2)
		release := make(chan struct{})
		var mu sync.Mutex
		active, maxActive := 0, 0
		runMaintenance = func(ctx context.Context, dw repo.DirectRepositoryWriter, _ maintenance.Mode) error {
			// 维护期间会话配置目录必须存在，kopia 在其中加锁
			if _, err := os.Stat(filepath.Dir(dw.ConfigFilename())); err != nil {
				return err
			}
			mu.Lock()
			active++
			maxActive = max(maxActive, active)
			mu.Unlock()
			entered <- struct{}{}
			<-release
			mu.Lock()
			active--
			mu.Unlock()
			return nil
		}

		errs := make(chan error, 2)
		go func() { errs <- w1.Maintain(ctx, MaintenanceQuick) }()
		select {
		case <-entered:
		case err := <-errs:
			t.Fatalf("维护没有执行就结束了: %v", err)
		case <-time.After(10 * time.Second):
			t.Fatal("维护没有执行")
		}
		go func() { errs <- w2.Maintain(ctx, MaintenanceQuick) }()
		select {
		case <-entered:
			t.Fatal("第二个维护在第一个结束前就开始了")
		case <-time.After(300 * time.Millisecond):
		}
		close(release)
		require.NoError(t, <-errs)
		require.NoError(t, <-errs)
		assert.Len(t, entered, 1, "第二个维护在第一个结束后执行")
		assert.Equal(t, 1, maxActive)
	})
}
