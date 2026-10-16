package run_svc

import (
	"context"
	"sync"
	"time"
)

// limitRecheck 等待名额时重新读取上限的间隔，设置里调大上限后排队的运行能尽快开始
const limitRecheck = 2 * time.Second

// limiter 全局同时运行数的限制。上限每次取名额时读取（设置「同时运行的任务数」），
// 计划触发与手动触发都经 Trigger 进入同一个限制，调度器不需要另外处理。
type limiter struct {
	mu      sync.Mutex
	active  int
	changed chan struct{}
}

func newLimiter() *limiter { return &limiter{changed: make(chan struct{})} }

// acquire 等到有空闲名额；第一次需要等待时调用 waiting。ctx 结束时放弃，返回 context.Cause。
func (l *limiter) acquire(ctx context.Context, limit func() int, waiting func(limit int)) (func(), error) {
	notified := false
	for {
		n := max(limit(), 1)
		l.mu.Lock()
		if l.active < n {
			l.active++
			l.mu.Unlock()
			var once sync.Once
			return func() { once.Do(l.release) }, nil
		}
		ch := l.changed
		l.mu.Unlock()
		if !notified && waiting != nil {
			waiting(n)
			notified = true
		}
		t := time.NewTimer(limitRecheck)
		select {
		case <-ch:
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return nil, context.Cause(ctx)
		}
		t.Stop()
	}
}

func (l *limiter) release() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.active--
	close(l.changed)
	l.changed = make(chan struct{})
}
