package sshconn

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opsnap/internal/pkg/sshconn/sshconntest"
)

// sshHop 指向进程内 SSH 服务的一跳，指纹默认已核对
func sshHop(t *testing.T, name string, s *sshconntest.SSHServer, user, password string) Hop {
	t.Helper()
	host, port := sshconntest.HostPort(t, s.Addr)
	return Hop{Kind: KindSSH, Name: name, Host: host, Port: port, User: user, Password: password, Fingerprint: s.Fingerprint}
}

func proxyHop(t *testing.T, kind Kind, name string, addr, user, password string) Hop {
	t.Helper()
	host, port := sshconntest.HostPort(t, addr)
	return Hop{Kind: kind, Name: name, Host: host, Port: port, User: user, Password: password}
}

// dial 建立连接并在测试结束时关闭
func dial(t *testing.T, cfg Config) (*Conn, []HopResult, error) {
	t.Helper()
	c, results, err := DialConfig(context.Background(), cfg)
	if c != nil {
		t.Cleanup(func() { _ = c.Close() })
	}
	return c, results, err
}

func TestDialChain(t *testing.T) {
	t.Run("按 SOCKS5 → SSH 跳板 → 目标 的顺序逐跳连接，并逐跳给出延迟", func(t *testing.T) {
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "target-pw"})
		jump := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "ops", Password: "jump-pw"})
		socks := sshconntest.StartSOCKS5(t, sshconntest.ProxyOptions{User: "px", Password: "px-pw", Delay: 300 * time.Millisecond})

		c, results, err := dial(t, Config{
			Hops: []Hop{
				proxyHop(t, KindSOCKS5, "代理", socks.Addr, "px", "px-pw"),
				sshHop(t, "跳板", jump, "ops", "jump-pw"),
			},
			Target: sshHop(t, "目标", target, "app", "target-pw"),
		})
		require.NoError(t, err)
		require.NotNil(t, c)
		require.NotNil(t, c.Client())

		// 本机只连 SOCKS5，SOCKS5 只连跳板，跳板只转发到目标
		assert.Equal(t, []string{jump.Addr}, socks.Targets())
		assert.Equal(t, []string{target.Addr}, jump.DirectTCPIP())
		assert.Equal(t, 1, target.Connections())

		require.Len(t, results, 3)
		for i, r := range results {
			assert.Equal(t, i+1, r.Index)
			assert.True(t, r.OK, r.Message)
			assert.Empty(t, r.Reason)
			assert.Greater(t, r.Latency, time.Duration(0))
		}
		assert.Equal(t, []Kind{KindSOCKS5, KindSSH, KindSSH}, []Kind{results[0].Kind, results[1].Kind, results[2].Kind})
		assert.Equal(t, "跳板", results[1].Name)
		assert.Equal(t, jump.Addr, results[1].Addr)
		// 代理的握手延迟只算在第 1 跳上
		assert.GreaterOrEqual(t, results[0].LatencyMs, int64(300))
		assert.Less(t, results[1].Latency, 300*time.Millisecond)
		assert.Less(t, results[2].Latency, 300*time.Millisecond)
		// SSH 跳返回实际的主机指纹
		assert.Equal(t, jump.Fingerprint, results[1].Fingerprint)
		assert.Equal(t, target.Fingerprint, results[2].Fingerprint)
	})

	t.Run("经 HTTP CONNECT 代理连接，目标用带口令的私钥认证", func(t *testing.T) {
		key := sshconntest.GenerateKey(t, "key-pass")
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", AuthorizedKey: key.Public})
		proxy := sshconntest.StartHTTPConnect(t, sshconntest.ProxyOptions{User: "hp", Password: "hp-pw"})

		tgt := sshHop(t, "目标", target, "app", "")
		tgt.PrivateKey, tgt.Passphrase = key.PrivatePEM, "key-pass"
		_, results, err := dial(t, Config{
			Hops:   []Hop{proxyHop(t, KindHTTP, "HTTP 代理", proxy.Addr, "hp", "hp-pw")},
			Target: tgt,
		})
		require.NoError(t, err)
		assert.Equal(t, []string{target.Addr}, proxy.Targets())
		require.Len(t, results, 2)
		assert.True(t, results[0].OK)
		assert.True(t, results[1].OK)
	})

	t.Run("没有代理时直连目标", func(t *testing.T) {
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "pw"})
		_, results, err := dial(t, Config{Target: sshHop(t, "目标", target, "app", "pw")})
		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.True(t, results[0].OK)
	})
}

