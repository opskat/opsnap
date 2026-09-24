// Package authctx 在请求上下文中传递已认证的身份。
package authctx

import "context"

// 认证方式
const (
	ViaSession = "session"
	ViaToken   = "token"
)

// Principal 当前请求的已认证身份
type Principal struct {
	AdminID   int64
	Username  string
	Via       string
	SessionID int64
}

type key struct{}

func With(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, key{}, p)
}

// From 未认证时返回 nil
func From(ctx context.Context) *Principal {
	p, _ := ctx.Value(key{}).(*Principal)
	return p
}
