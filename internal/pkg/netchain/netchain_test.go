package netchain

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opsnap/internal/pkg/fakessh"
)

func connect(t *testing.T, hops ...Hop) (*Tunnel, error) {
	t.Helper()
	c, err := NewChain(hops)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tun, err := c.Connect(ctx)
	if tun != nil {
		t.Cleanup(func() { _ = tun.Close() })
	}
	return tun, err
}

// hopErr 断言 err 是第 index 跳、原因为 reason 的 *HopError
func hopErr(t *testing.T, err error, index int, name string, reason Reason) {
	t.Helper()
	require.Error(t, err)
	var he *HopError
	require.True(t, errors.As(err, &he), "不是 *HopError: %v", err)
	assert.Equal(t, index, he.Index, "err=%v", err)
	assert.Equal(t, name, he.Name)
	assert.Equal(t, reason, he.Reason, "err=%v", err)
}

func TestNewChain(t *testing.T) {
	hop := func(id int64) Hop {
		return Hop{ID: id, Name: fmt.Sprint("h", id), Kind: KindSOCKS5, Host: "127.0.0.1", Port: 1080}
	}

	t.Run("5 跳可以，6 跳被拒绝", func(t *testing.T) {
		hops := []Hop{hop(1), hop(2), hop(3), hop(4), hop(5)}
		_, err := NewChain(hops)
		require.NoError(t, err)
		_, err = NewChain(append(hops, hop(6)))
		assert.ErrorIs(t, err, ErrTooManyHops)
	})

	t.Run("同一通道出现两次视为成环", func(t *testing.T) {
		_, err := NewChain([]Hop{hop(1), hop(2), hop(1)})
		assert.ErrorIs(t, err, ErrCycle)
	})

	t.Run("尚未保存的通道（ID 为 0）不参与成环检查", func(t *testing.T) {
		_, err := NewChain([]Hop{hop(0), hop(1), hop(0)})
		assert.NoError(t, err)
	})

	t.Run("不完整的跳被拒绝", func(t *testing.T) {
		cases := map[string]Hop{
			"缺少主机":          {Kind: KindSOCKS5, Port: 1080},
			"端口越界":          {Kind: KindSOCKS5, Host: "h", Port: 65536},
			"未知类型":          {Kind: "http", Host: "h", Port: 1},
			"SSH 缺少用户名":     {Kind: KindSSH, Host: "h", Port: 22, Password: "x"},
			"SSH 没有凭据":      {Kind: KindSSH, Host: "h", Port: 22, User: "root"},
			"SSH 密码与私钥同时填写": {Kind: KindSSH, Host: "h", Port: 22, User: "root", Password: "x", PrivateKey: []byte("k")},
		}
		for name, h := range cases {
			_, err := NewChain([]Hop{hop(1), h})
			assert.ErrorIs(t, err, ErrInvalidHop, name)
		}
	})

	t.Run("私钥问题在联网前按跳报告", func(t *testing.T) {
		k := genKeys(t)
		h := Hop{ID: 9, Name: "bastion", Kind: KindSSH, Host: "h", Port: 22, User: "root", PrivateKey: openSSHPEM(t, k.ed25519, "pp")}
		_, err := NewChain([]Hop{hop(1), h})
		hopErr(t, err, 2, "bastion", ReasonPassphraseMissing)
		assert.ErrorIs(t, err, ErrPassphraseMissing)
	})
}

