// Package token_repo 读写 API 令牌。
package token_repo

import (
	"context"

	"github.com/cago-frame/cago/database/db"

	"github.com/opskat/opsnap/internal/model/entity/token_entity"
)

//go:generate mockgen -source token.go -destination mock/token.go

type TokenRepo interface {
	Create(ctx context.Context, t *token_entity.Token) error
	// List 按创建时间倒序
	List(ctx context.Context) ([]*token_entity.Token, error)
	// Find 不存在时返回 nil, nil
	Find(ctx context.Context, id int64) (*token_entity.Token, error)
	// FindByHash 不存在时返回 nil, nil
	FindByHash(ctx context.Context, hash string) (*token_entity.Token, error)
	// FindByName 返回同名的全部令牌
	FindByName(ctx context.Context, name string) ([]*token_entity.Token, error)
	Revoke(ctx context.Context, id, now int64) error
	TouchLastUsed(ctx context.Context, id, now int64) error
}

var defaultToken TokenRepo

func Token() TokenRepo {
	return defaultToken
}

func RegisterToken(i TokenRepo) {
	defaultToken = i
}

type tokenRepo struct{}

func NewToken() TokenRepo {
	return &tokenRepo{}
}

func (r *tokenRepo) Create(ctx context.Context, t *token_entity.Token) error {
	return db.Ctx(ctx).Create(t).Error
}

func (r *tokenRepo) List(ctx context.Context) ([]*token_entity.Token, error) {
	var rows []*token_entity.Token
	err := db.Ctx(ctx).Order("createtime DESC, id DESC").Find(&rows).Error
	return rows, err
}

func (r *tokenRepo) first(ctx context.Context, query string, arg any) (*token_entity.Token, error) {
	var rows []*token_entity.Token
	if err := db.Ctx(ctx).Where(query, arg).Limit(1).Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

func (r *tokenRepo) Find(ctx context.Context, id int64) (*token_entity.Token, error) {
	return r.first(ctx, "id = ?", id)
}

func (r *tokenRepo) FindByHash(ctx context.Context, hash string) (*token_entity.Token, error) {
	return r.first(ctx, "token_hash = ?", hash)
}

func (r *tokenRepo) FindByName(ctx context.Context, name string) ([]*token_entity.Token, error) {
	var rows []*token_entity.Token
	err := db.Ctx(ctx).Where("name = ?", name).Find(&rows).Error
	return rows, err
}

func (r *tokenRepo) Revoke(ctx context.Context, id, now int64) error {
	return db.Ctx(ctx).Model(&token_entity.Token{}).Where("id = ? AND revoketime = 0", id).
		Update("revoketime", now).Error
}

func (r *tokenRepo) TouchLastUsed(ctx context.Context, id, now int64) error {
	return db.Ctx(ctx).Model(&token_entity.Token{}).Where("id = ?", id).Update("lastusedtime", now).Error
}
