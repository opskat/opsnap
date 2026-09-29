package kopiarepo

import (
	"context"

	"github.com/kopia/kopia/repo"
	"github.com/kopia/kopia/repo/maintenance"
	"github.com/kopia/kopia/snapshot/snapshotmaintenance"

	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/l10n"
)

// MaintenanceMode 仓库维护方式
type MaintenanceMode string

const (
	// MaintenanceQuick 快速维护：整理索引等，每次删除快照后做一次
	MaintenanceQuick MaintenanceMode = "quick"
	// MaintenanceFull 完整维护：回收不再被任何快照引用的数据块，释放空间
	MaintenanceFull MaintenanceMode = "full"
)

// runMaintenance 执行 kopia 维护。测试可替换
var runMaintenance = func(ctx context.Context, dw repo.DirectRepositoryWriter, mode maintenance.Mode) error {
	// force：仓库的维护者可能未设置或是别的 kopia 客户端，OpsNap 仍要维护自己用的仓库。
	// SafetyFull：其他任务可能正在向同一仓库写快照，回收要留足安全间隔（数据块在删除后要经过两次完整维护、约一天以上才释放）
	return snapshotmaintenance.Run(ctx, dw, mode, true, maintenance.SafetyFull)
}

// Maintain 对仓库做快速或完整维护。同一位置的维护在本进程内依次进行。
// kopia 在会话配置旁加维护锁，配置目录在 Close 之前一直存在。
func (w *Writer) Maintain(ctx context.Context, mode MaintenanceMode) error {
	var km maintenance.Mode
	switch mode {
	case MaintenanceQuick:
		km = maintenance.ModeQuick
	case MaintenanceFull:
		km = maintenance.ModeFull
	default:
		return l10n.Errorf(code.KopiaMaintainMode, string(mode))
	}
	dr, ok := w.rep.(repo.DirectRepository)
	if !ok {
		return l10n.Errorf(code.KopiaMaintainUnsupported)
	}
	defer w.m.lockMaintenance(w.key)()
	err := repo.DirectWriteSession(ctx, dr, repo.WriteSessionOptions{Purpose: "opsnap:maintenance"},
		func(ctx context.Context, dw repo.DirectRepositoryWriter) error {
			return runMaintenance(ctx, dw, km)
		})
	if err != nil {
		return l10n.Errorf(code.KopiaMaintain, mode, err)
	}
	return nil
}

// lockMaintenance 锁住该位置的维护，返回解锁函数
func (m *Manager) lockMaintenance(key string) func() {
	return lockIn(&m.mu, m.maintLocks, key)
}