func TestConnect(t *testing.T) {
	echo := echoServer(t)

	t.Run("没有跳时直连", func(t *testing.T) {
		tun, err := connect(t)
		require.NoError(t, err)
		c, err := tun.Dial(context.Background(), "tcp", echo)
		require.NoError(t, err)
		defer func() { _ = c.Close() }()
		assertEcho(t, c)
	})

	t.Run("单跳 SSH 密码认证并转发", func(t *testing.T) {
		s := startSSH(t)
		tun, err := connect(t, sshHop(t, s, 1, "bastion"))
		require.NoError(t, err)
		c, err := tun.Dial(context.Background(), "tcp", echo)
		require.NoError(t, err)
		defer func() { _ = c.Close() }()
		assertEcho(t, c)
		assert.Equal(t, []string{echo}, s.Forwards())
	})

	t.Run("单跳 SSH 私钥认证：RSA、ECDSA、Ed25519，含带口令的私钥", func(t *testing.T) {
		k := genKeys(t)
		s := startSSH(t, pubKey(t, k.ed25519), pubKey(t, k.rsa), pubKey(t, k.ecdsa))
		keys := map[string][2]string{
			"Ed25519 OpenSSH 带口令": {string(openSSHPEM(t, k.ed25519, "pp")), "pp"},
			"RSA PEM":             {string(pkcs1PEM(k.rsa)), ""},
			"ECDSA PEM":           {string(ecPEM(t, k.ecdsa)), ""},
		}
		for name, kp := range keys {
			h := sshHop(t, s, 1, "bastion")
			h.Password, h.PrivateKey, h.Passphrase = "", []byte(kp[0]), []byte(kp[1])
			_, err := connect(t, h)
			assert.NoError(t, err, name)
		}
	})

	t.Run("SOCKS5 → SSH → SSH 混合链路，目标主机名交给代理解析", func(t *testing.T) {
		p := startSOCKS(t, fakessh.SOCKS5Config{User: "u", Password: "p"})
		s2, s3 := startSSH(t), startSSH(t)
		h2 := sshHop(t, s2, 2, "bastion")
		h2.Host = "localhost"
		tun, err := connect(t, socksHop(t, p, 1, "office-socks", "u", "p"), h2, sshHop(t, s3, 3, "inner"))
		require.NoError(t, err)
		c, err := tun.Dial(context.Background(), "tcp", echo)
		require.NoError(t, err)
		defer func() { _ = c.Close() }()
		assertEcho(t, c)
		assert.Equal(t, []string{fmt.Sprintf("localhost:%d", h2.Port)}, p.Requests())
		assert.Equal(t, []string{s3.Addr()}, s2.Forwards())
		assert.Equal(t, []string{echo}, s3.Forwards())
	})

	t.Run("SSH → SOCKS5：SOCKS5 作为最后一跳也校验认证", func(t *testing.T) {
		s := startSSH(t)
		p := startSOCKS(t, fakessh.SOCKS5Config{User: "u", Password: "p"})
		tun, err := connect(t, sshHop(t, s, 1, "bastion"), socksHop(t, p, 2, "inner-socks", "u", "p"))
		require.NoError(t, err)
		assert.Equal(t, 1, p.AuthAttempts())
		c, err := tun.Dial(context.Background(), "tcp", echo)
		require.NoError(t, err)
		defer func() { _ = c.Close() }()
		assertEcho(t, c)

		_, err = connect(t, sshHop(t, s, 1, "bastion"), socksHop(t, p, 2, "inner-socks", "u", "bad"))
		hopErr(t, err, 2, "inner-socks", ReasonAuthFailed)
	})
}

