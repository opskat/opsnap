package kopiarepo

import (
	"context"
	"testing"

	"github.com/kopia/kopia/snapshot"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 整个仓库的快照数与用量：所有来源的快照都算，相同内容只计一次；写入会话与只读校验读到的相同
func TestRepoStats(t *testing.T) {
	ctx := context.Background()
	loc := newTestRepo(t)
	m := NewManager(t.TempDir())
	w := openTestWriter(t, m, loc)

	empty, err := w.RepoStats(ctx)
	require.NoError(t, err)
	assert.Equal(t, &RepoStats{Usage: &Usage{}}, empty)

	data := randomBytes(t, 1<<20)
	a := writeJobSnapshot(t, w, "mysql/a", 1, 1, data)
	writeJobSnapshot(t, w, "mysql/a", 1, 2, data)
	writeJobSnapshot(t, w, "mysql/b", 2, 1, make([]byte, 4<<20))
	// 用户自己用 kopia 命令行建的快照也在仓库里
	copySnapshotAs(t, w, a, snapshot.SourceInfo{Host: "laptop", UserName: "alice", Path: "/data"}, nil)

	stats, err := w.RepoStats(ctx)
	require.NoError(t, err)
	require.NoError(t, stats.UsageErr)
	assert.Equal(t, 4, stats.Snapshots)
	require.NotNil(t, stats.Usage)
	assert.Equal(t, int64(3<<20+4<<20), stats.Usage.ExportBytes, "原始总大小按份累加")
	one, err := w.Usage(ctx, []string{a})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, stats.Usage.PackedBytes, one.PackedBytes)
	assert.Less(t, stats.Usage.PackedBytes, one.PackedBytes+64<<10, "相同内容只计一次，全零数据压缩后几乎不占空间")

	verified, err := m.Verify(ctx, 1, loc, testKey)
	require.NoError(t, err)
	assert.Equal(t, stats, verified, "只读校验读到相同的快照数与用量")
}

func TestDiskUsage(t *testing.T) {
	d, err := DiskUsage(t.TempDir())
	require.NoError(t, err)
	assert.Positive(t, d.Total)
	assert.Positive(t, d.Used, "临时目录所在的文件系统不会是空的")
	assert.Positive(t, d.Free)
	assert.LessOrEqual(t, d.Used+d.Free, d.Total)

	_, err = DiskUsage("/nonexistent/opsnap-disk-usage")
	assert.Error(t, err)
}
