// Package setting_ctr 设置接口控制器
package setting_ctr

import (
	"context"

	settingapi "github.com/opskat/opsnap/internal/api/setting"
	"github.com/opskat/opsnap/internal/service/masterkey_svc"
	"github.com/opskat/opsnap/internal/service/setting_svc"
)

type Setting struct{}

func NewSetting() *Setting { return &Setting{} }

func (s *Setting) Get(ctx context.Context, req *settingapi.GetRequest) (*settingapi.GetResponse, error) {
	return setting_svc.Setting().View(ctx)
}

func (s *Setting) Update(ctx context.Context, req *settingapi.UpdateRequest) (*settingapi.UpdateResponse, error) {
	return setting_svc.Setting().Update(ctx, req)
}

// RotateMasterKey 轮换主密钥
func (s *Setting) RotateMasterKey(ctx context.Context, req *settingapi.RotateMasterKeyRequest) (*settingapi.RotateMasterKeyResponse, error) {
	return masterkey_svc.MasterKey().Rotate(ctx, req)
}
