package credential_svc

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	credentialapi "github.com/opskat/opsnap/internal/api/credential"
	"github.com/opskat/opsnap/internal/model/entity/credential_entity"
	"github.com/opskat/opsnap/internal/model/entity/server_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/secret"
	"github.com/opskat/opsnap/internal/pkg/sshconn"
	"github.com/opskat/opsnap/internal/pkg/sshconn/sshconntest"
	"github.com/opskat/opsnap/internal/repository/credential_repo"
	"github.com/opskat/opsnap/migrations"
)

const (
	// 带标记的假值：断言它不出现在响应、错误文案和库里
	testPassword   = "SECRET-credential-password"
	testPassphrase = "SECRET-credential-passphrase"
)

type fixture struct {
	ctx context.Context
	db  *gorm.DB
	svc CredentialSvc
	now time.Time
}

func setupCredentialTest(t *testing.T) *fixture {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, migrations.RunMigrations(gdb))

	s, err := secret.New("credential-test-master-key")
	require.NoError(t, err)
	secret.SetDefault(s)
	t.Cleanup(func() { secret.SetDefault(nil) })

	credential_repo.RegisterCredential(credential_repo.NewCredential())
	f := &fixture{ctx: db.WithContextDB(context.Background(), gdb), db: gdb, now: time.Unix(1_800_000_000, 0)}
	f.svc = NewCredential(func() time.Time { return f.now })
	return f
}

// codeOf 取出业务错误码
func codeOf(t *testing.T, err error) int {
	t.Helper()
	var he *httputils.Error
	require.ErrorAs(t, err, &he)
	return he.Code
}

// raw 直接读库里保存的行
func (f *fixture) raw(t *testing.T, id int64) *credential_entity.Credential {
	t.Helper()
	got := &credential_entity.Credential{}
	require.NoError(t, f.db.Where("id = ?", id).First(got).Error)
	return got
}

// passwordInput 一条密码密钥
func passwordInput(name string) *credentialapi.Input {
	return &credentialapi.Input{
		Name: name, Kind: credentialapi.KindPassword, Password: testPassword,
		Username: "root", Note: "生产机的 root",
	}
}

// keyInput 一条私钥密钥
func keyInput(t *testing.T, name, passphrase string) *credentialapi.Input {
	t.Helper()
	key := sshconntest.GenerateKey(t, passphrase)
	return &credentialapi.Input{
		Name: name, Kind: credentialapi.KindSSHKey, PrivateKey: key.PrivatePEM, Passphrase: passphrase,
	}
}

// useByServer 建一台引用这条密钥的服务器，模拟「密钥被服务器使用」
func (f *fixture) useByServer(t *testing.T, name string, credentialID int64) *server_entity.Server {
	t.Helper()
	srv := &server_entity.Server{
		Name: name, Host: "10.0.0.1", Port: 22, Username: "root",
		CredentialID: credentialID, Hops: []server_entity.Hop{}, HostKeyFingerprint: "SHA256:test",
	}
	require.NoError(t, f.db.Create(srv).Error)
	return srv
}

