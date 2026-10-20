package server_svc

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	credentialapi "github.com/opskat/opsnap/internal/api/credential"
	serverapi "github.com/opskat/opsnap/internal/api/server"
	"github.com/opskat/opsnap/internal/model/entity/credential_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/secret"
	"github.com/opskat/opsnap/internal/pkg/sshconn/sshconntest"
	"github.com/opskat/opsnap/internal/service/credential_svc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// countRows 直接数一张表的行数，用来断言「测试连接不写库」「同一台服务器不会多出一条私有凭据」
func (f *fixture) countRows(t *testing.T, table string) int64 {
	t.Helper()
	var n int64
	require.NoError(t, f.db.Table(table).Count(&n).Error)
	return n
}

// shared 通过密钥服务建一条共享密钥，返回它的 ID
func (f *fixture) shared(t *testing.T, name, kind, username, passwordOrKey, passphrase string) int64 {
	t.Helper()
	in := &credentialapi.Input{Name: name, Kind: kind, Username: username}
	switch kind {
	case credential_entity.KindPassword:
		in.Password = passwordOrKey
	case credential_entity.KindSSHKey:
		in.PrivateKey, in.Passphrase = passwordOrKey, passphrase
	}
	resp, err := credential_svc.Credential().Create(f.ctx, credentialapi.NewCreateRequest(in))
	require.NoError(t, err)
	return resp.ID
}

// sharedInput 用一条共享密钥的服务器配置
func sharedInput(t *testing.T, name string, s *sshconntest.SSHServer, user string, credentialID int64) serverapi.ServerInput {
	t.Helper()
	host, port := sshconntest.HostPort(t, s.Addr)
	return serverapi.ServerInput{
		Name: name, Host: host, Port: port, Username: user,
		CredentialID: credentialID, HostKeyFingerprint: s.Fingerprint,
	}
}

