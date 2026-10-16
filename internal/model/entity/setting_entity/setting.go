// Package setting_entity 全局设置
package setting_entity

import (
	"context"
	"time"

	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/opskat/opsnap/internal/pkg/code"
)

const (
	// SingletonID 设置只有一行，主键固定为 1
	SingletonID = 1
	// DefaultTimezone 默认时区
	DefaultTimezone = "Asia/Shanghai"

	maxConcurrentRunsLimit = 64
	maxRetentionDays       = 3650
)

// Setting 全局设置
type Setting struct {
	ID                     int64  `gorm:"column:id;primaryKey;autoIncrement:false"`
	MaxConcurrentRuns      int    `gorm:"column:max_concurrent_runs;not null"`
	RetentionDays          int    `gorm:"column:retention_days;not null"`
	Timezone               string `gorm:"column:timezone;not null"`
	ClearMirrorAfterBackup bool   `gorm:"column:clear_mirror_after_backup;not null"`
	Updatetime             int64  `gorm:"column:updatetime"`
}

func (Setting) TableName() string { return "settings" }

// Default 尚未保存过设置时使用的默认值
func Default() *Setting {
	return &Setting{
		ID:                SingletonID,
		MaxConcurrentRuns: 2,
		RetentionDays:     90,
		Timezone:          DefaultTimezone,
	}
}

// Validate 校验取值范围；时区必须能被 time.LoadLocation 识别
func (s *Setting) Validate(ctx context.Context) error {
	if s.MaxConcurrentRuns < 1 || s.MaxConcurrentRuns > maxConcurrentRunsLimit {
		return i18n.NewError(ctx, code.SettingMaxConcurrentInvalid, maxConcurrentRunsLimit)
	}
	if s.RetentionDays < 1 || s.RetentionDays > maxRetentionDays {
		return i18n.NewError(ctx, code.SettingRetentionInvalid, maxRetentionDays)
	}
	// Local 取决于服务器环境，不是稳定的时区名
	if s.Timezone == "" || s.Timezone == "Local" {
		return i18n.NewError(ctx, code.SettingTimezoneInvalid, s.Timezone)
	}
	if _, err := time.LoadLocation(s.Timezone); err != nil {
		return i18n.NewError(ctx, code.SettingTimezoneInvalid, s.Timezone)
	}
	return nil
}

// Location 返回设置的时区；时区无法加载时退回 UTC
func (s *Setting) Location() *time.Location {
	loc, err := time.LoadLocation(s.Timezone)
	if err != nil {
		return time.UTC
	}
	return loc
}
