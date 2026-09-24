package auth_svc

import (
	"context"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/opskat/opsnap/internal/pkg/code"
)

// 登录保护：同一来源 IP 在 guardWindow 内连续失败 guardMaxFailures 次，锁定 guardLockout
const (
	guardWindow      = 15 * time.Minute
	guardMaxFailures = 5
	guardLockout     = 15 * time.Minute
	// guardMaxEntries 记录条数超过它时清理过期条目，防止内存无限增长
	guardMaxEntries = 10000
)

type attempt struct {
	failures    int
	first       time.Time
	lockedUntil time.Time
}

// ipLock 同一 IP 的凭据校验串行执行；refs 为 0 时从表中移除
type ipLock struct {
	mu   sync.Mutex
	refs int
}

// loginGuard 按来源 IP 记录失败次数；只在内存中，重启后清零
type loginGuard struct {
	mu       sync.Mutex
	attempts map[string]*attempt
	locks    map[string]*ipLock
}

func newLoginGuard() *loginGuard {
	return &loginGuard{attempts: map[string]*attempt{}, locks: map[string]*ipLock{}}
}

// serialize 让同一 IP 的“检查锁定 → 校验凭据 → 记录结果”串行执行，返回释放函数。
// 否则并发请求会在第一次失败被记录前全部通过检查，绕过失败次数上限
func (g *loginGuard) serialize(ip string) func() {
	g.mu.Lock()
	l := g.locks[ip]
	if l == nil {
		l = &ipLock{}
		g.locks[ip] = l
	}
	l.refs++
	g.mu.Unlock()

	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		g.mu.Lock()
		if l.refs--; l.refs == 0 {
			delete(g.locks, ip)
		}
		g.mu.Unlock()
	}
}

// check 该 IP 处于锁定期时返回“尝试次数过多”错误
func (g *loginGuard) check(ctx context.Context, ip string, now time.Time) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	a := g.attempts[ip]
	if a == nil || !now.Before(a.lockedUntil) {
		return nil
	}
	minutes := int(math.Ceil(a.lockedUntil.Sub(now).Minutes()))
	return i18n.NewErrorWithStatus(ctx, http.StatusTooManyRequests, code.TooManyAttempts, minutes)
}

func (g *loginGuard) fail(ip string, now time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.attempts) > guardMaxEntries {
		for k, a := range g.attempts {
			if !now.Before(a.lockedUntil) && now.Sub(a.first) > guardWindow {
				delete(g.attempts, k)
			}
		}
	}
	a := g.attempts[ip]
	if a == nil {
		a = &attempt{}
		g.attempts[ip] = a
	}
	if a.failures == 0 || now.Sub(a.first) > guardWindow {
		a.failures, a.first = 0, now
	}
	a.failures++
	if a.failures >= guardMaxFailures {
		a.lockedUntil = now.Add(guardLockout)
		a.failures = 0
	}
}

func (g *loginGuard) succeed(ip string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.attempts, ip)
}
