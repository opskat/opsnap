package dump

import (
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opsnap/internal/pkg/fakessh"
	"github.com/opskat/opsnap/internal/pkg/netchain"
)

// echoServer 原样回写的 TCP 服务，模拟链路另一端的数据库
func echoServer(t *testing.T) string {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				_, _ = io.Copy(c, c)
			}()
		}
	}()
	return ln.Addr().String()
}

// 本机端口上的连接经 SSH 跳板转发到数据源；关闭会话后端口与已转发的连接都关闭
func TestForwardThroughChain(t *testing.T) {
	e := newFakeEnv(t)
	e.tool("mysqldump", oracleVersion, mysqlOK)
	(&fakeDB{answer: tablesOnly()}).install(t)

	srv, err := fakessh.Listen("127.0.0.1:0", fakessh.Config{User: "root", Password: "jump-pw"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Close() })
	sshHost, sshPort, err := net.SplitHostPort(srv.Addr())
	require.NoError(t, err)
	port, err := strconv.Atoi(sshPort)
	require.NoError(t, err)
	chain, err := netchain.NewChain([]netchain.Hop{{
		ID: 1, Name: "jump", Kind: netchain.KindSSH, Host: sshHost, Port: port,
		User: "root", Password: "jump-pw", HostKey: srv.Fingerprint(),
	}})
	require.NoError(t, err)
	tun, err := chain.Connect(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { _ = tun.Close() })

	target := echoServer(t)
	host, tport, err := net.SplitHostPort(target)
	require.NoError(t, err)
	src := mysqlSource()
	src.Dialer = tun
	src.Config.Host = host
	src.Config.Port, err = strconv.Atoi(tport)
	require.NoError(t, err)

	s, err := Start(context.Background(), e.base, src, Options{Databases: []string{"app"}})
	require.NoError(t, err)
	defer func() { _ = s.Close() }()

	d := &net.Dialer{Timeout: 5 * time.Second}
	c, err := d.DialContext(context.Background(), "tcp", s.fwd.addr())
	require.NoError(t, err)
	defer func() { _ = c.Close() }()
	_, err = c.Write([]byte("ping"))
	require.NoError(t, err)
	buf := make([]byte, 4)
	_, err = io.ReadFull(c, buf)
	require.NoError(t, err)
	assert.Equal(t, "ping", string(buf))
	assert.Contains(t, srv.Forwards(), target, "连接经跳板转发到数据源地址")

	addr := s.fwd.addr()
	require.NoError(t, s.Close())
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err = c.Read(buf)
	assert.Error(t, err, "关闭会话后已转发的连接也被关闭")
	_, err = d.DialContext(context.Background(), "tcp", addr)
	assert.Error(t, err, "关闭会话后本机端口不再监听")
}

// flakyListener 第一次 Accept 返回错误（如文件描述符用尽），之后正常
type flakyListener struct {
	net.Listener
	failed atomic.Bool
}

func (l *flakyListener) Accept() (net.Conn, error) {
	if !l.failed.Swap(true) {
		return nil, errors.New("accept tcp: too many open files")
	}
	return l.Listener.Accept()
}

type plainDialer struct {
	noDialer
	fails atomic.Int32
}

func (d *plainDialer) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	if d.fails.Add(-1) >= 0 {
		return nil, errors.New("经链路拨号失败")
	}
	return (&net.Dialer{}).DialContext(ctx, network, addr)
}

func echo(t *testing.T, addr string) error {
	t.Helper()
	c, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(context.Background(), "tcp", addr)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Write([]byte("ping")); err != nil {
		return err
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(c, buf); err != nil {
		return err
	}
	if string(buf) != "ping" {
		return errors.New("回写内容不对")
	}
	return nil
}

// 接受连接偶尔失败（如文件描述符暂时用尽）后继续接受：端口仍在监听，工具的连接不能被挂起直到超时
func TestForwardKeepsAcceptingAfterError(t *testing.T) {
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	f := &forwarder{ln: &flakyListener{Listener: ln}, dialer: &plainDialer{}, target: echoServer(t), ctx: ctx, cancel: cancel,
		conns: map[net.Conn]struct{}{}}
	f.wg.Add(1)
	go f.serve()
	defer f.close()
	assert.NoError(t, echo(t, f.addr()))
}

// 工具失败时附带的链路错误是最近一次拨号的结果：之前某条连接拨号失败、之后又拨通了，不再把旧错误算到工具头上
func TestForwardDialErrorIsLatest(t *testing.T) {
	d := &plainDialer{}
	d.fails.Store(1)
	f, err := listen(context.Background(), d, echoServer(t))
	require.NoError(t, err)
	defer f.close()
	assert.Error(t, echo(t, f.addr()), "第一次拨号失败，本机连接被关闭")
	require.Eventually(t, func() bool { return f.lastErr() != nil }, 5*time.Second, 10*time.Millisecond)
	require.NoError(t, echo(t, f.addr()))
	assert.NoError(t, f.lastErr())
}
