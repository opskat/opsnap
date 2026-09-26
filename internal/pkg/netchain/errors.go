package netchain

import (
	"context"
	"errors"
	"fmt"
	"net"
)

// Reason 一跳失败的原因，供上层映射为状态与提示
type Reason string

const (
	// ReasonUnreachable 连不上这一跳（拒绝连接、无法解析、上一跳无法转发到它）
	ReasonUnreachable Reason = "unreachable"
	// ReasonTimeout 整体超时发生在这一跳
	ReasonTimeout Reason = "timeout"
	// ReasonCanceled 调用方取消
	ReasonCanceled Reason = "canceled"
	// ReasonProtocol 对端不是 SSH / SOCKS5，或握手出错
	ReasonProtocol Reason = "protocol"
	// ReasonNegotiation SOCKS5 方法协商失败（例如代理要求认证而未配置）
	ReasonNegotiation Reason = "negotiation"
	// ReasonAuthFailed SSH 或 SOCKS5 认证被拒绝
	ReasonAuthFailed Reason = "auth_failed"
	// ReasonHostKeyUnknown SSH 主机密钥尚未确认
	ReasonHostKeyUnknown Reason = "host_key_unknown"
	// ReasonHostKeyChanged SSH 主机密钥与保存的不一致
	ReasonHostKeyChanged Reason = "host_key_changed"
	// ReasonPassphraseMissing 私钥已加密但没有口令
	ReasonPassphraseMissing Reason = "passphrase_missing"
	// ReasonPassphraseWrong 私钥口令错误
	ReasonPassphraseWrong Reason = "passphrase_wrong"
	// ReasonKeyInvalid 私钥格式无法识别
	ReasonKeyInvalid Reason = "key_invalid"
)

var (
	ErrTooManyHops       = errors.New("链路超过 5 跳")
	ErrCycle             = errors.New("链路成环")
	ErrInvalidHop        = errors.New("通道配置不完整")
	ErrAuthFailed        = errors.New("认证失败")
	ErrNegotiation       = errors.New("SOCKS5 方法协商失败")
	ErrProtocol          = errors.New("协议错误")
	ErrHostKeyUnknown    = errors.New("主机密钥未确认")
	ErrHostKeyChanged    = errors.New("主机密钥已变化")
	ErrPassphraseMissing = errors.New("私钥已加密，缺少口令")
	ErrPassphraseWrong   = errors.New("私钥口令错误")
	ErrKeyInvalid        = errors.New("无法识别的私钥格式")
)

// HopError 第 Index 跳（从 1 开始）失败。只记录通道的标识与地址，不含任何秘密。
// DialSSH 的目标主机按链路之后的一跳编号（len(hops)+1）。
type HopError struct {
	Index  int
	ID     int64
	Name   string
	Kind   Kind
	Addr   string
	Reason Reason
	Err    error
}

func (e *HopError) Error() string {
	return fmt.Sprintf("第 %d 跳 %s（%s）：%v", e.Index, e.Name, e.Kind.label(), e.Err)
}

func (e *HopError) Unwrap() error { return e.Err }

// HostKeyError 主机密钥未确认（Changed=false）或与保存的不一致（Changed=true）。
// Fingerprint 为主机出示的密钥的 SHA256 指纹，Saved 为保存的指纹。
type HostKeyError struct {
	Changed     bool
	KeyType     string
	Fingerprint string
	Saved       string
}

func (e *HostKeyError) Error() string {
	if e.Changed {
		return fmt.Sprintf("%v：保存的是 %s，现在出示的是 %s %s", ErrHostKeyChanged, e.Saved, e.KeyType, e.Fingerprint)
	}
	return fmt.Sprintf("%v：%s %s", ErrHostKeyUnknown, e.KeyType, e.Fingerprint)
}

func (e *HostKeyError) Unwrap() error {
	if e.Changed {
		return ErrHostKeyChanged
	}
	return ErrHostKeyUnknown
}

func (k Kind) label() string {
	switch k {
	case KindSSH:
		return "SSH"
	case KindSOCKS5:
		return "SOCKS5"
	}
	return string(k)
}

// newHopError 把 err 归因到第 index 跳；err 已是 *HopError（更早的一跳失败）时原样返回
func newHopError(ctx context.Context, index int, h Hop, err error) error {
	var he *HopError
	if errors.As(err, &he) {
		return err
	}
	reason := classify(ctx, err)
	switch reason {
	case ReasonTimeout:
		err = fmt.Errorf("连接超时: %w", err)
	case ReasonCanceled:
		err = fmt.Errorf("已取消: %w", err)
	}
	return &HopError{Index: index, ID: h.ID, Name: h.Name, Kind: h.Kind, Addr: h.addr(), Reason: reason, Err: err}
}

func classify(ctx context.Context, err error) Reason {
	var netErr net.Error
	switch {
	case errors.Is(err, ErrHostKeyUnknown):
		return ReasonHostKeyUnknown
	case errors.Is(err, ErrHostKeyChanged):
		return ReasonHostKeyChanged
	case errors.Is(err, ErrPassphraseMissing):
		return ReasonPassphraseMissing
	case errors.Is(err, ErrPassphraseWrong):
		return ReasonPassphraseWrong
	case errors.Is(err, ErrKeyInvalid):
		return ReasonKeyInvalid
	case errors.Is(err, ErrAuthFailed):
		return ReasonAuthFailed
	case errors.Is(err, ErrNegotiation):
		return ReasonNegotiation
	// ctx 结束后连接被关闭，底层错误往往只是“use of closed connection”，以 ctx 为准
	case errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled):
		return ReasonCanceled
	case ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) || errors.As(err, &netErr) && netErr.Timeout():
		return ReasonTimeout
	case errors.Is(err, ErrProtocol):
		return ReasonProtocol
	}
	return ReasonUnreachable
}
