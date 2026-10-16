// Package setting_svc 全局设置：并发数、记录保留天数、时区、清空文件镜像开关
package setting_svc

import (
	"context"
	"path/filepath"
	"time"

	settingapi "github.com/opskat/opsnap/internal/api/setting"
	"github.com/opskat/opsnap/internal/model/entity/setting_entity"
	"github.com/opskat/opsnap/internal/pkg/secret"
	"github.com/opskat/opsnap/internal/repository/admin_repo"
	"github.com/opskat/opsnap/internal/repository/setting_repo"
	"github.com/opskat/opsnap/internal/service/masterkey_svc"
)

// SettingSvc 设置服务
type SettingSvc interface {
	// Get 返回当前设置；从未保存过时返回默认值。其他模块读取设置用它。
	Get(ctx context.Context) (*setting_entity.Setting, error)
	// View 返回面板展示用的设置，附带管理员账号和主密钥状态
	View(ctx context.Context) (*settingapi.GetResponse, error)
	Update(ctx context.Context, req *settingapi.UpdateRequest) (*settingapi.UpdateResponse, error)
}

type settingSvc struct{ now func() time.Time }

// mirrorDir 文件镜像目录（<数据目录>/mirror），启动时由 main 按配置设置
var mirrorDir = filepath.Join("/data", "mirror")

// SetMirrorDir 设置文件镜像目录，设置页据此标出本机存放明文的位置
func SetMirrorDir(dir string) { mirrorDir = dir }

func newSetting(now func() time.Time) *settingSvc { return &settingSvc{now: now} }

var defaultSetting SettingSvc = newSetting(time.Now)

// Setting 取得设置服务
func Setting() SettingSvc { return defaultSetting }

// RegisterSetting 替换服务实现
func RegisterSetting(i SettingSvc) { defaultSetting = i }

// Get 读取当前设置的快捷方式，等同 Setting().Get(ctx)
func Get(ctx context.Context) (*setting_entity.Setting, error) { return defaultSetting.Get(ctx) }

func (s *settingSvc) Get(ctx context.Context) (*setting_entity.Setting, error) {
	got, err := setting_repo.Setting().Get(ctx)
	if err != nil {
		return nil, err
	}
	if got == nil {
		return setting_entity.Default(), nil
	}
	return got, nil
}

func (s *settingSvc) View(ctx context.Context) (*settingapi.GetResponse, error) {
	st, err := s.Get(ctx)
	if err != nil {
		return nil, err
	}
	admin, err := admin_repo.Admin().Find(ctx)
	if err != nil {
		return nil, err
	}
	resp := &settingapi.GetResponse{
		MaxConcurrentRuns:      st.MaxConcurrentRuns,
		RetentionDays:          st.RetentionDays,
		Timezone:               st.Timezone,
		ClearMirrorAfterBackup: st.ClearMirrorAfterBackup,
		MasterKey:              settingapi.MasterKeyStatus{Configured: secret.Default() != nil, Algorithm: "AES-256-GCM"},
		MirrorDir:              mirrorDir,
	}
	if resp.MasterKey.RotatedAt, err = masterkey_svc.MasterKey().RotatedAt(ctx); err != nil {
		return nil, err
	}
	if admin != nil {
		resp.AdminUsername = admin.Username
	}
	return resp, nil
}

func (s *settingSvc) Update(ctx context.Context, req *settingapi.UpdateRequest) (*settingapi.UpdateResponse, error) {
	st := &setting_entity.Setting{
		ID:                     setting_entity.SingletonID,
		MaxConcurrentRuns:      req.MaxConcurrentRuns,
		RetentionDays:          req.RetentionDays,
		Timezone:               req.Timezone,
		ClearMirrorAfterBackup: req.ClearMirrorAfterBackup,
		Updatetime:             s.now().Unix(),
	}
	if err := st.Validate(ctx); err != nil {
		return nil, err
	}
	if err := setting_repo.Setting().Save(ctx, st); err != nil {
		return nil, err
	}
	view, err := s.View(ctx)
	if err != nil {
		return nil, err
	}
	return &settingapi.UpdateResponse{GetResponse: *view}, nil
}

// NewSetting 创建使用指定时钟的设置服务
func NewSetting(now func() time.Time) SettingSvc { return newSetting(now) }
