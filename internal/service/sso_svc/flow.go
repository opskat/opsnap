package sso_svc

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	authapi "github.com/opskat/opsnap/internal/api/auth"
	"github.com/opskat/opsnap/internal/model/auth"
	"github.com/opskat/opsnap/internal/model/entity/sso_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/secret"
	"github.com/opskat/opsnap/internal/repository/sso_repo"
	"github.com/opskat/opsnap/internal/service/auth_svc"
)

const (
	// PendingTTL 发起后多久内必须回到回调地址；浏览器 Cookie 的有效期与它一致
	PendingTTL = 10 * time.Minute
	// maxPending 同时进行中的登录请求上限；未登录也能发起，满了先丢最早的
	maxPending = 256
	// resultTTL 失败原因保留多久，供登录页读取
	resultTTL = 5 * time.Minute
	// maxResults 失败原因的条数上限
	maxResults = 256
)

// purpose 发起的目的
type purpose int

const (
	purposeLogin purpose = iota
	purposeBind
)

// pending 进行中的登录或绑定请求，只在内存里保存；重启后全部失效
type pending struct {
	purpose  purpose
	browser  string // 浏览器 Cookie 的哈希，回调时比对，保证由同一个浏览器完成
	nonce    string
	verifier string // PKCE code_verifier
	next     string
	created  time.Time
}

type result struct {
	message string
	created time.Time
}

// flowState 进行中的请求与失败原因
type flowState struct {
	mu      sync.Mutex
	pending map[string]*pending // 键是 state
	results map[string]result   // 键是随机 id
}

// Start 开始登录或绑定的结果：浏览器要跳去的授权地址，以及要写进浏览器 Cookie 的值
type Start struct {
	URL     string
	Browser string
}

// CallbackResult 回调的处理结果：浏览器接下来去哪里；单点登录成功时带上新会话
type CallbackResult struct {
	Redirect string
	Session  *auth_svc.LoginResult
}

func randomToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func hashBrowser(v string) string {
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:])
}

// safeNext 登录后要回到的地址只接受站内路径，在面板的 safeNext 规则之外还拒绝控制字符：
// 这里的结果会写进 302 的 Location，浏览器解析时删掉制表符和换行，「/<Tab>/evil」会变成「//evil」
func safeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") {
		return "/"
	}
	if strings.IndexFunc(next, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return "/"
	}
	if strings.HasPrefix(next, "/login") || strings.HasPrefix(next, "/setup") {
		return "/"
	}
	return next
}

// oauthConfig 由已保存的配置和发现文档组装 OAuth2 客户端；Client Secret 在这里解密
func oauthConfig(cfg *sso_entity.SSO, d *discovery) (*oauth2.Config, error) {
	sec := secret.Default()
	if sec == nil {
		return nil, secret.ErrMissingKey
	}
	clientSecret, err := sec.Decrypt(cfg.ClientSecret)
	if err != nil {
		return nil, err
	}
	return &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: clientSecret,
		Endpoint:     oauth2.Endpoint{AuthURL: d.AuthURL, TokenURL: d.TokenURL},
		RedirectURL:  cfg.CallbackURL(),
		Scopes:       []string{oidc.ScopeOpenID, "profile"},
	}, nil
}

// unreachable 连不上提供方
func unreachable(ctx context.Context, cfg *sso_entity.SSO, reason string) error {
	return i18n.NewError(ctx, code.SSOProviderUnreachable, cfg.ButtonLabel, cfg.Issuer, reason)
}

// discover 读取发现文档；开始和回调时都重新读，提供方换了密钥或地址也能跟上
func (s *ssoSvc) discover(ctx context.Context, cfg *sso_entity.SSO) (*discovery, error) {
	d, de := s.fetchDiscovery(ctx, cfg.Issuer)
	if de != nil {
		return nil, unreachable(ctx, cfg, de.Error())
	}
	return d, nil
}

