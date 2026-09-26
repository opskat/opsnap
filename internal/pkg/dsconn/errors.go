package dsconn

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"strings"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/opskat/opsnap/internal/pkg/netchain"
)

// Reason 数据源本身连接失败的原因，供上层映射为状态与提示
type Reason string

const (
	// ReasonUnreachable 连不上数据源（主机不存在、拒绝连接、链路末端无法转发到它）
	ReasonUnreachable Reason = "unreachable"
	// ReasonTimeout 整体超时
	ReasonTimeout Reason = "timeout"
	// ReasonCanceled 调用方取消
	ReasonCanceled Reason = "canceled"
	// ReasonAuthFailed 认证失败
	ReasonAuthFailed Reason = "auth_failed"
	// ReasonTLS TLS 握手失败，或服务端不支持 TLS
	ReasonTLS Reason = "tls"
	// ReasonCertificate 服务端证书校验失败
	ReasonCertificate Reason = "certificate"
	// ReasonFailed 其他失败，原文见 Error()
	ReasonFailed Reason = "failed"
)

// Error 数据源本身连接失败。Msg 为驱动或服务端的原文（已去掉秘密），不保留原始错误以免秘密经 Unwrap 泄露
type Error struct {
	Reason Reason
	Msg    string
}

func (e *Error) Error() string { return e.Msg }

// FieldError 某个字段的内容无法使用，Field 为 "ca"、"client_cert" 或 "client_key"
type FieldError struct {
	Field string
	Err   error
}

func (e *FieldError) Error() string { return e.Field + ": " + e.Err.Error() }

func (e *FieldError) Unwrap() error { return e.Err }

// dialError 拨号数据源本身失败（链路各跳正常），用于把驱动包装过的错误归为 unreachable
type dialError struct{ err error }

func (e *dialError) Error() string { return e.err.Error() }

func (e *dialError) Unwrap() error { return e.err }

// pgRefusedTLS pgconn 在服务端拒绝 TLS 时返回的错误原文（未导出为变量）
const pgRefusedTLS = "server refused TLS connection"

// wrapError 把连接数据源的错误转换为不含秘密的 *Error；链路某一跳的 *netchain.HopError 原样返回（其中没有秘密）
func wrapError(ctx context.Context, err error, secrets ...string) error {
	var he *netchain.HopError
	if errors.As(err, &he) {
		return he
	}
	msg := err.Error()
	for _, s := range secrets {
		if s != "" {
			msg = strings.ReplaceAll(msg, s, "******")
		}
	}
	reason := classify(ctx, err)
	switch reason {
	case ReasonTimeout:
		msg = "连接超时: " + msg
	case ReasonCanceled:
		msg = "已取消: " + msg
	}
	return &Error{Reason: reason, Msg: msg}
}

func classify(ctx context.Context, err error) Reason {
	var (
		myErr    *mysql.MySQLError
		pgErr    *pgconn.PgError
		verify   *tls.CertificateVerificationError
		unknown  x509.UnknownAuthorityError
		invalid  x509.CertificateInvalidError
		hostname x509.HostnameError
		alert    tls.AlertError
		record   tls.RecordHeaderError
		dial     *dialError
		dnsErr   *net.DNSError
	)
	switch {
	// ctx 结束后连接被关闭，底层错误往往只是“use of closed connection”，以 ctx 为准
	case errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled):
		return ReasonCanceled
	case ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded):
		return ReasonTimeout
	case errors.As(err, &myErr) && (myErr.Number == 1045 || myErr.Number == 1698):
		return ReasonAuthFailed
	case errors.As(err, &pgErr) && (pgErr.Code == "28P01" || pgErr.Code == "28000"):
		return ReasonAuthFailed
	case errors.As(err, &verify) || errors.As(err, &unknown) || errors.As(err, &invalid) || errors.As(err, &hostname):
		return ReasonCertificate
	case errors.Is(err, mysql.ErrNoTLS) || strings.Contains(err.Error(), pgRefusedTLS) ||
		errors.As(err, &alert) || errors.As(err, &record):
		return ReasonTLS
	case errors.As(err, &dial) || errors.As(err, &dnsErr):
		return ReasonUnreachable
	}
	return ReasonFailed
}
