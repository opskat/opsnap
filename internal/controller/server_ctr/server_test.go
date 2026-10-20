package server_ctr

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/server/mux/muxtest"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	credentialapi "github.com/opskat/opsnap/internal/api/credential"
	serverapi "github.com/opskat/opsnap/internal/api/server"
	"github.com/opskat/opsnap/internal/model/entity/credential_entity"
	"github.com/opskat/opsnap/internal/pkg/secret"
	"github.com/opskat/opsnap/internal/pkg/sshconn/sshconntest"
	"github.com/opskat/opsnap/internal/repository/credential_repo"
	"github.com/opskat/opsnap/internal/repository/repo_repo"
	"github.com/opskat/opsnap/internal/repository/server_repo"
	"github.com/opskat/opsnap/internal/service/credential_svc"
	"github.com/opskat/opsnap/internal/service/server_svc"
	"github.com/opskat/opsnap/migrations"
)

func setupServerTest(t *testing.T) (context.Context, *muxtest.TestMux) {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, migrations.RunMigrations(gdb))
	s, err := secret.New("server-ctr-test-key")
	require.NoError(t, err)
	secret.SetDefault(s)
	t.Cleanup(func() { secret.SetDefault(nil) })

	server_repo.RegisterServer(server_repo.NewServer())
	credential_repo.RegisterCredential(credential_repo.NewCredential())
	repo_repo.RegisterRepo(repo_repo.NewRepo())
	server_svc.RegisterServer(server_svc.NewServer(time.Now))
	testMux := muxtest.NewTestMux()
	ctr := NewServer()
	testMux.Group("/api/v1").Bind(ctr.List, ctr.Get, ctr.Create, ctr.Update, ctr.Delete, ctr.Test, ctr.TestSaved)
	return db.WithContextDB(context.Background(), gdb), testMux
}

func TestServerController(t *testing.T) {
	ctx, testMux := setupServerTest(t)
	target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "s3cret-pw"})
	host, port := sshconntest.HostPort(t, target.Addr)
	in := serverapi.ServerInput{
		Name: "web", Host: host, Port: port, Username: "app",
		AuthType: serverapi.AuthPassword, Password: "s3cret-pw",
	}

	// 未核对指纹：测试连接只返回指纹
	probe := &serverapi.TestResponse{}
	require.NoError(t, testMux.Do(ctx, testReq(0, in), probe))
	assert.False(t, probe.OK)
	assert.Equal(t, target.Fingerprint, probe.Fingerprint)

	in.HostKeyFingerprint = probe.Fingerprint
	created := &serverapi.CreateResponse{}
	require.NoError(t, testMux.Do(ctx, createReq(in), created))
	require.NotZero(t, created.ID)
	require.NotNil(t, created.Credential)
	assert.Equal(t, credential_entity.KindPassword, created.Credential.Kind)
	assert.False(t, created.Credential.Shared, "直接填写默认不进密钥库")
	assert.True(t, created.Credential.HasSecret)

	in.Name, in.Password = "web-renamed", secret.Masked
	updated := &serverapi.UpdateResponse{}
	require.NoError(t, testMux.Do(ctx, updateReq(created.ID, in), updated))
	assert.Equal(t, "web-renamed", updated.Name)
	assert.Equal(t, created.ID, updated.ID)
	assert.Equal(t, created.Credential.ID, updated.Credential.ID, "编辑时复用同一条私有凭据")

	tested := &serverapi.TestResponse{}
	require.NoError(t, testMux.Do(ctx, &serverapi.TestSavedRequest{ID: created.ID}, tested))
	assert.True(t, tested.OK)

	list := &serverapi.ListResponse{}
	require.NoError(t, testMux.Do(ctx, &serverapi.ListRequest{}, list))
	require.Len(t, list.List, 1)
	assert.Equal(t, serverapi.StatusOK, list.List[0].Status)
	require.NotNil(t, list.List[0].Credential)
	assert.True(t, list.List[0].Credential.HasSecret)

	require.NoError(t, testMux.Do(ctx, &serverapi.DeleteRequest{ID: created.ID}, &serverapi.DeleteResponse{}))
	assert.Error(t, testMux.Do(ctx, &serverapi.GetRequest{ID: created.ID}, &serverapi.GetResponse{}))
}

func TestServerControllerSharedCredential(t *testing.T) {
	ctx, testMux := setupServerTest(t)
	target := sshconntest.StartSSH(t, sshconntest.SSHOptions{User: "app", Password: "SECRET-shared-pw"})
	host, port := sshconntest.HostPort(t, target.Addr)

	cred, err := credential_svc.Credential().Create(ctx, credentialapi.NewCreateRequest(&credentialapi.Input{
		Name: "生产 root", Kind: credential_entity.KindPassword, Username: "app", Password: "SECRET-shared-pw",
	}))
	require.NoError(t, err)

	in := serverapi.ServerInput{
		Name: "web", Host: host, Port: port, Username: "app",
		CredentialID: cred.ID, HostKeyFingerprint: target.Fingerprint,
	}
	created := &serverapi.CreateResponse{}
	require.NoError(t, testMux.Do(ctx, createReq(in), created))

	require.NotNil(t, created.Credential)
	assert.Equal(t, cred.ID, created.Credential.ID)
	assert.True(t, created.Credential.Shared)
	assert.Equal(t, "生产 root", created.Credential.Name)
	assert.Equal(t, credential_entity.KindPassword, created.Credential.Kind)

	// 测试连接用共享密钥里的凭据，不写库
	before := countRows(t, ctx, "credentials")
	tested := &serverapi.TestResponse{}
	require.NoError(t, testMux.Do(ctx, testReq(created.ID, in), tested))
	assert.True(t, tested.OK)
	assert.Equal(t, before, countRows(t, ctx, "credentials"), "测试连接不写凭据")

	// 引用一条不存在的密钥
	missing := in
	missing.CredentialID = 9999
	assert.Error(t, testMux.Do(ctx, createReq(missing), &serverapi.CreateResponse{}))

	// 响应里不含明文
	got := &serverapi.GetResponse{}
	require.NoError(t, testMux.Do(ctx, &serverapi.GetRequest{ID: created.ID}, got))
	raw, err := json.Marshal(got)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "SECRET-shared-pw")
	assert.NotContains(t, string(raw), "auth_type")
	assert.NotContains(t, string(raw), "private_key")
}

// countRows 直接数一张表的行数，用来断言「测试连接不写库」
func countRows(t *testing.T, ctx context.Context, table string) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Ctx(ctx).Table(table).Count(&n).Error)
	return n
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