func TestDialStopsAtFirstFailure(t *testing.T) {
	t.Run("跳板认证失败：标出第 2 跳与认证失败，目标不再尝试", func(t *testing.T) {
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "pw"})
		jump := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "ops", Password: "jump-pw"})
		socks := sshconntest.StartSOCKS5(t, sshconntest.ProxyOptions{})

		c, results, err := dial(t, Config{
			Hops: []Hop{
				proxyHop(t, KindSOCKS5, "代理", socks.Addr, "", ""),
				sshHop(t, "跳板", jump, "ops", "wrong"),
			},
			Target: sshHop(t, "目标", target, "app", "pw"),
		})
		require.Error(t, err)
		assert.Nil(t, c)
		require.Len(t, results, 2)
		assert.True(t, results[0].OK)
		assert.False(t, results[1].OK)
		assert.Equal(t, ReasonAuthFailed, results[1].Reason)
		assert.Contains(t, results[1].Message, "认证失败")
		assert.Equal(t, 0, target.Connections())

		var he *HopError
		require.ErrorAs(t, err, &he)
		assert.Equal(t, 2, he.Index)
		assert.Equal(t, ReasonAuthFailed, he.Reason)
		assert.Contains(t, err.Error(), "第 2 跳")
		assert.NotContains(t, err.Error(), "wrong")
	})

	t.Run("SOCKS5 代理密码错误：第 1 跳认证失败", func(t *testing.T) {
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "pw"})
		socks := sshconntest.StartSOCKS5(t, sshconntest.ProxyOptions{User: "px", Password: "px-pw"})
		_, results, err := dial(t, Config{
			Hops:   []Hop{proxyHop(t, KindSOCKS5, "代理", socks.Addr, "px", "bad")},
			Target: sshHop(t, "目标", target, "app", "pw"),
		})
		require.Error(t, err)
		require.Len(t, results, 1)
		assert.Equal(t, ReasonAuthFailed, results[0].Reason)
		assert.Equal(t, 0, target.Connections())
	})

	t.Run("HTTP CONNECT 代理返回 407：代理这一跳认证失败", func(t *testing.T) {
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "pw"})
		proxy := sshconntest.StartHTTPConnect(t, sshconntest.ProxyOptions{User: "hp", Password: "hp-pw"})
		_, results, err := dial(t, Config{
			Hops:   []Hop{proxyHop(t, KindHTTP, "HTTP 代理", proxy.Addr, "hp", "bad")},
			Target: sshHop(t, "目标", target, "app", "pw"),
		})
		require.Error(t, err)
		require.Len(t, results, 1)
		assert.False(t, results[0].OK)
		assert.Equal(t, ReasonAuthFailed, results[0].Reason)
		assert.Equal(t, 0, target.Connections())
	})

	t.Run("代理不应答：第 1 跳超时，后面各跳不再尝试", func(t *testing.T) {
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "pw"})
		hole := sshconntest.StartBlackhole(t)
		start := time.Now()
		_, results, err := dial(t, Config{
			Hops:    []Hop{proxyHop(t, KindSOCKS5, "代理", hole, "", "")},
			Target:  sshHop(t, "目标", target, "app", "pw"),
			Timeout: 200 * time.Millisecond,
		})
		require.Error(t, err)
		assert.Less(t, time.Since(start), 3*time.Second)
		require.Len(t, results, 1)
		assert.Equal(t, ReasonTimeout, results[0].Reason)
		assert.Contains(t, results[0].Message, "超时")
		assert.Equal(t, 0, target.Connections())
	})

	t.Run("目标 SSH 不应答：目标这一跳超时", func(t *testing.T) {
		hole := sshconntest.StartBlackhole(t)
		host, port := sshconntest.HostPort(t, hole)
		_, results, err := dial(t, Config{
			Target:  Hop{Kind: KindSSH, Name: "目标", Host: host, Port: port, User: "app", Password: "pw", Fingerprint: "SHA256:x"},
			Timeout: 200 * time.Millisecond,
		})
		require.Error(t, err)
		require.Len(t, results, 1)
		assert.Equal(t, ReasonTimeout, results[0].Reason)
	})

	t.Run("调用方的截止时间先到：记为超时而不是取消", func(t *testing.T) {
		hole := sshconntest.StartBlackhole(t)
		host, port := sshconntest.HostPort(t, hole)
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		_, results, err := DialConfig(ctx, Config{
			Target: Hop{Kind: KindSSH, Name: "目标", Host: host, Port: port, User: "app", Password: "pw", Fingerprint: "SHA256:x"},
		})
		require.Error(t, err)
		require.Len(t, results, 1)
		assert.Equal(t, ReasonTimeout, results[0].Reason)
	})

	t.Run("目标端口没有监听：连接失败", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		addr := ln.Addr().String()
		require.NoError(t, ln.Close())
		host, port := sshconntest.HostPort(t, addr)
		_, results, err := dial(t, Config{Target: Hop{Kind: KindSSH, Name: "目标", Host: host, Port: port, User: "app", Password: "pw", Fingerprint: "SHA256:x"}})
		require.Error(t, err)
		require.Len(t, results, 1)
		assert.Equal(t, ReasonUnreachable, results[0].Reason)
	})
}

