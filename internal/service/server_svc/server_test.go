package server_svc

import (
	"context"
	"testing"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	serverapi "github.com/opskat/opsnap/internal/api/server"
	"github.com/opskat/opsnap/internal/model/entity/repo_entity"
	"github.com/opskat/opsnap/internal/model/entity/server_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/secret"
	"github.com/opskat/opsnap/internal/pkg/sshconn/sshconntest"
	"github.com/opskat/opsnap/internal/repository/repo_repo"
	"github.com/opskat/opsnap/internal/repository/server_repo"
	"github.com/opskat/opsnap/migrations"
)

type fixture struct {
	ctx context.Context
	db  *gorm.DB
	svc ServerSvc
	now time.Time
}

func setupServerTest(t *testing.T) *fixture {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, migrations.RunMigrations(gdb))

	s, err := secret.New("server-test-master-key")
	require.NoError(t, err)
	secret.SetDefault(s)
	t.Cleanup(func() { secret.SetDefault(nil) })

	server_repo.RegisterServer(server_repo.NewServer())
	repo_repo.RegisterRepo(repo_repo.NewRepo())
	RegisterJobCounter(nil)
	t.Cleanup(func() { RegisterJobCounter(nil) })
	f := &fixture{ctx: db.WithContextDB(context.Background(), gdb), db: gdb, now: time.Unix(1_800_000_000, 0)}
	f.svc = NewServer(func() time.Time { return f.now })
	return f
}

// codeOf 取出业务错误码
func codeOf(t *testing.T, err error) int {
	t.Helper()
	var he *httputils.Error
	require.ErrorAs(t, err, &he)
	return he.Code
}

// passwordInput 用密码认证的服务器配置，指纹已核对
func passwordInput(t *testing.T, name string, s *sshconntest.SSHServer, user, password string) serverapi.ServerInput {
	t.Helper()
	host, port := sshconntest.HostPort(t, s.Addr)
	return serverapi.ServerInput{
		Name: name, Host: host, Port: port, Username: user,
		AuthType: serverapi.AuthPassword, Password: password,
		HostKeyFingerprint: s.Fingerprint,
	}
}

func (f *fixture) create(t *testing.T, in serverapi.ServerInput) *serverapi.Item {
	t.Helper()
	resp, err := f.svc.Create(f.ctx, createReq(in))
	require.NoError(t, err)
	return &resp.Item
}

// raw 直接读库里保存的行
func (f *fixture) raw(t *testing.T, id int64) *server_entity.Server {
	t.Helper()
	got := &server_entity.Server{}
	require.NoError(t, f.db.Where("id = ?", id).First(got).Error)
	return got
}

func TestServerFingerprintConfirmation(t *testing.T) {
	t.Run("首次测试只取回指纹、不认证；核对后带指纹测试通过，才能保存", func(t *testing.T) {
		f := setupServerTest(t)
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "pw"})
		in := passwordInput(t, "web-1", target, "app", "pw")
		in.HostKeyFingerprint = ""

		_, err := f.svc.Create(f.ctx, createReq(in))
		assert.Equal(t, code.ServerFingerprintUnconfirmed, codeOf(t, err))

		probe, err := f.svc.Test(f.ctx, testReq(0, in))
		require.NoError(t, err)
		assert.False(t, probe.OK)
		assert.Equal(t, target.Fingerprint, probe.Fingerprint)
		require.Len(t, probe.Hops, 1)
		assert.Equal(t, "fingerprint_unconfirmed", probe.Hops[0].Reason)
		assert.Equal(t, 0, target.AuthAttempts())

		in.HostKeyFingerprint = probe.Fingerprint
		ok, err := f.svc.Test(f.ctx, testReq(0, in))
		require.NoError(t, err)
		assert.True(t, ok.OK)

		item := f.create(t, in)
		assert.Equal(t, target.Fingerprint, item.HostKeyFingerprint)
	})

	t.Run("指纹格式不对不能保存", func(t *testing.T) {
		f := setupServerTest(t)
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "pw"})
		in := passwordInput(t, "web-1", target, "app", "pw")
		in.HostKeyFingerprint = "md5:aa:bb"
		_, err := f.svc.Create(f.ctx, createReq(in))
		assert.Equal(t, code.ServerFingerprintInvalid, codeOf(t, err))
	})

	t.Run("修改地址时必须重新核对指纹；地址不变时留空沿用已核对的指纹", func(t *testing.T) {
		f := setupServerTest(t)
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "pw"})
		other := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "pw"})
		item := f.create(t, passwordInput(t, "web-1", target, "app", "pw"))

		in := passwordInput(t, "web-1-renamed", target, "app", secret.Masked)
		in.HostKeyFingerprint = ""
		resp, err := f.svc.Update(f.ctx, updateReq(item.ID, in))
		require.NoError(t, err)
		assert.Equal(t, target.Fingerprint, resp.HostKeyFingerprint)

		moved := passwordInput(t, "web-1", other, "app", "pw")
		moved.HostKeyFingerprint = ""
		_, err = f.svc.Update(f.ctx, updateReq(item.ID, moved))
		assert.Equal(t, code.ServerFingerprintUnconfirmed, codeOf(t, err))
	})
}

