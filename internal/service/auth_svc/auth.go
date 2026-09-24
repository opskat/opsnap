// Package auth_svc 实现首次设置、会话与认证。
package auth_svc

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/cago-frame/cago/pkg/i18n"

	api "github.com/opskat/opsnap/internal/api/auth"
	"github.com/opskat/opsnap/internal/model/entity/admin_entity"
	"github.com/opskat/opsnap/internal/pkg/authctx"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/password"
	"github.com/opskat/opsnap/internal/pkg/secret"
	"github.com/opskat/opsnap/internal/repository/admin_repo"
	"github.com/opskat/opsnap/internal/repository/oidc_repo"
	"github.com/opskat/opsnap/internal/repository/session_repo"
	"github.com/opskat/opsnap/internal/repository/setting_repo"
)

// MinPasswordLength 密码最少字符数（按字符而不是字节计）
const MinPasswordLength = 12

// touchInterval 会话在这段时间内重复使用时不重复顺延，避免每个请求都写库
const touchInterval = time.Minute

var usernamePattern = regexp.MustCompile(`^[a-z0-9._-]{3,32}$`)

// ClientMeta 发起请求的客户端信息，记录在会话上
type ClientMeta struct {
	IP        string
	UserAgent string
}

// IssuedSession 需要写入浏览器 Cookie 的会话标识
type IssuedSession struct {
	Token   string
	Expires time.Time
}

type AuthSvc interface {
	// PrepareSetupCode 尚未创建管理员时生成新的设置码（每次启动换新）；已创建时返回空串
	PrepareSetupCode(ctx context.Context) (string, error)
	Status(ctx context.Context, req *api.StatusRequest) (*api.StatusResponse, error)
	// Setup 凭设置码创建管理员并登录
	Setup(ctx context.Context, req *api.SetupRequest, meta ClientMeta) (*api.SetupResponse, *IssuedSession, error)
	// Login 密码登录；失败计入来源 IP 的登录保护
	Login(ctx context.Context, req *api.LoginRequest, meta ClientMeta) (*api.LoginResponse, *IssuedSession, error)
	// AuthenticateSession 校验会话标识；需要顺延时返回新的过期时间，否则 refreshed 为 nil
	AuthenticateSession(ctx context.Context, token string) (p *authctx.Principal, refreshed *IssuedSession, err error)
	Logout(ctx context.Context, p *authctx.Principal) error
	Me(ctx context.Context, req *api.MeRequest) (*api.MeResponse, error)
	// ChangePassword 修改密码：当前密码错误计入登录保护；成功后除当前会话外全部失效
	ChangePassword(ctx context.Context, req *api.ChangePasswordRequest, meta ClientMeta) (*api.ChangePasswordResponse, error)
	// ResetPassword 命令行重置密码：所有会话失效
	ResetPassword(ctx context.Context, newPassword string) (*ResetResult, error)
	// IssueSession 为管理员创建浏览器会话（OIDC 登录成功后使用）
	IssueSession(ctx context.Context, meta ClientMeta) (*IssuedSession, error)
	PasswordLoginEnabled(ctx context.Context) (bool, error)
	// SetPasswordLoginEnabled 只负责保存开关；关闭的前置条件由调用方（登录方式）检查
	SetPasswordLoginEnabled(ctx context.Context, enabled bool) error
}

// ResetResult 命令行重置的结果
type ResetResult struct {
	// PasswordLoginReenabled 重置前密码登录处于关闭状态，已重新开启
	PasswordLoginReenabled bool
}

// passwordLoginDisabledKey settings 中的开关；值为 "1" 表示关闭
const passwordLoginDisabledKey = "password_login_disabled"

type authSvc struct {
	now       func() time.Time
	guard     *loginGuard
	mu        sync.Mutex
	setupCode string
}

var defaultAuth = newAuth()

func Auth() AuthSvc {
	return defaultAuth
}

func newAuth() *authSvc {
	return &authSvc{now: time.Now, guard: newLoginGuard()}
}

