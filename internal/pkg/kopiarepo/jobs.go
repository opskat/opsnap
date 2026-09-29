package kopiarepo

import (
	"cmp"
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kopia/kopia/repo"
	"github.com/kopia/kopia/repo/manifest"
	"github.com/kopia/kopia/snapshot"
	"github.com/kopia/kopia/snapshot/snapshotfs"

	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/l10n"
)

// ErrNotJobSnapshot 要删除的快照不存在或不属于该任务，未删除任何快照
var ErrNotJobSnapshot error = l10n.Errorf(code.KopiaErrNotJobSnapshot)

// JobRef 标识一个任务在仓库中的快照：来源 opsnap@opsnap:/<Prefix> 且标签 tag:job 为 JobID。
// 两者同时匹配才算本任务的快照：其他任务、非 OpsNap 客户端产生的、或同 ID 旧任务留在别的前缀下的快照都不算。
type JobRef struct {
	JobID int64
	// Prefix 任务的路径前缀，两端的 / 与写入时一样忽略
	Prefix string
}

// SnapshotInfo 仓库中的一份任务快照
type SnapshotInfo struct {
	ID        string
	StartTime time.Time
	Tags      SnapshotTags
}

// DeleteResult 删除任务全部快照的结果
type DeleteResult struct {
	Deleted int
	// Failed 找到但未能删除的份数
	Failed int
}

// Usage 一组快照在仓库中的占用
type Usage struct {
	// ExportBytes 各快照文件大小之和（导出总量，不去重）
	ExportBytes int64
	// PackedBytes 这些快照的文件引用的数据块去重、压缩后在仓库中的大小
	PackedBytes int64
}

func (j JobRef) source() snapshot.SourceInfo {
	return snapshot.SourceInfo{Host: sourceHost, UserName: sourceUser, Path: "/" + strings.Trim(j.Prefix, "/")}
}

// jobManifests 读取任务的全部快照清单
func (w *Writer) jobManifests(ctx context.Context, job JobRef) ([]*snapshot.Manifest, error) {
	src := job.source()
	ids, err := snapshot.ListSnapshotManifests(ctx, w.rep, &src, map[string]string{"tag:job": strconv.FormatInt(job.JobID, 10)})
	if err != nil {
		return nil, l10n.Errorf(code.KopiaListSnapshots, err)
	}
	mans, err := snapshot.LoadSnapshots(ctx, w.rep, ids)
	if err != nil {
		return nil, l10n.Errorf(code.KopiaLoadManifests, err)
	}
	return mans, nil
}

// ListJobSnapshots 列出任务的快照（见 JobRef），按开始时间升序
func (w *Writer) ListJobSnapshots(ctx context.Context, job JobRef) ([]SnapshotInfo, error) {
	mans, err := w.jobManifests(ctx, job)
	if err != nil {
		return nil, err
	}
	out := make([]SnapshotInfo, len(mans))
	for i, m := range mans {
		out[i] = SnapshotInfo{ID: string(m.ID), StartTime: m.StartTime.ToTime(), Tags: parseTags(m.Tags)}
	}
	slices.SortFunc(out, func(a, b SnapshotInfo) int {
		return cmp.Or(a.StartTime.Compare(b.StartTime), strings.Compare(a.ID, b.ID))
	})
	return out, nil
}

// parseTags 还原写入时的标签（见 SnapshotTags.labels）
func parseTags(l map[string]string) SnapshotTags {
	job, _ := strconv.ParseInt(l["tag:job"], 10, 64)
	run, _ := strconv.ParseInt(l["tag:run"], 10, 64)
	return SnapshotTags{JobID: job, RunID: run, Type: l["tag:type"], Kind: l["tag:kind"]}
}

// DeleteSnapshots 删除任务的指定快照，返回删除的份数。
// 任一 ID 不存在或不属于该任务（见 JobRef）时返回 ErrNotJobSnapshot，一份也不删；
// 删除在一个写入会话中完成，失败时同样一份也不删。数据块在仓库维护后才释放。
func (w *Writer) DeleteSnapshots(ctx context.Context, job JobRef, ids []string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	mans, err := w.jobManifests(ctx, job)
	if err != nil {
		return 0, err
	}
	own := make(map[manifest.ID]bool, len(mans))
	for _, m := range mans {
		own[m.ID] = true
	}
	var del []manifest.ID
	for _, id := range ids {
		mid := manifest.ID(id)
		if !own[mid] {
			return 0, l10n.Errorf(code.WrapColon, ErrNotJobSnapshot, id)
		}
		if !slices.Contains(del, mid) {
			del = append(del, mid)
		}
	}
	if err := w.deleteManifests(ctx, del); err != nil {
		return 0, err
	}
	return len(del), nil
}

// DeleteJobSnapshots 删除任务的全部快照（见 JobRef），用于删除任务时“同时删除快照”。
// 删除失败时 Failed 为找到的份数，仓库不变；读取快照列表失败时两者都为 0。
func (w *Writer) DeleteJobSnapshots(ctx context.Context, job JobRef) (DeleteResult, error) {
	mans, err := w.jobManifests(ctx, job)
	if err != nil {
		return DeleteResult{}, err
	}
	if len(mans) == 0 {
		return DeleteResult{}, nil
	}
	ids := make([]manifest.ID, len(mans))
	for i, m := range mans {
		ids[i] = m.ID
	}
	if err := w.deleteManifests(ctx, ids); err != nil {
		return DeleteResult{Failed: len(ids)}, err
	}
	return DeleteResult{Deleted: len(ids)}, nil
}

func (w *Writer) deleteManifests(ctx context.Context, ids []manifest.ID) error {
	err := repo.WriteSession(ctx, w.rep, repo.WriteSessionOptions{Purpose: "opsnap:delete"},
		func(ctx context.Context, rw repo.RepositoryWriter) error {
			for _, id := range ids {
				if err := deleteManifest(ctx, rw, id); err != nil {
					return err
				}
			}
			return nil
		})
	if err != nil {
		return l10n.Errorf(code.KopiaDeleteSnapshots, err)
	}
	return nil
}

// deleteManifest 删除一份清单。测试可替换
var deleteManifest = func(ctx context.Context, rw repo.RepositoryWriter, id manifest.ID) error {
	return rw.DeleteManifest(ctx, id)
}

// Usage 计算一组快照（重复的 ID 只算一份）的导出总量与去重、压缩后的仓库占用。
// 需要遍历这些快照引用的全部数据块的索引，快照越大越慢。任一 ID 不存在时报错。
func (w *Writer) Usage(ctx context.Context, ids []string) (*Usage, error) {
	var mans []*snapshot.Manifest
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		m, err := snapshot.LoadSnapshot(ctx, w.rep, manifest.ID(id))
		if err != nil {
			return nil, l10n.Errorf(code.KopiaReadSnapshot, id, err)
		}
		mans = append(mans, m)
	}
	u := &Usage{}
	if len(mans) == 0 {
		return u, nil
	}
	for _, m := range mans {
		if m.RootEntry != nil && m.RootEntry.DirSummary != nil {
			u.ExportBytes += m.RootEntry.DirSummary.TotalFileSize
		}
	}
	err := snapshotfs.CalculateStorageStats(ctx, w.rep, mans, func(m *snapshot.Manifest) error {
		u.PackedBytes = m.StorageStats.RunningTotal.PackedContentBytes
		return nil
	})
	if err != nil {
		return nil, l10n.Errorf(code.KopiaUsage, err)
	}
	return u, nil
}
