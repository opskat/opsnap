package probe

import (
	"context"
	"net"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opsnap/internal/pkg/dsconn"
	"github.com/opskat/opsnap/internal/pkg/fakessh"
	"github.com/opskat/opsnap/internal/pkg/netchain"
)

// === 纯判定逻辑，不联网 ===

func TestDecideSSHReachable(t *testing.T) {
	assert.Equal(t, TierOK, decideSSHReachable().Tier)
}

func TestDecideCPUArch(t *testing.T) {
	t.Run("amd64", func(t *testing.T) {
		item := decideCPUArch("Linux x86_64")
		assert.Equal(t, TierOK, item.Tier)
	})
	t.Run("arm64", func(t *testing.T) {
		item := decideCPUArch("Linux aarch64")
		assert.Equal(t, TierOK, item.Tier)
	})
	t.Run("其他架构不可用且没有修复方法", func(t *testing.T) {
		item := decideCPUArch("Linux ppc64le")
		assert.Equal(t, TierFail, item.Tier)
		assert.Contains(t, item.Detail.ZhCN, "ppc64le")
		assert.Empty(t, item.Fix.ZhCN, "架构无法通过配置修复")
	})
}

func TestDecideTempDirExec(t *testing.T) {
	t.Run("可以", func(t *testing.T) {
		item := decideTempDirExec(true, "")
		assert.Equal(t, TierOK, item.Tier)
		assert.Empty(t, item.Fix.ZhCN)
	})
	t.Run("不能：附原因与修复方法", func(t *testing.T) {
		item := decideTempDirExec(false, "Permission denied")
		assert.Equal(t, TierFail, item.Tier)
		assert.Contains(t, item.Detail.ZhCN, "Permission denied")
		assert.Contains(t, item.Fix.ZhCN, "noexec")
		assert.Contains(t, item.Fix.ZhCN, "/tmp")
	})
}

func TestDecideSudo(t *testing.T) {
	t.Run("成功", func(t *testing.T) {
		item := decideSudo(true, "deploy", "")
		assert.Equal(t, TierOK, item.Tier)
	})
	t.Run("失败为风险，修复方法带具体用户名", func(t *testing.T) {
		item := decideSudo(false, "deploy", "a password is required")
		assert.Equal(t, TierWarn, item.Tier)
		assert.Contains(t, item.Fix.ZhCN, "deploy")
		assert.Contains(t, item.Fix.ZhCN, "NOPASSWD")
	})
}

// === 经进程内 SSH 服务端的集成测试 ===

func startFakeSSH(t *testing.T, cfg fakessh.Config) *fakessh.Server {
	t.Helper()
	if cfg.User == "" {
		cfg.User = "root"
	}
	if cfg.Password == "" {
		cfg.Password = "s3cret-pw"
	}
	s, err := fakessh.Listen("127.0.0.1:0", cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// openServerFileConn 经直连（无跳板）连接假 SSH 服务端，得到一个可供 serverFileItems 使用的连接
func openServerFileConn(t *testing.T, s *fakessh.Server) *dsconn.Conn {
	t.Helper()
	host, portStr, err := net.SplitHostPort(s.Addr())
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)
	chain, err := netchain.NewChain(nil)
	require.NoError(t, err)
	tun, err := chain.Connect(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { _ = tun.Close() })
	cfg := dsconn.Config{
		Type: dsconn.TypeServerFile, Host: host, Port: port,
		User: "root", Password: "s3cret-pw", HostKey: s.Fingerprint(),
	}
	conn, err := dsconn.Open(context.Background(), tun, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func TestServerFileItemsAllHealthy(t *testing.T) {
	s := startFakeSSH(t, fakessh.Config{Exec: func(cmd string) (string, string, int) {
		switch {
		case cmd == "uname -sm":
			return "Linux x86_64\n", "", 0
		case strings.Contains(cmd, "mktemp"):
			return "", "", 0
		case cmd == "sudo -n true":
			return "", "", 0
		}
		return "", cmd + ": command not found", 127
	}})
	conn := openServerFileConn(t, s)

	items := Run(context.Background(), dsconn.TypeServerFile, conn)

	require.Len(t, items, 4)
	byKey := map[string]Item{}
	for _, it := range items {
		byKey[it.Key] = it
	}
	assert.Equal(t, TierOK, byKey["server_file.ssh_reachable"].Tier)
	assert.Equal(t, TierOK, byKey["server_file.cpu_arch"].Tier)
	assert.Equal(t, TierOK, byKey["server_file.tmp_exec"].Tier)
	assert.Equal(t, TierOK, byKey["server_file.sudo"].Tier)

	// 临时目录检查只应该执行一次写入/执行/删除的命令，不留下任何东西
	cmds := s.Commands()
	mktempCalls := 0
	for _, c := range cmds {
		if strings.Contains(c, "mktemp") {
			mktempCalls++
			assert.Contains(t, c, "rm -f", "必须在检查后删除临时文件")
		}
	}
	assert.Equal(t, 1, mktempCalls, "临时目录可执行检查应恰好执行一次")
}

func TestServerFileItemsTempExecNoexec(t *testing.T) {
	s := startFakeSSH(t, fakessh.Config{Exec: func(cmd string) (string, string, int) {
		switch {
		case cmd == "uname -sm":
			return "Linux x86_64\n", "", 0
		case strings.Contains(cmd, "mktemp"):
			return "", "sh: /tmp/xxx: Permission denied", 126
		case cmd == "sudo -n true":
			return "", "sudo: a password is required", 1
		}
		return "", cmd + ": command not found", 127
	}})
	conn := openServerFileConn(t, s)

	items := Run(context.Background(), dsconn.TypeServerFile, conn)

	byKey := map[string]Item{}
	for _, it := range items {
		byKey[it.Key] = it
	}
	tmp := byKey["server_file.tmp_exec"]
	assert.Equal(t, TierFail, tmp.Tier)
	assert.Contains(t, tmp.Detail.ZhCN, "Permission denied")
	assert.NotEmpty(t, tmp.Fix.ZhCN)

	sudo := byKey["server_file.sudo"]
	assert.Equal(t, TierWarn, sudo.Tier)
	assert.Contains(t, sudo.Fix.ZhCN, "root", "修复方法应带上实际连接用户名")
}

func TestServerFileItemsOtherArch(t *testing.T) {
	s := startFakeSSH(t, fakessh.Config{Exec: func(cmd string) (string, string, int) {
		switch {
		case cmd == "uname -sm":
			return "Linux ppc64le\n", "", 0
		case strings.Contains(cmd, "mktemp"):
			return "", "", 0
		case cmd == "sudo -n true":
			return "", "", 0
		}
		return "", cmd + ": command not found", 127
	}})
	conn := openServerFileConn(t, s)

	items := Run(context.Background(), dsconn.TypeServerFile, conn)
	for _, it := range items {
		if it.Key == "server_file.cpu_arch" {
			assert.Equal(t, TierFail, it.Tier)
		}
	}
}
