package kopiarepo

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/kopia/kopia/repo"
	"github.com/kopia/kopia/repo/manifest"
	"github.com/kopia/kopia/snapshot"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeJobSnapshot 写一份任务快照，返回快照 ID
func writeJobSnapshot(t *testing.T, w *Writer, prefix string, jobID, runID int64, data []byte) string {
	t.Helper()
	res, err := w.WriteSnapshot(context.Background(), SnapshotRequest{
		Prefix: prefix,
		Tags:   SnapshotTags{JobID: jobID, RunID: runID, Type: "full", Kind: "mysql"},
		Files:  []SnapshotFile{{Name: "all.sql", Reader: bytes.NewReader(data)}},
	})
	require.NoError(t, err)
	return res.ID
}

// copySnapshotAs 把已有快照的清单另存为指定来源与标签的一份快照，模拟非 OpsNap 产生或其他前缀的快照
func copySnapshotAs(t *testing.T, w *Writer, id string, src snapshot.SourceInfo, tags map[string]string) string {
	t.Helper()
	ctx := context.Background()
	man, err := snapshot.LoadSnapshot(ctx, w.rep, manifest.ID(id))
	require.NoError(t, err)
	man.ID = ""
	man.Source = src
	man.Tags = tags
	var newID manifest.ID
	require.NoError(t, repo.WriteSession(ctx, w.rep, repo.WriteSessionOptions{Purpose: "test"},
		func(ctx context.Context, rw repo.RepositoryWriter) error {
			newID, err = snapshot.SaveSnapshot(ctx, rw, man)
			return err
		}))
	return string(newID)
}

// jobFixture 仓库中有任务 1（两份）、任务 2（一份），以及两份带 tag:job=1 但不属于任务 1 的快照
type jobFixture struct {
	loc                   Location
	w                     *Writer
	job1                  JobRef
	job2                  JobRef
	run1, run2, other     string
	foreignHost, otherPfx string
}

func newJobFixture(t *testing.T) *jobFixture {
	t.Helper()
	loc := newTestRepo(t)
	w := openTestWriter(t, NewManager(t.TempDir()), loc)
	f := &jobFixture{loc: loc, w: w, job1: JobRef{JobID: 1, Prefix: "mysql/a"}, job2: JobRef{JobID: 2, Prefix: "mysql/b"}}
	f.run1 = writeJobSnapshot(t, w, "mysql/a", 1, 11, randomBytes(t, 64<<10))
	f.run2 = writeJobSnapshot(t, w, "mysql/a", 1, 12, randomBytes(t, 64<<10))
	f.other = writeJobSnapshot(t, w, "mysql/b", 2, 21, randomBytes(t, 64<<10))
	job1Tags := map[string]string{"tag:job": "1", "tag:run": "11", "tag:type": "full", "tag:kind": "mysql"}
	// 用户自己用 kopia 命令行在同一路径建的快照，恰好带了同样的标签
	f.foreignHost = copySnapshotAs(t, w, f.run1, snapshot.SourceInfo{Host: "laptop", UserName: "alice", Path: "/mysql/a"}, job1Tags)
	// 已删除的旧任务留下的快照，任务 ID 相同但前缀不同
	f.otherPfx = copySnapshotAs(t, w, f.run1, snapshot.SourceInfo{Host: sourceHost, UserName: sourceUser, Path: "/pg/old"}, job1Tags)
	require.Equal(t, 5, snapshotCount(t, loc))
	return f
}

func ids(snaps []SnapshotInfo) []string {
	out := make([]string, len(snaps))
	for i, s := range snaps {
		out[i] = s.ID
	}
	return out
}

func TestListJobSnapshots(t *testing.T) {
	ctx := context.Background()
	f := newJobFixture(t)

	got, err := f.w.ListJobSnapshots(ctx, f.job1)
	require.NoError(t, err)
	require.Equal(t, []string{f.run1, f.run2}, ids(got), "只含任务 1 的快照，按开始时间升序")
	assert.Equal(t, SnapshotTags{JobID: 1, RunID: 11, Type: "full", Kind: "mysql"}, got[0].Tags)
	assert.Equal(t, SnapshotTags{JobID: 1, RunID: 12, Type: "full", Kind: "mysql"}, got[1].Tags)
	assert.False(t, got[0].StartTime.IsZero())
	assert.True(t, got[0].StartTime.Before(got[1].StartTime))

	slashed, err := f.w.ListJobSnapshots(ctx, JobRef{JobID: 1, Prefix: "/mysql/a/"})
	require.NoError(t, err)
	assert.Equal(t, ids(got), ids(slashed), "前缀两端的 / 与写入时一样忽略")

	none, err := f.w.ListJobSnapshots(ctx, JobRef{JobID: 3, Prefix: "mysql/c"})
	require.NoError(t, err)
	assert.Empty(t, none)
}