func TestConnectFailures(t *testing.T) {
	t.Run("SSH 密码错误：认证失败，错误信息不含密码", func(t *testing.T) {
		s := startSSH(t)
		h := sshHop(t, s, 1, "bastion")
		h.Password = "wrong-secret"
		_, err := connect(t, h)
		hopErr(t, err, 1, "bastion", ReasonAuthFailed)
		assert.ErrorIs(t, err, ErrAuthFailed)
		assert.Contains(t, err.Error(), "第 1 跳 bastion（SSH）")
		assert.NotContains(t, err.Error(), "wrong-secret")
	})

	t.Run("首次连接：主机密钥未确认，认证前中止并给出出示的指纹", func(t *testing.T) {
		p := startSOCKS(t, fakessh.SOCKS5Config{})
		s2, s3 := startSSH(t), startSSH(t)
		h2 := sshHop(t, s2, 2, "bastion")
		h2.HostKey = ""
		_, err := connect(t, socksHop(t, p, 1, "office-socks", "", ""), h2, sshHop(t, s3, 3, "inner"))
		hopErr(t, err, 2, "bastion", ReasonHostKeyUnknown)
		assert.ErrorIs(t, err, ErrHostKeyUnknown)
		assert.NotErrorIs(t, err, ErrHostKeyChanged)
		var hk *HostKeyError
		require.True(t, errors.As(err, &hk))
		assert.False(t, hk.Changed)
		assert.Equal(t, s2.Fingerprint(), hk.Fingerprint)
		assert.Regexp(t, `^SHA256:`, hk.Fingerprint)
		assert.Equal(t, "ssh-ed25519", hk.KeyType)
		assert.Empty(t, hk.Saved)
		assert.Zero(t, s2.AuthAttempts(), "未确认主机密钥时不能发送凭据")
		assert.Empty(t, s2.Forwards(), "未确认主机密钥时不能经由它转发")
		assert.Zero(t, s3.AuthAttempts())

		// 用户信任后带着指纹重试即可继续
		h2.HostKey = hk.Fingerprint
		_, err = connect(t, socksHop(t, p, 1, "office-socks", "", ""), h2, sshHop(t, s3, 3, "inner"))
		require.NoError(t, err)
	})

	t.Run("主机密钥已变化：认证前中止，并列出保存的与出示的指纹", func(t *testing.T) {
		s := startSSH(t)
		h := sshHop(t, s, 1, "bastion")
		saved := h.HostKey
		require.NoError(t, s.RotateHostKey())
		_, err := connect(t, h)
		hopErr(t, err, 1, "bastion", ReasonHostKeyChanged)
		assert.ErrorIs(t, err, ErrHostKeyChanged)
		var hk *HostKeyError
		require.True(t, errors.As(err, &hk))
		assert.True(t, hk.Changed)
		assert.Equal(t, saved, hk.Saved)
		assert.Equal(t, s.Fingerprint(), hk.Fingerprint)
		assert.Zero(t, s.AuthAttempts())
	})

	t.Run("第 3 跳不可达：经 SSH 转发被拒", func(t *testing.T) {
		p := startSOCKS(t, fakessh.SOCKS5Config{})
		s2 := startSSH(t)
		host, port := splitAddr(t, closedAddr(t))
		h3 := Hop{ID: 3, Name: "gone", Kind: KindSSH, Host: host, Port: port, User: "root", Password: "x", HostKey: "SHA256:x"}
		_, err := connect(t, socksHop(t, p, 1, "office-socks", "", ""), sshHop(t, s2, 2, "bastion"), h3)
		hopErr(t, err, 3, "gone", ReasonUnreachable)
	})

	t.Run("第 2 跳不可达：SOCKS5 代理无法连接", func(t *testing.T) {
		p := startSOCKS(t, fakessh.SOCKS5Config{})
		host, port := splitAddr(t, closedAddr(t))
		h2 := Hop{ID: 2, Name: "gone", Kind: KindSSH, Host: host, Port: port, User: "root", Password: "x", HostKey: "SHA256:x"}
		_, err := connect(t, socksHop(t, p, 1, "office-socks", "", ""), h2)
		hopErr(t, err, 2, "gone", ReasonUnreachable)
	})

	t.Run("第 1 跳不可达", func(t *testing.T) {
		host, port := splitAddr(t, closedAddr(t))
		_, err := connect(t, Hop{ID: 1, Name: "office-socks", Kind: KindSOCKS5, Host: host, Port: port})
		hopErr(t, err, 1, "office-socks", ReasonUnreachable)
	})

	t.Run("SOCKS5 认证失败与方法协商失败", func(t *testing.T) {
		p := startSOCKS(t, fakessh.SOCKS5Config{User: "u", Password: "p"})
		_, err := connect(t, socksHop(t, p, 1, "office-socks", "u", "bad"))
		hopErr(t, err, 1, "office-socks", ReasonAuthFailed)
		assert.ErrorIs(t, err, ErrAuthFailed)
		assert.Contains(t, err.Error(), "第 1 跳 office-socks（SOCKS5）")

		_, err = connect(t, socksHop(t, p, 1, "office-socks", "", ""))
		hopErr(t, err, 1, "office-socks", ReasonNegotiation)
	})

	t.Run("对端不是 SOCKS5：协议错误", func(t *testing.T) {
		host, port := splitAddr(t, echoServer(t))
		_, err := connect(t, Hop{ID: 1, Name: "echo", Kind: KindSOCKS5, Host: host, Port: port})
		hopErr(t, err, 1, "echo", ReasonProtocol)
	})
}