func (s *ssoSvc) start(ctx context.Context, cfg *sso_entity.SSO, p purpose, next string) (*Start, error) {
	d, err := s.discover(ctx, cfg)
	if err != nil {
		return nil, err
	}
	oc, err := oauthConfig(cfg, d)
	if err != nil {
		return nil, err
	}
	state, browser, nonce, verifier := randomToken(), randomToken(), randomToken(), oauth2.GenerateVerifier()
	now := s.now()
	s.flow.mu.Lock()
	s.prune(now)
	if len(s.flow.pending) >= maxPending {
		var oldest string
		for k, v := range s.flow.pending {
			if oldest == "" || v.created.Before(s.flow.pending[oldest].created) {
				oldest = k
			}
		}
		delete(s.flow.pending, oldest)
	}
	s.flow.pending[state] = &pending{
		purpose: p, browser: hashBrowser(browser), nonce: nonce, verifier: verifier, next: safeNext(next), created: now,
	}
	s.flow.mu.Unlock()
	return &Start{
		URL:     oc.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oidc.Nonce(nonce)),
		Browser: browser,
	}, nil
}

// prune 清掉过期的请求和失败原因；调用方持有锁
func (s *ssoSvc) prune(now time.Time) {
	for k, v := range s.flow.pending {
		if now.Sub(v.created) > PendingTTL {
			delete(s.flow.pending, k)
		}
	}
	for k, v := range s.flow.results {
		if now.Sub(v.created) > resultTTL {
			delete(s.flow.results, k)
		}
	}
}

func (s *ssoSvc) StartLogin(ctx context.Context, next string) (*Start, error) {
	cfg, err := load(ctx)
	if err != nil {
		return nil, err
	}
	if !cfg.Configured() || !cfg.Bound() {
		return nil, i18n.NewError(ctx, code.SSONotEnabled)
	}
	return s.start(ctx, cfg, purposeLogin, next)
}

func (s *ssoSvc) StartBind(ctx context.Context) (*Start, error) {
	// 绑定要在浏览器里完成授权，只接受面板会话；API 密钥拥有管理员权限，但不能把提供方账号绑上来
	if a, ok := auth.ActorFrom(ctx); !ok || a.Kind != auth.KindAdmin {
		return nil, i18n.NewForbiddenError(ctx, code.SSOBindRequiresSession)
	}
	cfg, err := load(ctx)
	if err != nil {
		return nil, err
	}
	if !cfg.Configured() {
		return nil, i18n.NewError(ctx, code.SSONotConfigured)
	}
	if cfg.Bound() {
		return nil, i18n.NewError(ctx, code.SSOAlreadyBound)
	}
	return s.start(ctx, cfg, purposeBind, "")
}

// take 取出并删除 state 对应的请求；不存在、已过期或不是这个浏览器发起的返回 nil
func (s *ssoSvc) take(state, browser string) *pending {
	now := s.now()
	s.flow.mu.Lock()
	defer s.flow.mu.Unlock()
	p := s.flow.pending[state]
	delete(s.flow.pending, state)
	if p == nil || now.Sub(p.created) > PendingTTL || browser == "" ||
		subtle.ConstantTimeCompare([]byte(p.browser), []byte(hashBrowser(browser))) != 1 {
		return nil
	}
	return p
}

// fail 记下失败原因，返回带 sso_error 的跳转地址
func (s *ssoSvc) fail(p purpose, err error) *CallbackResult {
	id := randomToken()[:22]
	now := s.now()
	s.flow.mu.Lock()
	s.prune(now)
	if len(s.flow.results) >= maxResults {
		for k := range s.flow.results {
			delete(s.flow.results, k)
			break
		}
	}
	s.flow.results[id] = result{message: err.Error(), created: now}
	s.flow.mu.Unlock()
	if p == purposeBind {
		return &CallbackResult{Redirect: "/settings?tab=sso&sso_error=" + id}
	}
	return &CallbackResult{Redirect: "/login?sso_error=" + id}
}

