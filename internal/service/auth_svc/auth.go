// Package auth_svc 管理员登录、会话与 API 密钥认证；单点登录的流程在 sso_svc，通过后调用这里签发会话
package auth_svc

import (
	"context"
	"crypto/subtle"
	"sync"
	"time"

	"github.com/cago-frame/cago/pkg/i18n"
	"golang.org/x/crypto/bcrypt"

	authapi "github.com/opskat/opsnap/internal/api/auth"
	"github.com/opskat/opsnap/internal/model/auth"
	"github.com/opskat/opsnap/internal/model/entity/admin_entity"
	"github.com/opskat/opsnap/internal/model/entity/apikey_entity"
	"github.com/opskat/opsnap/internal/model/entity/session_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/repository/admin_repo"
	"github.com/opskat/opsnap/internal/repository/apikey_repo"
	"github.com/opskat/opsnap/internal/repository/session_repo"
	"github.com/opskat/opsnap/internal/repository/sso_repo"
)

const (
	// SessionCookie 会话 Cookie 名
	SessionCookie = "opsnap_session"

	sessionTTL        = 7 * 24 * time.Hour
	minPasswordLen    = 8
	maxPasswordBytes  = 72 // bcrypt 只处理前 72 字节
	lastUsedThreshold = time.Minute
)

// passwordCost bcrypt 开销；测试里调低
var passwordCost = bcrypt.DefaultCost

// LoginResult 登录成功的结果；Token 由控制器写入 HttpOnly Cookie
type LoginResult struct {
	Token     string
	ExpiresAt time.Time
	Username  string
}

// AuthSvc 认证服务
type AuthSvc interface {
	// Status 是否已创建管理员
	Status(ctx context.Context) (*authapi.StatusResponse, error)
	// Setup 首次运行创建管理员，仅在尚无管理员时可用
	Setup(ctx context.Context, req *authapi.SetupRequest) (*authapi.SetupResponse, error)
	// Login 校验用户名密码；连续失败 5 次锁定 10 分钟
	Login(ctx context.Context, req *authapi.LoginRequest) (*LoginResult, error)
	// Logout 使会话令牌失效
	Logout(ctx context.Context, token string) error
	// Me 当前请求的身份（需要经过认证中间件）
	Me(ctx context.Context) (*authapi.MeResponse, error)
	// ChangePassword 修改密码，并使 currentToken 以外的会话失效
	ChangePassword(ctx context.Context, req *authapi.ChangePasswordRequest, currentToken string) (*authapi.ChangePasswordResponse, error)
	// IssueSession 单点登录通过后签发管理员会话（与密码登录相同的有效期）；不经过、也不影响密码错误计数
	IssueSession(ctx context.Context) (*LoginResult, error)
	// Authenticate 用会话令牌或 Bearer API 密钥确认身份；带了 Bearer 时只认密钥
	Authenticate(ctx context.Context, sessionToken, bearer string) (auth.Actor, error)
}

type authSvc struct {
	now func() time.Time
	// mu 串行化登录与初始化，保证失败计数不会被并发请求绕过
	mu sync.Mutex
}

func newAuth(now func() time.Time) *authSvc { return &authSvc{now: now} }

var defaultAuth AuthSvc = newAuth(time.Now)

// Auth 取得认证服务
func Auth() AuthSvc { return defaultAuth }

// RegisterAuth 替换认证服务实现（测试注入时钟用）
func RegisterAuth(i AuthSvc) { defaultAuth = i }

// NewAuth 创建使用指定时钟的认证服务
func NewAuth(now func() time.Time) AuthSvc { return newAuth(now) }

func (s *authSvc) Status(ctx context.Context) (*authapi.StatusResponse, error) {
	a, err := admin_repo.Admin().Find(ctx)
	if err != nil {
		return nil, err
	}
	resp := &authapi.StatusResponse{Initialized: a != nil}
	cfg, err := sso_repo.SSO().Get(ctx)
	if err != nil {
		return nil, err
	}
	if cfg.Configured() && cfg.Bound() {
		resp.SSOEnabled, resp.SSOLabel = true, cfg.ButtonLabel
	}
	return resp, nil
}

