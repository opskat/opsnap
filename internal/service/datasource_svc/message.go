package datasource_svc

import (
	"context"
	"errors"
	"strings"

	"github.com/cago-frame/cago/pkg/i18n"

	api "github.com/opskat/opsnap/internal/api/datasource"
	"github.com/opskat/opsnap/internal/model/entity/datasource_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/dsconn"
	"github.com/opskat/opsnap/internal/pkg/netchain"
	"github.com/opskat/opsnap/internal/service/channel_svc"
)

// reasonCodes 数据源本身连接失败的原因对应的错误码
var reasonCodes = map[dsconn.Reason]int{
	dsconn.ReasonUnreachable: code.DataSourceReasonUnreachable,
	dsconn.ReasonTimeout:     code.DataSourceReasonTimeout,
	dsconn.ReasonCanceled:    code.DataSourceReasonCanceled,
	dsconn.ReasonAuthFailed:  code.DataSourceReasonAuthFailed,
	dsconn.ReasonTLS:         code.DataSourceReasonTLS,
	dsconn.ReasonCertificate: code.DataSourceReasonCertificate,
	dsconn.ReasonFailed:      code.DataSourceReasonFailed,
}

// withoutDetail 文案中不带原文的原因（原文只是“连接超时”“已取消”的重复）
var withoutDetail = map[int]bool{
	code.DataSourceReasonTimeout:  true,
	code.DataSourceReasonCanceled: true,
}

func reasonCode(r dsconn.Reason) int {
	if c, ok := reasonCodes[r]; ok {
		return c
	}
	return code.DataSourceReasonFailed
}

// kindLabel 一跳的类型名称：通道为 SSH / SOCKS5，数据源为 MySQL / PostgreSQL / SSH（服务器文件）
func kindLabel(kind string) string {
	switch kind {
	case string(netchain.KindSOCKS5):
		return "SOCKS5"
	case datasource_entity.KindMySQL:
		return "MySQL"
	case datasource_entity.KindPostgreSQL:
		return "PostgreSQL"
	}
	return "SSH"
}

// scrub 去掉文本中出现的秘密；dsconn 已经处理过，这里再保证一次
func scrub(text string, secrets ...string) string {
	for _, s := range secrets {
		if s != "" {
			text = strings.ReplaceAll(text, s, "******")
		}
	}
	return text
}

// targetArgs 数据源本身失败时“第 N 跳 名称（类型）：原因”的参数
func targetArgs(ctx context.Context, reason int, hs datasource_entity.HopStatus) []any {
	text := i18n.T(ctx, reason)
	if !withoutDetail[reason] {
		text = i18n.T(ctx, reason, hs.Detail)
	}
	return []any{hs.Hop, hs.Name, kindLabel(hs.Kind), text}
}

// targetStatus 把数据源本身的连接失败转为可保存的原因码与详情
func targetStatus(de *dsconn.Error, hop int, ds *datasource_entity.DataSource, secrets ...string) (int, datasource_entity.HopStatus) {
	return reasonCode(de.Reason), datasource_entity.HopStatus{
		Hop: hop, Name: ds.Name, Kind: ds.Kind, Reason: string(de.Reason), Detail: scrub(de.Msg, secrets...),
	}
}

// hopStatus 把链路中一跳（或服务器文件的目标主机）的失败转为可保存的详情；chainLen 为链路中通道的个数
func hopStatus(he *netchain.HopError, chainLen int) datasource_entity.HopStatus {
	hs := datasource_entity.HopStatus{Hop: he.Index, Name: he.Name, Kind: string(he.Kind), Reason: string(he.Reason)}
	if he.Err != nil {
		hs.Detail = he.Err.Error()
	}
	if he.Index <= chainLen {
		hs.ChannelID = he.ID
	}
	return hs
}

// statusMessage 按请求语言描述保存的失败状态
func statusMessage(ctx context.Context, ds *datasource_entity.DataSource) string {
	hs := ds.HopStatus()
	if ds.StatusCode == code.ChannelHopFailed {
		// 链路中一跳或目标主机的 SSH 失败：文案与通道一致
		return channel_svc.HopMessage(ctx, &netchain.HopError{
			Index: hs.Hop, ID: hs.ChannelID, Name: hs.Name, Kind: netchain.Kind(hs.Kind),
			Reason: netchain.Reason(hs.Reason), Err: errors.New(hs.Detail),
		})
	}
	return i18n.T(ctx, code.DataSourceTestFailed, targetArgs(ctx, ds.StatusCode, hs)...)
}

func failedHop(ds *datasource_entity.DataSource) *api.FailedHop {
	if ds.Status == datasource_entity.StatusOK || ds.StatusDetail == "" {
		return nil
	}
	hs := ds.HopStatus()
	return &api.FailedHop{Hop: hs.Hop, ChannelID: hs.ChannelID, Name: hs.Name, Kind: hs.Kind}
}

// fieldError 把 TLS 证书与私钥的问题转为对应字段的错误码
func fieldError(ctx context.Context, fe *dsconn.FieldError) error {
	switch fe.Field {
	case "ca":
		return i18n.NewError(ctx, code.DataSourceCAInvalid)
	case "client_cert":
		return i18n.NewError(ctx, code.DataSourceClientCertInvalid)
	}
	return i18n.NewError(ctx, code.DataSourceClientKeyInvalid)
}

// keyError 把 SSH 私钥解析错误转为对应字段的错误码
func keyError(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, netchain.ErrPassphraseMissing):
		return i18n.NewError(ctx, code.DataSourcePassphraseMissing)
	case errors.Is(err, netchain.ErrPassphraseWrong):
		return i18n.NewError(ctx, code.DataSourcePassphraseWrong)
	}
	return i18n.NewError(ctx, code.DataSourceKeyInvalid)
}