func TestServerDirectCredential(t *testing.T) {
	t.Run("直接填写的密码能连上，接口只返回有没有内容", func(t *testing.T) {
		f := setupServerTest(t)
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "SECRET-direct-pw"})
		item := f.create(t, passwordInput(t, "web-1", target, "app", "SECRET-direct-pw"))

		require.NotNil(t, item.Credential)
		assert.Equal(t, credential_entity.KindPassword, item.Credential.Kind)
		assert.False(t, item.Credential.Shared)
		assert.Empty(t, item.Credential.Name, "私有凭据没有名称")
		assert.True(t, item.Credential.HasSecret)
		assert.False(t, item.Credential.HasPassphrase)

		res, err := f.svc.TestSaved(f.ctx, item.ID)
		require.NoError(t, err)
		require.True(t, res.OK, "%+v", res.Hops)
		assert.Equal(t, 1, target.AuthAttempts())
	})

	t.Run("直接填写的私钥（带口令）能连上", func(t *testing.T) {
		f := setupServerTest(t)
		key := sshconntest.GenerateKey(t, "SECRET-key-pass")
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", AuthorizedKey: key.Public})

		in := passwordInput(t, "db-1", target, "app", "")
		in.AuthType, in.PrivateKey, in.Passphrase = serverapi.AuthSSHKey, key.PrivatePEM, "SECRET-key-pass"
		item := f.create(t, in)

		require.NotNil(t, item.Credential)
		assert.Equal(t, credential_entity.KindSSHKey, item.Credential.Kind)
		assert.True(t, item.Credential.HasSecret)
		assert.True(t, item.Credential.HasPassphrase)

		res, err := f.svc.TestSaved(f.ctx, item.ID)
		require.NoError(t, err)
		require.True(t, res.OK, "%+v", res.Hops)
	})

	t.Run("口令错误不能保存", func(t *testing.T) {
		f := setupServerTest(t)
		key := sshconntest.GenerateKey(t, "right-pass")
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", AuthorizedKey: key.Public})

		in := passwordInput(t, "db-1", target, "app", "")
		in.AuthType, in.PrivateKey, in.Passphrase = serverapi.AuthSSHKey, key.PrivatePEM, "wrong-pass"
		_, err := f.svc.Create(f.ctx, createReq(in))
		assert.Equal(t, code.ServerPrivateKeyInvalid, codeOf(t, err))
	})

	t.Run("编辑时留空或占位都保留原私有凭据，行数不变", func(t *testing.T) {
		f := setupServerTest(t)
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "SECRET-orig-pw"}) //nolint:gosec // 测试用的假凭据
		item := f.create(t, passwordInput(t, "web-1", target, "app", "SECRET-orig-pw"))
		credID := f.raw(t, item.ID).CredentialID
		require.Equal(t, int64(1), f.countRows(t, "credentials"))

		// 留空：保留
		blank := passwordInput(t, "web-1-renamed", target, "app", "")
		_, err := f.svc.Update(f.ctx, updateReq(item.ID, blank))
		require.NoError(t, err)
		// 占位：同样保留
		masked := passwordInput(t, "web-1-renamed", target, "app", secret.Masked)
		_, err = f.svc.Update(f.ctx, updateReq(item.ID, masked))
		require.NoError(t, err)

		assert.Equal(t, credID, f.raw(t, item.ID).CredentialID, "同一台服务器始终只有这一条私有凭据")
		assert.Equal(t, int64(1), f.countRows(t, "credentials"), "不会新建第二条私有凭据")
		p, err := secret.Default().Decrypt(f.rawCredential(t, item.ID).Password)
		require.NoError(t, err)
		assert.Equal(t, "SECRET-orig-pw", p)

		res, err := f.svc.TestSaved(f.ctx, item.ID)
		require.NoError(t, err)
		require.True(t, res.OK, "%+v", res.Hops)
	})

	t.Run("改密码就是改这条私有凭据，不新建", func(t *testing.T) {
		f := setupServerTest(t)
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "SECRET-new-pw"}) //nolint:gosec // 测试用的假凭据
		item := f.create(t, passwordInput(t, "web-1", target, "app", "SECRET-old-pw"))
		credID := f.raw(t, item.ID).CredentialID

		_, err := f.svc.Update(f.ctx, updateReq(item.ID, passwordInput(t, "web-1", target, "app", "SECRET-new-pw")))
		require.NoError(t, err)
		assert.Equal(t, credID, f.raw(t, item.ID).CredentialID)
		assert.Equal(t, int64(1), f.countRows(t, "credentials"))

		res, err := f.svc.TestSaved(f.ctx, item.ID)
		require.NoError(t, err)
		require.True(t, res.OK, "%+v", res.Hops)
	})

	t.Run("认证方式从密码切到私钥时清掉旧密码", func(t *testing.T) {
		f := setupServerTest(t)
		key := sshconntest.GenerateKey(t, "")
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", AuthorizedKey: key.Public})
		item := f.create(t, passwordInput(t, "web-1", target, "app", "SECRET-old-pw"))

		in := passwordInput(t, "web-1", target, "app", "")
		in.AuthType, in.PrivateKey = serverapi.AuthSSHKey, key.PrivatePEM
		_, err := f.svc.Update(f.ctx, updateReq(item.ID, in))
		require.NoError(t, err)

		cred := f.rawCredential(t, item.ID)
		assert.Equal(t, credential_entity.KindSSHKey, cred.Kind)
		assert.Empty(t, cred.Password, "切换到私钥后不留下旧的密码")

		res, err := f.svc.TestSaved(f.ctx, item.ID)
		require.NoError(t, err)
		require.True(t, res.OK, "%+v", res.Hops)
	})

	t.Run("改地址或用户时私钥仍然沿用已保存的值", func(t *testing.T) {
		// 私钥不会发给对方（公钥认证只发签名），换目标不构成泄露；
		// 只有密码会被原样发出去，才需要在地址或用户变化时强制重填
		f := setupServerTest(t)
		key := sshconntest.GenerateKey(t, "SECRET-key-pass")
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", AuthorizedKey: key.Public})
		other := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", AuthorizedKey: key.Public})

		in := passwordInput(t, "db-1", target, "app", "")
		in.AuthType, in.PrivateKey, in.Passphrase = serverapi.AuthSSHKey, key.PrivatePEM, "SECRET-key-pass"
		item := f.create(t, in)
		before := f.rawCredential(t, item.ID).PrivateKey

		// 只换地址与用户，私钥与口令回传占位
		moved := passwordInput(t, "db-1", other, "root", "")
		moved.AuthType, moved.PrivateKey, moved.Passphrase = serverapi.AuthSSHKey, secret.Masked, secret.Masked
		_, err := f.svc.Update(f.ctx, updateReq(item.ID, moved))
		require.NoError(t, err, "私钥不随地址变化而失效")

		cred := f.rawCredential(t, item.ID)
		assert.Equal(t, before, cred.PrivateKey, "沿用已保存的私钥")
		assert.NotEmpty(t, cred.Passphrase, "口令同样保留")
	})
}

