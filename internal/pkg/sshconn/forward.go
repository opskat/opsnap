package sshconn

import (
	"errors"
	"io"
	"net"
	"sync"

	"github.com/cago-frame/cago/pkg/gogo"
)

// Forward 在 127.0.0.1 的随机端口上监听，把每个本地连接经目标 SSH 主机转发到 addr
// （addr 按目标主机的视角解析，例如 "127.0.0.1:3306"）。
// 返回本地地址和关闭器；关闭后停止监听并断开已建立的转发连接。Conn 关闭时也会一并关闭。
func (c *Conn) Forward(addr string) (string, io.Closer, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	f := &forwarder{ln: ln, conn: c, addr: addr, active: map[net.Conn]struct{}{}}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		_ = ln.Close()
		return "", nil, errors.New("连接已关闭，无法转发端口")
	}
	c.forwarders = append(c.forwarders, f)
	c.mu.Unlock()
	gogo.Go(func() error {
		f.serve()
		return nil
	})
	return ln.Addr().String(), f, nil
}

type forwarder struct {
	ln   net.Listener
	conn *Conn
	addr string

	mu     sync.Mutex
	closed bool
	active map[net.Conn]struct{}
}

func (f *forwarder) serve() {
	for {
		local, err := f.ln.Accept()
		if err != nil {
			return
		}
		gogo.Go(func() error {
			f.handle(local)
			return nil
		})
	}
}

func (f *forwarder) handle(local net.Conn) {
	if !f.track(local) {
		_ = local.Close()
		return
	}
	defer f.untrack(local)
	remote, err := f.conn.client.Dial("tcp", f.addr)
	if err != nil {
		_ = local.Close()
		return
	}
	if !f.track(remote) {
		_ = local.Close()
		_ = remote.Close()
		return
	}
	defer f.untrack(remote)

	done := make(chan struct{}, 2)
	copyHalf := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		done <- struct{}{}
	}
	gogo.Go(func() error { copyHalf(remote, local); return nil })
	gogo.Go(func() error { copyHalf(local, remote); return nil })
	<-done
	_ = local.Close()
	_ = remote.Close()
	<-done
}

// track 登记活动连接；已关闭时返回 false
func (f *forwarder) track(c net.Conn) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return false
	}
	f.active[c] = struct{}{}
	return true
}

func (f *forwarder) untrack(c net.Conn) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.active, c)
}

// Close 停止监听并断开所有转发中的连接
func (f *forwarder) Close() error {
	f.mu.Lock()
	f.closed = true
	conns := make([]net.Conn, 0, len(f.active))
	for c := range f.active {
		conns = append(conns, c)
	}
	f.mu.Unlock()
	err := f.ln.Close()
	for _, c := range conns {
		_ = c.Close()
	}
	return err
}