func TestServerCredentials(t *testing.T) {
	t.Run("密码、私钥、口令和代理密码加密保存，读取只显示已设置", func(t *testing.T) {
		f := setupServerTest(t)
		key := sshconntest.GenerateKey(t, "key-pass")
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", AuthorizedKey: key.Public})
		socks := sshconntest.StartSOCKS5(t, sshconntest.ProxyOptions{User: "px", Password: "px-secret"})
		sh, sp := sshconntest.HostPort(t, socks.Addr)

		in := passwordInput(t, "db-1", target, "app", "")
		in.AuthType = serverapi.AuthPrivateKey
		in.PrivateKey, in.Passphrase = key.PrivatePEM, "key-pass"
		in.Hops = []serverapi.HopInput{{Type: serverapi.HopSOCKS5, Host: sh, Port: sp, Username: "px", Password: "px-secret"}}
		item := f.create(t, in)

		assert.Equal(t, secret.Masked, item.PrivateKey)
		assert.Equal(t, secret.Masked, item.Passphrase)
		assert.Empty(t, item.Password)
		require.Len(t, item.Hops, 1)
		assert.Equal(t, secret.Masked, item.Hops[0].Password)
		assert.Equal(t, "px", item.Hops[0].Username)

		row := f.raw(t, item.ID)
		for plain, cipher := range map[string]string{
			key.PrivatePEM: row.PrivateKey,
			"key-pass":     row.Passphrase,
			"px-secret":    row.Hops[0].Password,
		} {
			assert.NotEmpty(t, cipher)
			assert.NotContains(t, cipher, plain)
			dec, err := secret.Default().Decrypt(cipher)
			require.NoError(t, err)
			assert.Equal(t, plain, dec)
		}

		got, err := f.svc.Get(f.ctx, item.ID)
		require.NoError(t, err)
		assert.Equal(t, secret.Masked, got.PrivateKey)
		list, err := f.svc.List(f.ctx)
		require.NoError(t, err)
		require.Len(t, list.List, 1)
		assert.Equal(t, secret.Masked, list.List[0].Passphrase)

		// 回传占位保留已保存的凭据，连接仍然成功
		in.PrivateKey, in.Passphrase = secret.Masked, secret.Masked
		in.Hops[0].Password = secret.Masked
		_, err = f.svc.Update(f.ctx, updateReq(item.ID, in))
		require.NoError(t, err)
		assert.Equal(t, row.PrivateKey, f.raw(t, item.ID).PrivateKey)
		res, err := f.svc.TestSaved(f.ctx, item.ID)
		require.NoError(t, err)
		assert.True(t, res.OK, "%+v", res.Hops)
	})

	t.Run("没有已保存的值时不能回传占位", func(t *testing.T) {
		f := setupServerTest(t)
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "pw"})
		_, err := f.svc.Create(f.ctx, createReq(passwordInput(t, "web", target, "app", secret.Masked)))
		assert.Equal(t, code.ServerCredentialMissing, codeOf(t, err))
	})

	t.Run("缺少密码或私钥无法解析时不能保存", func(t *testing.T) {
		f := setupServerTest(t)
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "pw"})
		_, err := f.svc.Create(f.ctx, createReq(passwordInput(t, "web", target, "app", "")))
		assert.Equal(t, code.ServerCredentialRequired, codeOf(t, err))

		in := passwordInput(t, "web", target, "app", "")
		in.AuthType, in.PrivateKey = serverapi.AuthPrivateKey, "not a key"
		_, err = f.svc.Create(f.ctx, createReq(in))
		assert.Equal(t, code.ServerPrivateKeyInvalid, codeOf(t, err))
	})
}

