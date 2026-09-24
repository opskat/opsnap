// Package setting_repo 读写 settings 键值表。
package setting_repo

import (
	"context"
	"time"

	"github.com/cago-frame/cago/database/db"
	"gorm.io/gorm/clause"
)

//go:generate mockgen -source setting.go -destination mock/setting.go

type SettingRepo interface {
	// Get 读取配置项；不存在时 ok 为 false
	Get(ctx context.Context, key string) (value string, ok bool, err error)
	// Set 写入配置项，已存在则覆盖
	Set(ctx context.Context, key, value string) error
	// Delete 删除配置项；不存在时不报错
	Delete(ctx context.Context, key string) error
}

var defaultSetting SettingRepo

func Setting() SettingRepo {
	return defaultSetting
}

func RegisterSetting(i SettingRepo) {
	defaultSetting = i
}

type setting struct {
	Key        string `gorm:"column:key;primaryKey"`
	Value      string `gorm:"column:value"`
	Updatetime int64  `gorm:"column:updatetime"`
}

func (setting) TableName() string { return "settings" }

type settingRepo struct{}

func NewSetting() SettingRepo {
	return &settingRepo{}
}

func (r *settingRepo) Get(ctx context.Context, key string) (string, bool, error) {
	var rows []setting
	if err := db.Ctx(ctx).Where("key = ?", key).Limit(1).Find(&rows).Error; err != nil {
		return "", false, err
	}
	if len(rows) == 0 {
		return "", false, nil
	}
	return rows[0].Value, true, nil
}

func (r *settingRepo) Set(ctx context.Context, key, value string) error {
	return db.Ctx(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value", "updatetime"}),
	}).Create(&setting{Key: key, Value: value, Updatetime: time.Now().Unix()}).Error
}

func (r *settingRepo) Delete(ctx context.Context, key string) error {
	return db.Ctx(ctx).Where("key = ?", key).Delete(&setting{}).Error
}
