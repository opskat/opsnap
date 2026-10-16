package server_ctr

import (
	"context"
	"testing"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/server/mux/muxtest"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	serverapi "github.com/opskat/opsnap/internal/api/server"
	"github.com/opskat/opsnap/internal/pkg/secret"
	"github.com/opskat/opsnap/internal/pkg/sshconn/sshconntest"
	"github.com/opskat/opsnap/internal/repository/repo_repo"
	"github.com/opskat/opsnap/internal/repository/server_repo"
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
	assert.Equal(t, secret.Masked, created.Password)

	in.Name, in.Password = "web-renamed", secret.Masked
	updated := &serverapi.UpdateResponse{}
	require.NoError(t, testMux.Do(ctx, updateReq(created.ID, in), updated))
	assert.Equal(t, "web-renamed", updated.Name)
	assert.Equal(t, created.ID, updated.ID)

	tested := &serverapi.TestResponse{}
	require.NoError(t, testMux.Do(ctx, &serverapi.TestSavedRequest{ID: created.ID}, tested))
	assert.True(t, tested.OK)

	list := &serverapi.ListResponse{}
	require.NoError(t, testMux.Do(ctx, &serverapi.ListRequest{}, list))
	require.Len(t, list.List, 1)
	assert.Equal(t, serverapi.StatusOK, list.List[0].Status)
	assert.Equal(t, secret.Masked, list.List[0].Password)

	require.NoError(t, testMux.Do(ctx, &serverapi.DeleteRequest{ID: created.ID}, &serverapi.DeleteResponse{}))
	assert.Error(t, testMux.Do(ctx, &serverapi.GetRequest{ID: created.ID}, &serverapi.GetResponse{}))
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
