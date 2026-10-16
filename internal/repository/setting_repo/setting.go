// Package setting_repo 全局设置的数据访问
package setting_repo

import (
	"context"

	"github.com/cago-frame/cago/database/db"
	"gorm.io/gorm/clause"

	"github.com/opskat/opsnap/internal/model/entity/setting_entity"
)

// SettingRepo 设置仓储；设置只有一行
//
//go:generate mockgen -source setting.go -destination mock_setting_repo/setting.go -package mock_setting_repo
type SettingRepo interface {
	// Get 返回已保存的设置，从未保存过时返回 nil
	Get(ctx context.Context) (*setting_entity.Setting, error)
	// Save 覆盖保存
	Save(ctx context.Context, s *setting_entity.Setting) error
}

var defaultSetting SettingRepo

// Setting 取得已注册的仓储
func Setting() SettingRepo { return defaultSetting }

// RegisterSetting 注册仓储实现
func RegisterSetting(i SettingRepo) { defaultSetting = i }

type settingRepo struct{}

// NewSetting 创建基于 GORM 的实现
func NewSetting() SettingRepo { return &settingRepo{} }

func (r *settingRepo) Get(ctx context.Context) (*setting_entity.Setting, error) {
	ret := &setting_entity.Setting{}
	if err := db.Ctx(ctx).Where("id = ?", setting_entity.SingletonID).First(ret).Error; err != nil {
		if db.RecordNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return ret, nil
}

func (r *settingRepo) Save(ctx context.Context, s *setting_entity.Setting) error {
	s.ID = setting_entity.SingletonID
	return db.Ctx(ctx).Clauses(clause.OnConflict{UpdateAll: true}).Create(s).Error
}
