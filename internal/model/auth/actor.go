// Package auth 请求发起者（Actor）在 ctx 中的传递，供后续任务记录触发方式。
package auth

import "context"

// Kind 发起者类型
type Kind string

const (
	// KindAdmin 管理员会话
	KindAdmin Kind = "admin"
	// KindAPIKey API 密钥
	KindAPIKey Kind = "api_key"
)

// Actor 发起请求的主体：管理员会话，或某把 API 密钥（ID、Name、Prefix 为该密钥的 id、名称和列表里显示的前缀）
type Actor struct {
	Kind   Kind
	ID     int64
	Name   string
	Prefix string
}

// String 用于运行记录的触发方式展示
func (a Actor) String() string {
	if a.Kind == KindAPIKey {
		return "api_key:" + a.Name
	}
	return string(KindAdmin)
}

type actorKey struct{}

// WithActor 把发起者写入 ctx
func WithActor(ctx context.Context, a Actor) context.Context {
	return context.WithValue(ctx, actorKey{}, a)
}

// ActorFrom 从 ctx 取出发起者；未经认证中间件时 ok 为 false
func ActorFrom(ctx context.Context) (Actor, bool) {
	a, ok := ctx.Value(actorKey{}).(Actor)
	return a, ok
}