func checkPassword(ctx context.Context, pw string) error {
	if len([]rune(pw)) < minPasswordLen {
		return i18n.NewError(ctx, code.PasswordTooShort, minPasswordLen)
	}
	if len(pw) > maxPasswordBytes {
		return i18n.NewError(ctx, code.PasswordTooLong, maxPasswordBytes)
	}
	return nil
}

func (s *authSvc) Setup(ctx context.Context, req *authapi.SetupRequest) (*authapi.SetupResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, err := admin_repo.Admin().Find(ctx)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, i18n.NewError(ctx, code.AdminAlreadyInitialized)
	}
	if err := checkPassword(ctx, req.Password); err != nil {
		return nil, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), passwordCost)
	if err != nil {
		return nil, err
	}
	now := s.now().Unix()
	if err := admin_repo.Admin().Create(ctx, &admin_entity.Admin{
		Username: req.Username, PasswordHash: string(hash), Createtime: now, Updatetime: now,
	}); err != nil {
		return nil, err
	}
	return &authapi.SetupResponse{}, nil
}

func (s *authSvc) Login(ctx context.Context, req *authapi.LoginRequest) (*LoginResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	admin, err := admin_repo.Admin().Find(ctx)
	if err != nil {
		return nil, err
	}
	if admin == nil {
		return nil, i18n.NewError(ctx, code.AdminNotInitialized)
	}
	if admin.IsLocked(now) {
		return nil, lockedError(ctx, admin.LockRemaining(now))
	}

	// 无论用户名是否匹配都做一次密码比对，避免通过耗时判断用户名
	pwOK := bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(req.Password)) == nil
	userOK := subtle.ConstantTimeCompare([]byte(admin.Username), []byte(req.Username)) == 1
	if !pwOK || !userOK {
		remaining, locked := admin.RecordFailure(now)
		admin.Updatetime = now.Unix()
		if err := admin_repo.Admin().Update(ctx, admin); err != nil {
			return nil, err
		}
		if locked {
			return nil, lockedError(ctx, admin_entity.LockDuration)
		}
		return nil, i18n.NewError(ctx, code.LoginFailed, remaining)
	}

	if admin.FailedCount != 0 || admin.LockedUntil != 0 {
		admin.ResetFailures()
		admin.Updatetime = now.Unix()
		if err := admin_repo.Admin().Update(ctx, admin); err != nil {
			return nil, err
		}
	}
	return newSession(ctx, now, admin.Username)
}

// newSession 创建一个会话，密码登录与单点登录共用
func newSession(ctx context.Context, now time.Time, username string) (*LoginResult, error) {
	token, hash, err := session_entity.NewToken()
	if err != nil {
		return nil, err
	}
	expires := now.Add(sessionTTL)
	_ = session_repo.Session().DeleteExpired(ctx, now.Unix()) // 顺手清理，失败不影响登录
	if err := session_repo.Session().Create(ctx, &session_entity.Session{
		TokenHash: hash, ExpiresAt: expires.Unix(), Createtime: now.Unix(),
	}); err != nil {
		return nil, err
	}
	return &LoginResult{Token: token, ExpiresAt: expires, Username: username}, nil
}

func (s *authSvc) IssueSession(ctx context.Context) (*LoginResult, error) {
	admin, err := admin_repo.Admin().Find(ctx)
	if err != nil {
		return nil, err
	}
	if admin == nil {
		return nil, i18n.NewError(ctx, code.AdminNotInitialized)
	}
	return newSession(ctx, s.now(), admin.Username)
}

// lockedError 向上取整到分钟，保证提示的时间不会短于实际剩余时间
func lockedError(ctx context.Context, remaining time.Duration) error {
	minutes := int((remaining + time.Minute - 1) / time.Minute)
	return i18n.NewError(ctx, code.LoginLocked, minutes)
}

func (s *authSvc) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return session_repo.Session().Delete(ctx, session_entity.HashToken(token))
}