func TestServerChainAndStatus(t *testing.T) {
	t.Run("经 SOCKS5 与已保存的跳板连接；列表显示路径、状态、延迟与任务数", func(t *testing.T) {
		f := setupServerTest(t)
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "app-pw"})
		jump := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "ops", Password: "jump-pw"})
		socks := sshconntest.StartSOCKS5(t, sshconntest.ProxyOptions{})
		sh, sp := sshconntest.HostPort(t, socks.Addr)

		jumpItem := f.create(t, passwordInput(t, "bastion", jump, "ops", "jump-pw"))
		in := passwordInput(t, "app-1", target, "app", "app-pw")
		in.Hops = []serverapi.HopInput{
			{Type: serverapi.HopSOCKS5, Host: sh, Port: sp},
			{Type: serverapi.HopSSH, ServerID: jumpItem.ID},
		}
		item := f.create(t, in)
		require.Len(t, item.Hops, 2)
		assert.Equal(t, "bastion", item.Hops[1].ServerName)
		assert.Equal(t, serverapi.StatusUnknown, item.Status)

		RegisterJobCounter(func(context.Context) (map[int64]int, error) { return map[int64]int{item.ID: 3}, nil })

		res, err := f.svc.TestSaved(f.ctx, item.ID)
		require.NoError(t, err)
		require.True(t, res.OK, "%+v", res.Hops)
		require.Len(t, res.Hops, 3)
		assert.Equal(t, []string{"socks5", "ssh", "ssh"}, []string{res.Hops[0].Type, res.Hops[1].Type, res.Hops[2].Type})
		assert.Equal(t, "bastion", res.Hops[1].Name)
		assert.Equal(t, []string{jump.Addr}, socks.Targets())
		assert.Equal(t, []string{target.Addr}, jump.DirectTCPIP())

		got, err := f.svc.Get(f.ctx, item.ID)
		require.NoError(t, err)
		assert.Equal(t, serverapi.StatusOK, got.Status)
		assert.Equal(t, f.now.Unix(), got.LastCheckAt)
		assert.Equal(t, 3, got.JobCount)
		assert.Zero(t, got.FailSince)
	})

	t.Run("指纹变化时拒绝连接，状态记为从某时起连不上，原因是主机指纹已变化", func(t *testing.T) {
		f := setupServerTest(t)
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "pw"})
		other := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "pw"})
		item := f.create(t, passwordInput(t, "web", target, "app", "pw"))
		// 模拟主机换了密钥：已核对的指纹不再与实际一致
		require.NoError(t, f.db.Model(&server_entity.Server{}).Where("id = ?", item.ID).
			Update("host_key_fingerprint", other.Fingerprint).Error)

		res, err := f.svc.TestSaved(f.ctx, item.ID)
		require.NoError(t, err)
		assert.False(t, res.OK)
		require.Len(t, res.Hops, 1)
		assert.Equal(t, "fingerprint_mismatch", res.Hops[0].Reason)
		assert.Equal(t, "指纹不符", res.Hops[0].ReasonLabel)
		assert.Contains(t, res.Hops[0].Message, "主机指纹已变化")
		assert.Equal(t, target.Fingerprint, res.Fingerprint)
		assert.Equal(t, 0, target.AuthAttempts())

		failedAt := f.now.Unix()
		f.now = f.now.Add(time.Hour)
		_, err = f.svc.TestSaved(f.ctx, item.ID)
		require.NoError(t, err)
		got, err := f.svc.Get(f.ctx, item.ID)
		require.NoError(t, err)
		assert.Equal(t, serverapi.StatusFailed, got.Status)
		assert.Equal(t, failedAt, got.FailSince, "连不上的起始时间保持第一次失败的时间")
		assert.Contains(t, got.LastError, "主机指纹已变化")
	})

	t.Run("草稿测试：认证失败标出目标这一跳，不改已保存服务器的状态", func(t *testing.T) {
		f := setupServerTest(t)
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "pw"})
		item := f.create(t, passwordInput(t, "web", target, "app", "pw"))
		in := passwordInput(t, "web", target, "app", "wrong")
		res, err := f.svc.Test(f.ctx, testReq(item.ID, in))
		require.NoError(t, err)
		assert.False(t, res.OK)
		require.Len(t, res.Hops, 1)
		assert.Equal(t, "auth_failed", res.Hops[0].Reason)
		assert.Equal(t, "认证失败", res.Hops[0].ReasonLabel)
		got, err := f.svc.Get(f.ctx, item.ID)
		require.NoError(t, err)
		assert.Equal(t, serverapi.StatusUnknown, got.Status)
	})
}