// 设置码字母表去掉了容易混淆的 0/O、1/I/L
const setupAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

func (s *authSvc) PrepareSetupCode(ctx context.Context) (string, error) {
	admin, err := admin_repo.Admin().Get(ctx)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if admin != nil {
		s.setupCode = ""
		return "", nil
	}
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成设置码: %w", err)
	}
	var b strings.Builder
	for i, v := range buf {
		if i > 0 && i%4 == 0 {
			b.WriteByte('-')
		}
		b.WriteByte(setupAlphabet[int(v)%len(setupAlphabet)])
	}
	s.setupCode = b.String()
	return s.setupCode, nil
}

func (s *authSvc) Status(ctx context.Context, _ *api.StatusRequest) (*api.StatusResponse, error) {
	admin, err := admin_repo.Admin().Get(ctx)
	if err != nil {
		return nil, err
	}
	enabled, err := s.PasswordLoginEnabled(ctx)
	if err != nil {
		return nil, err
	}
	resp := &api.StatusResponse{Initialized: admin != nil, PasswordLogin: enabled}
	provider, err := oidc_repo.OIDC().GetProvider(ctx)
	if err != nil {
		return nil, err
	}
	binding, err := oidc_repo.OIDC().GetBinding(ctx)
	if err != nil {
		return nil, err
	}
	if provider != nil && binding != nil {
		resp.OIDCLogin = &api.OIDCLogin{DisplayName: provider.DisplayName}
	}
	return resp, nil
}

