package dsconn

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/opskat/opsnap/internal/pkg/netchain"
)

// dialVia 驱动使用的拨号函数：经链路连接数据源。
// 链路某一跳失败时原样返回 *netchain.HopError，连不上数据源本身时标记为 *dialError。
func dialVia(d Dialer) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := d.Dial(ctx, network, addr)
		if err != nil {
			var he *netchain.HopError
			if errors.As(err, &he) {
				return nil, err
			}
			return nil, &dialError{err: err}
		}
		return withDeadlines(conn), nil
	}
}

// withDeadlines 连接不支持读写截止时间（经 SSH 转发）时，用“到期即关闭连接”来模拟，
// 使驱动基于截止时间的超时与取消（如 pgx 的 ctx 监视）仍能打断阻塞的读写
func withDeadlines(conn net.Conn) net.Conn {
	if conn.SetDeadline(time.Time{}) == nil {
		return conn
	}
	return &deadlineConn{Conn: conn}
}

type deadlineConn struct {
	net.Conn
	mu     sync.Mutex
	closed bool
	// timers 读、写截止时间各一个
	timers [2]*time.Timer
}

func (c *deadlineConn) SetDeadline(t time.Time) error {
	c.set(0, t)
	c.set(1, t)
	return nil
}

func (c *deadlineConn) SetReadDeadline(t time.Time) error {
	c.set(0, t)
	return nil
}

func (c *deadlineConn) SetWriteDeadline(t time.Time) error {
	c.set(1, t)
	return nil
}

func (c *deadlineConn) set(i int, t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.timers[i] != nil {
		c.timers[i].Stop()
		c.timers[i] = nil
	}
	if t.IsZero() || c.closed {
		return
	}
	c.timers[i] = time.AfterFunc(time.Until(t), func() { _ = c.Close() })
}

func (c *deadlineConn) Close() error {
	c.mu.Lock()
	for i, tm := range c.timers {
		if tm != nil {
			tm.Stop()
			c.timers[i] = nil
		}
	}
	c.closed = true
	c.mu.Unlock()
	return c.Conn.Close()
}