func TestServerHopsAndDelete(t *testing.T) {
	t.Run("SSH 跳板必须是已添加的其他服务器，代理须填地址", func(t *testing.T) {
		f := setupServerTest(t)
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "pw"})
		item := f.create(t, passwordInput(t, "web", target, "app", "pw"))

		cases := [][]serverapi.HopInput{
			{{Type: serverapi.HopSSH, ServerID: 999}},
			{{Type: serverapi.HopSSH, ServerID: item.ID}},
			{{Type: serverapi.HopSOCKS5, Host: "", Port: 1080}},
			{{Type: serverapi.HopHTTP, Host: "proxy", Port: 0}},
			{{Type: "ftp", Host: "proxy", Port: 21}},
		}
		for _, hops := range cases {
			in := passwordInput(t, "web", target, "app", secret.Masked)
			in.Hops = hops
			_, err := f.svc.Update(f.ctx, updateReq(item.ID, in))
			assert.Equal(t, code.ServerHopInvalid, codeOf(t, err), "%+v", hops)
		}
	})

	t.Run("被用作跳板或被任务使用时不能删除", func(t *testing.T) {
		f := setupServerTest(t)
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "pw"})
		jump := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "ops", Password: "pw"})
		jumpItem := f.create(t, passwordInput(t, "bastion", jump, "ops", "pw"))
		in := passwordInput(t, "web", target, "app", "pw")
		in.Hops = []serverapi.HopInput{{Type: serverapi.HopSSH, ServerID: jumpItem.ID}}
		item := f.create(t, in)

		assert.Equal(t, code.ServerUsedAsJump, codeOf(t, f.svc.Delete(f.ctx, jumpItem.ID)))

		RegisterJobCounter(func(context.Context) (map[int64]int, error) { return map[int64]int{item.ID: 1}, nil })
		assert.Equal(t, code.ServerInUse, codeOf(t, f.svc.Delete(f.ctx, item.ID)))

		RegisterJobCounter(nil)
		require.NoError(t, f.svc.Delete(f.ctx, item.ID))
		require.NoError(t, f.svc.Delete(f.ctx, jumpItem.ID))
		_, err := f.svc.Get(f.ctx, item.ID)
		assert.Equal(t, code.ServerNotFound, codeOf(t, err))
	})
}

func createReq(in serverapi.ServerInput) *serverapi.CreateRequest {
	return serverapi.NewCreateRequest(&in)
}

func updateReq(id int64, in serverapi.ServerInput) *serverapi.UpdateRequest {
	return serverapi.NewUpdateRequest(id, &in)
}

func testReq(id int64, in serverapi.ServerInput) *serverapi.TestRequest {
	return serverapi.NewTestRequest(id, &in)
}