func TestTimeout(t *testing.T) {
	run := func(t *testing.T, hops ...Hop) (time.Duration, error) {
		c, err := NewChain(hops)
		require.NoError(t, err)
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		start := time.Now()
		tun, err := c.Connect(ctx)
		if tun != nil {
			_ = tun.Close()
		}
		return time.Since(start), err
	}

	t.Run("第 1 跳不应答：整体超时", func(t *testing.T) {
		host, port := splitAddr(t, silentServer(t))
		took, err := run(t, Hop{ID: 1, Name: "mute", Kind: KindSSH, Host: host, Port: port, User: "root", Password: "x"})
		hopErr(t, err, 1, "mute", ReasonTimeout)
		assert.Less(t, took, 5*time.Second)
	})

	t.Run("经 SSH 转发的第 2 跳不应答：整体超时", func(t *testing.T) {
		s := startSSH(t)
		host, port := splitAddr(t, silentServer(t))
		took, err := run(t, sshHop(t, s, 1, "bastion"), Hop{ID: 2, Name: "mute", Kind: KindSOCKS5, Host: host, Port: port})
		hopErr(t, err, 2, "mute", ReasonTimeout)
		assert.Less(t, took, 5*time.Second)
	})

	t.Run("取消", func(t *testing.T) {
		host, port := splitAddr(t, silentServer(t))
		c, err := NewChain([]Hop{{ID: 1, Name: "mute", Kind: KindSSH, Host: host, Port: port, User: "root", Password: "x"}})
		require.NoError(t, err)
		ctx, cancel := context.WithCancel(context.Background())
		time.AfterFunc(100*time.Millisecond, cancel)
		_, err = c.Connect(ctx)
		hopErr(t, err, 1, "mute", ReasonCanceled)
	})
}

func TestDialSSH(t *testing.T) {
	k := genKeys(t)
	p := startSOCKS(t, fakessh.SOCKS5Config{})
	s2 := startSSH(t)
	target := startSSH(t, pubKey(t, k.ecdsa))
	tun, err := connect(t, socksHop(t, p, 1, "office-socks", "", ""), sshHop(t, s2, 2, "bastion"))
	require.NoError(t, err)

	th := sshHop(t, target, 7, "web-01")
	th.Password, th.PrivateKey = "", ecPEM(t, k.ecdsa)

	t.Run("经链路登录目标主机并执行 uname -sm", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cl, err := tun.DialSSH(ctx, th)
		require.NoError(t, err)
		defer func() { _ = cl.Close() }()
		sess, err := cl.NewSession()
		require.NoError(t, err)
		defer func() { _ = sess.Close() }()
		out, err := sess.Output("uname -sm")
		require.NoError(t, err)
		assert.Equal(t, "Linux x86_64\n", string(out))
	})

	t.Run("目标主机密钥未确认：按链路之后的一跳报告，不发送凭据", func(t *testing.T) {
		before := target.AuthAttempts()
		h := th
		h.HostKey = ""
		_, err := tun.DialSSH(context.Background(), h)
		hopErr(t, err, 3, "web-01", ReasonHostKeyUnknown)
		assert.Equal(t, before, target.AuthAttempts())
	})

	t.Run("目标不是 SSH 配置", func(t *testing.T) {
		_, err := tun.DialSSH(context.Background(), Hop{Kind: KindSOCKS5, Host: "h", Port: 1})
		assert.ErrorIs(t, err, ErrInvalidHop)
	})
}

func TestHopRedactsSecrets(t *testing.T) {
	h := Hop{Name: "bastion", Kind: KindSSH, Host: "h", Port: 22, User: "root", Password: "pw-XYZ", PrivateKey: []byte("KEY-XYZ"), Passphrase: []byte("PP-XYZ")}
	for _, f := range []string{"%v", "%+v", "%#v", "%s"} {
		out := fmt.Sprintf(f, h)
		for _, secret := range []string{"pw-XYZ", "KEY-XYZ", "PP-XYZ"} {
			assert.NotContains(t, out, secret, f)
		}
	}
}
