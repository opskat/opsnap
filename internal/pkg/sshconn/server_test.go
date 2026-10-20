package sshconn

import (
	"context"
	"testing"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/opskat/opsnap/internal/model/entity/credential_entity"
	"github.com/opskat/opsnap/internal/model/entity/server_entity"
	"github.com/opskat/opsnap/internal/pkg/secret"
	"github.com/opskat/opsnap/internal/pkg/sshconn/sshconntest"
	"github.com/opskat/opsnap/internal/repository/credential_repo"
	"github.com/opskat/opsnap/internal/repository/server_repo"
	"github.com/opskat/opsnap/migrations"
)

func setupRepo(t *testing.T) context.Context {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, migrations.RunMigrations(gdb))
	s, err := secret.New("sshconn-test-key")
	require.NoError(t, err)
	secret.SetDefault(s)
	t.Cleanup(func() { secret.SetDefault(nil) })
	server_repo.RegisterServer(server_repo.NewServer())
	credential_repo.RegisterCredential(credential_repo.NewCredential())
	return db.WithContextDB(context.Background(), gdb)
}

// saveServer 保存一台用密码认证的服务器：密码写在它自己的私有凭据行里
func saveServer(t *testing.T, ctx context.Context, name string, s *sshconntest.SSHServer, user, password string, hops ...server_entity.Hop) *server_entity.Server {
	t.Helper()
	cred := savePasswordCredential(t, ctx, password)
	host, port := sshconntest.HostPort(t, s.Addr)
	srv := &server_entity.Server{
		Name: name, Host: host, Port: port, Username: user, CredentialID: cred.ID,
		Hops: hops, HostKeyFingerprint: s.Fingerprint,
	}
	require.NoError(t, server_repo.Server().Create(ctx, srv))
	return srv
}

// savePasswordCredential 保存一条私有的密码凭据，密码加密存储
func savePasswordCredential(t *testing.T, ctx context.Context, password string) *credential_entity.Credential {
	t.Helper()
	enc, err := secret.Default().Encrypt(password)
	require.NoError(t, err)
	cred := &credential_entity.Credential{Kind: credential_entity.KindPassword, Password: enc}
	require.NoError(t, credential_repo.Credential().Create(ctx, cred))
	return cred
}

func TestDialSavedServer(t *testing.T) {
	t.Run("按已保存的代理链连接，并记录正常状态", func(t *testing.T) {
		ctx := setupRepo(t)
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "app-pw"})
		jump := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "ops", Password: "jump-pw"})
		j := saveServer(t, ctx, "bastion", jump, "ops", "jump-pw")
		srv := saveServer(t, ctx, "app", target, "app", "app-pw", server_entity.Hop{Type: server_entity.HopSSH, ServerID: j.ID})

		at := time.Unix(1_800_000_000, 0)
		c, results, err := Dial(ctx, srv.ID, WithNow(func() time.Time { return at }))
		require.NoError(t, err)
		defer func() { _ = c.Close() }()
		require.Len(t, results, 2)
		assert.Equal(t, "bastion", results[0].Name)
		assert.Equal(t, []string{target.Addr}, jump.DirectTCPIP())

		got, err := server_repo.Server().Find(ctx, srv.ID)
		require.NoError(t, err)
		assert.Equal(t, server_entity.StatusOK, got.Status())
		assert.Equal(t, at.Unix(), got.LastCheckAt)
	})

	t.Run("跳板指纹变化：拒绝连接并记录主机指纹已变化", func(t *testing.T) {
		ctx := setupRepo(t)
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "pw"})
		jump := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "ops", Password: "pw"})
		j := saveServer(t, ctx, "bastion", jump, "ops", "pw")
		srv := saveServer(t, ctx, "app", target, "app", "pw", server_entity.Hop{Type: server_entity.HopSSH, ServerID: j.ID})
		j.HostKeyFingerprint = target.Fingerprint
		require.NoError(t, server_repo.Server().Update(ctx, j))

		c, results, err := Dial(ctx, srv.ID)
		assert.Nil(t, c)
		require.ErrorIs(t, err, ErrHostKeyChanged)
		require.Len(t, results, 1)
		assert.Equal(t, 0, jump.AuthAttempts())
		assert.Equal(t, 0, target.Connections())

		got, err := server_repo.Server().Find(ctx, srv.ID)
		require.NoError(t, err)
		assert.Equal(t, server_entity.StatusFailed, got.Status())
		assert.Contains(t, got.LastError, "主机指纹已变化")
		assert.Contains(t, got.LastError, "第 1 跳")
	})

	t.Run("服务器不存在", func(t *testing.T) {
		ctx := setupRepo(t)
		_, _, err := Dial(ctx, 404)
		assert.ErrorIs(t, err, ErrServerNotFound)
	})

	t.Run("调用方取消时不记录状态", func(t *testing.T) {
		ctx := setupRepo(t)
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "pw"})
		srv := saveServer(t, ctx, "app", target, "app", "pw")
		// 目标改指向不应答的地址，在握手中途取消
		srv.Host, srv.Port = sshconntest.HostPort(t, sshconntest.StartBlackhole(t))
		require.NoError(t, server_repo.Server().Update(ctx, srv))
		canceled, cancel := context.WithCancel(ctx)
		time.AfterFunc(100*time.Millisecond, cancel)
		_, results, err := Dial(canceled, srv.ID)
		var he *HopError
		require.ErrorAs(t, err, &he)
		assert.Equal(t, ReasonCanceled, he.Reason)
		require.Len(t, results, 1)
		got, err := server_repo.Server().Find(ctx, srv.ID)
		require.NoError(t, err)
		assert.Equal(t, server_entity.StatusUnknown, got.Status())
	})
}