func TestServerSharedCredential(t *testing.T) {
	t.Run("引用共享密钥时用它的凭据连接，改完密码立刻生效", func(t *testing.T) {
		f := setupServerTest(t)
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "SECRET-right-pw"})
		// 先存一个错的密码：证明连接用的是密钥库里的当前内容
		id := f.shared(t, "生产 root", credential_entity.KindPassword, "app", "SECRET-wrong-pw", "")

		item := f.create(t, sharedInput(t, "web-1", target, "app", id))
		require.NotNil(t, item.Credential)
		assert.True(t, item.Credential.Shared)
		assert.Equal(t, "生产 root", item.Credential.Name)
		assert.Equal(t, credential_entity.KindPassword, item.Credential.Kind)
		assert.Equal(t, "app", item.Credential.Username)
		assert.True(t, item.Credential.HasSecret)
		assert.Equal(t, id, f.raw(t, item.ID).CredentialID, "服务器只记共享密钥的 ID")
		assert.Equal(t, int64(1), f.countRows(t, "credentials"), "引用共享密钥不会多出私有凭据")

		res, err := f.svc.TestSaved(f.ctx, item.ID)
		require.NoError(t, err)
		require.Len(t, res.Hops, 1)
		assert.Equal(t, "auth_failed", res.Hops[0].Reason)

		// 改一次共享密钥，同一台服务器立刻用新密码
		_, err = credential_svc.Credential().Update(f.ctx, credentialapi.NewUpdateRequest(id, &credentialapi.Input{
			Name: "生产 root", Kind: credential_entity.KindPassword, Password: "SECRET-right-pw",
		}))
		require.NoError(t, err)

		res, err = f.svc.TestSaved(f.ctx, item.ID)
		require.NoError(t, err)
		require.True(t, res.OK, "%+v", res.Hops)
		assert.Equal(t, int64(1), f.countRows(t, "credentials"))
	})

	t.Run("共享的私钥密钥也能用", func(t *testing.T) {
		f := setupServerTest(t)
		key := sshconntest.GenerateKey(t, "SECRET-key-pass")
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", AuthorizedKey: key.Public})
		id := f.shared(t, "运维私钥", credential_entity.KindSSHKey, "app", key.PrivatePEM, "SECRET-key-pass")

		item := f.create(t, sharedInput(t, "db-1", target, "app", id))
		require.NotNil(t, item.Credential)
		assert.Equal(t, credential_entity.KindSSHKey, item.Credential.Kind)
		assert.True(t, item.Credential.HasPassphrase)

		res, err := f.svc.TestSaved(f.ctx, item.ID)
		require.NoError(t, err)
		require.True(t, res.OK, "%+v", res.Hops)
	})

	t.Run("credential_id 指向不存在或私有凭据时按密钥不存在处理", func(t *testing.T) {
		f := setupServerTest(t)
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "pw"})
		private := f.create(t, passwordInput(t, "web-1", target, "app", "pw"))
		privateCredID := f.raw(t, private.ID).CredentialID

		for _, id := range []int64{9999, privateCredID} {
			in := sharedInput(t, "web-2", target, "app", id)
			_, err := f.svc.Create(f.ctx, createReq(in))
			assert.Equal(t, code.CredentialNotFound, codeOf(t, err), "credential_id=%d", id)

			// 保存服务器时同样拒绝
			_, err = f.svc.Update(f.ctx, updateReq(private.ID, in))
			assert.Equal(t, code.CredentialNotFound, codeOf(t, err), "credential_id=%d", id)
		}
		// 原来的私有凭据没有被改坏
		assert.Equal(t, privateCredID, f.raw(t, private.ID).CredentialID)
	})

	t.Run("从共享切到直接填写时新建一条私有凭据，共享的不动", func(t *testing.T) {
		f := setupServerTest(t)
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "SECRET-direct-pw"})
		sharedID := f.shared(t, "生产 root", credential_entity.KindPassword, "app", "SECRET-shared-pw", "")
		item := f.create(t, sharedInput(t, "web-1", target, "app", sharedID))
		other := f.create(t, sharedInput(t, "web-2", target, "app", sharedID))

		_, err := f.svc.Update(f.ctx, updateReq(item.ID, passwordInput(t, "web-1", target, "app", "SECRET-direct-pw")))
		require.NoError(t, err)

		privateID := f.raw(t, item.ID).CredentialID
		assert.NotEqual(t, sharedID, privateID, "切换到直接填写后服务器改用新的私有凭据")
		assert.Equal(t, int64(2), f.countRows(t, "credentials"), "多出来的只有那条私有凭据")
		shared := f.rawCredential(t, other.ID)
		assert.Equal(t, sharedID, shared.ID, "另一台服务器仍然用着共享密钥")
		assert.True(t, shared.IsShared())
		assert.Equal(t, sharedID, f.raw(t, other.ID).CredentialID)
	})
	t.Run("从直接填写切到共享密钥时丢掉不再使用的私有凭据", func(t *testing.T) {
		f := setupServerTest(t)
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "SECRET-shared-pw"})
		item := f.create(t, passwordInput(t, "web-1", target, "app", "SECRET-shared-pw"))
		privateID := f.raw(t, item.ID).CredentialID
		sharedID := f.shared(t, "生产 root", credential_entity.KindPassword, "app", "SECRET-shared-pw", "")
		require.Equal(t, int64(2), f.countRows(t, "credentials"))

		_, err := f.svc.Update(f.ctx, updateReq(item.ID, sharedInput(t, "web-1", target, "app", sharedID)))
		require.NoError(t, err)

		assert.Equal(t, sharedID, f.raw(t, item.ID).CredentialID)
		var n int64
		require.NoError(t, f.db.Model(&credential_entity.Credential{}).Where("id = ?", privateID).Count(&n).Error)
		assert.Zero(t, n, "被丢下的私有凭据不能留成孤儿行")
		assert.Equal(t, int64(1), f.countRows(t, "credentials"))

		res, err := f.svc.TestSaved(f.ctx, item.ID)
		require.NoError(t, err)
		require.True(t, res.OK, "%+v", res.Hops)
	})
}

