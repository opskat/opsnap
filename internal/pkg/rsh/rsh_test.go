package rsh

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"

	"github.com/opskat/opsnap/internal/pkg/executor"
	"github.com/opskat/opsnap/internal/pkg/sshconn"
	"github.com/opskat/opsnap/internal/pkg/sshconn/sshconntest"
)

// childEnv 让测试二进制自己充当 `opsnap rsh`，供真实 rsync 的端到端测试使用
const childEnv = "OPSNAP_RSH_TEST_CHILD"

func TestMain(m *testing.M) {
	if os.Getenv(childEnv) == "1" && len(os.Args) > 1 && os.Args[1] == Subcommand {
		os.Exit(Main(os.Getenv, os.Args[2:], os.Stdin, os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

// execServer 进程内 SSH 服务，支持密码认证与 exec 会话：在本机用 sh -c 执行收到的命令，
// 模拟服务器上的 rsync --server
type execServer struct {
	Addr        string
	Fingerprint string

	mu       sync.Mutex
	commands []string
}

func (s *execServer) Commands() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.commands...)
}

func startExecSSH(t *testing.T, user, password string) *execServer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(priv)
	require.NoError(t, err)
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(meta ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			if meta.User() == user && string(pw) == password {
				return &ssh.Permissions{}, nil
			}
			return nil, fmt.Errorf("密码错误")
		},
	}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	s := &execServer{Addr: ln.Addr().String(), Fingerprint: ssh.FingerprintSHA256(signer.PublicKey())}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.handle(c, cfg)
		}
	}()
	return s
}

func (s *execServer) handle(c net.Conn, cfg *ssh.ServerConfig) {
	sc, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		_ = c.Close()
		return
	}
	defer func() { _ = sc.Close() }()
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() != "session" {
			_ = nc.Reject(ssh.UnknownChannelType, "只支持 session")
			continue
		}
		ch, chReqs, err := nc.Accept()
		if err != nil {
			continue
		}
		go s.session(ch, chReqs)
	}
}

func (s *execServer) session(ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer func() { _ = ch.Close() }()
	for req := range reqs {
		if req.Type != "exec" {
			_ = req.Reply(false, nil)
			continue
		}
		var payload struct{ Command string }
		if err := ssh.Unmarshal(req.Payload, &payload); err != nil {
			_ = req.Reply(false, nil)
			return
		}
		_ = req.Reply(true, nil)
		s.mu.Lock()
		s.commands = append(s.commands, payload.Command)
		s.mu.Unlock()
		cmd := exec.Command("sh", "-c", payload.Command) //nolint:gosec // 测试服务执行测试给出的命令
		cmd.Stdout, cmd.Stderr = ch, ch.Stderr()
		// 与 sshd 一样：命令结束就回报退出码，不等客户端关闭标准输入
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return
		}
		go func() {
			_, _ = io.Copy(stdin, ch)
			_ = stdin.Close()
		}()
		code := 0
		if err := cmd.Run(); err != nil {
			code = 255
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			}
		}
		status := make([]byte, 4)
		binary.BigEndian.PutUint32(status, uint32(code))
		_, _ = ch.SendRequest("exit-status", false, status)
		return
	}
}

// syncBuffer 可并发写入的缓冲区（桥接把服务器的标准错误写到这里）
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

const sshPassword = "SECRET-SSH-PW" //nolint:gosec // 测试用的假凭据