func TestCredentialCreateGetListDelete(t *testing.T) {
	f := setupCredentialTest(t)

	created, err := f.svc.Create(f.ctx, credentialapi.NewCreateRequest(passwordInput("生产 root")))
	require.NoError(t, err)
	require.NotZero(t, created.ID)
	assert.Equal(t, "生产 root", created.Name)
	assert.Equal(t, credentialapi.KindPassword, created.Kind)
	assert.Equal(t, "root", created.Username)
	assert.Equal(t, "生产机的 root", created.Note)
	assert.True(t, created.HasSecret)
	assert.False(t, created.HasPassphrase)
	assert.Empty(t, created.Servers)
	assert.Equal(t, f.now.Unix(), created.CreatedAt)
	assert.Equal(t, f.now.Unix(), created.UpdatedAt)

	t.Run("读取一条", func(t *testing.T) {
		got, err := f.svc.Get(f.ctx, created.ID)
		require.NoError(t, err)
		assert.Equal(t, created.ID, got.ID)
		assert.Equal(t, "生产 root", got.Name)
		assert.True(t, got.HasSecret)
	})

	t.Run("不存在时报密钥不存在", func(t *testing.T) {
		_, err := f.svc.Get(f.ctx, 999)
		assert.Equal(t, code.CredentialNotFound, codeOf(t, err))
	})

	t.Run("列表按名称排序并聚合使用者", func(t *testing.T) {
		other := keyInput(t, "A 私钥", "key-pass")
		key, err := f.svc.Create(f.ctx, credentialapi.NewCreateRequest(other))
		require.NoError(t, err)
		assert.True(t, key.HasSecret)
		assert.True(t, key.HasPassphrase)

		f.useByServer(t, "web-1", created.ID)
		f.useByServer(t, "web-2", created.ID)

		list, err := f.svc.List(f.ctx)
		require.NoError(t, err)
		require.Len(t, list.List, 2)
		assert.Equal(t, "A 私钥", list.List[0].Name, "按名称排序")
		assert.Equal(t, "生产 root", list.List[1].Name)
		refs := list.List[1].Servers
		require.Len(t, refs, 2)
		assert.Equal(t, "web-1", refs[0].Name)
		require.NotZero(t, refs[0].ID)
		assert.Equal(t, "web-2", refs[1].Name)
	})

	t.Run("使用者的地址与用户一并返回，供抽屉里写「用户@地址」", func(t *testing.T) {
		// 换一条只被这台服务器使用的密钥，使用者列表才只有一条
		own := keyInput(t, "B 私钥", "")
		mine, err := f.svc.Create(f.ctx, credentialapi.NewCreateRequest(own))
		require.NoError(t, err)
		srv := f.useByServer(t, "web-03", mine.ID)
		srv.Host, srv.Port, srv.Username = "10.0.9.5", 2222, "ops"
		require.NoError(t, f.db.Save(srv).Error)

		got, err := f.svc.Get(f.ctx, mine.ID)
		require.NoError(t, err)
		require.Len(t, got.Servers, 1)
		ref := got.Servers[0]
		assert.Equal(t, srv.ID, ref.ID)
		assert.Equal(t, "web-03", ref.Name)
		assert.Equal(t, "10.0.9.5", ref.Host)
		assert.Equal(t, 2222, ref.Port)
		assert.Equal(t, "ops", ref.Username)

		// 地址与用户是明文的非敏感信息，不包含任何凭据内容
		raw, err := json.Marshal(got)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), testPassword)
	})

	t.Run("列表不返回私有凭据", func(t *testing.T) {
		private := &credential_entity.Credential{Kind: credential_entity.KindPassword, Password: "SECRET-private", Shared: false}
		_, err := f.svc.Create(f.ctx, credentialapi.NewCreateRequest(passwordInput("临时")))
		require.NoError(t, err)
		require.NoError(t, f.db.Create(private).Error)

		list, err := f.svc.List(f.ctx)
		require.NoError(t, err)
		for _, item := range list.List {
			assert.True(t, item.Name != "", "私有凭据的名字是空串，不出现在密钥列表里")
		}
		_, err = f.svc.Get(f.ctx, private.ID)
		assert.Equal(t, code.CredentialNotFound, codeOf(t, err), "私有凭据不能经密钥接口读取")
	})
}

func TestCredentialUpdate(t *testing.T) {
	f := setupCredentialTest(t)
	created, err := f.svc.Create(f.ctx, credentialapi.NewCreateRequest(passwordInput("生产 root")))
	require.NoError(t, err)
	before := f.raw(t, created.ID)

	f.now = f.now.Add(time.Hour)
	// 密码留空、名称与用户改了
	updated, err := f.svc.Update(f.ctx, credentialapi.NewUpdateRequest(created.ID, &credentialapi.Input{
		Name: "生产 root 新", Kind: credentialapi.KindPassword, Username: "admin", Note: "",
	}))
	require.NoError(t, err)
	assert.Equal(t, created.ID, updated.ID)
	assert.Equal(t, "生产 root 新", updated.Name)
	assert.Equal(t, "admin", updated.Username)
	assert.Empty(t, updated.Note)
	assert.True(t, updated.HasSecret)
	assert.Equal(t, f.now.Unix(), updated.UpdatedAt)
	assert.Equal(t, before.Createtime, updated.CreatedAt)

	// 留空保留原密文：库里还是原来那一份，能解回原文
	after := f.raw(t, created.ID)
	assert.Equal(t, before.Password, after.Password, "密码留空表示保留已保存的值")
	plain, err := secret.Default().Decrypt(after.Password)
	require.NoError(t, err)
	assert.Equal(t, testPassword, plain)

	t.Run("占位也表示保留", func(t *testing.T) {
		_, err := f.svc.Update(f.ctx, credentialapi.NewUpdateRequest(created.ID, &credentialapi.Input{
			Name: "生产 root 新", Kind: credentialapi.KindPassword, Password: secret.Masked,
		}))
		require.NoError(t, err)
		assert.Equal(t, before.Password, f.raw(t, created.ID).Password)
	})

	t.Run("填了新密码就换成新密文", func(t *testing.T) {
		const next = "SECRET-rotated-password"
		_, err := f.svc.Update(f.ctx, credentialapi.NewUpdateRequest(created.ID, &credentialapi.Input{
			Name: "生产 root 新", Kind: credentialapi.KindPassword, Password: next,
		}))
		require.NoError(t, err)
		got := f.raw(t, created.ID)
		assert.NotEqual(t, before.Password, got.Password)
		plain, err := secret.Default().Decrypt(got.Password)
		require.NoError(t, err)
		assert.Equal(t, next, plain)
	})

	t.Run("类型切到私钥时清掉密码", func(t *testing.T) {
		key := keyInput(t, "生产 root 新", testPassphrase)
		switched, err := f.svc.Update(f.ctx, credentialapi.NewUpdateRequest(created.ID, key))
		require.NoError(t, err)
		assert.Equal(t, credentialapi.KindSSHKey, switched.Kind)
		assert.True(t, switched.HasSecret)
		assert.True(t, switched.HasPassphrase)
		got := f.raw(t, created.ID)
		assert.Empty(t, got.Password, "类型切换时清掉另一侧")
		assert.NotEmpty(t, got.PrivateKey)
	})

	t.Run("不存在时报密钥不存在", func(t *testing.T) {
		_, err := f.svc.Update(f.ctx, credentialapi.NewUpdateRequest(999, passwordInput("x")))
		assert.Equal(t, code.CredentialNotFound, codeOf(t, err))
	})
}