func TestServerShareAs(t *testing.T) {
	t.Run("share_as 把私有凭据原地转成共享：行 ID 与引用都不变，并出现在密钥列表里", func(t *testing.T) {
		f := setupServerTest(t)
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "SECRET-pw"})
		item := f.create(t, passwordInput(t, "web-1", target, "app", "SECRET-pw"))
		credID := f.raw(t, item.ID).CredentialID

		in := passwordInput(t, "web-1", target, "app", secret.Masked)
		in.ShareAs = "生产 root"
		saved, err := f.svc.Update(f.ctx, updateReq(item.ID, in))
		require.NoError(t, err)

		assert.Equal(t, credID, f.raw(t, item.ID).CredentialID, "原地转共享，服务器的引用不变")
		assert.Equal(t, credID, saved.Credential.ID, "行 ID 不变")
		assert.True(t, saved.Credential.Shared)
		assert.Equal(t, "生产 root", saved.Credential.Name)
		assert.Equal(t, int64(1), f.countRows(t, "credentials"), "不复制出一条新的共享密钥")

		list, err := credential_svc.Credential().List(f.ctx)
		require.NoError(t, err)
		require.Len(t, list.List, 1)
		assert.Equal(t, credID, list.List[0].ID)
		assert.Equal(t, "生产 root", list.List[0].Name)

		res, err := f.svc.TestSaved(f.ctx, item.ID)
		require.NoError(t, err)
		require.True(t, res.OK, "%+v", res.Hops)
	})

	t.Run("转共享时与其它共享密钥重名自动加序号，凭据仍然原地转成共享", func(t *testing.T) {
		f := setupServerTest(t)
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "SECRET-pw"})
		// 「同时存成密钥」的名字默认是服务器名，同一台机器的第二、三份凭据要能各自存下来
		f.shared(t, "web-01", credential_entity.KindPassword, "app", "SECRET-other-pw", "")
		f.shared(t, "web-01-2", credential_entity.KindPassword, "app", "SECRET-other-pw", "")
		item := f.create(t, passwordInput(t, "web-01", target, "app", "SECRET-pw"))
		credID := f.raw(t, item.ID).CredentialID

		in := passwordInput(t, "web-01", target, "app", secret.Masked)
		in.ShareAs = "web-01"
		saved, err := f.svc.Update(f.ctx, updateReq(item.ID, in))
		require.NoError(t, err)

		assert.Equal(t, "web-01-3", saved.Credential.Name, "重名时自动加序号")
		assert.True(t, saved.Credential.Shared)
		assert.Equal(t, credID, f.raw(t, item.ID).CredentialID, "原地转共享，行 ID 与引用都不变")
		assert.Equal(t, int64(3), f.countRows(t, "credentials"), "没有复制出新的凭据行")

		// 序号接在已有名称后面，不会覆盖掉别人
		list, err := credential_svc.Credential().List(f.ctx)
		require.NoError(t, err)
		names := make([]string, 0, len(list.List))
		for _, c := range list.List {
			names = append(names, c.Name)
		}
		assert.Equal(t, []string{"web-01", "web-01-2", "web-01-3"}, names)
	})

	t.Run("自动加序号后名称仍不超过 64 个字", func(t *testing.T) {
		f := setupServerTest(t)
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "SECRET-pw"})
		long := strings.Repeat("名", 64)
		f.shared(t, long, credential_entity.KindPassword, "app", "SECRET-other-pw", "")
		item := f.create(t, passwordInput(t, "web-1", target, "app", "SECRET-pw"))

		in := passwordInput(t, "web-1", target, "app", secret.Masked)
		in.ShareAs = long
		saved, err := f.svc.Update(f.ctx, updateReq(item.ID, in))
		require.NoError(t, err)

		assert.LessOrEqual(t, len([]rune(saved.Credential.Name)), 64)
		assert.True(t, strings.HasSuffix(saved.Credential.Name, "-2"), saved.Credential.Name)
	})

	t.Run("同一台服务器再次带 share_as 保存时沿用已经转成共享的那条，不改名字", func(t *testing.T) {
		f := setupServerTest(t)
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "SECRET-pw"})
		item := f.create(t, passwordInput(t, "web-1", target, "app", "SECRET-pw"))
		credID := f.raw(t, item.ID).CredentialID

		first := passwordInput(t, "web-1", target, "app", secret.Masked)
		first.ShareAs = "web-01"
		_, err := f.svc.Update(f.ctx, updateReq(item.ID, first))
		require.NoError(t, err)

		// 这台服务器已经是共享的了：再勾一次「同时存成密钥」不会变成 web-01-2
		second := sharedInput(t, "web-1", target, "app", credID)
		second.ShareAs = "web-01"
		saved, err := f.svc.Update(f.ctx, updateReq(item.ID, second))
		require.NoError(t, err)
		assert.Equal(t, "web-01", saved.Credential.Name)
		assert.Equal(t, int64(1), f.countRows(t, "credentials"))
	})

	t.Run("新建服务器时直接带 share_as 就直接存成共享密钥", func(t *testing.T) {
		f := setupServerTest(t)
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "SECRET-pw"})
		in := passwordInput(t, "web-1", target, "app", "SECRET-pw")
		in.ShareAs = "生产 root"
		item := f.create(t, in)

		require.NotNil(t, item.Credential)
		assert.True(t, item.Credential.Shared)
		assert.Equal(t, "生产 root", item.Credential.Name)
		assert.Equal(t, f.raw(t, item.ID).CredentialID, item.Credential.ID)

		list, err := credential_svc.Credential().List(f.ctx)
		require.NoError(t, err)
		require.Len(t, list.List, 1)
		assert.Equal(t, item.Credential.ID, list.List[0].ID)
	})

	t.Run("已经引用共享密钥时传 share_as 视为无操作", func(t *testing.T) {
		f := setupServerTest(t)
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "SECRET-pw"})
		sharedID := f.shared(t, "生产 root", credential_entity.KindPassword, "app", "SECRET-pw", "")
		item := f.create(t, sharedInput(t, "web-1", target, "app", sharedID))

		in := sharedInput(t, "web-1", target, "app", sharedID)
		in.ShareAs = "另一个名字"
		saved, err := f.svc.Update(f.ctx, updateReq(item.ID, in))
		require.NoError(t, err)

		assert.Equal(t, sharedID, f.raw(t, item.ID).CredentialID)
		require.NotNil(t, saved.Credential)
		assert.Equal(t, "生产 root", saved.Credential.Name, "共享密钥的名称没有被改掉")
		assert.Equal(t, int64(1), f.countRows(t, "credentials"))
	})
}

