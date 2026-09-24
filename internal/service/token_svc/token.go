// Package token_svc 实现 API 令牌的生成、吊销与认证。
package token_svc

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cago-frame/cago/pkg/i18n"

	api "github.com/opskat/opsnap/internal/api/token"
	"github.com/opskat/opsnap/internal/model/entity/token_entity"
	"github.com/opskat/opsnap/internal/pkg/authctx"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/secret"
	"github.com/opskat/opsnap/internal/repository/admin_repo"
	"github.com/opskat/opsnap/internal/repository/token_repo"
)

// TokenPrefix 令牌固定前缀，便于在日志、代码仓库中识别泄露的令牌
const TokenPrefix = "onp_"

// displayPrefixLen 列表中显示的令牌前缀长度（含 onp_）
const displayPrefixLen = 8

const maxNameLength = 64

// allowedDays 允许的有效期（天），0 表示永不过期
var allowedDays = map[int]bool{0: true, 30: true, 90: true, 365: true}

type TokenSvc interface {
	List(ctx context.Context, req *api.ListRequest) (*api.ListResponse, error)
	Create(ctx context.Context, req *api.CreateRequest) (*api.CreateResponse, error)
	Revoke(ctx context.Context, req *api.RevokeRequest) (*api.RevokeResponse, error)
	// Authenticate 校验 Bearer 令牌并更新最近使用时间（精确到分钟，记录所在分钟的起点）
	Authenticate(ctx context.Context, token string) (*authctx.Principal, error)
}

type tokenSvc struct {
	now func() time.Time
}

var defaultToken = newToken()

func Token() TokenSvc {
	return defaultToken
}

func newToken() *tokenSvc {
	return &tokenSvc{now: time.Now}
}

func (s *tokenSvc) toItem(t *token_entity.Token) *api.Item {
	return &api.Item{
		ID:         t.ID,
		Name:       t.Name,
		Prefix:     t.Prefix,
		Status:     t.Status(s.now()),
		CreatedAt:  t.Createtime,
		LastUsedAt: t.Lastusedtime,
		ExpiresAt:  t.Expiretime,
		RevokedAt:  t.Revoketime,
	}
}

func (s *tokenSvc) List(ctx context.Context, _ *api.ListRequest) (*api.ListResponse, error) {
	rows, err := token_repo.Token().List(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]*api.Item, 0, len(rows))
	for _, t := range rows {
		items = append(items, s.toItem(t))
	}
	return &api.ListResponse{Items: items}, nil
}

func (s *tokenSvc) Create(ctx context.Context, req *api.CreateRequest) (*api.CreateResponse, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" || utf8.RuneCountInString(name) > maxNameLength {
		return nil, i18n.NewError(ctx, code.TokenNameInvalid)
	}
	if !allowedDays[req.ExpiresInDays] {
		return nil, i18n.NewError(ctx, code.TokenExpiryInvalid)
	}
	now := s.now()
	same, err := token_repo.Token().FindByName(ctx, name)
	if err != nil {
		return nil, err
	}
	for _, t := range same {
		if t.Status(now) == token_entity.StatusActive {
			return nil, i18n.NewError(ctx, code.TokenNameDuplicate)
		}
	}

	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, fmt.Errorf("生成令牌: %w", err)
	}
	raw := TokenPrefix + base64.RawURLEncoding.EncodeToString(buf)
	t := &token_entity.Token{
		Name:       name,
		Prefix:     raw[:displayPrefixLen],
		TokenHash:  secret.HashToken(raw),
		Createtime: now.Unix(),
	}
	if req.ExpiresInDays > 0 {
		t.Expiretime = now.Add(time.Duration(req.ExpiresInDays) * 24 * time.Hour).Unix()
	}
	if err := token_repo.Token().Create(ctx, t); err != nil {
		return nil, err
	}
	return &api.CreateResponse{Item: s.toItem(t), Token: raw}, nil
}

func (s *tokenSvc) Revoke(ctx context.Context, req *api.RevokeRequest) (*api.RevokeResponse, error) {
	t, err := token_repo.Token().Find(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, i18n.NewNotFoundError(ctx, code.TokenNotFound)
	}
	if err := token_repo.Token().Revoke(ctx, t.ID, s.now().Unix()); err != nil {
		return nil, err
	}
	return &api.RevokeResponse{}, nil
}

func (s *tokenSvc) Authenticate(ctx context.Context, token string) (*authctx.Principal, error) {
	if !strings.HasPrefix(token, TokenPrefix) {
		return nil, i18n.NewUnauthorizedError(ctx, code.TokenInvalid)
	}
	t, err := token_repo.Token().FindByHash(ctx, secret.HashToken(token))
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, i18n.NewUnauthorizedError(ctx, code.TokenInvalid)
	}
	now := s.now()
	switch t.Status(now) {
	case token_entity.StatusRevoked:
		return nil, i18n.NewUnauthorizedError(ctx, code.TokenRevoked)
	case token_entity.StatusExpired:
		return nil, i18n.NewUnauthorizedError(ctx, code.TokenExpired)
	}
	admin, err := admin_repo.Admin().Get(ctx)
	if err != nil {
		return nil, err
	}
	if admin == nil {
		return nil, i18n.NewUnauthorizedError(ctx, code.TokenInvalid)
	}
	// 精度到分钟：记录所在分钟的起点，同一分钟内的多次使用只写一次
	if minute := now.Truncate(time.Minute).Unix(); t.Lastusedtime != minute {
		if err := token_repo.Token().TouchLastUsed(ctx, t.ID, minute); err != nil {
			return nil, err
		}
	}
	return &authctx.Principal{AdminID: admin.ID, Username: admin.Username, Via: authctx.ViaToken}, nil
}
