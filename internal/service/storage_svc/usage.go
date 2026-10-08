package storage_svc

import (
	"context"
	"errors"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opsnap/internal/model/entity/storage_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/kopiarepo"
	"github.com/opskat/opsnap/internal/pkg/l10n"
	"github.com/opskat/opsnap/internal/repository/storage_repo"
)

// 仓库用量（docs/specs/2026-09-29-overview-docker.md「存储目标」）：新建、更改位置、测试连接、重新解锁、
// 每次运行与完整维护之后读取并记录在存储上，概览只读记录的值，不打开仓库

func (s *storageSvc) RecordUsage(ctx context.Context, id int64, w *kopiarepo.Writer) {
	stats, err := w.RepoStats(ctx)
	if err != nil {
		stats = &kopiarepo.RepoStats{UsageErr: err}
	}
	s.recordStats(ctx, id, stats)
}

func (s *storageSvc) RecordUsageError(ctx context.Context, id int64, err error) {
	if errors.Is(err, ErrNotReady) {
		return
	}
	s.recordStats(ctx, id, &kopiarepo.RepoStats{UsageErr: err})
}

// recordStats 记录读到的快照数与用量；读取失败时只记录原因（中文、英文），保留上一次的数字。
// 被取消的读取不记录：它说明不了存储的情况
func (s *storageSvc) recordStats(ctx context.Context, id int64, stats *kopiarepo.RepoStats) {
	if errors.Is(stats.UsageErr, context.Canceled) || ctx.Err() != nil {
		return
	}
	now := s.now().Unix()
	var err error
	if stats.UsageErr != nil {
		err = storage_repo.Storage().SetUsageError(ctx, id,
			l10n.Text(l10n.ZhCN, stats.UsageErr), l10n.Text(l10n.En, stats.UsageErr), now)
	} else {
		err = storage_repo.Storage().SetUsage(ctx, id, storage_entity.Usage{Snapshots: stats.Snapshots,
			PackedBytes: stats.Usage.PackedBytes, OriginalBytes: stats.Usage.ExportBytes, Checktime: now})
	}
	if err != nil {
		logger.Ctx(ctx).Warn("记录存储用量失败", zap.Int64("storage_id", id), zap.Error(err))
	}
}

// UsageReason 存储记录的仓库用量为什么不能用，按 ctx 的语言；状态为正常且最近一次读取成功时为空。
// 状态不是正常时给出状态的原因，如“无法连接：目标位置不是 kopia 仓库”
func UsageReason(ctx context.Context, st *storage_entity.Storage) string {
	switch {
	case st.Status != storage_entity.StatusOK:
		return statusReason(ctx, st)
	case st.Usage.Checktime == 0:
		return i18n.T(ctx, code.StorageUsageNotRead)
	case st.Usage.Error != "":
		return i18n.T(ctx, code.StorageUsageUnreadable, l10n.Pick(ctx, st.Usage.Error, st.Usage.ErrorEn))
	}
	return ""
}

// statusReason 状态不是正常的原因：无法连接时以“无法连接：”开头
func statusReason(ctx context.Context, st *storage_entity.Storage) string {
	msg := statusMessage(ctx, st)
	switch {
	case msg == "":
		return i18n.T(ctx, code.StorageErrNotReady)
	case st.Status == storage_entity.StatusUnreachable && st.StatusCode != code.StorageUnreachable:
		return i18n.T(ctx, code.StorageUnreachable, msg)
	}
	return msg
}

// statusMessage 最近一次测试失败的说明，正常时为空
func statusMessage(ctx context.Context, st *storage_entity.Storage) string {
	switch {
	case st.StatusCode == 0:
		return ""
	case detailCodes[st.StatusCode]:
		return i18n.T(ctx, st.StatusCode, st.StatusDetail)
	}
	return i18n.T(ctx, st.StatusCode)
}