func (s *authSvc) checkSetupCode(input string) bool {
	s.mu.Lock()
	want := s.setupCode
	s.mu.Unlock()
	got := strings.ToUpper(strings.TrimSpace(input))
	return want != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// ValidatePassword 校验密码强度，修改密码与命令行重置复用
func ValidatePassword(ctx context.Context, pw string) error {
	if utf8.RuneCountInString(pw) < MinPasswordLength {
		return i18n.NewError(ctx, code.PasswordTooShort)
	}
	return nil
}

func (s *authSvc) Setup(ctx context.Context, req *api.SetupRequest, meta ClientMeta) (*api.SetupResponse, *IssuedSession, error) {
	defer s.guard.serialize(meta.IP)()
	if err := s.guard.check(ctx, meta.IP, s.now()); err != nil {
		return nil, nil, err
	}
	admin, err := admin_repo.Admin().Get(ctx)
	if err != nil {
		return nil, nil, err
	}
	if admin != nil {
		return nil, nil, i18n.NewErrorWithStatus(ctx, http.StatusConflict, code.AlreadyInitialized)
	}
	if !s.checkSetupCode(req.SetupCode) {
		s.guard.fail(meta.IP, s.now())
		return nil, nil, i18n.NewError(ctx, code.SetupCodeInvalid)
	}
	if !usernamePattern.MatchString(req.Username) {
		return nil, nil, i18n.NewError(ctx, code.UsernameInvalid)
	}
	if err := ValidatePassword(ctx, req.Password); err != nil {
		return nil, nil, err
	}
	hash, err := password.Hash(req.Password)
	if err != nil {
		return nil, nil, err
	}
	now := s.now().Unix()
	admin = &admin_entity.Admin{
		ID:                 admin_entity.AdminID,
		Username:           req.Username,
		PasswordHash:       hash,
		PasswordUpdatetime: now,
		Createtime:         now,
		Updatetime:         now,
	}
	if err := admin_repo.Admin().Create(ctx, admin); err != nil {
		if errors.Is(err, admin_repo.ErrAlreadyExists) {
			return nil, nil, i18n.NewErrorWithStatus(ctx, http.StatusConflict, code.AlreadyInitialized)
		}
		return nil, nil, err
	}
	s.mu.Lock()
	s.setupCode = ""
	s.mu.Unlock()

	issued, err := s.startSession(ctx, admin, meta)
	if err != nil {
		return nil, nil, err
	}
	return &api.SetupResponse{Username: admin.Username}, issued, nil
}

// dummyHash 用户名不存在时也做一次同等代价的哈希校验，避免通过响应时间判断用户名是否正确
var dummyHash = func() string {
	h, err := password.Hash("opsnap-dummy-password")
	if err != nil {
		panic(err)
	}
	return h
}()

func (s *authSvc) Login(ctx context.Context, req *api.LoginRequest, meta ClientMeta) (*api.LoginResponse, *IssuedSession, error) {
	defer s.guard.serialize(meta.IP)()
	if err := s.guard.check(ctx, meta.IP, s.now()); err != nil {
		return nil, nil, err
	}
	enabled, err := s.PasswordLoginEnabled(ctx)
	if err != nil {
		return nil, nil, err
	}
	if !enabled {
		return nil, nil, i18n.NewForbiddenError(ctx, code.PasswordLoginDisabled)
	}
	admin, err := admin_repo.Admin().Get(ctx)
	if err != nil {
		return nil, nil, err
	}
	hash := dummyHash
	if admin != nil && admin.Username == req.Username {
		hash = admin.PasswordHash
	}
	if !password.Verify(req.Password, hash) || admin == nil || admin.Username != req.Username {
		s.guard.fail(meta.IP, s.now())
		return nil, nil, i18n.NewError(ctx, code.LoginFailed)
	}
	s.guard.succeed(meta.IP)
	issued, err := s.startSession(ctx, admin, meta)
	if err != nil {
		return nil, nil, err
	}
	return &api.LoginResponse{Username: admin.Username}, issued, nil
}

func (s *authSvc) IssueSession(ctx context.Context, meta ClientMeta) (*IssuedSession, error) {
	admin, err := admin_repo.Admin().Get(ctx)
	if err != nil {
		return nil, err
	}
	if admin == nil {
		return nil, i18n.NewError(ctx, code.NotInitialized)
	}
	return s.startSession(ctx, admin, meta)
}

func (s *authSvc) startSession(ctx context.Context, admin *admin_entity.Admin, meta ClientMeta) (*IssuedSession, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, fmt.Errorf("生成会话标识: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(buf)
	now := s.now()
	expires := now.Add(admin_entity.SessionTTL)
	if err := session_repo.Session().Create(ctx, &admin_entity.Session{
		TokenHash:  secret.HashToken(token),
		AdminID:    admin.ID,
		UserAgent:  meta.UserAgent,
		IP:         meta.IP,
		Expiretime: expires.Unix(),
		Createtime: now.Unix(),
		Updatetime: now.Unix(),
	}); err != nil {
		return nil, err
	}
	return &IssuedSession{Token: token, Expires: expires}, nil
}

func (s *authSvc) AuthenticateSession(ctx context.Context, token string) (*authctx.Principal, *IssuedSession, error) {
	if token == "" {
		return nil, nil, i18n.NewUnauthorizedError(ctx, code.Unauthorized)
	}
	sess, err := session_repo.Session().FindByTokenHash(ctx, secret.HashToken(token))
	if err != nil {
		return nil, nil, err
	}
	now := s.now()
	if sess == nil || sess.Expired(now) {
		return nil, nil, i18n.NewUnauthorizedError(ctx, code.Unauthorized)
	}
	admin, err := admin_repo.Admin().Get(ctx)
	if err != nil {
		return nil, nil, err
	}
	if admin == nil || admin.ID != sess.AdminID {
		return nil, nil, i18n.NewUnauthorizedError(ctx, code.Unauthorized)
	}
	p := &authctx.Principal{AdminID: admin.ID, Username: admin.Username, Via: authctx.ViaSession, SessionID: sess.ID}

	var refreshed *IssuedSession
	if now.Sub(time.Unix(sess.Updatetime, 0)) >= touchInterval {
		expires := now.Add(admin_entity.SessionTTL)
		if err := session_repo.Session().Touch(ctx, sess.ID, expires.Unix(), now.Unix()); err != nil {
			return nil, nil, err
		}
		refreshed = &IssuedSession{Token: token, Expires: expires}
	}
	return p, refreshed, nil
}

func (s *authSvc) Logout(ctx context.Context, p *authctx.Principal) error {
	if p == nil || p.Via != authctx.ViaSession {
		return nil
	}
	return session_repo.Session().Delete(ctx, p.SessionID)
}

func (s *authSvc) Me(ctx context.Context, _ *api.MeRequest) (*api.MeResponse, error) {
	p := authctx.From(ctx)
	if p == nil {
		return nil, i18n.NewUnauthorizedError(ctx, code.Unauthorized)
	}
	admin, err := admin_repo.Admin().Get(ctx)
	if err != nil {
		return nil, err
	}
	if admin == nil {
		return nil, i18n.NewUnauthorizedError(ctx, code.Unauthorized)
	}
	resp := &api.MeResponse{Username: admin.Username, PasswordUpdatedAt: admin.PasswordUpdatetime}
	if p.Via == authctx.ViaSession {
		sess, err := session_repo.Session().Find(ctx, p.SessionID)
		if err != nil {
			return nil, err
		}
		if sess != nil {
			resp.Session = &api.SessionInfo{UserAgent: sess.UserAgent, IP: sess.IP, ExpiresAt: sess.Expiretime}
		}
	}
	return resp, nil
}

func (s *authSvc) ChangePassword(ctx context.Context, req *api.ChangePasswordRequest, meta ClientMeta) (*api.ChangePasswordResponse, error) {
	p := authctx.From(ctx)
	if p == nil {
		return nil, i18n.NewUnauthorizedError(ctx, code.Unauthorized)
	}
	defer s.guard.serialize(meta.IP)()
	if err := s.guard.check(ctx, meta.IP, s.now()); err != nil {
		return nil, err
	}
	admin, err := admin_repo.Admin().Get(ctx)
	if err != nil {
		return nil, err
	}
	if admin == nil || !password.Verify(req.CurrentPassword, admin.PasswordHash) {
		s.guard.fail(meta.IP, s.now())
		return nil, i18n.NewError(ctx, code.CurrentPasswordWrong)
	}
	if err := s.setPassword(ctx, req.NewPassword, p.SessionID); err != nil {
		return nil, err
	}
	return &api.ChangePasswordResponse{}, nil
}

func (s *authSvc) ResetPassword(ctx context.Context, newPassword string) (*ResetResult, error) {
	admin, err := admin_repo.Admin().Get(ctx)
	if err != nil {
		return nil, err
	}
	if admin == nil {
		return nil, i18n.NewError(ctx, code.NotInitialized)
	}
	if err := s.setPassword(ctx, newPassword, 0); err != nil {
		return nil, err
	}
	enabled, err := s.PasswordLoginEnabled(ctx)
	if err != nil {
		return nil, err
	}
	if !enabled {
		if err := s.SetPasswordLoginEnabled(ctx, true); err != nil {
			return nil, err
		}
	}
	return &ResetResult{PasswordLoginReenabled: !enabled}, nil
}

func (s *authSvc) PasswordLoginEnabled(ctx context.Context) (bool, error) {
	v, _, err := setting_repo.Setting().Get(ctx, passwordLoginDisabledKey)
	return v != "1", err
}

func (s *authSvc) SetPasswordLoginEnabled(ctx context.Context, enabled bool) error {
	v := "1"
	if enabled {
		v = "0"
	}
	return setting_repo.Setting().Set(ctx, passwordLoginDisabledKey, v)
}

// setPassword 校验并保存新密码，然后让会话失效（keepSessionID 非 0 时保留该会话）
func (s *authSvc) setPassword(ctx context.Context, newPassword string, keepSessionID int64) error {
	if err := ValidatePassword(ctx, newPassword); err != nil {
		return err
	}
	hash, err := password.Hash(newPassword)
	if err != nil {
		return err
	}
	if err := admin_repo.Admin().UpdatePassword(ctx, hash, s.now().Unix()); err != nil {
		return err
	}
	return session_repo.Session().DeleteAllExcept(ctx, admin_entity.AdminID, keepSessionID)
}