func TestServerCredentialCleanup(t *testing.T) {
	t.Run("删除服务器删掉它的私有凭据，共享凭据不动", func(t *testing.T) {
		f := setupServerTest(t)
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "SECRET-pw"})
		sharedID := f.shared(t, "生产 root", credential_entity.KindPassword, "app", "SECRET-pw", "")

		privateItem := f.create(t, passwordInput(t, "web-1", target, "app", "SECRET-pw"))
		privateID := f.raw(t, privateItem.ID).CredentialID
		sharedItem := f.create(t, sharedInput(t, "web-2", target, "app", sharedID))
		require.Equal(t, int64(2), f.countRows(t, "credentials"))

		require.NoError(t, f.svc.Delete(f.ctx, privateItem.ID))
		assert.Equal(t, int64(1), f.countRows(t, "credentials"), "私有凭据随服务器一起删除")
		var n int64
		require.NoError(t, f.db.Model(&credential_entity.Credential{}).Where("id = ?", privateID).Count(&n).Error)
		assert.Zero(t, n)

		require.NoError(t, f.svc.Delete(f.ctx, sharedItem.ID))
		require.NoError(t, f.db.Model(&credential_entity.Credential{}).Where("id = ?", sharedID).Count(&n).Error)
		assert.Equal(t, int64(1), n, "共用的密钥不随服务器删除")
	})

	t.Run("转成共享后再删服务器，凭据仍然留着", func(t *testing.T) {
		f := setupServerTest(t)
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "SECRET-pw"})
		in := passwordInput(t, "web-1", target, "app", "SECRET-pw")
		in.ShareAs = "生产 root"
		item := f.create(t, in)

		require.NoError(t, f.svc.Delete(f.ctx, item.ID))
		assert.Equal(t, int64(1), f.countRows(t, "credentials"), "已经转成共享的凭据不是服务器的私有凭据")
	})
}

