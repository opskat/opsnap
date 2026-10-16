// Package sso_svc 单点登录（OIDC）：提供方配置
package sso_svc

import (
	"context"
	"net/http"
	"time"

	"github.com/cago-frame/cago/pkg/i18n"

	ssoapi "github.com/opskat/opsnap/internal/api/sso"
	"github.com/opskat/opsnap/internal/model/entity/sso_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/secret"
	"github.com/opskat/opsnap/internal/repository/sso_repo"
)

// defaultDiscoveryTimeout 读取发现文档的超时
const defaultDiscoveryTimeout = 10 * time.Second

// SSOSvc 单点登录服务
type SSOSvc interface {
	// View 面板展示用的设置；Client Secret 只返回是否已设置
	View(ctx context.Context) (*ssoapi.GetResponse, error)
	// Update 检查发现文档通过后保存提供方配置
	Update(ctx context.Context, req *ssoapi.UpdateRequest) (*ssoapi.UpdateResponse, error)
	// Disable 停用单点登录
	Disable(ctx context.Context) (*ssoapi.DisableResponse, error)
}

type ssoSvc struct {
	now              func() time.Time
	client           *http.Client
	discoveryTimeout time.Duration
}

func newSSO(now func() time.Time) *ssoSvc {
	return &ssoSvc{now: now, client: &http.Client{}, discoveryTimeout: defaultDiscoveryTimeout}
}

var defaultSSO SSOSvc = newSSO(time.Now)

// SSO 取得单点登录服务
func SSO() SSOSvc { return defaultSSO }

// RegisterSSO 替换服务实现
func RegisterSSO(i SSOSvc) { defaultSSO = i }

// NewSSO 创建使用指定时钟的单点登录服务
func NewSSO(now func() time.Time) SSOSvc { return newSSO(now) }

// load 读取保存的配置，没有时返回默认值
func load(ctx context.Context) (*sso_entity.SSO, error) {
	s, err := sso_repo.SSO().Get(ctx)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return sso_entity.Default(), nil
	}
	return s, nil
}

func view(s *sso_entity.SSO) *ssoapi.GetResponse {
	resp := &ssoapi.GetResponse{
		Configured:   s.Configured(),
		Issuer:       s.Issuer,
		ClientID:     s.ClientID,
		ClientSecret: secret.Mask(s.ClientSecret),
		ButtonLabel:  s.ButtonLabel,
		PanelURL:     s.PanelURL,
		CallbackURL:  s.CallbackURL(),
	}
	if resp.ButtonLabel == "" {
		resp.ButtonLabel = sso_entity.DefaultButtonLabel
	}
	return resp
}

func (s *ssoSvc) View(ctx context.Context) (*ssoapi.GetResponse, error) {
	cur, err := load(ctx)
	if err != nil {
		return nil, err
	}
	return view(cur), nil
}

func (s *ssoSvc) Update(ctx context.Context, req *ssoapi.UpdateRequest) (*ssoapi.UpdateResponse, error) {
	// 与轮换主密钥互斥，见 secret.BeginWrite
	endWrite := secret.BeginWrite()
	defer endWrite()
	cur, err := load(ctx)
	if err != nil {
		return nil, err
	}
	next := *cur
	next.Issuer, next.ClientID, next.ButtonLabel, next.PanelURL = req.Issuer, req.ClientID, req.ButtonLabel, req.PanelURL
	if err := next.ValidateProvider(ctx); err != nil {
		return nil, err
	}
	if req.ClientSecret != "" && req.ClientSecret != secret.Masked {
		sec := secret.Default()
		if sec == nil {
			return nil, secret.ErrMissingKey
		}
		if next.ClientSecret, err = sec.Encrypt(req.ClientSecret); err != nil {
			return nil, err
		}
	}
	if next.ClientSecret == "" {
		return nil, i18n.NewError(ctx, code.SSOClientSecretRequired)
	}
	if _, err := s.fetchDiscovery(ctx, next.Issuer); err != nil {
		return nil, err
	}
	next.Updatetime = s.now().Unix()
	if err := sso_repo.SSO().Save(ctx, &next); err != nil {
		return nil, err
	}
	return &ssoapi.UpdateResponse{GetResponse: *view(&next)}, nil
}

func (s *ssoSvc) Disable(ctx context.Context) (*ssoapi.DisableResponse, error) {
	if err := sso_repo.SSO().Delete(ctx); err != nil {
		return nil, err
	}
	return &ssoapi.DisableResponse{GetResponse: *view(sso_entity.Default())}, nil
}