// chain 经 SOCKS5 代理连到用密码认证的目标 SSH 服务
func chain(t *testing.T) (*sshconn.Conn, *execServer, *sshconntest.Proxy) {
	t.Helper()
	target := startExecSSH(t, "app", sshPassword)
	socks := sshconntest.StartSOCKS5(t, sshconntest.ProxyOptions{})
	sh, sp := sshconntest.HostPort(t, socks.Addr)
	th, tp := sshconntest.HostPort(t, target.Addr)
	conn, _, err := sshconn.DialConfig(context.Background(), sshconn.Config{
		Hops:   []sshconn.Hop{{Kind: sshconn.KindSOCKS5, Host: sh, Port: sp}},
		Target: sshconn.Hop{Kind: sshconn.KindSSH, Name: "app", Host: th, Port: tp, User: "app", Password: sshPassword, Fingerprint: target.Fingerprint},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return conn, target, socks
}

func envLookup(env []string) func(string) string {
	m := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	return func(k string) string { return m[k] }
}

func TestBridgeThroughProxyChain(t *testing.T) {
	conn, target, socks := chain(t)
	var remoteErr syncBuffer
	b, err := Serve(conn.Client(), &remoteErr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = b.Close() })

	env := b.Env()
	require.Len(t, env, 2)
	for _, kv := range env {
		assert.NotContains(t, kv, sshPassword, "环境变量里不能有服务器凭据")
	}
	assert.NotEmpty(t, b.Token())

	t.Run("标准输入输出往返（rsync 协议流）", func(t *testing.T) {
		in := "hello rsync\x00\x01\x02" + strings.Repeat("x", 200_000)
		var out, errOut bytes.Buffer
		code := Main(envLookup(env), []string{"-l", "app", Host, "cat"}, strings.NewReader(in), &out, &errOut)
		assert.Equal(t, 0, code, errOut.String())
		assert.Equal(t, in, out.String())
		assert.Contains(t, target.Commands(), "cat")
		assert.Contains(t, socks.Targets(), target.Addr, "必须经过代理链")
	})

	t.Run("远端退出码原样返回，远端标准错误交给主进程", func(t *testing.T) {
		var out, errOut bytes.Buffer
		code := Main(envLookup(env), []string{Host, "echo", "oops", ">&2;", "exit", "7"}, strings.NewReader(""), &out, &errOut)
		assert.Equal(t, 7, code)
		assert.Eventually(t, func() bool { return strings.Contains(remoteErr.String(), "oops") }, time.Second, 10*time.Millisecond)
	})

	t.Run("令牌不对：拒绝，不执行任何命令", func(t *testing.T) {
		before := len(target.Commands())
		bad := append([]string{}, env...)
		for i, kv := range bad {
			if strings.HasPrefix(kv, EnvToken+"=") {
				bad[i] = EnvToken + "=wrong"
			}
		}
		var out, errOut bytes.Buffer
		code := Main(envLookup(bad), []string{Host, "touch", "/tmp/should-not-run"}, strings.NewReader(""), &out, &errOut)
		assert.NotEqual(t, 0, code)
		assert.Len(t, target.Commands(), before)
	})

	t.Run("没有远端命令：拒绝", func(t *testing.T) {
		var out, errOut bytes.Buffer
		code := Main(envLookup(env), []string{"-l", "app", Host}, strings.NewReader(""), &out, &errOut)
		assert.NotEqual(t, 0, code)
	})
}

func TestMainWithoutBridge(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Main(func(string) string { return "" }, []string{Host, "cat"}, strings.NewReader(""), &out, &errOut)
	assert.Equal(t, 255, code)
	assert.Contains(t, errOut.String(), "opsnap")
}

func TestBridgeClose(t *testing.T) {
	conn, _, _ := chain(t)
	b, err := Serve(conn.Client(), &syncBuffer{})
	require.NoError(t, err)
	sock := envLookup(b.Env())(EnvSocket)
	_, err = os.Stat(sock)
	require.NoError(t, err)
	fi, err := os.Stat(filepath.Dir(sock))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), fi.Mode().Perm(), "socket 所在目录只有当前用户可访问")
	require.NoError(t, b.Close())
	_, err = os.Stat(filepath.Dir(sock))
	assert.True(t, os.IsNotExist(err), "关闭后删除临时目录")
}