func TestServerTestDoesNotWrite(t *testing.T) {
	t.Run("测试连接不写任何行：新建的配置与保留占位都一样", func(t *testing.T) {
		f := setupServerTest(t)
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "SECRET-pw"})
		item := f.create(t, passwordInput(t, "web-1", target, "app", "SECRET-pw"))
		credentials, servers := f.countRows(t, "credentials"), f.countRows(t, "servers")

		// 新服务器的草稿：待写入的私有凭据只在内存里
		draft := passwordInput(t, "web-2", target, "app", "SECRET-pw")
		res, err := f.svc.Test(f.ctx, testReq(0, draft))
		require.NoError(t, err)
		require.True(t, res.OK, "%+v", res.Hops)

		// 编辑已保存的服务器：占位保留原值
		kept := passwordInput(t, "web-1", target, "app", secret.Masked)
		_, err = f.svc.Test(f.ctx, testReq(item.ID, kept))
		require.NoError(t, err)

		// 带 share_as 的草稿同样不写库
		share := passwordInput(t, "web-3", target, "app", "SECRET-draft-pw")
		share.ShareAs = "草稿密钥"
		_, err = f.svc.Test(f.ctx, testReq(0, share))
		require.NoError(t, err)

		assert.Equal(t, credentials, f.countRows(t, "credentials"), "测试连接不写凭据")
		assert.Equal(t, servers, f.countRows(t, "servers"), "测试连接不写服务器")
		list, err := credential_svc.Credential().List(f.ctx)
		require.NoError(t, err)
		assert.Empty(t, list.List, "草稿的 share_as 不会真的存成共享密钥")
	})

	t.Run("测试连接缺凭据时不发请求", func(t *testing.T) {
		f := setupServerTest(t)
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "pw"})
		in := passwordInput(t, "web-1", target, "app", "")
		_, err := f.svc.Test(f.ctx, testReq(0, in))
		assert.Equal(t, code.ServerCredentialRequired, codeOf(t, err))
		assert.Zero(t, target.AuthAttempts())
	})
}

