package dump

import (
	"context"
	"io"
	"net"
	"strconv"
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
