package dump

import (
	"context"
	"io"
	"net"
	"sync"

	"github.com/opskat/opsnap/internal/pkg/dsconn"
)

// forwarder 在 127.0.0.1 的临时端口上接受导出工具的连接，把每条连接经链路转发到数据源
type forwarder struct {
	ln     net.Listener
	dialer dsconn.Dialer
	target string
	ctx    context.Context //nolint:containedctx // 每条转发连接在接受时才拨号，沿用会话的 ctx
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu      sync.Mutex
	closed  bool
	conns   map[net.Conn]struct{}
	dialErr error
}

func listen(ctx context.Context, d dsconn.Dialer, target string) (*forwarder, error) {
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	fctx, cancel := context.WithCancel(ctx)
	f := &forwarder{ln: ln, dialer: d, target: target, ctx: fctx, cancel: cancel, conns: map[net.Conn]struct{}{}}
	f.wg.Add(1)
	go f.serve()
	return f, nil
}

func (f *forwarder) addr() string { return f.ln.Addr().String() }

func (f *forwarder) serve() {
	defer f.wg.Done()
	for {
		c, err := f.ln.Accept()
		if err != nil {
			return
		}
		f.wg.Add(1)
		go f.handle(c)
	}
}

func (f *forwarder) handle(local net.Conn) {
	defer f.wg.Done()
	if !f.track(local) {
		return
	}
	defer f.untrack(local)
	up, err := f.dialer.Dial(f.ctx, "tcp", f.target)
	if err != nil {
		f.mu.Lock()
		f.dialErr = err
		f.mu.Unlock()
		return
	}
	if !f.track(up) {
		return
	}
	defer f.untrack(up)
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		done <- struct{}{}
	}
	go cp(up, local)
	go cp(local, up)
	<-done
	// 一端结束即关闭两端，另一个方向的复制随之结束
	_ = local.Close()
	_ = up.Close()
	<-done
}

// track 登记连接以便 close 时关闭；已关闭时直接关闭该连接并返回 false
func (f *forwarder) track(c net.Conn) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		_ = c.Close()
		return false
	}
	f.conns[c] = struct{}{}
	return true
}

func (f *forwarder) untrack(c net.Conn) {
	f.mu.Lock()
	delete(f.conns, c)
	f.mu.Unlock()
	_ = c.Close()
}

// lastErr 最近一次经链路拨号数据源的错误
func (f *forwarder) lastErr() error {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.dialErr
}

// close 停止监听，关闭所有已转发的连接并等待转发结束
func (f *forwarder) close() {
	f.cancel()
	_ = f.ln.Close()
	f.mu.Lock()
	f.closed = true
	for c := range f.conns {
		_ = c.Close()
	}
	f.mu.Unlock()
	f.wg.Wait()
}
