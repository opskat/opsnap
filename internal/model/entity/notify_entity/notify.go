// Package notify_entity 通知设置（Webhook）
package notify_entity

import (
	"context"
	"net/url"
	"strings"

	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/opskat/opsnap/internal/pkg/code"
)

// EventType 通知事件类型
type EventType string

const (
	EventFailed    EventType = "failed"    // 备份失败
	EventRecovered EventType = "recovered" // 恢复正常
	EventStale     EventType = "stale"     // 太久没有成功
	EventSuccess   EventType = "success"   // 每次成功
)

const (
	// SingletonID 通知设置只有一行，主键固定为 1
	SingletonID = 1
	// DefaultStaleHours 太久没有成功的默认阈值（小时）
	DefaultStaleHours = 48

	maxStaleHours = 8760
)

// Notify 通知设置
type Notify struct {
	ID          int64             `gorm:"column:id;primaryKey;autoIncrement:false"`
	URL         string            `gorm:"column:url;not null"`
	Headers     map[string]string `gorm:"column:headers;serializer:json;not null"` // 值是加密后的密文
	OnFailed    bool              `gorm:"column:on_failed;not null"`
	OnRecovered bool              `gorm:"column:on_recovered;not null"`
	OnStale     bool              `gorm:"column:on_stale;not null"`
	OnSuccess   bool              `gorm:"column:on_success;not null"`
	StaleHours  int               `gorm:"column:stale_hours;not null"`
	// PanelURL 面板地址（如 http://opsnap.lan:8080），消息里的链接以它开头；为空时只发路径
	PanelURL   string `gorm:"column:panel_url;not null"`
	Updatetime int64  `gorm:"column:updatetime"`
}

func (Notify) TableName() string { return "notify" }

// Default 尚未保存过设置时使用的默认值：失败、恢复、太久没有成功默认开，每次成功默认关
func Default() *Notify {
	return &Notify{
		ID:          SingletonID,
		Headers:     map[string]string{},
		OnFailed:    true,
		OnRecovered: true,
		OnStale:     true,
		OnSuccess:   false,
		StaleHours:  DefaultStaleHours,
	}
}

// Validate 校验地址和阈值。地址为空表示未配置，允许保存。
func (n *Notify) Validate(ctx context.Context) error {
	if n.URL != "" {
		u, err := url.Parse(n.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return i18n.NewError(ctx, code.NotifyURLInvalid)
		}
	}
	if n.PanelURL != "" {
		u, err := url.Parse(n.PanelURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return i18n.NewError(ctx, code.NotifyPanelURLInvalid)
		}
	}
	if n.StaleHours < 1 || n.StaleHours > maxStaleHours {
		return i18n.NewError(ctx, code.NotifyStaleHoursInvalid, maxStaleHours)
	}
	return nil
}

// LinkTo 把面板里的路径拼成完整链接；没有面板地址或已是完整地址时原样返回
func (n *Notify) LinkTo(path string) string {
	if n == nil || n.PanelURL == "" || !strings.HasPrefix(path, "/") {
		return path
	}
	return strings.TrimRight(n.PanelURL, "/") + path
}

// Enabled 地址已配置且该事件开启时返回 true
func (n *Notify) Enabled(t EventType) bool {
	if n == nil || n.URL == "" {
		return false
	}
	switch t {
	case EventFailed:
		return n.OnFailed
	case EventRecovered:
		return n.OnRecovered
	case EventStale:
		return n.OnStale
	case EventSuccess:
		return n.OnSuccess
	}
	return false
}
