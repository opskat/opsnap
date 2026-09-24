// Package oidc_svc 实现 OIDC 提供方配置、身份绑定与 OIDC 登录。
package oidc_svc

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"github.com/coreos/go-oidc/v3/oidc"
	"go.uber.org/zap"
	"golang.org/x/oauth2"

	api "github.com/opskat/opsnap/internal/api/oidc"
	"github.com/opskat/opsnap/internal/model/entity/oidc_entity"
	"github.com/opskat/opsnap/internal/pkg/authctx"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/repository/oidc_repo"
	"github.com/opskat/opsnap/internal/service/auth_svc"
	"github.com/opskat/opsnap/internal/service/secret_svc"
)

// CallbackPath 回调地址的固定路径
const CallbackPath = "/api/v1/auth/oidc/callback"

// 发起的授权请求在这段时间内有效
const pendingTTL = 10 * time.Minute

// maxPending 同时等待回调的授权请求上限：发起登录是公开接口，超过时丢弃最早的一条，避免内存被刷爆
const maxPending = 1000

var defaultScopes = []string{oidc.ScopeOpenID, "profile", "email"}

// 回调模式
const (
	ModeLogin = "login"
	ModeBind  = "bind"
)

// 回调失败的原因，前端按它显示对应文案（spec「OIDC · 登录」四类错误）
const (
	ErrNotBound    = "not_bound"
	ErrIdP         = "idp"
	ErrInvalid     = "invalid"
	ErrUnreachable = "unreachable"
	// ErrAlreadyBound 已绑定身份时再次绑定（spec「OIDC · 绑定」：只能绑定一个身份）
	ErrAlreadyBound = "already_bound"
)

// CallbackResult 回调的处理结果；ErrorKind 非空表示失败
type CallbackResult struct {
	Mode             string
	Next             string
	Session          *auth_svc.IssuedSession
	ErrorKind        string
	ErrorDescription string
}

type OIDCSvc interface {
	GetConfig(ctx context.Context, req *api.GetConfigRequest) (*api.ConfigResponse, error)
	SaveConfig(ctx context.Context, req *api.SaveConfigRequest) (*api.ConfigResponse, error)
	Unbind(ctx context.Context, req *api.UnbindRequest) (*api.UnbindResponse, error)
	// BeginLogin 返回 IdP 授权地址；未配置或未绑定时报错
	BeginLogin(ctx context.Context, next string) (string, error)
	// BeginBind 返回 IdP 授权地址；已绑定时报错
	BeginBind(ctx context.Context) (string, error)
	Callback(ctx context.Context, req *api.CallbackRequest, meta auth_svc.ClientMeta) *CallbackResult
	// SetPasswordLogin 关闭密码登录前要求已绑定并通过 OIDC 登录过
	SetPasswordLogin(ctx context.Context, req *api.SetPasswordLoginRequest) (*api.SetPasswordLoginResponse, error)
}

type pending struct {
	mode     string
	nonce    string
	verifier string
	next     string
	created  time.Time
}

type oidcSvc struct {
	now        func() time.Time
	httpClient *http.Client
	mu         sync.Mutex
	pending    map[string]*pending
}

var defaultOIDC = newOIDC()

func OIDC() OIDCSvc {
	return defaultOIDC
}

func newOIDC() *oidcSvc {
	return &oidcSvc{
		now:        time.Now,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		pending:    map[string]*pending{},
	}
}

func randomString() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// safeNext 只接受站内路径，防止登录后被带去其他站点。含控制字符的一律拒绝：
// 浏览器解析跳转地址时会删掉制表符与换行，"/\t/evil.com" 会变成 "//evil.com"
func safeNext(next string) string {
	if strings.ContainsFunc(next, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return "/"
	}
	if strings.HasPrefix(next, "/") && !strings.HasPrefix(next, "//") && !strings.HasPrefix(next, "/\\") {
		return next
	}
	return "/"
}