func (s *authSvc) Me(ctx context.Context) (*authapi.MeResponse, error) {
	a, ok := auth.ActorFrom(ctx)
	if !ok {
		return nil, i18n.NewUnauthorizedError(ctx, code.Unauthorized)
	}
	if a.Kind == auth.KindAPIKey {
		return &authapi.MeResponse{Kind: string(a.Kind), KeyID: a.ID, KeyName: a.Name}, nil
	}
	return &authapi.MeResponse{Kind: string(a.Kind), Username: a.Name}, nil
}

func (s *authSvc) ChangePassword(ctx context.Context, req *authapi.ChangePasswordRequest, currentToken string) (*authapi.ChangePasswordResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	admin, err := admin_repo.Admin().Find(ctx)
	if err != nil {
		return nil, err
	}
	if admin == nil {
		return nil, i18n.NewError(ctx, code.AdminNotInitialized)
	}
	// 原密码错误与登录失败共用计数和锁定，避免拿到会话或 API 密钥的人借这里不限次数地猜密码
	now := s.now()
	if admin.IsLocked(now) {
		return nil, lockedError(ctx, admin.LockRemaining(now))
	}
	if bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(req.OldPassword)) != nil {
		_, locked := admin.RecordFailure(now)
		admin.Updatetime = now.Unix()
		if err := admin_repo.Admin().Update(ctx, admin); err != nil {
			return nil, err
		}
		if locked {
			return nil, lockedError(ctx, admin_entity.LockDuration)
		}
		return nil, i18n.NewError(ctx, code.OldPasswordWrong)
	}
	if err := checkPassword(ctx, req.NewPassword); err != nil {
		return nil, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), passwordCost)
	if err != nil {
		return nil, err
	}
	admin.PasswordHash = string(hash)
	admin.ResetFailures()
	admin.Updatetime = now.Unix()
	if err := admin_repo.Admin().Update(ctx, admin); err != nil {
		return nil, err
	}
	if err := session_repo.Session().DeleteExcept(ctx, session_entity.HashToken(currentToken)); err != nil {
		return nil, err
	}
	return &authapi.ChangePasswordResponse{}, nil
}

func (s *authSvc) Authenticate(ctx context.Context, sessionToken, bearer string) (auth.Actor, error) {
	if bearer != "" {
		return s.authenticateKey(ctx, bearer)
	}
	return s.authenticateSession(ctx, sessionToken)
}

func (s *authSvc) authenticateKey(ctx context.Context, key string) (auth.Actor, error) {
	unauthorized := i18n.NewUnauthorizedError(ctx, code.Unauthorized)
	if len(key) <= len(apikey_entity.KeyPrefix) || key[:len(apikey_entity.KeyPrefix)] != apikey_entity.KeyPrefix {
		return auth.Actor{}, unauthorized
	}
	k, err := apikey_repo.APIKey().FindByHash(ctx, apikey_entity.HashKey(key))
	if err != nil {
		return auth.Actor{}, err
	}
	now := s.now()
	if k == nil || !k.IsUsable(now) {
		return auth.Actor{}, unauthorized
	}
	if now.Unix()-k.LastUsedAt >= int64(lastUsedThreshold/time.Second) {
		// 最近使用时间只是展示信息，更新失败不拒绝请求
		_ = apikey_repo.APIKey().TouchLastUsed(ctx, k.ID, now.Unix())
	}
	return auth.Actor{Kind: auth.KindAPIKey, ID: k.ID, Name: k.Name, Prefix: k.Prefix}, nil
}

func (s *authSvc) authenticateSession(ctx context.Context, token string) (auth.Actor, error) {
	unauthorized := i18n.NewUnauthorizedError(ctx, code.Unauthorized)
	if token == "" {
		return auth.Actor{}, unauthorized
	}
	sess, err := session_repo.Session().FindByHash(ctx, session_entity.HashToken(token))
	if err != nil {
		return auth.Actor{}, err
	}
	if sess == nil || sess.Expired(s.now()) {
		return auth.Actor{}, unauthorized
	}
	admin, err := admin_repo.Admin().Find(ctx)
	if err != nil {
		return auth.Actor{}, err
	}
	if admin == nil {
		return auth.Actor{}, unauthorized
	}
	return auth.Actor{Kind: auth.KindAdmin, Name: admin.Username}, nil
}
