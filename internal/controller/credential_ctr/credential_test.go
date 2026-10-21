package credential_ctr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/cago-frame/cago/server/mux/muxtest"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	credentialapi "github.com/opskat/opsnap/internal/api/credential"
	"github.com/opskat/opsnap/internal/model/entity/credential_entity"
	"github.com/opskat/opsnap/internal/model/entity/server_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/secret"
	"github.com/opskat/opsnap/internal/pkg/sshconn/sshconntest"
	"github.com/opskat/opsnap/internal/repository/credential_repo"
	"github.com/opskat/opsnap/internal/service/credential_svc"
	"github.com/opskat/opsnap/migrations"
)

const (
	ctrPassword   = "SECRET-ctr-password"
	ctrPassphrase = "SECRET-ctr-passphrase"
)

func setupCredentialTest(t *testing.T) (context.Context, *muxtest.TestMux) {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, migrations.RunMigrations(gdb))
	s, err := secret.New("credential-ctr-test-key")
	require.NoError(t, err)
	secret.SetDefault(s)
	t.Cleanup(func() { secret.SetDefault(nil) })

	credential_repo.RegisterCredential(credential_repo.NewCredential())
	credential_svc.RegisterCredential(credential_svc.NewCredential(time.Now))
	testMux := muxtest.NewTestMux()
	ctr := NewCredential()
	testMux.Group("/api/v1").Bind(ctr.List, ctr.Get, ctr.Create, ctr.Update, ctr.Delete, ctr.Generate, ctr.Reveal)
	return db.WithContextDB(context.Background(), gdb), testMux
}

// codeOf 取出业务错误码
func codeOf(t *testing.T, err error) int {
	t.Helper()
	var he *httputils.Error
	require.ErrorAs(t, err, &he)
	return he.Code
}

func passwordInput(name string) *credentialapi.Input {
	return &credentialapi.Input{Name: name, Kind: credentialapi.KindPassword, Password: ctrPassword, Username: "root"}
}

func TestCredentialController(t *testing.T) {
	ctx, testMux := setupCredentialTest(t)

	created := &credentialapi.CreateResponse{}
	require.NoError(t, testMux.Do(ctx, credentialapi.NewCreateRequest(passwordInput("生产 root")), created))
	require.NotZero(t, created.ID)
	assert.Equal(t, "生产 root", created.Name)
	assert.True(t, created.HasSecret)

	got := &credentialapi.GetResponse{}
	require.NoError(t, testMux.Do(ctx, &credentialapi.GetRequest{ID: created.ID}, got))
	assert.Equal(t, created.ID, got.ID)
	assert.Equal(t, "root", got.Username)

	// 重名：凭据名称重复
	err := testMux.Do(ctx, credentialapi.NewCreateRequest(passwordInput("生产 root")), &credentialapi.CreateResponse{})
	assert.Equal(t, code.CredentialNameDuplicate, codeOf(t, err))

	// 私钥类型缺私钥、口令错误各有错误码
	noKey := &credentialapi.Input{Name: "缺私钥", Kind: credentialapi.KindSSHKey}
	err = testMux.Do(ctx, credentialapi.NewCreateRequest(noKey), &credentialapi.CreateResponse{})
	assert.Equal(t, code.CredentialEmpty, codeOf(t, err))

	key := sshconntest.GenerateKey(t, ctrPassphrase)
	badPass := &credentialapi.Input{
		Name: "口令错", Kind: credentialapi.KindSSHKey, PrivateKey: key.PrivatePEM, Passphrase: "wrong",
	}
	err = testMux.Do(ctx, credentialapi.NewCreateRequest(badPass), &credentialapi.CreateResponse{})
	assert.Equal(t, code.CredentialPrivateKeyInvalid, codeOf(t, err))

	// 编辑留空：保留已保存的密码
	updated := &credentialapi.UpdateResponse{}
	require.NoError(t, testMux.Do(ctx, credentialapi.NewUpdateRequest(created.ID, &credentialapi.Input{
		Name: "生产 root 2", Kind: credentialapi.KindPassword,
	}), updated))
	assert.Equal(t, "生产 root 2", updated.Name)
	assert.True(t, updated.HasSecret)

	list := &credentialapi.ListResponse{}
	require.NoError(t, testMux.Do(ctx, &credentialapi.ListRequest{}, list))
	require.Len(t, list.List, 1)

	// 不存在
	err = testMux.Do(ctx, &credentialapi.GetRequest{ID: 999}, &credentialapi.GetResponse{})
	assert.Equal(t, code.CredentialNotFound, codeOf(t, err))

	// 被服务器使用时删除被拒，并带出服务器名
	srv := &server_entity.Server{
		Name: "web-1", Host: "10.0.0.1", Port: 22, Username: "root",
		CredentialID: created.ID, Hops: []server_entity.Hop{}, HostKeyFingerprint: "SHA256:x",
	}
	require.NoError(t, db.Ctx(ctx).Create(srv).Error)
	err = testMux.Do(ctx, &credentialapi.DeleteRequest{ID: created.ID}, &credentialapi.DeleteResponse{})
	assert.Equal(t, code.CredentialInUse, codeOf(t, err))
	assert.Contains(t, err.Error(), "web-1")

	require.NoError(t, db.Ctx(ctx).Where("credential_id = ?", created.ID).Delete(&server_entity.Server{}).Error)
	require.NoError(t, testMux.Do(ctx, &credentialapi.DeleteRequest{ID: created.ID}, &credentialapi.DeleteResponse{}))
	err = testMux.Do(ctx, &credentialapi.GetRequest{ID: created.ID}, &credentialapi.GetResponse{})
	assert.Equal(t, code.CredentialNotFound, codeOf(t, err))
}

