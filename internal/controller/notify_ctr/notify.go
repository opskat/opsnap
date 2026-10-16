// Package notify_ctr 通知接口控制器
package notify_ctr

import (
	"context"

	notifyapi "github.com/opskat/opsnap/internal/api/notify"
	"github.com/opskat/opsnap/internal/service/notify_svc"
)

type Notify struct{}

func NewNotify() *Notify { return &Notify{} }

func (n *Notify) Get(ctx context.Context, req *notifyapi.GetRequest) (*notifyapi.GetResponse, error) {
	return notify_svc.Notify().View(ctx)
}

func (n *Notify) Update(ctx context.Context, req *notifyapi.UpdateRequest) (*notifyapi.UpdateResponse, error) {
	return notify_svc.Notify().Update(ctx, req)
}

func (n *Notify) Test(ctx context.Context, req *notifyapi.TestRequest) (*notifyapi.TestResponse, error) {
	return notify_svc.Notify().Test(ctx)
}
