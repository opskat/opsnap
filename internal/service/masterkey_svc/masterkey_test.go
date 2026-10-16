package masterkey_svc

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

	settingapi "github.com/opskat/opsnap/internal/api/setting"
	"github.com/opskat/opsnap/internal/model/entity/job_entity"
	"github.com/opskat/opsnap/internal/model/entity/notify_entity"
	"github.com/opskat/opsnap/internal/model/entity/repo_entity"
	"github.com/opskat/opsnap/internal/model/entity/server_entity"
	"github.com/opskat/opsnap/internal/model/entity/sso_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/secret"
	"github.com/opskat/opsnap/internal/repository/masterkey_repo"
	"github.com/opskat/opsnap/migrations"
)

// 带标记的假凭据：轮换后仍能用新主密钥解出原值
//
//nolint:gosec // 测试用的假凭据
const (
	srvPW     = "SECRET-server-pw"
	srvKey    = "SECRET-server-key"
	srvPhrase = "SECRET-server-phrase"
	hopPW     = "SECRET-hop-pw"
	repoCred  = "SECRET-repo-cred"
	repoPK    = "SECRET-repo-pk"
	repoKey   = "SECRET-opsnap-key"
	jobPW     = "SECRET-db-pw"
	headerVal = "SECRET-header"
	ssoSecret = "SECRET-oidc-client"
	oldMaster = "old-master-key-0123456789"
	newMaster = "new-master-key-9876543210"
)

type fixture struct {
	ctx context.Context
	db  *gorm.DB
	svc *masterKeySvc
	old secret.Secret
}

func setup(t *testing.T) *fixture {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, migrations.RunMigrations(gdb))
	masterkey_repo.RegisterMasterKey(masterkey_repo.NewMasterKey())

	old, err := secret.New(oldMaster)
	require.NoError(t, err)
	secret.SetDefault(old)
	t.Cleanup(func() { secret.SetDefault(nil) })

	f := &fixture{
		ctx: db.WithContextDB(context.Background(), gdb), db: gdb, old: old,
		svc: newMasterKey(func() time.Time { return time.Unix(1_800_000_000, 0) }),
	}
	enc := func(v string) string {
		c, err := old.Encrypt(v)
		require.NoError(t, err)
		return c
	}
	require.NoError(t, gdb.Create(&server_entity.Server{
		Name: "web", Host: "h", Port: 22, Username: "u", AuthType: server_entity.AuthPrivateKey,
		Password: enc(srvPW), PrivateKey: enc(srvKey), Passphrase: enc(srvPhrase),
		Hops:               []server_entity.Hop{{Type: "socks5", Host: "p", Port: 1080, Username: "pu", Password: enc(hopPW)}, {Type: "http", Host: "q", Port: 3128}},
		HostKeyFingerprint: "SHA256:x",
	}).Error)
	require.NoError(t, gdb.Create(&repo_entity.Repo{
		Name: "nas", Type: repo_entity.TypeSFTP, Credential: enc(repoCred), PrivateKey: enc(repoPK), OpsnapKey: enc(repoKey),
	}).Error)
	require.NoError(t, gdb.Create(&job_entity.Job{
		Name: "db", SourceType: job_entity.SourceMySQL, DBPassword: enc(jobPW),
		Databases: []string{}, Paths: []string{}, Excludes: []string{}, ExtraArgs: []string{},
	}).Error)
	require.NoError(t, gdb.Create(&notify_entity.Notify{
		ID: notify_entity.SingletonID, URL: "https://hook", Headers: map[string]string{"Authorization": enc(headerVal)}, StaleHours: 48,
	}).Error)
	require.NoError(t, gdb.Create(&sso_entity.SSO{
		ID: sso_entity.SingletonID, Issuer: "https://git.example.com/", ClientID: "cid", ClientSecret: enc(ssoSecret), PasswordLogin: true,
	}).Error)
	require.NoError(t, f.svc.Verify(f.ctx), "首次启动写入校验值")
	return f
}