func TestSavedPasswordStaysWithTarget(t *testing.T) {
	f := setupServerTest(t)
	target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "SECRET-saved-pw"})
	item := f.create(t, passwordInput(t, "web-1", target, "app", "SECRET-saved-pw"))
	// 另一台主机（例如持有 API 密钥的人自己的机器）：指纹可以核对，但不能拿到已保存的密码
	other := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "SECRET-saved-pw"})

	moved := passwordInput(t, "web-1", other, "app", secret.Masked)
	_, err := f.svc.Test(f.ctx, testReq(item.ID, moved))
	assert.Equal(t, code.ServerCredentialRetarget, codeOf(t, err), "测试连接不能把已保存的密码发往新地址")
	_, err = f.svc.Update(f.ctx, updateReq(item.ID, moved))
	assert.Equal(t, code.ServerCredentialRetarget, codeOf(t, err))

	otherUser := passwordInput(t, "web-1", target, "root", secret.Masked)
	_, err = f.svc.Update(f.ctx, updateReq(item.ID, otherUser))
	assert.Equal(t, code.ServerCredentialRetarget, codeOf(t, err), "换了登录用户同样要重新填写")

	// 地址和用户不变：占位仍然保留已保存的密码
	renamed := passwordInput(t, "web-1-renamed", target, "app", secret.Masked)
	_, err = f.svc.Update(f.ctx, updateReq(item.ID, renamed))
	require.NoError(t, err)
	p, err := secret.Default().Decrypt(f.raw(t, item.ID).Password)
	require.NoError(t, err)
	assert.Equal(t, "SECRET-saved-pw", p)
}

func TestSaveWaitsForMasterKeyRotation(t *testing.T) {
	f := setupServerTest(t)
	target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "pw"})
	done := secret.BeginRotate()
	saved := make(chan error, 1)
	go func() {
		_, err := f.svc.Create(f.ctx, createReq(passwordInput(t, "web-1", target, "app", "pw")))
		saved <- err
	}()
	select {
	case err := <-saved:
		t.Fatalf("轮换主密钥期间保存了凭据：%v", err)
	case <-time.After(200 * time.Millisecond):
	}
	done()
	require.NoError(t, <-saved)
}

func TestDeleteRefusesWhenRepoConnectsThrough(t *testing.T) {
	f := setupServerTest(t)
	target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "pw"})
	item := f.create(t, passwordInput(t, "nas-gw", target, "app", "pw"))
	require.NoError(t, f.db.Create(&repo_entity.Repo{
		Name: "群晖", Type: repo_entity.TypeSFTP,
		Params: repo_entity.Params{Host: "10.0.0.2", Port: 22, Username: "backup", Path: "/volume1/opsnap", ServerID: item.ID},
	}).Error)

	err := f.svc.Delete(f.ctx, item.ID)
	assert.Equal(t, code.ServerUsedByRepo, codeOf(t, err), "删掉后经它连接的存储位置每次备份都会失败")
	f.raw(t, item.ID)
}

func TestMovedServerStatusReset(t *testing.T) {
	f := setupServerTest(t)
	target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "pw"})
	other := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "pw"})
	item := f.create(t, passwordInput(t, "web-1", target, "app", "pw"))
	_, err := f.svc.TestSaved(f.ctx, item.ID)
	require.NoError(t, err)
	require.Equal(t, server_entity.StatusOK, f.raw(t, item.ID).Status())

	// 只改名字：状态保留
	_, err = f.svc.Update(f.ctx, updateReq(item.ID, passwordInput(t, "web-1b", target, "app", secret.Masked)))
	require.NoError(t, err)
	assert.Equal(t, server_entity.StatusOK, f.raw(t, item.ID).Status())

	// 换了地址：旧地址的「连接正常」和延迟不再适用
	_, err = f.svc.Update(f.ctx, updateReq(item.ID, passwordInput(t, "web-1b", other, "app", "pw")))
	require.NoError(t, err)
	got := f.raw(t, item.ID)
	assert.Equal(t, server_entity.StatusUnknown, got.Status())
	assert.Zero(t, got.LastLatencyMs)
}
