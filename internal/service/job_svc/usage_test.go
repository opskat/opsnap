package job_svc

import (
	"bytes"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opsnap/internal/model/entity/job_entity"
	"github.com/opskat/opsnap/internal/model/entity/storage_entity"
	"github.com/opskat/opsnap/internal/pkg/kopiarepo"
	"github.com/opskat/opsnap/internal/repository/storage_repo"
	"github.com/opskat/opsnap/internal/service/storage_svc"
)

func (e *runEnv) usage() storage_entity.Usage {
	e.t.Helper()
	st, err := storage_repo.Storage().Find(e.ctx, e.storage)
	require.NoError(e.t, err)
	return st.Usage
}

// writeOther 绕过运行向存储写一份其他来源的快照（不记录用量），返回写入的字节数
func (e *runEnv) writeOther(data []byte) int64 {
	e.t.Helper()
	e.withWriter(func(w *kopiarepo.Writer) {
		_, err := w.WriteSnapshot(e.ctx, kopiarepo.SnapshotRequest{Prefix: "other", Tags: kopiarepo.SnapshotTags{JobID: 99},
			Files: []kopiarepo.SnapshotFile{{Name: "data.bin", Reader: bytes.NewReader(data)}}})
		require.NoError(e.t, err)
	})
	return int64(len(data))
}

// 每次运行之后记录存储的仓库用量（整个仓库，含其他来源的快照）；打不开存储时记录原因并保留上一次的数字
func TestRunRecordsStorageUsage(t *testing.T) {
	e := newRunEnv(t)
	other := e.writeOther(bytes.Repeat([]byte("x"), 1<<20))
	run := e.manual()
	require.Equal(t, job_entity.RunSuccess, run.Status, run.Reason)

	u := e.usage()
	assert.Equal(t, 2, u.Snapshots, "整个仓库的快照数")
	assert.Equal(t, other+run.ExportedBytes, u.OriginalBytes)
	assert.Positive(t, u.PackedBytes)
	assert.Empty(t, u.Error)
	assert.Positive(t, u.Checktime)

	// 失败的运行只要打开过存储，也在结束后更新
	e.writeOther(bytes.Repeat([]byte("y"), 1<<20))
	e.tool("pg_dump", "pg_dump (PostgreSQL) 16.4", pgDumpFail)
	failed := e.manual()
	require.Equal(t, job_entity.RunFailed, failed.Status)
	assert.Equal(t, 3, e.usage().Snapshots)

	// 存储位置已不是仓库：运行在准备时打不开存储，记录原因，保留上一次读到的数字
	st, err := storage_repo.Storage().Find(e.ctx, e.storage)
	require.NoError(t, err)
	require.NoError(t, os.Rename(st.Path, st.Path+".moved"))
	failed = e.manual()
	require.Equal(t, job_entity.RunFailed, failed.Status)
	assert.Equal(t, job_entity.StepPrepare, failed.FailedStep)
	u = e.usage()
	assert.Equal(t, 3, u.Snapshots)
	assert.NotEmpty(t, u.Error)
	assert.NotEmpty(t, u.ErrorEn)
}

// 完整维护之后记录存储用量；状态不是正常时不维护也不记录
func TestFullMaintenanceRecordsStorageUsage(t *testing.T) {
	e := newRunEnv(t)
	size := e.writeOther(bytes.Repeat([]byte("z"), 1<<20))
	require.NoError(t, fullMaintenance(e.ctx, e.storage))
	u := e.usage()
	assert.Equal(t, 1, u.Snapshots)
	assert.Equal(t, size, u.OriginalBytes)
	assert.Positive(t, u.PackedBytes)

	e.writeOther(bytes.Repeat([]byte("w"), 1<<20))
	e.setStorageStatus(storage_entity.StatusUnreachable)
	require.ErrorIs(t, fullMaintenance(e.ctx, e.storage), storage_svc.ErrNotReady)
	assert.Equal(t, u, e.usage(), "状态不是正常：不打开存储，也不记录原因（由状态说明）")
}