func TestDeleteSnapshots(t *testing.T) {
	ctx := context.Background()

	t.Run("只删除指定的本任务快照", func(t *testing.T) {
		f := newJobFixture(t)
		n, err := f.w.DeleteSnapshots(ctx, f.job1, []string{f.run1})
		require.NoError(t, err)
		assert.Equal(t, 1, n)
		left, err := f.w.ListJobSnapshots(ctx, f.job1)
		require.NoError(t, err)
		assert.Equal(t, []string{f.run2}, ids(left))
		assert.Equal(t, 4, snapshotCount(t, f.loc), "另一连接看到删除已写入仓库")
	})

	t.Run("含有不属于本任务的快照时拒绝，一份也不删", func(t *testing.T) {
		f := newJobFixture(t)
		for name, bad := range map[string]string{
			"其他任务":     f.other,
			"非 OpsNap": f.foreignHost,
			"其他前缀":     f.otherPfx,
			"不存在":      "k0123456789abcdef0123456789abcdef",
		} {
			n, err := f.w.DeleteSnapshots(ctx, f.job1, []string{f.run1, bad})
			require.ErrorIs(t, err, ErrNotJobSnapshot, name)
			assert.Equal(t, 0, n, name)
		}
		assert.Equal(t, 5, snapshotCount(t, f.loc))
	})

	t.Run("空列表什么也不做", func(t *testing.T) {
		f := newJobFixture(t)
		n, err := f.w.DeleteSnapshots(ctx, f.job1, nil)
		require.NoError(t, err)
		assert.Equal(t, 0, n)
		assert.Equal(t, 5, snapshotCount(t, f.loc))
	})
}

func TestDeleteJobSnapshots(t *testing.T) {
	ctx := context.Background()

	t.Run("删除本任务全部快照，其他任务和非 OpsNap 快照不受影响", func(t *testing.T) {
		f := newJobFixture(t)
		res, err := f.w.DeleteJobSnapshots(ctx, f.job1)
		require.NoError(t, err)
		assert.Equal(t, DeleteResult{Deleted: 2}, res)

		r := inspectRepo(t, f.loc)
		left, err := snapshot.ListSnapshotManifests(ctx, r, nil, nil)
		require.NoError(t, err)
		assert.ElementsMatch(t, []manifest.ID{manifest.ID(f.other), manifest.ID(f.foreignHost), manifest.ID(f.otherPfx)}, left)
	})

	t.Run("删除失败时报告未能删除的份数，仓库不变", func(t *testing.T) {
		f := newJobFixture(t)
		orig := deleteManifest
		t.Cleanup(func() { deleteManifest = orig })
		boom := errors.New("写入清单失败")
		deleteManifest = func(context.Context, repo.RepositoryWriter, manifest.ID) error { return boom }

		res, err := f.w.DeleteJobSnapshots(ctx, f.job1)
		require.ErrorIs(t, err, boom)
		assert.Equal(t, DeleteResult{Failed: 2}, res)
		assert.Equal(t, 5, snapshotCount(t, f.loc))
	})

	t.Run("没有快照时为零", func(t *testing.T) {
		f := newJobFixture(t)
		res, err := f.w.DeleteJobSnapshots(ctx, JobRef{JobID: 3, Prefix: "mysql/c"})
		require.NoError(t, err)
		assert.Equal(t, DeleteResult{}, res)
		assert.Equal(t, 5, snapshotCount(t, f.loc))
	})
}

func TestUsage(t *testing.T) {
	ctx := context.Background()
	loc := newTestRepo(t)
	w := openTestWriter(t, NewManager(t.TempDir()), loc)

	data := randomBytes(t, 1<<20)
	a := writeJobSnapshot(t, w, "mysql/a", 1, 1, data)
	same := writeJobSnapshot(t, w, "mysql/a", 1, 2, data)
	diff := writeJobSnapshot(t, w, "mysql/a", 1, 3, randomBytes(t, 1<<20))
	zeros := writeJobSnapshot(t, w, "mysql/z", 2, 1, make([]byte, 4<<20))

	one, err := w.Usage(ctx, []string{a})
	require.NoError(t, err)
	assert.Equal(t, int64(1<<20), one.ExportBytes)
	assert.GreaterOrEqual(t, one.PackedBytes, int64(1<<20), "随机数据压不小")
	assert.Less(t, one.PackedBytes, int64(1<<20)+64<<10)

	dup, err := w.Usage(ctx, []string{a, same})
	require.NoError(t, err)
	assert.Equal(t, int64(2<<20), dup.ExportBytes, "导出总量按份累加")
	assert.Equal(t, one.PackedBytes, dup.PackedBytes, "相同内容只计一次")

	two, err := w.Usage(ctx, []string{a, diff})
	require.NoError(t, err)
	assert.Greater(t, two.PackedBytes, 2*one.PackedBytes-64<<10, "不同内容各自计入")

	repeated, err := w.Usage(ctx, []string{a, a})
	require.NoError(t, err)
	assert.Equal(t, one, repeated, "重复的 ID 只算一份")

	z, err := w.Usage(ctx, []string{zeros})
	require.NoError(t, err)
	assert.Equal(t, int64(4<<20), z.ExportBytes)
	assert.Less(t, z.PackedBytes, z.ExportBytes/100, "按压缩后的大小计")

	empty, err := w.Usage(ctx, nil)
	require.NoError(t, err)
	assert.Equal(t, &Usage{}, empty)

	_, err = w.Usage(ctx, []string{"k0123456789abcdef0123456789abcdef"})
	require.Error(t, err)
}