// plaintexts 用 sec 解出所有加密保存的字段
func (f *fixture) plaintexts(t *testing.T, sec secret.Secret) ([]string, error) {
	t.Helper()
	var srv server_entity.Server
	require.NoError(t, f.db.First(&srv).Error)
	var rp repo_entity.Repo
	require.NoError(t, f.db.First(&rp).Error)
	var j job_entity.Job
	require.NoError(t, f.db.First(&j).Error)
	var n notify_entity.Notify
	require.NoError(t, f.db.First(&n).Error)
	var sso sso_entity.SSO
	require.NoError(t, f.db.First(&sso).Error)
	var out []string
	for _, c := range []string{srv.Password, srv.PrivateKey, srv.Passphrase, srv.Hops[0].Password, rp.Credential, rp.PrivateKey, rp.OpsnapKey, j.DBPassword, n.Headers["Authorization"], sso.ClientSecret} {
		p, err := sec.Decrypt(c)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	assert.Empty(t, srv.Hops[1].Password, "没有密码的跳保持为空")
	return out, nil
}

var allPlain = []string{srvPW, srvKey, srvPhrase, hopPW, repoCred, repoPK, repoKey, jobPW, headerVal, ssoSecret}

func codeOf(t *testing.T, err error) int {
	t.Helper()
	var he *httputils.Error
	require.ErrorAs(t, err, &he)
	return he.Code
}

func TestRotate(t *testing.T) {
	f := setup(t)
	resp, err := f.svc.Rotate(f.ctx, &settingapi.RotateMasterKeyRequest{NewKey: newMaster})
	require.NoError(t, err)
	assert.Equal(t, int64(1_800_000_000), resp.RotatedAt)

	newSec, err := secret.New(newMaster)
	require.NoError(t, err)
	got, err := f.plaintexts(t, newSec)
	require.NoError(t, err)
	assert.Equal(t, allPlain, got, "所有凭据都用新主密钥重新加密")
	_, err = f.plaintexts(t, f.old)
	assert.Error(t, err, "旧主密钥不再能解开")

	// 立即生效：本进程改用新主密钥
	p, err := secret.Default().Decrypt(mustEncrypt(t, newSec, "x"))
	require.NoError(t, err)
	assert.Equal(t, "x", p)

	// 重启时：用新主密钥启动通过，仍用旧主密钥启动被拒绝
	assert.NoError(t, f.svc.Verify(f.ctx))
	secret.SetDefault(f.old)
	err = f.svc.Verify(f.ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "主密钥")
}

func TestRotateWaitsForCredentialWrite(t *testing.T) {
	f := setup(t)
	// 保存凭据的一方：用当前主密钥加密，稍后才落库（例如更换仓库密钥要先跑 restic）
	done := secret.BeginWrite()
	late, err := secret.Default().Encrypt("SECRET-late")
	require.NoError(t, err)

	rotated := make(chan error, 1)
	go func() {
		_, err := f.svc.Rotate(f.ctx, &settingapi.RotateMasterKeyRequest{NewKey: newMaster})
		rotated <- err
	}()
	select {
	case err := <-rotated:
		t.Fatalf("凭据还没落库，轮换就结束了：%v", err)
	case <-time.After(200 * time.Millisecond):
	}
	require.NoError(t, f.db.Model(&job_entity.Job{}).Where("1 = 1").Update("db_password", late).Error)
	done()
	require.NoError(t, <-rotated)

	newSec, err := secret.New(newMaster)
	require.NoError(t, err)
	var j job_entity.Job
	require.NoError(t, f.db.First(&j).Error)
	p, err := newSec.Decrypt(j.DBPassword)
	require.NoError(t, err, "轮换期间写入的凭据也换成了新主密钥的密文")
	assert.Equal(t, "SECRET-late", p)
}

func TestRotateRejects(t *testing.T) {
	t.Run("新主密钥为空或与当前相同", func(t *testing.T) {
		f := setup(t)
		_, err := f.svc.Rotate(f.ctx, &settingapi.RotateMasterKeyRequest{NewKey: "  "})
		assert.Equal(t, code.MasterKeyEmpty, codeOf(t, err))
		_, err = f.svc.Rotate(f.ctx, &settingapi.RotateMasterKeyRequest{NewKey: oldMaster})
		assert.Equal(t, code.MasterKeySame, codeOf(t, err))
		got, err := f.plaintexts(t, f.old)
		require.NoError(t, err)
		assert.Equal(t, allPlain, got)
	})

	t.Run("有一项解不开时整体回滚，保持原状", func(t *testing.T) {
		f := setup(t)
		require.NoError(t, f.db.Model(&job_entity.Job{}).Where("1 = 1").Update("db_password", "broken").Error)
		_, err := f.svc.Rotate(f.ctx, &settingapi.RotateMasterKeyRequest{NewKey: newMaster})
		assert.Equal(t, code.MasterKeyRotateFailed, codeOf(t, err))
		var srv server_entity.Server
		require.NoError(t, f.db.First(&srv).Error)
		p, err := f.old.Decrypt(srv.Password)
		require.NoError(t, err, "已处理的服务器密码也回滚为旧密文")
		assert.Equal(t, srvPW, p)
		assert.Same(t, f.old, secret.Default(), "本进程仍用旧主密钥")
		assert.NoError(t, f.svc.Verify(f.ctx))
	})
}

func mustEncrypt(t *testing.T, s secret.Secret, v string) string {
	t.Helper()
	c, err := s.Encrypt(v)
	require.NoError(t, err)
	return c
}
