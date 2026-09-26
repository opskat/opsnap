package channel_svc

import (
	"context"

	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/opskat/opsnap/internal/model/entity/channel_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/netchain"
)

// reasonCodes 一跳失败的原因对应的错误码
var reasonCodes = map[netchain.Reason]int{
	netchain.ReasonUnreachable:       code.ChannelReasonUnreachable,
	netchain.ReasonTimeout:           code.ChannelReasonTimeout,
	netchain.ReasonCanceled:          code.ChannelReasonCanceled,
	netchain.ReasonProtocol:          code.ChannelReasonProtocol,
	netchain.ReasonNegotiation:       code.ChannelReasonNegotiation,
	netchain.ReasonAuthFailed:        code.ChannelReasonAuthFailed,
	netchain.ReasonHostKeyUnknown:    code.ChannelReasonHostKeyUnknown,
	netchain.ReasonHostKeyChanged:    code.ChannelReasonHostKeyChanged,
	netchain.ReasonPassphraseMissing: code.ChannelPassphraseMissing,
	netchain.ReasonPassphraseWrong:   code.ChannelPassphraseWrong,
	netchain.ReasonKeyInvalid:        code.ChannelKeyInvalid,
}

// detailReasons 文案中带 %s 详情的原因
var detailReasons = map[int]bool{
	code.ChannelReasonUnreachable: true,
	code.ChannelReasonProtocol:    true,
	code.ChannelReasonNegotiation: true,
}

func kindLabel(kind string) string {
	if kind == channel_entity.KindSOCKS5 {
		return "SOCKS5"
	}
	return "SSH"
}

// hopStatus 把一跳失败转为可保存的原因码与详情；netchain 的错误不含秘密
func hopStatus(he *netchain.HopError) (int, channel_entity.HopStatus) {
	c, ok := reasonCodes[he.Reason]
	if !ok {
		c = code.ChannelReasonUnreachable
	}
	hs := channel_entity.HopStatus{Hop: he.Index, Name: he.Name, Kind: string(he.Kind)}
	if detailReasons[c] {
		hs.Detail = he.Err.Error()
	}
	return c, hs
}

func hopArgs(ctx context.Context, reason int, hs channel_entity.HopStatus) []any {
	text := i18n.T(ctx, reason)
	if detailReasons[reason] {
		text = i18n.T(ctx, reason, hs.Detail)
	}
	return []any{hs.Hop, hs.Name, kindLabel(hs.Kind), text}
}

// hopMessage 按请求语言拼出“第 N 跳 名称（类型）：原因”
func hopMessage(ctx context.Context, reason int, hs channel_entity.HopStatus) string {
	return i18n.T(ctx, code.ChannelHopFailed, hopArgs(ctx, reason, hs)...)
}

// HopMessage 按请求语言描述链路中一跳的失败，例如“第 1 跳 office-socks（SOCKS5）：认证失败”
func HopMessage(ctx context.Context, he *netchain.HopError) string {
	reason, hs := hopStatus(he)
	return hopMessage(ctx, reason, hs)
}

// HopError 把一跳失败转为接口错误（ChannelHopFailed，文案同 HopMessage）
func HopError(ctx context.Context, he *netchain.HopError) error {
	reason, hs := hopStatus(he)
	return i18n.NewError(ctx, code.ChannelHopFailed, hopArgs(ctx, reason, hs)...)
}