func toResponse(p *oidc_entity.Provider, b *oidc_entity.Binding) *api.ConfigResponse {
	resp := &api.ConfigResponse{}
	if p != nil {
		resp.Configured = true
		resp.DisplayName = p.DisplayName
		resp.Issuer = p.Issuer
		resp.ClientID = p.ClientID
		resp.HasSecret = p.ClientSecret != ""
		resp.Scopes = p.Scopes
		resp.RedirectURL = p.RedirectURL
	}
	if b != nil {
		display := b.Email
		if display == "" {
			display = b.PreferredUsername
		}
		resp.Binding = &api.Binding{Subject: b.Subject, Display: display, BoundAt: b.BoundAt, LastLoginAt: b.LastLoginAt}
	}
	return resp
}

func (s *oidcSvc) GetConfig(ctx context.Context, _ *api.GetConfigRequest) (*api.ConfigResponse, error) {
	p, err := oidc_repo.OIDC().GetProvider(ctx)
	if err != nil {
		return nil, err
	}
	b, err := oidc_repo.OIDC().GetBinding(ctx)
	if err != nil {
		return nil, err
	}
	return s.withPasswordLogin(ctx, toResponse(p, b), b)
}

func (s *oidcSvc) withPasswordLogin(ctx context.Context, resp *api.ConfigResponse, b *oidc_entity.Binding) (*api.ConfigResponse, error) {
	enabled, err := auth_svc.Auth().PasswordLoginEnabled(ctx)
	if err != nil {
		return nil, err
	}
	resp.PasswordLogin = enabled
	resp.CanDisablePasswordLogin = b != nil && b.LastLoginAt != 0
	return resp, nil
}

// removeBinding 解除绑定；绑定是关闭密码登录的前提，因此同时重新开启密码登录，避免没有任何登录方式
func removeBinding(ctx context.Context) error {
	if err := oidc_repo.OIDC().DeleteBinding(ctx); err != nil {
		return err
	}
	return auth_svc.Auth().SetPasswordLoginEnabled(ctx, true)
}

func (s *oidcSvc) SetPasswordLogin(ctx context.Context, req *api.SetPasswordLoginRequest) (*api.SetPasswordLoginResponse, error) {
	if !req.Enabled {
		b, err := oidc_repo.OIDC().GetBinding(ctx)
		if err != nil {
			return nil, err
		}
		if b == nil || b.LastLoginAt == 0 {
			return nil, i18n.NewError(ctx, code.PasswordLoginDisableNotAllowed)
		}
	}
	if err := auth_svc.Auth().SetPasswordLoginEnabled(ctx, req.Enabled); err != nil {
		return nil, err
	}
	return &api.SetPasswordLoginResponse{PasswordLogin: req.Enabled}, nil
}

// discover 拉取 discovery 文档；区分“连不上”与“文档无效或 Issuer 不一致”
func (s *oidcSvc) discover(ctx context.Context, issuer string) (*oidc.Provider, error) {
	provider, err := oidc.NewProvider(oidc.ClientContext(ctx, s.httpClient), issuer)
	if err == nil {
		return provider, nil
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return nil, i18n.NewError(ctx, code.OIDCIssuerUnreachable, issuer, urlErr.Err.Error())
	}
	return nil, i18n.NewError(ctx, code.OIDCDiscoveryInvalid, err.Error())
}

func validRedirect(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.Path == CallbackPath && u.RawQuery == ""
}

func (s *oidcSvc) SaveConfig(ctx context.Context, req *api.SaveConfigRequest) (*api.ConfigResponse, error) {
	old, err := oidc_repo.OIDC().GetProvider(ctx)
	if err != nil {
		return nil, err
	}
	p := &oidc_entity.Provider{
		DisplayName: strings.TrimSpace(req.DisplayName),
		Issuer:      strings.TrimSpace(req.Issuer),
		ClientID:    strings.TrimSpace(req.ClientID),
		Scopes:      req.Scopes,
		RedirectURL: strings.TrimSpace(req.RedirectURL),
	}
	if len(p.Scopes) == 0 {
		p.Scopes = defaultScopes
	}
	switch {
	case req.ClientSecret != "":
		enc, err := secret_svc.Secret().Encrypt(ctx, req.ClientSecret)
		if err != nil {
			return nil, err
		}
		p.ClientSecret = enc
	case old != nil:
		p.ClientSecret = old.ClientSecret
	}
	if p.DisplayName == "" || p.Issuer == "" || p.ClientID == "" || p.ClientSecret == "" {
		return nil, i18n.NewError(ctx, code.OIDCFieldRequired)
	}
	if !validRedirect(p.RedirectURL) {
		return nil, i18n.NewError(ctx, code.OIDCRedirectInvalid)
	}

	binding, err := oidc_repo.OIDC().GetBinding(ctx)
	if err != nil {
		return nil, err
	}
	resetBinding := binding != nil && old != nil && (old.Issuer != p.Issuer || old.ClientID != p.ClientID)
	if resetBinding && !req.ConfirmReset {
		return nil, i18n.NewErrorWithStatus(ctx, http.StatusConflict, code.OIDCResetConfirmRequired)
	}
	if _, err := s.discover(ctx, p.Issuer); err != nil {
		return nil, err
	}
	if err := oidc_repo.OIDC().SaveProvider(ctx, p); err != nil {
		return nil, err
	}
	if resetBinding {
		if err := removeBinding(ctx); err != nil {
			return nil, err
		}
		binding = nil
	}
	return s.withPasswordLogin(ctx, toResponse(p, binding), binding)
}