func TestServerResponseHasNoPlaintext(t *testing.T) {
	f := setupServerTest(t)
	key := sshconntest.GenerateKey(t, "SECRET-key-pass")
	target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", AuthorizedKey: key.Public})
	socks := sshconntest.StartSOCKS5(t, sshconntest.ProxyOptions{User: "px", Password: "SECRET-proxy-pw"})
	sh, sp := sshconntest.HostPort(t, socks.Addr)

	in := passwordInput(t, "web-1", target, "app", "")
	in.AuthType, in.PrivateKey, in.Passphrase = serverapi.AuthSSHKey, key.PrivatePEM, "SECRET-key-pass"
	in.Hops = []serverapi.HopInput{{Type: serverapi.HopSOCKS5, Host: sh, Port: sp, Username: "px", Password: "SECRET-proxy-pw"}}
	item := f.create(t, in)

	// 同样检查引用共享密钥的服务器
	sharedID := f.shared(t, "运维私钥", credential_entity.KindSSHKey, "app", key.PrivatePEM, "SECRET-key-pass")
	second := f.create(t, sharedInput(t, "web-2", target, "app", sharedID))

	list, err := f.svc.List(f.ctx)
	require.NoError(t, err)
	require.Len(t, list.List, 2)

	for _, got := range []*serverapi.Item{item, second} {
		raw, err := json.Marshal(got)
		require.NoError(t, err)
		text := fmt.Sprintf("%+v %s", got, raw)
		for _, plain := range []string{"SECRET-key-pass", "SECRET-proxy-pw", "BEGIN OPENSSH PRIVATE KEY", key.PrivatePEM} {
			assert.NotContains(t, text, plain, "响应里不能出现明文或密文")
		}
		assert.Contains(t, text, `"credential"`)
	}
}