func TestRsyncCommand(t *testing.T) {
	env := []string{EnvSocket + "=/tmp/x/s", EnvToken + "=t"}
	c, err := Rsync(RsyncSpec{
		Self: "/usr/local/bin/opsnap", Paths: []string{"/var/www", "/etc/nginx"}, Excludes: []string{"*.log"},
		Dest: "/data/mirror/3", BwLimitKiB: 100, ExtraArgs: []string{"--checksum"},
	}, env)
	require.NoError(t, err)
	assert.Equal(t, "rsync", c.Name)
	assert.Equal(t, env, c.Env)
	assert.Contains(t, c.Args, "--info=progress2", "整体进度供运行引擎读取实时字节数")
	assert.Equal(t, []string{
		"--archive", "--relative", "--delete", "--delete-excluded", "--numeric-ids", "--info=progress2",
		"-e", "/usr/local/bin/opsnap rsh",
		"--exclude=*.log", "--bwlimit=100", "--checksum",
		"opsnap:/var/www", "opsnap:/etc/nginx", "/data/mirror/3/",
	}, c.Args)

	c, err = Rsync(RsyncSpec{Self: "/opt/my apps/opsnap", Paths: []string{"/a"}, Dest: "/m"}, env)
	require.NoError(t, err)
	assert.Contains(t, c.Args, "'/opt/my apps/opsnap' rsh")

	for _, bad := range []RsyncSpec{
		{Self: "/o", Paths: []string{"relative"}, Dest: "/m"},
		{Self: "/o", Paths: []string{"/a"}, Dest: "m"},
		{Self: "/o", Dest: "/m"},
		{Self: "", Paths: []string{"/a"}, Dest: "/m"},
	} {
		_, err := Rsync(bad, env)
		assert.Error(t, err, "%+v", bad)
	}
}

// TestRealRsyncThroughBridge 用本机真实的 rsync 经桥接同步一个目录：rsync -e 指向测试二进制自身的 rsh 子命令，
// 服务器一侧由进程内 SSH 服务执行 rsync --server。
func TestRealRsyncThroughBridge(t *testing.T) {
	if _, err := exec.LookPath("rsync"); err != nil {
		t.Skip("本机没有 rsync")
	}
	// Rsync 总是带 --info=progress2；macOS 自带的 openrsync 不认识它，镜像里的 rsync 3.2.7 支持
	if out, _ := exec.Command("rsync", "--info=help").CombinedOutput(); !strings.Contains(string(out), "PROGRESS2") {
		t.Skip("本机 rsync 不支持 --info=progress2")
	}
	conn, target, _ := chain(t)
	var remoteErr syncBuffer
	b, err := Serve(conn.Client(), &remoteErr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = b.Close() })

	src := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(src, "site"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "site", "index.html"), []byte("<h1>hi</h1>"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(src, "site", "debug.log"), []byte("noise"), 0o644))
	dest := t.TempDir()

	self, err := os.Executable()
	require.NoError(t, err)
	cmd, err := Rsync(RsyncSpec{Self: self, Paths: []string{filepath.Join(src, "site")}, Excludes: []string{"*.log"}, Dest: dest},
		append(b.Env(), childEnv+"=1"))
	require.NoError(t, err)

	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	code, err := executor.New().Run(ctx, cmd, &out)
	require.NoError(t, err)
	require.Equal(t, 0, code, "rsync 输出：%s\n远端：%s", out.String(), remoteErr.String())

	got, err := os.ReadFile(filepath.Join(dest, src, "site", "index.html")) //nolint:gosec // 测试临时目录
	require.NoError(t, err)
	assert.Equal(t, "<h1>hi</h1>", string(got))
	_, err = os.Stat(filepath.Join(dest, src, "site", "debug.log"))
	assert.True(t, os.IsNotExist(err), "排除规则生效")
	require.NotEmpty(t, target.Commands())
	assert.Contains(t, target.Commands()[0], "--server")
	assert.NotContains(t, strings.Join(cmd.Args, " "), sshPassword)
}
