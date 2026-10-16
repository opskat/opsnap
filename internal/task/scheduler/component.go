package scheduler

import (
	"context"
	"sync"

	"github.com/cago-frame/cago"
	"github.com/cago-frame/cago/configs"
	"github.com/cago-frame/cago/pkg/gogo"
)

type component struct {
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// Component 把调度器注册为 cago 组件：启动时用 gogo.Go 开始循环（不使用任何请求的 ctx），应用停止时取消并等待循环退出。
func Component() cago.Component { return &component{} }

func (c *component) Start(ctx context.Context, _ *configs.Config) error {
	ctx, c.cancel = context.WithCancel(ctx)
	s := New(Deps{})
	c.wg.Add(1)
	gogo.Go(func() error {
		defer c.wg.Done()
		s.Run(ctx)
		return nil
	})
	return nil
}

func (c *component) CloseHandle() {
	if c.cancel != nil {
		c.cancel()
	}
	c.wg.Wait()
}