func TestHostKeyVerification(t *testing.T) {
	t.Run("指纹与已核对的不一致：拒绝连接，原因是主机指纹已变化，且不发送凭据", func(t *testing.T) {
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "pw"})
		other := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "pw"})
		hop := sshHop(t, "目标", target, "app", "pw")
		hop.Fingerprint = other.Fingerprint

		c, results, err := dial(t, Config{Target: hop})
		require.Error(t, err)
		assert.Nil(t, c)
		assert.True(t, errors.Is(err, ErrHostKeyChanged))
		assert.Contains(t, err.Error(), "主机指纹已变化")
		require.Len(t, results, 1)
		assert.Equal(t, ReasonFingerprintMismatch, results[0].Reason)
		assert.Contains(t, results[0].Message, "主机指纹已变化")
		assert.Equal(t, target.Fingerprint, results[0].Fingerprint)
		assert.Equal(t, 0, target.AuthAttempts())
	})

	t.Run("跳板指纹变化：在跳板处停止", func(t *testing.T) {
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "pw"})
		jump := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "ops", Password: "pw"})
		jh := sshHop(t, "跳板", jump, "ops", "pw")
		jh.Fingerprint = target.Fingerprint
		_, results, err := dial(t, Config{Hops: []Hop{jh}, Target: sshHop(t, "目标", target, "app", "pw")})
		require.ErrorIs(t, err, ErrHostKeyChanged)
		require.Len(t, results, 1)
		assert.Equal(t, ReasonFingerprintMismatch, results[0].Reason)
		assert.Equal(t, 0, target.Connections())
	})

	t.Run("首次连接（没有已核对的指纹）：返回指纹待核对，不发送凭据", func(t *testing.T) {
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "pw"})
		hop := sshHop(t, "目标", target, "app", "pw")
		hop.Fingerprint = ""

		_, results, err := dial(t, Config{Target: hop})
		require.ErrorIs(t, err, ErrHostKeyUnconfirmed)
		require.Len(t, results, 1)
		assert.Equal(t, ReasonFingerprintUnconfirmed, results[0].Reason)
		assert.Equal(t, target.Fingerprint, results[0].Fingerprint)
		assert.Equal(t, 0, target.AuthAttempts())
	})

	t.Run("服务器有多种主机密钥时取 ed25519 的指纹，与面板提示核对的 ssh_host_ed25519_key.pub 一致", func(t *testing.T) {
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "pw", ExtraECDSAHostKey: true})
		hop := sshHop(t, "目标", target, "app", "pw")
		hop.Fingerprint = ""

		_, results, err := dial(t, Config{Target: hop})
		require.ErrorIs(t, err, ErrHostKeyUnconfirmed)
		require.Len(t, results, 1)
		assert.Equal(t, target.Fingerprint, results[0].Fingerprint)
	})
}

func TestForward(t *testing.T) {
	echo := sshconntest.StartEcho(t)
	target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "pw"})
	socks := sshconntest.StartSOCKS5(t, sshconntest.ProxyOptions{})
	c, _, err := dial(t, Config{
		Hops:   []Hop{proxyHop(t, KindSOCKS5, "代理", socks.Addr, "", "")},
		Target: sshHop(t, "目标", target, "app", "pw"),
	})
	require.NoError(t, err)

	local, closer, err := c.Forward(echo)
	require.NoError(t, err)
	host, _, err := net.SplitHostPort(local)
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1", host)

	// 两个并发的本地连接都能经代理链往返数据
	for _, msg := range []string{"hello opsnap", "第二条连接"} {
		lc, err := net.Dial("tcp", local)
		require.NoError(t, err)
		_, err = lc.Write([]byte(msg))
		require.NoError(t, err)
		buf := make([]byte, len(msg))
		require.NoError(t, lc.SetReadDeadline(time.Now().Add(5*time.Second)))
		_, err = io.ReadFull(lc, buf)
		require.NoError(t, err)
		assert.Equal(t, msg, string(buf))
		require.NoError(t, lc.Close())
	}
	assert.Equal(t, []string{echo, echo}, target.DirectTCPIP())

	// 关闭后本地端口不再接受连接
	require.NoError(t, closer.Close())
	_, err = net.DialTimeout("tcp", local, time.Second)
	assert.Error(t, err)

	// 关闭整条连接时，仍在监听的转发一并停止
	local2, _, err := c.Forward(echo)
	require.NoError(t, err)
	require.NoError(t, c.Close())
	_, err = net.DialTimeout("tcp", local2, time.Second)
	assert.Error(t, err)
	_, _, err = c.Forward(echo)
	assert.Error(t, err)
}
