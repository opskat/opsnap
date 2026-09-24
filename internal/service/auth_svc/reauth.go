package auth_svc

import (
	"context"
	"time"

	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/password"
	"github.com/opskat/opsnap/internal/repository/admin_repo"
)

// reauthTTL OIDC 重新验证后可以查看密钥的时限
const reauthTTL = 5 * time.Minute

func (s *authSvc) VerifyPassword(ctx context.Context, pw string, meta ClientMeta) error {
	defer s.guard.serialize(meta.IP)()
	if err := s.guard.check(ctx, meta.IP, s.now()); err != nil {
		return err
	}
	enabled, err := s.PasswordLoginEnabled(ctx)
	if err != nil {
		return err
	}
	if !enabled {
		return i18n.NewForbiddenError(ctx, code.PasswordLoginDisabled)
	}
	admin, err := admin_repo.Admin().Get(ctx)
	if err != nil {
		return err
	}
	if admin == nil || !password.Verify(pw, admin.PasswordHash) {
		s.guard.fail(meta.IP, s.now())
		return i18n.NewError(ctx, code.ReauthPasswordWrong)
	}
	return nil
}

func (s *authSvc) GrantReauth(sessionID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for id, until := range s.reauth {
		if !now.Before(until) {
			delete(s.reauth, id)
		}
	}
	s.reauth[sessionID] = now.Add(reauthTTL)
}

func (s *authSvc) ConsumeReauth(sessionID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	until, ok := s.reauth[sessionID]
	delete(s.reauth, sessionID)
	return ok && s.now().Before(until)
}