func TestCredentialNameDuplicate(t *testing.T) {
	f := setupCredentialTest(t)
	first, err := f.svc.Create(f.ctx, credentialapi.NewCreateRequest(passwordInput("生产 root")))
	require.NoError(t, err)
	second, err := f.svc.Create(f.ctx, credentialapi.NewCreateRequest(passwordInput("备份 root")))
	require.NoError(t, err)

	t.Run("新增重名被拒", func(t *testing.T) {
		_, err := f.svc.Create(f.ctx, credentialapi.NewCreateRequest(passwordInput("生产 root")))
		require.Error(t, err)
		assert.Equal(t, code.CredentialNameDuplicate, codeOf(t, err))
		assert.Contains(t, err.Error(), "生产 root")
		assert.NotContains(t, err.Error(), "UNIQUE", "不暴露数据库索引错误")
	})

	t.Run("改成已有名称被拒", func(t *testing.T) {
		_, err := f.svc.Update(f.ctx, credentialapi.NewUpdateRequest(second.ID, passwordInput("生产 root")))
		require.Error(t, err)
		assert.Equal(t, code.CredentialNameDuplicate, codeOf(t, err))
		assert.NotContains(t, err.Error(), "UNIQUE")
	})

	t.Run("改名成自己的名称可以通过", func(t *testing.T) {
		_, err := f.svc.Update(f.ctx, credentialapi.NewUpdateRequest(first.ID, &credentialapi.Input{
			Name: "生产 root", Kind: credentialapi.KindPassword,
		}))
		require.NoError(t, err)
	})
}

func TestCredentialValidation(t *testing.T) {
	f := setupCredentialTest(t)

	t.Run("名称必填", func(t *testing.T) {
		in := passwordInput("")
		_, err := f.svc.Create(f.ctx, credentialapi.NewCreateRequest(in))
		assert.Equal(t, code.CredentialFieldInvalid, codeOf(t, err))
	})

	t.Run("名称超长", func(t *testing.T) {
		in := passwordInput(strings.Repeat("名", 65))
		_, err := f.svc.Create(f.ctx, credentialapi.NewCreateRequest(in))
		assert.Equal(t, code.CredentialFieldInvalid, codeOf(t, err))
	})

	t.Run("类型不合法", func(t *testing.T) {
		in := passwordInput("x")
		in.Kind = "token"
		_, err := f.svc.Create(f.ctx, credentialapi.NewCreateRequest(in))
		assert.Equal(t, code.CredentialKindInvalid, codeOf(t, err))
	})

	t.Run("密码类型缺密码", func(t *testing.T) {
		in := passwordInput("缺密码")
		in.Password = ""
		_, err := f.svc.Create(f.ctx, credentialapi.NewCreateRequest(in))
		require.Error(t, err)
		assert.Equal(t, code.CredentialEmpty, codeOf(t, err))
		assert.Contains(t, err.Error(), "密码")
	})

	t.Run("私钥类型缺私钥", func(t *testing.T) {
		in := keyInput(t, "缺私钥", "")
		in.PrivateKey = ""
		_, err := f.svc.Create(f.ctx, credentialapi.NewCreateRequest(in))
		require.Error(t, err)
		assert.Equal(t, code.CredentialEmpty, codeOf(t, err))
		assert.Contains(t, err.Error(), "私钥")
	})

	t.Run("私钥无法解析", func(t *testing.T) {
		in := keyInput(t, "坏私钥", "")
		in.PrivateKey = "not a key"
		_, err := f.svc.Create(f.ctx, credentialapi.NewCreateRequest(in))
		require.Error(t, err)
		assert.Equal(t, code.CredentialPrivateKeyInvalid, codeOf(t, err))
		assert.NotContains(t, err.Error(), "not a key", "错误文案不带私钥内容")
	})

	t.Run("私钥口令错误", func(t *testing.T) {
		in := keyInput(t, "口令错", "right-pass")
		in.Passphrase = "wrong-pass"
		_, err := f.svc.Create(f.ctx, credentialapi.NewCreateRequest(in))
		require.Error(t, err)
		assert.Equal(t, code.CredentialPrivateKeyInvalid, codeOf(t, err))
		assert.NotContains(t, err.Error(), "wrong-pass")
	})

	t.Run("校验失败不落库", func(t *testing.T) {
		list, err := f.svc.List(f.ctx)
		require.NoError(t, err)
		assert.Empty(t, list.List, "上面每一条都被拒绝，一条也没有写进去")
	})
}

