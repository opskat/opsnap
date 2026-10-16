// Package sso_svc 单点登录（OIDC）：提供方配置、绑定账号、授权码 + PKCE 登录
package sso_svc

import (
	"context"
	"net/http"
	"time"

	"github.com/cago-frame/cago/pkg/i18n"

	authapi "github.com/opskat/opsnap/internal/api/auth"
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
	// Unbind 解除绑定
	Unbind(ctx context.Context) (*ssoapi.UnbindResponse, error)
	// StartLogin 开始单点登录（需要已配置并已绑定）
	StartLogin(ctx context.Context, next string) (*Start, error)
	// StartBind 开始绑定（需要已配置）；只接受面板会话，API 密钥返回 403
	StartBind(ctx context.Context) (*Start, error)
	// Callback 处理提供方的回调；browser 是开始时写进浏览器 Cookie 的值。失败也返回跳转地址
	Callback(ctx context.Context, browser string, req *authapi.OIDCCallbackRequest) *CallbackResult
	// Result 回调失败的原因，id 来自跳转地址里的 sso_error；不存在或已过期时为空
	Result(id string) string
}

type ssoSvc struct {
	now              func() time.Time
	client           *http.Client
	discoveryTimeout time.Duration
	flow             flowState
}

func newSSO(now func() time.Time) *ssoSvc {
	return &ssoSvc{
		now: now, client: &http.Client{}, discoveryTimeout: defaultDiscoveryTimeout,
		flow: flowState{pending: map[string]*pending{}, results: map[string]result{}},
	}
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
	if s.Bound() {
		resp.Binding = &ssoapi.Binding{Issuer: s.BoundIssuer, Sub: s.BoundSub, Name: s.BoundName, BoundAt: s.BoundAt}
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
	if err := s.checkDiscovery(ctx, next.Issuer); err != nil {
		return nil, err
	}
	// 同一个 sub 在不同提供方或不同应用下代表的不是同一个人
	if next.Issuer != cur.Issuer || next.ClientID != cur.ClientID {
		next.ClearBinding()
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

func (s *ssoSvc) Unbind(ctx context.Context) (*ssoapi.UnbindResponse, error) {
	cur, err := load(ctx)
	if err != nil {
		return nil, err
	}
	if cur.Bound() {
		cur.ClearBinding()
		cur.Updatetime = s.now().Unix()
		if err := sso_repo.SSO().Save(ctx, cur); err != nil {
			return nil, err
		}
	}
	return &ssoapi.UnbindResponse{GetResponse: *view(cur)}, nil
}
