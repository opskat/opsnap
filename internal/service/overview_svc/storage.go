package overview_svc

import (
	"context"

	api "github.com/opskat/opsnap/internal/api/overview"
	"github.com/opskat/opsnap/internal/model/entity/storage_entity"
	"github.com/opskat/opsnap/internal/pkg/kopiarepo"
	"github.com/opskat/opsnap/internal/service/storage_svc"
)

// storages 存储目标与“存储占用”（spec「存储目标」）：仓库用量取存储上记录的值（运行、完整维护、测试连接之后读取），
// 不打开仓库；本地目录所在磁盘的用量每次读取。状态不是正常或读不到用量的存储给出原因，不计入总和
func storages(ctx context.Context, list []*storage_entity.Storage) ([]*api.Storage, api.StorageUsage) {
	rows := make([]*api.Storage, 0, len(list))
	var total api.StorageUsage
	for _, st := range list {
		row := &api.Storage{
			ID:              st.ID,
			Name:            st.Name,
			Kind:            st.Kind,
			Location:        st.Location("").String(),
			Status:          st.Status,
			Reason:          storage_svc.UsageReason(ctx, st),
			UsageRecordedAt: st.Usage.Checktime,
		}
		if st.Kind == string(kopiarepo.KindLocal) {
			row.Path = st.Path
			if d, err := kopiarepo.DiskUsage(st.Path); err == nil {
				row.Disk = &api.Disk{UsedBytes: d.Used, TotalBytes: d.Total, FreeBytes: d.Free}
			}
		}
		if row.Reason == "" {
			row.Readable = true
			row.Snapshots, row.PackedBytes, row.OriginalBytes = st.Usage.Snapshots, st.Usage.PackedBytes, st.Usage.OriginalBytes
			total.PackedBytes += row.PackedBytes
			total.OriginalBytes += row.OriginalBytes
		} else {
			total.Unreadable++
		}
		rows = append(rows, row)
	}
	if total.OriginalBytes > 0 {
		total.Savings = max(1-float64(total.PackedBytes)/float64(total.OriginalBytes), 0)
	}
	return rows, total
}
