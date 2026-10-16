// Package server_entity 服务器（备份数据源所在的主机）与代理链
package server_entity

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/opskat/opsnap/internal/pkg/code"
)

// 认证方式
const (
	AuthPassword   = "password"
	AuthPrivateKey = "private_key"
)

// 代理链中一跳的类型
const (
	HopSSH    = "ssh"
	HopSOCKS5 = "socks5"
	HopHTTP   = "http"
)

// 连接状态
const (
	StatusUnknown = "unknown"
	StatusOK      = "ok"
	StatusFailed  = "failed"
)

const (
	maxNameLen  = 64
	maxErrorLen = 500
)

// fingerprintPattern OpenSSH 风格的 SHA256 指纹：SHA256: 加 43 位无填充 base64
var fingerprintPattern = regexp.MustCompile(`^SHA256:[A-Za-z0-9+/]{43}$`)

// Hop 代理链中的一跳。SSH 跳板只记 ServerID；代理记地址与可选的用户名、密码（密文）。
type Hop struct {
	Type     string `json:"type"`
	ServerID int64  `json:"server_id,omitempty"`
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

// SameProxy 是否是同一个代理（类型、地址、端口、用户名都相同），用于保留已保存的代理密码
func (h Hop) SameProxy(o Hop) bool {
	return h.Type == o.Type && h.Host == o.Host && h.Port == o.Port && h.Username == o.Username
}

// Server 服务器。Password、PrivateKey、Passphrase 与各跳的 Password 都是主密钥加密后的密文。
type Server struct {
	ID                 int64  `gorm:"column:id;primaryKey;autoIncrement"`
	Name               string `gorm:"column:name;not null"`
	Host               string `gorm:"column:host;not null"`
	Port               int    `gorm:"column:port;not null"`
	Username           string `gorm:"column:username;not null"`
	AuthType           string `gorm:"column:auth_type;not null"`
	Password           string `gorm:"column:password;not null"`
	PrivateKey         string `gorm:"column:private_key;not null"`
	Passphrase         string `gorm:"column:passphrase;not null"`
	Hops               []Hop  `gorm:"column:hops;serializer:json;not null"`
	HostKeyFingerprint string `gorm:"column:host_key_fingerprint;not null"`
	LastCheckAt        int64  `gorm:"column:last_check_at;not null"`
	LastLatencyMs      int64  `gorm:"column:last_latency_ms;not null"`
	FailSince          int64  `gorm:"column:fail_since;not null"`
	LastError          string `gorm:"column:last_error;not null"`
	Createtime         int64  `gorm:"column:createtime"`
	Updatetime         int64  `gorm:"column:updatetime"`
}

func (Server) TableName() string { return "servers" }

// Check 服务器是否存在
func (s *Server) Check(ctx context.Context) error {
	if s == nil {
		return i18n.NewError(ctx, code.ServerNotFound)
	}
	return nil
}

// ValidateConnection 校验连接所需的字段，不含指纹。测试未保存的配置时使用。
func (s *Server) ValidateConnection(ctx context.Context) error {
	switch {
	case s.Name == "" || len([]rune(s.Name)) > maxNameLen:
		return i18n.NewError(ctx, code.ServerFieldInvalid, "名称不能为空，且不超过 64 个字")
	case s.Host == "" || strings.ContainsAny(s.Host, " \t\r\n/"):
		return i18n.NewError(ctx, code.ServerFieldInvalid, "地址不能为空，也不能含空格或斜杠")
	case s.Port < 1 || s.Port > 65535:
		return i18n.NewError(ctx, code.ServerFieldInvalid, "端口须在 1 到 65535 之间")
	case s.Username == "":
		return i18n.NewError(ctx, code.ServerFieldInvalid, "用户不能为空")
	}
	switch s.AuthType {
	case AuthPassword:
		if s.Password == "" {
			return i18n.NewError(ctx, code.ServerCredentialRequired, "请填写密码")
		}
	case AuthPrivateKey:
		if s.PrivateKey == "" {
			return i18n.NewError(ctx, code.ServerCredentialRequired, "请填写私钥")
		}
	default:
		return i18n.NewError(ctx, code.ServerAuthTypeInvalid)
	}
	for i, h := range s.Hops {
		if err := h.validate(ctx, i+1, s.ID); err != nil {
			return err
		}
	}
	return nil
}

// Validate 保存前校验：连接字段合法，且目标主机指纹已核对
func (s *Server) Validate(ctx context.Context) error {
	if err := s.ValidateConnection(ctx); err != nil {
		return err
	}
	if s.HostKeyFingerprint == "" {
		return i18n.NewError(ctx, code.ServerFingerprintUnconfirmed)
	}
	if !ValidFingerprint(s.HostKeyFingerprint) {
		return i18n.NewError(ctx, code.ServerFingerprintInvalid)
	}
	return nil
}

// ValidFingerprint 是否是 SHA256: 开头的指纹
func ValidFingerprint(fp string) bool { return fingerprintPattern.MatchString(fp) }

func (h Hop) validate(ctx context.Context, index int, selfID int64) error {
	switch h.Type {
	case HopSSH:
		if h.ServerID <= 0 {
			return i18n.NewError(ctx, code.ServerHopInvalid, index, "请选择作为跳板的服务器")
		}
		if selfID != 0 && h.ServerID == selfID {
			return i18n.NewError(ctx, code.ServerHopInvalid, index, "不能把服务器自己用作跳板")
		}
	case HopSOCKS5, HopHTTP:
		if h.Host == "" || strings.ContainsAny(h.Host, " \t\r\n/") {
			return i18n.NewError(ctx, code.ServerHopInvalid, index, "代理地址不能为空，也不能含空格或斜杠")
		}
		if h.Port < 1 || h.Port > 65535 {
			return i18n.NewError(ctx, code.ServerHopInvalid, index, "代理端口须在 1 到 65535 之间")
		}
	default:
		return i18n.NewError(ctx, code.ServerHopInvalid, index, "类型须是 SSH 跳板、SOCKS5 或 HTTP CONNECT")
	}
	return nil
}

// UsesJump 代理链中是否把 serverID 用作 SSH 跳板
func (s *Server) UsesJump(serverID int64) bool {
	for _, h := range s.Hops {
		if h.Type == HopSSH && h.ServerID == serverID {
			return true
		}
	}
	return false
}

// SameEndpoint 两份配置是否连到同一个地方：目标地址、端口、用户和代理链（不比较凭据）都相同
func (s *Server) SameEndpoint(o *Server) bool {
	if s.Host != o.Host || s.Port != o.Port || s.Username != o.Username || len(s.Hops) != len(o.Hops) {
		return false
	}
	for i, h := range s.Hops {
		g := o.Hops[i]
		if h.Type != g.Type || h.ServerID != g.ServerID || h.Host != g.Host || h.Port != g.Port || h.Username != g.Username {
			return false
		}
	}
	return true
}

// Status 最近一次连接的状态
func (s *Server) Status() string {
	switch {
	case s.LastCheckAt == 0:
		return StatusUnknown
	case s.FailSince != 0:
		return StatusFailed
	}
	return StatusOK
}

// RecordCheck 记录一次连接结果。失败时保留第一次失败的时间，用于显示「从某时起连不上」。
func (s *Server) RecordCheck(ok bool, latency time.Duration, errMsg string, now time.Time) {
	s.LastCheckAt = now.Unix()
	if ok {
		s.LastLatencyMs = latency.Milliseconds()
		s.FailSince = 0
		s.LastError = ""
		return
	}
	if s.FailSince == 0 {
		s.FailSince = now.Unix()
	}
	if r := []rune(errMsg); len(r) > maxErrorLen {
		errMsg = string(r[:maxErrorLen])
	}
	s.LastError = errMsg
}
