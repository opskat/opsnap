package kopiarepo

import (
	"context"

	"github.com/kopia/kopia/repo"
	"github.com/kopia/kopia/snapshot"
	"github.com/kopia/kopia/snapshot/snapshotfs"

	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/l10n"
)

// RepoStats 整个仓库的快照数与用量（含其他任务与非 OpsNap 产生的快照）
type RepoStats struct {
	Snapshots int
	// Usage 全部快照的原始总大小（ExportBytes）与去重、压缩后的占用（PackedBytes）；统计失败时为 nil，原因在 UsageErr
	Usage    *Usage
	UsageErr error
}

// RepoStats 读取仓库的快照数与全部快照的用量。列出快照失败时返回错误；只是统计用量失败时原因在 RepoStats.UsageErr
func (w *Writer) RepoStats(ctx context.Context) (*RepoStats, error) {
	return repoStats(ctx, w.rep)
}

// repoStats 列出仓库中的全部快照并统计用量
func repoStats(ctx context.Context, r repo.Repository) (*RepoStats, error) {
	ids, err := snapshot.ListSnapshotManifests(ctx, r, nil, nil)
	if err != nil {
		return nil, l10n.Errorf(code.KopiaListSnapshots, err)
	}
	out := &RepoStats{Snapshots: len(ids)}
	mans, err := snapshot.LoadSnapshots(ctx, r, ids)
	if err != nil {
		out.UsageErr = l10n.Errorf(code.KopiaLoadManifests, err)
		return out, nil
	}
	out.Usage, out.UsageErr = usageOf(ctx, r, mans)
	return out, nil
}

// usageOf 一组快照的原始总大小与引用的数据块去重、压缩后的大小。需要遍历这些快照引用的全部数据块的索引
func usageOf(ctx context.Context, r repo.Repository, mans []*snapshot.Manifest) (*Usage, error) {
	u := &Usage{}
	if len(mans) == 0 {
		return u, nil
	}
	for _, m := range mans {
		if m.RootEntry != nil && m.RootEntry.DirSummary != nil {
			u.ExportBytes += m.RootEntry.DirSummary.TotalFileSize
		}
	}
	err := snapshotfs.CalculateStorageStats(ctx, r, mans, func(m *snapshot.Manifest) error {
		u.PackedBytes = m.StorageStats.RunningTotal.PackedContentBytes
		return nil
	})
	if err != nil {
		return nil, l10n.Errorf(code.KopiaUsage, err)
	}
	return u, nil
}