func (s *oidcSvc) Unbind(ctx context.Context, _ *api.UnbindRequest) (*api.UnbindResponse, error) {
	if err := removeBinding(ctx); err != nil {
		return nil, err
	}
	return &api.UnbindResponse{}, nil
}

func (s *oidcSvc) oauthConfig(ctx context.Context, p *oidc_entity.Provider, provider *oidc.Provider) (*oauth2.Config, error) {
	secret, err := secret_svc.Secret().Decrypt(ctx, p.ClientSecret)
	if err != nil {
		return nil, err
	}
	return &oauth2.Config{
		ClientID:     p.ClientID,
		ClientSecret: secret,
		Endpoint:     provider.Endpoint(),
		RedirectURL:  p.RedirectURL,
		Scopes:       p.Scopes,
	}, nil
}

func (s *oidcSvc) begin(ctx context.Context, mode, next string) (string, error) {
	p, err := oidc_repo.OIDC().GetProvider(ctx)
	if err != nil {
		return "", err
	}
	if p == nil {
		return "", i18n.NewError(ctx, code.OIDCNotConfigured)
	}
	provider, err := s.discover(ctx, p.Issuer)
	if err != nil {
		return "", err
	}
	cfg, err := s.oauthConfig(ctx, p, provider)
	if err != nil {
		return "", err
	}
	state, err := randomString()
	if err != nil {
		return "", err
	}
	nonce, err := randomString()
	if err != nil {
		return "", err
	}
	verifier := oauth2.GenerateVerifier()

	s.mu.Lock()
	now := s.now()
	for k, v := range s.pending {
		if now.Sub(v.created) > pendingTTL {
			delete(s.pending, k)
		}
	}
	for len(s.pending) >= maxPending {
		s.dropOldest()
	}
	s.pending[state] = &pending{mode: mode, nonce: nonce, verifier: verifier, next: safeNext(next), created: now}
	s.mu.Unlock()
	return cfg.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)), nil
}

// dropOldest 丢弃最早发起的一条授权请求；调用方持有 s.mu
func (s *oidcSvc) dropOldest() {
	var oldest string
	var at time.Time
	for k, v := range s.pending {
		if oldest == "" || v.created.Before(at) {
			oldest, at = k, v.created
		}
	}
	delete(s.pending, oldest)
}

func (s *oidcSvc) BeginLogin(ctx context.Context, next string) (string, error) {
	b, err := oidc_repo.OIDC().GetBinding(ctx)
	if err != nil {
		return "", err
	}
	if b == nil {
		return "", i18n.NewError(ctx, code.OIDCNotConfigured)
	}
	return s.begin(ctx, ModeLogin, next)
}

func (s *oidcSvc) BeginBind(ctx context.Context) (string, error) {
	if p := authctx.From(ctx); p == nil || p.Via != authctx.ViaSession {
		return "", i18n.NewForbiddenError(ctx, code.SessionRequired)
	}
	b, err := oidc_repo.OIDC().GetBinding(ctx)
	if err != nil {
		return "", err
	}
	if b != nil {
		return "", i18n.NewError(ctx, code.OIDCAlreadyBound)
	}
	return s.begin(ctx, ModeBind, "/settings")
}