func (s *ssoSvc) Result(id string) string {
	now := s.now()
	s.flow.mu.Lock()
	defer s.flow.mu.Unlock()
	r, ok := s.flow.results[id]
	if !ok || now.Sub(r.created) > resultTTL {
		return ""
	}
	return r.message
}

// oauthErrorCode 提供方返回的错误码只保留 OAuth2 规定的字符，其他内容不显示
var oauthErrorCode = regexp.MustCompile(`^[A-Za-z0-9_.\-]{1,64}$`)

func sanitizeErrorCode(v string) string {
	if oauthErrorCode.MatchString(v) {
		return v
	}
	return "未知错误"
}

func (s *ssoSvc) Callback(ctx context.Context, browser, session string, req *authapi.OIDCCallbackRequest) *CallbackResult {
	p := s.take(req.State, browser)
	if p == nil {
		// 不知道这次是登录还是绑定：带着有效面板会话的浏览器不需要登录，只可能是在绑定，回到设置页显示「绑定失败」
		pur := purposeLogin
		if session != "" {
			if a, err := auth_svc.Auth().Authenticate(ctx, session, ""); err == nil && a.Kind == auth.KindAdmin {
				pur = purposeBind
			}
		}
		return s.fail(pur, i18n.NewError(ctx, code.SSOStateInvalid))
	}
	res, err := s.callback(ctx, p, req)
	if err != nil {
		return s.fail(p.purpose, err)
	}
	return res
}

func (s *ssoSvc) callback(ctx context.Context, p *pending, req *authapi.OIDCCallbackRequest) (*CallbackResult, error) {
	cfg, err := load(ctx)
	if err != nil {
		return nil, err
	}
	if !cfg.Configured() || (p.purpose == purposeLogin && !cfg.Bound()) {
		return nil, i18n.NewError(ctx, code.SSONotEnabled)
	}
	if req.Error != "" {
		return nil, i18n.NewError(ctx, code.SSOProviderDenied, cfg.ButtonLabel, sanitizeErrorCode(req.Error))
	}
	d, err := s.discover(ctx, cfg)
	if err != nil {
		return nil, err
	}
	oc, err := oauthConfig(cfg, d)
	if err != nil {
		return nil, err
	}
	hctx, cancel := context.WithTimeout(oidc.ClientContext(ctx, s.client), s.discoveryTimeout)
	defer cancel()
	tok, err := oc.Exchange(hctx, req.Code, oauth2.VerifierOption(p.verifier))
	if err != nil {
		var re *oauth2.RetrieveError
		if errors.As(err, &re) {
			if re.ErrorCode != "" {
				return nil, i18n.NewError(ctx, code.SSOProviderDenied, cfg.ButtonLabel, sanitizeErrorCode(re.ErrorCode))
			}
			// 没有 OAuth2 错误码：提供方响应异常，只显示状态码，不显示响应内容
			return nil, unreachable(ctx, cfg, fmt.Sprintf("令牌端点返回 HTTP %d", re.Response.StatusCode))
		}
		return nil, unreachable(ctx, cfg, transportReason(err))
	}
	invalid := func(which string) error {
		return i18n.NewError(ctx, code.SSOTokenInvalid, cfg.ButtonLabel, which)
	}
	raw, _ := tok.Extra("id_token").(string)
	if raw == "" {
		return nil, invalid("提供方没有返回 id_token")
	}
	provider := (&oidc.ProviderConfig{
		IssuerURL: cfg.Issuer, AuthURL: d.AuthURL, TokenURL: d.TokenURL, JWKSURL: d.JWKSURL, Algorithms: d.Algs,
	}).NewProvider(hctx)
	idt, err := provider.Verifier(&oidc.Config{ClientID: cfg.ClientID, Now: s.now}).Verify(hctx, raw)
	if err != nil {
		if reason, ok := jwksFailure(d, err); ok {
			return nil, unreachable(ctx, cfg, reason)
		}
		return nil, invalid(verifyReason(err))
	}
	if subtle.ConstantTimeCompare([]byte(idt.Nonce), []byte(p.nonce)) != 1 {
		return nil, invalid("nonce 不一致")
	}
	var claims struct {
		PreferredUsername string `json:"preferred_username"`
		Name              string `json:"name"`
	}
	_ = idt.Claims(&claims)
	name := claims.PreferredUsername
	if name == "" {
		name = claims.Name
	}
	if name == "" {
		name = idt.Subject
	}

	if p.purpose == purposeBind {
		defer s.beginWrite()()
		cur, err := load(ctx)
		if err != nil {
			return nil, err
		}
		// 授权期间提供方配置被修改或停用：这个身份是按旧配置校验的，不能绑到新配置上
		if cur.Issuer != cfg.Issuer || cur.ClientID != cfg.ClientID {
			return nil, i18n.NewError(ctx, code.SSOConfigChanged)
		}
		// 同时发起的另一次绑定已经完成：不顶掉已有绑定，换账号要先解除
		if cur.Bound() {
			return nil, i18n.NewError(ctx, code.SSOAlreadyBound)
		}
		cur.BoundIssuer, cur.BoundSub, cur.BoundName, cur.BoundAt = idt.Issuer, idt.Subject, name, s.now().Unix()
		cur.Updatetime = s.now().Unix()
		if err := sso_repo.SSO().Save(ctx, cur); err != nil {
			return nil, err
		}
		return &CallbackResult{Redirect: "/settings?tab=sso"}, nil
	}

	// 授权期间可能解除了绑定或停用了单点登录：按现在保存的绑定判断
	cur, err := load(ctx)
	if err != nil {
		return nil, err
	}
	if !cur.Configured() || !cur.Bound() {
		return nil, i18n.NewError(ctx, code.SSONotEnabled)
	}
	if idt.Issuer != cur.BoundIssuer || idt.Subject != cur.BoundSub {
		return nil, i18n.NewError(ctx, code.SSOAccountMismatch, cfg.ButtonLabel, name)
	}
	sess, err := auth_svc.Auth().IssueSession(ctx)
	if err != nil {
		return nil, err
	}
	return &CallbackResult{Redirect: p.next, Session: sess}, nil
}