func TestCredentialEncryption(t *testing.T) {
	f := setupCredentialTest(t)
	withPass := "key-pass"
	in := keyInput(t, "生产私钥", withPass)
	created, err := f.svc.Create(f.ctx, credentialapi.NewCreateRequest(in))
	require.NoError(t, err)

	got := f.raw(t, created.ID)
	assert.NotEqual(t, in.PrivateKey, got.PrivateKey, "落库的是密文")
	assert.NotEqual(t, withPass, got.Passphrase, "口令落库也是密文")
	key, err := secret.Default().Decrypt(got.PrivateKey)
	require.NoError(t, err)
	assert.Equal(t, in.PrivateKey, key)
	pass, err := secret.Default().Decrypt(got.Passphrase)
	require.NoError(t, err)
	assert.Equal(t, withPass, pass)
	_, err = sshconn.ParsePrivateKey(key, pass)
	require.NoError(t, err, "解回来的私钥能用口令解析")

	t.Run("库里查不到明文", func(t *testing.T) {
		var dump string
		require.NoError(t, f.db.Raw(`SELECT id || '|' || name || '|' || kind || '|' || password || '|' ||
			private_key || '|' || passphrase || '|' || username || '|' || note FROM credentials`).Scan(&dump).Error)
		require.NotEmpty(t, dump)
		assert.NotContains(t, dump, testPassword)
		assert.NotContains(t, dump, withPass)
		assert.NotContains(t, dump, strings.Split(in.PrivateKey, "\n")[2], "私钥正文不在库里")
	})
}

func TestCredentialResponseHasNoSecret(t *testing.T) {
	f := setupCredentialTest(t)
	keyIn := keyInput(t, "生产私钥", testPassphrase)
	created, err := f.svc.Create(f.ctx, credentialapi.NewCreateRequest(keyIn))
	require.NoError(t, err)
	got, err := f.svc.Get(f.ctx, created.ID)
	require.NoError(t, err)

	for name, payload := range map[string]any{"Item": got.Item, "Create": created.Item} {
		raw, err := json.Marshal(payload)
		require.NoError(t, err)
		body := string(raw)
		assert.NotContains(t, body, `"password"`, name)
		assert.NotContains(t, body, `"private_key"`, name)
		assert.NotContains(t, body, `"passphrase"`, name)
		assert.NotContains(t, body, testPassphrase, name)
		assert.NotContains(t, body, strings.Split(keyIn.PrivateKey, "\n")[2], name)
		assert.Contains(t, body, `"has_secret":true`, name)
		assert.Contains(t, body, `"has_passphrase":true`, name)
	}
}

func TestCredentialDeleteInUse(t *testing.T) {
	f := setupCredentialTest(t)
	created, err := f.svc.Create(f.ctx, credentialapi.NewCreateRequest(passwordInput("生产 root")))
	require.NoError(t, err)

	f.useByServer(t, "web-1", created.ID)
	f.useByServer(t, "db-1", created.ID)

	err = f.svc.Delete(f.ctx, created.ID)
	require.Error(t, err)
	assert.Equal(t, code.CredentialInUse, codeOf(t, err))
	assert.Contains(t, err.Error(), "web-1")
	assert.Contains(t, err.Error(), "db-1")
	assert.NotZero(t, f.raw(t, created.ID).ID, "被使用时删除被拒，行还在")

	require.NoError(t, f.db.Where("credential_id = ?", created.ID).Delete(&server_entity.Server{}).Error)
	require.NoError(t, f.svc.Delete(f.ctx, created.ID))
	_, err = f.svc.Get(f.ctx, created.ID)
	assert.Equal(t, code.CredentialNotFound, codeOf(t, err))
}

func TestCredentialListEmpty(t *testing.T) {
	f := setupCredentialTest(t)
	list, err := f.svc.List(f.ctx)
	require.NoError(t, err)
	assert.NotNil(t, list.List, "没有密钥时返回空数组而不是 null")
	assert.Empty(t, list.List)
}