func (s *oidcSvc) take(state string) *pending {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.pending[state]
	delete(s.pending, state)
	if p == nil || s.now().Sub(p.created) > pendingTTL {
		return nil
	}
	return p
}

type idClaims struct {
	Email             string `json:"email"`
	PreferredUsername string `json:"preferred_username"`
}

func (s *oidcSvc) Callback(ctx context.Context, req *api.CallbackRequest, meta auth_svc.ClientMeta) *CallbackResult {
	pend := s.take(req.State)
	if pend == nil {
		return &CallbackResult{Mode: ModeLogin, Next: "/", ErrorKind: ErrInvalid}
	}
	res := &CallbackResult{Mode: pend.mode, Next: pend.next}
	fail := func(kind string, err error) *CallbackResult {
		res.ErrorKind = kind
		if err != nil {
			// 只记录错误类型与描述，不记录授权码与令牌
			logger.Ctx(ctx).Warn("OIDC 回调失败", zap.String("mode", pend.mode), zap.String("kind", kind), zap.Error(err))
		}
		return res
	}
	if req.Error != "" {
		res.ErrorDescription = req.ErrorDescription
		if res.ErrorDescription == "" {
			res.ErrorDescription = req.Error
		}
		return fail(ErrIdP, nil)
	}

	p, err := oidc_repo.OIDC().GetProvider(ctx)
	if err != nil || p == nil {
		return fail(ErrInvalid, err)
	}
	provider, err := oidc.NewProvider(oidc.ClientContext(ctx, s.httpClient), p.Issuer)
	if err != nil {
		return fail(ErrUnreachable, err)
	}
	cfg, err := s.oauthConfig(ctx, p, provider)
	if err != nil {
		return fail(ErrInvalid, err)
	}
	tok, err := cfg.Exchange(oidc.ClientContext(ctx, s.httpClient), req.Code, oauth2.VerifierOption(pend.verifier))
	if err != nil {
		var retrieve *oauth2.RetrieveError
		if errors.As(err, &retrieve) {
			return fail(ErrInvalid, err)
		}
		return fail(ErrUnreachable, err)
	}
	rawID, ok := tok.Extra("id_token").(string)
	if !ok {
		return fail(ErrInvalid, errors.New("响应中没有 id_token"))
	}
	idToken, err := provider.VerifierContext(oidc.ClientContext(ctx, s.httpClient), &oidc.Config{ClientID: p.ClientID, Now: s.now}).
		Verify(ctx, rawID)
	if err != nil {
		return fail(ErrInvalid, err)
	}
	if idToken.Nonce != pend.nonce {
		return fail(ErrInvalid, errors.New("nonce 不匹配"))
	}
	var claims idClaims
	if err := idToken.Claims(&claims); err != nil {
		return fail(ErrInvalid, err)
	}

	now := s.now().Unix()
	if pend.mode == ModeBind {
		// 发起绑定后、回调前可能已由另一次绑定完成；只能绑定一个身份，不覆盖已有绑定
		existing, err := oidc_repo.OIDC().GetBinding(ctx)
		if err != nil {
			return fail(ErrInvalid, err)
		}
		if existing != nil {
			return fail(ErrAlreadyBound, nil)
		}
		err = oidc_repo.OIDC().SaveBinding(ctx, &oidc_entity.Binding{
			Issuer: idToken.Issuer, Subject: idToken.Subject,
			Email: claims.Email, PreferredUsername: claims.PreferredUsername, BoundAt: now,
		})
		if err != nil {
			return fail(ErrInvalid, err)
		}
		return res
	}

	binding, err := oidc_repo.OIDC().GetBinding(ctx)
	if err != nil {
		return fail(ErrInvalid, err)
	}
	if !binding.Matches(idToken.Issuer, idToken.Subject) {
		return fail(ErrNotBound, nil)
	}
	binding.LastLoginAt = now
	if err := oidc_repo.OIDC().SaveBinding(ctx, binding); err != nil {
		return fail(ErrInvalid, err)
	}
	issued, err := auth_svc.Auth().IssueSession(ctx, meta)
	if err != nil {
		return fail(ErrInvalid, err)
	}
	res.Session = issued
	return res
}