// jwksFailure 校验失败是不是因为读不到提供方的签名公钥（JWKS）；是的话返回只含地址和状态码或网络错误的原因。
// go-oidc 用 %v 包装，错误链已断开，只能按它的固定文字判断；提供方返回的响应内容不放进原因里
func jwksFailure(d *discovery, err error) (string, bool) {
	msg := err.Error()
	if !strings.Contains(msg, "fetching keys") {
		return "", false
	}
	reason := "读取签名公钥 " + d.JWKSURL + " 失败"
	const httpFailed, transportFailed = "get keys failed: ", "get keys failed "
	if i := strings.Index(msg, httpFailed); i >= 0 {
		// 「get keys failed: 503 Service Unavailable <响应内容>」只取状态码
		if f := strings.Fields(msg[i+len(httpFailed):]); len(f) > 0 {
			return reason + "：HTTP " + f[0], true
		}
		return reason, true
	}
	if i := strings.Index(msg, transportFailed); i >= 0 {
		// 「get keys failed Get "<地址>": <网络错误>」只取网络错误
		rest := msg[i+len(transportFailed):]
		if j := strings.Index(rest, "\": "); j >= 0 {
			return reason + "：" + rest[j+3:], true
		}
		return reason, true
	}
	return reason + "：不是合法的 JWKS", true
}

// verifyReason 把 go-oidc 的校验错误归到具体是哪一项
func verifyReason(err error) string {
	var exp *oidc.TokenExpiredError
	if errors.As(err, &exp) {
		return "已过期"
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "issued by a different provider"):
		return "issuer 与配置的不一致"
	case strings.Contains(msg, "audience"):
		return "audience 与 Client ID 不一致"
	case strings.Contains(msg, "signature"):
		return "签名校验失败"
	case strings.Contains(msg, "malformed"):
		return "格式错误"
	default:
		return "校验失败"
	}
}
