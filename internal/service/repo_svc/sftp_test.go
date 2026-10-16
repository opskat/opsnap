package repo_svc

import (
	"bufio"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	repoapi "github.com/opskat/opsnap/internal/api/repo"
	"github.com/opskat/opsnap/internal/model/entity/server_entity"
	"github.com/opskat/opsnap/internal/pkg/cmdbuild"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/executor"
	"github.com/opskat/opsnap/internal/pkg/secret"
	"github.com/opskat/opsnap/internal/pkg/sshconn/sshconntest"
	"github.com/opskat/opsnap/internal/repository/server_repo"
)

// envOf 取出命令里某个环境变量的值
func envOf(c cmdbuild.Cmd, key string) string {
	for _, e := range c.Env {
		if v, ok := strings.CutPrefix(e, key+"="); ok {
			return v
		}
	}
	return ""
}

// scriptInit 让空位置初始化成功
func scriptInit(f *fixture) {
	f.fake.On(executor.Match("rclone", "obscure", "-"), executor.Step{Output: "OBSCURED-pw\n"})
	f.fake.On(restic("snapshots"), executor.Step{ExitCode: 10})
	f.fake.On(restic("init"))
	f.fake.On(restic("key", "list"), keyList("op1", "op1"))
}

func TestSFTPHostKeyAndEnv(t *testing.T) {
	t.Run("直连：取回公钥不发送凭据；私钥与已知主机写进 0600 临时文件，用完删除", func(t *testing.T) {
		f := setupRepoTest(t)
		nas := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "backup", Password: "pw"})
		host, port := sshconntest.HostPort(t, nas.Addr)

		probe, err := f.svc.SFTPHostKey(f.ctx, &repoapi.SFTPHostKeyRequest{Host: host, Port: port})
		require.NoError(t, err)
		assert.Equal(t, nas.Fingerprint, probe.Fingerprint)
		assert.Zero(t, nas.AuthAttempts(), "取回公钥时不认证")

		key := sshconntest.GenerateKey(t, "")
		scriptInit(f)
		resp, err := f.svc.Create(f.ctx, &repoapi.CreateRequest{
			Name: "群晖", Type: repoapi.TypeSFTP, RecoveryKey: no(),
			Params: repoapi.Params{Host: host, Port: port, Username: "backup", Path: "/volume1/opsnap",
				PrivateKey: key.PrivatePEM, HostKey: probe.HostKey},
		})
		require.NoError(t, err)
		assert.Equal(t, secret.Masked, resp.Params.PrivateKey)
		assert.NotContains(t, f.rawRow(t, resp.ID), "PRIVATE KEY", "私钥加密保存")
		assert.Equal(t, "sftp://backup@"+net.JoinHostPort(host, strconv.Itoa(port))+"//volume1/opsnap", resp.Address)

		r, release, err := f.svc.Env(f.ctx, resp.ID)
		require.NoError(t, err)
		c, err := cmdbuild.ResticSnapshots(r, nil)
		require.NoError(t, err)
		keyFile, knownFile := envOf(c, "RCLONE_CONFIG_OPSNAP_KEY_FILE"), envOf(c, "RCLONE_CONFIG_OPSNAP_KNOWN_HOSTS_FILE")
		got, err := os.ReadFile(keyFile) //nolint:gosec // 测试临时目录里的文件
		require.NoError(t, err)
		assert.Equal(t, key.PrivatePEM, string(got))
		fi, err := os.Stat(keyFile)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
		known, err := os.ReadFile(knownFile) //nolint:gosec // 测试临时目录里的文件
		require.NoError(t, err)
		assert.Contains(t, string(known), strings.Fields(probe.HostKey)[1])
		assert.NotContains(t, c.String(), "PRIVATE KEY")
		release()
		_, err = os.Stat(keyFile)
		assert.True(t, os.IsNotExist(err), "release 后删除临时文件")
	})

	t.Run("经服务器：取回公钥与访问都走该服务器的转发", func(t *testing.T) {
		f := setupRepoTest(t)
		nas := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "backup", Password: "pw"})
		jump := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "jump-pw"})
		jh, jp := sshconntest.HostPort(t, jump.Addr)
		enc, err := secret.Default().Encrypt("jump-pw")
		require.NoError(t, err)
		srv := &server_entity.Server{Name: "跳板", Host: jh, Port: jp, Username: "app",
			AuthType: server_entity.AuthPassword, Password: enc, HostKeyFingerprint: jump.Fingerprint}
		require.NoError(t, server_repo.Server().Create(f.ctx, srv))
		host, port := sshconntest.HostPort(t, nas.Addr)

		probe, err := f.svc.SFTPHostKey(f.ctx, &repoapi.SFTPHostKeyRequest{Host: host, Port: port, ServerID: srv.ID})
		require.NoError(t, err)
		assert.Equal(t, nas.Fingerprint, probe.Fingerprint)
		assert.Contains(t, jump.DirectTCPIP(), nas.Addr)

		_, err = f.svc.SFTPHostKey(f.ctx, &repoapi.SFTPHostKeyRequest{Host: host, Port: port, ServerID: 999})
		assert.Equal(t, code.RepoServerNotFound, codeOf(t, err))

		scriptInit(f)
		resp, err := f.svc.Create(f.ctx, &repoapi.CreateRequest{
			Name: "群晖", Type: repoapi.TypeSFTP, RecoveryKey: no(),
			Params: repoapi.Params{Host: host, Port: port, Username: "backup", Path: "/volume1/opsnap", //nolint:gosec // 测试用的虚构凭据
				Password: "SECRET-sftp-pw", HostKey: probe.HostKey, ServerID: srv.ID},
		})
		require.NoError(t, err)
		obs := callsOf(f.fake, executor.Match("rclone", "obscure"))
		require.NotEmpty(t, obs)
		assert.Equal(t, "SECRET-sftp-pw", string(obs[0].Stdin))

		r, release, err := f.svc.Env(f.ctx, resp.ID)
		require.NoError(t, err)
		defer release()
		c, err := cmdbuild.ResticSnapshots(r, nil)
		require.NoError(t, err)
		assert.Equal(t, "127.0.0.1", envOf(c, "RCLONE_CONFIG_OPSNAP_HOST"))
		local := net.JoinHostPort("127.0.0.1", envOf(c, "RCLONE_CONFIG_OPSNAP_PORT"))
		assert.Equal(t, "OBSCURED-pw", envOf(c, "RCLONE_CONFIG_OPSNAP_PASS"))
		known, err := os.ReadFile(envOf(c, "RCLONE_CONFIG_OPSNAP_KNOWN_HOSTS_FILE"))
		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(string(known), "[127.0.0.1]:"), "按转发后的地址校验 NAS 公钥")

		// 本地转发端口连到的就是 NAS 的 SSH 服务
		conn, err := net.DialTimeout("tcp", local, 2*time.Second)
		require.NoError(t, err)
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		banner, err := bufio.NewReader(conn).ReadString('\n')
		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(banner, "SSH-2.0-"))
	})
}