func TestServerConfigUsesCredentialRow(t *testing.T) {
	t.Run("连接用的是凭据行里的密码：改掉密文后连接随之失败", func(t *testing.T) {
		ctx := setupRepo(t)
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "app-pw"})
		srv := saveServer(t, ctx, "app", target, "app", "app-pw")

		// 用正确的密码能连上
		_, _, err := Dial(ctx, srv.ID)
		require.NoError(t, err)
		require.Equal(t, 1, target.AuthAttempts())

		// 把凭据行的密文换成另一个密码的密文：服务器行没动，连接必须跟着变
		enc, err := secret.Default().Encrypt("wrong-pw")
		require.NoError(t, err)
		require.NoError(t, db.Ctx(ctx).Model(&credential_entity.Credential{}).
			Where("id = ?", srv.CredentialID).Update("password", enc).Error)

		c, results, err := Dial(ctx, srv.ID)
		assert.Nil(t, c)
		var he *HopError
		require.ErrorAs(t, err, &he)
		assert.Equal(t, ReasonAuthFailed, he.Reason)
		require.Len(t, results, 1)
	})

	t.Run("私钥认证按凭据行的类型决定", func(t *testing.T) {
		ctx := setupRepo(t)
		key := sshconntest.GenerateKey(t, "")
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", AuthorizedKey: key.Public})
		host, port := sshconntest.HostPort(t, target.Addr)
		encKey, err := secret.Default().Encrypt(key.PrivatePEM)
		require.NoError(t, err)
		cred := &credential_entity.Credential{Kind: credential_entity.KindSSHKey, PrivateKey: encKey}
		require.NoError(t, credential_repo.Credential().Create(ctx, cred))
		srv := &server_entity.Server{
			Name: "app", Host: host, Port: port, Username: "app", CredentialID: cred.ID,
			Hops: []server_entity.Hop{}, HostKeyFingerprint: target.Fingerprint,
		}
		require.NoError(t, server_repo.Server().Create(ctx, srv))

		c, _, err := Dial(ctx, srv.ID)
		require.NoError(t, err)
		defer func() { _ = c.Close() }()
		assert.Equal(t, 1, target.AuthAttempts())
	})

	t.Run("凭据不存在时报错，不去认证", func(t *testing.T) {
		ctx := setupRepo(t)
		target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "pw"})
		srv := saveServer(t, ctx, "app", target, "app", "pw")
		require.NoError(t, db.Ctx(ctx).Where("id = ?", srv.CredentialID).
			Delete(&credential_entity.Credential{}).Error)

		_, _, err := Dial(ctx, srv.ID)
		require.Error(t, err)
		assert.Zero(t, target.AuthAttempts())
	})
}