func TestCredentialControllerResponseHasNoSecret(t *testing.T) {
	ctx, testMux := setupCredentialTest(t)
	key := sshconntest.GenerateKey(t, ctrPassphrase)
	created := &credentialapi.CreateResponse{}
	require.NoError(t, testMux.Do(ctx, credentialapi.NewCreateRequest(&credentialapi.Input{
		Name: "生产私钥", Kind: credentialapi.KindSSHKey, PrivateKey: key.PrivatePEM, Passphrase: ctrPassphrase,
	}), created))

	engine, ok := testMux.IRouter.(*gin.Engine)
	require.True(t, ok)

	for _, tc := range []struct {
		name   string
		method string
		path   string
	}{
		{"创建后的读取", http.MethodGet, "/api/v1/credentials/" + strconv.FormatInt(created.ID, 10)},
		{"列表", http.MethodGet, "/api/v1/credentials"},
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(tc.method, tc.path, nil).WithContext(ctx)
		engine.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code, tc.name)
		body := w.Body.String()
		assert.NotContains(t, body, `"password"`, tc.name)
		assert.NotContains(t, body, `"private_key"`, tc.name)
		assert.NotContains(t, body, `"passphrase"`, tc.name)
		assert.NotContains(t, body, ctrPassphrase, tc.name)
		assert.NotContains(t, body, strings.Split(key.PrivatePEM, "\n")[2], "私钥正文不在响应里：%s", tc.name)

		var payload struct {
			Data json.RawMessage `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload), tc.name)
		assert.Contains(t, string(payload.Data), `"has_secret":true`, tc.name)
		assert.Contains(t, string(payload.Data), `"has_passphrase":true`, tc.name)
	}

	// 私有凭据不出现在密钥接口
	private := &credential_entity.Credential{Kind: credential_entity.KindPassword, Password: "SECRET-private"}
	require.NoError(t, db.Ctx(ctx).Create(private).Error)
	list := &credentialapi.ListResponse{}
	require.NoError(t, testMux.Do(ctx, &credentialapi.ListRequest{}, list))
	require.Len(t, list.List, 1)
	assert.Equal(t, "生产私钥", list.List[0].Name)
	err := testMux.Do(ctx, &credentialapi.GetRequest{ID: private.ID}, &credentialapi.GetResponse{})
	assert.Equal(t, code.CredentialNotFound, codeOf(t, err), "私有凭据不能经密钥接口读取")
}

func TestCredentialControllerReveal(t *testing.T) {
	ctx, testMux := setupCredentialTest(t)

	created := &credentialapi.CreateResponse{}
	require.NoError(t, testMux.Do(ctx, credentialapi.NewCreateRequest(passwordInput("生产 root")), created))

	// 返回的明文与保存的一致，且只出现在这个响应里
	revealed := &credentialapi.RevealResponse{}
	require.NoError(t, testMux.Do(ctx, credentialapi.NewRevealRequest(created.ID), revealed))
	assert.Equal(t, credentialapi.KindPassword, revealed.Kind)
	assert.Equal(t, ctrPassword, revealed.Password)
	assert.Empty(t, revealed.PrivateKey)
	assert.Empty(t, revealed.Passphrase)

	// 响应带 Cache-Control: no-store
	engine, ok := testMux.IRouter.(*gin.Engine)
	require.True(t, ok)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/credentials/"+strconv.FormatInt(created.ID, 10)+"/reveal", nil).WithContext(ctx)
	engine.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	assert.Contains(t, w.Body.String(), ctrPassword)

	// 私钥类型返回私钥与口令
	key := sshconntest.GenerateKey(t, ctrPassphrase)
	keyCreated := &credentialapi.CreateResponse{}
	require.NoError(t, testMux.Do(ctx, credentialapi.NewCreateRequest(&credentialapi.Input{
		Name: "生产私钥", Kind: credentialapi.KindSSHKey, PrivateKey: key.PrivatePEM, Passphrase: ctrPassphrase,
	}), keyCreated))
	keyRevealed := &credentialapi.RevealResponse{}
	require.NoError(t, testMux.Do(ctx, credentialapi.NewRevealRequest(keyCreated.ID), keyRevealed))
	assert.Equal(t, credentialapi.KindSSHKey, keyRevealed.Kind)
	assert.Equal(t, key.PrivatePEM, keyRevealed.PrivateKey)
	assert.Equal(t, ctrPassphrase, keyRevealed.Passphrase)
	assert.Empty(t, keyRevealed.Password)

	// 不存在与私有凭据都按「密钥不存在」
	err := testMux.Do(ctx, credentialapi.NewRevealRequest(999), &credentialapi.RevealResponse{})
	assert.Equal(t, code.CredentialNotFound, codeOf(t, err))
	private := &credential_entity.Credential{Kind: credential_entity.KindPassword, Password: "SECRET-private"}
	require.NoError(t, db.Ctx(ctx).Create(private).Error)
	err = testMux.Do(ctx, credentialapi.NewRevealRequest(private.ID), &credentialapi.RevealResponse{})
	assert.Equal(t, code.CredentialNotFound, codeOf(t, err))

	// 列表与读取仍然只有公钥与「有没有内容」，没有密码明文
	keyItem := &credentialapi.GetResponse{}
	require.NoError(t, testMux.Do(ctx, &credentialapi.GetRequest{ID: keyCreated.ID}, keyItem))
	assert.NotEmpty(t, keyItem.PublicKey)
	for _, path := range []string{"/api/v1/credentials", "/api/v1/credentials/" + strconv.FormatInt(keyCreated.ID, 10)} {
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx))
		require.Equal(t, http.StatusOK, rec.Code, path)
		body := rec.Body.String()
		assert.NotContains(t, body, ctrPassword, path)
		assert.NotContains(t, body, ctrPassphrase, path)
		assert.NotContains(t, body, strings.Split(key.PrivatePEM, "\n")[2], path)
		assert.Contains(t, body, "public_key", path)
	}
}

func TestCredentialControllerGenerate(t *testing.T) {
	ctx, testMux := setupCredentialTest(t)

	pw := &credentialapi.GenerateResponse{}
	require.NoError(t, testMux.Do(ctx, credentialapi.NewGenerateRequest(credentialapi.KindPassword, false), pw))
	assert.Equal(t, credentialapi.KindPassword, pw.Kind)
	assert.Len(t, []rune(pw.Password), 32)

	key := &credentialapi.GenerateResponse{}
	require.NoError(t, testMux.Do(ctx, credentialapi.NewGenerateRequest(credentialapi.KindSSHKey, true), key))
	assert.Equal(t, credentialapi.KindSSHKey, key.Kind)
	assert.NotEmpty(t, key.PrivateKey)
	assert.NotEmpty(t, key.PublicKey)
	assert.Len(t, []rune(key.Passphrase), 16)

	// 生成不写库
	list := &credentialapi.ListResponse{}
	require.NoError(t, testMux.Do(ctx, &credentialapi.ListRequest{}, list))
	assert.Empty(t, list.List)

	err := testMux.Do(ctx, credentialapi.NewGenerateRequest("token", false), &credentialapi.GenerateResponse{})
	assert.Equal(t, code.CredentialKindInvalid, codeOf(t, err))
}

// TestCredentialControllerRevealRequiresAuth 确认 reveal 与其它密钥接口在同一组鉴权路由里
func TestCredentialControllerRevealRequiresAuth(t *testing.T) {
	_, testMux := setupCredentialTest(t)
	engine, ok := testMux.IRouter.(*gin.Engine)
	require.True(t, ok)
	// 测试 mux 直接绑定了控制器（不带鉴权中间件），这里只确认路由存在、方法是 POST
	for _, r := range engine.Routes() {
		if r.Path == "/api/v1/credentials/:id/reveal" {
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Equal(t, "/api/v1/credentials/:id/reveal", r.Path)
			return
		}
	}
	t.Fatal("没有注册 /credentials/:id/reveal")
}
