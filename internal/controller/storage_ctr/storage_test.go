package storage_ctr

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/cago-frame/cago/server/mux/muxclient"
	"github.com/cago-frame/cago/server/mux/muxtest"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authapi "github.com/opskat/opsnap/internal/api/auth"
	api "github.com/opskat/opsnap/internal/api/storage"
	tokenapi "github.com/opskat/opsnap/internal/api/token"
	"github.com/opskat/opsnap/internal/middleware"
	"github.com/opskat/opsnap/internal/model/entity/storage_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/kopiarepo"
	"github.com/opskat/opsnap/internal/pkg/testdb"
	"github.com/opskat/opsnap/internal/repository/admin_repo"
	"github.com/opskat/opsnap/internal/repository/session_repo"
	"github.com/opskat/opsnap/internal/repository/setting_repo"
	"github.com/opskat/opsnap/internal/repository/storage_repo"
	"github.com/opskat/opsnap/internal/repository/token_repo"
	"github.com/opskat/opsnap/internal/service/auth_svc"
	"github.com/opskat/opsnap/internal/service/secret_svc"
	"github.com/opskat/opsnap/internal/service/storage_svc"
	"github.com/opskat/opsnap/internal/service/token_svc"
)

const adminPassword = "correct-horse-battery"

const (
	keyA = "Abcd-Efgh-Ijkl-Mnop-Qrst-Uvwx"
	keyB = "Zyxw-Vuts-Rqpo-Nmlk-Jihg-Fedc"
)

type env struct {
	ctx       context.Context
	mux       *muxtest.TestMux
	token     string // API 令牌
	session   string // 浏览器会话
	sessionID int64
	dataDir   string
}

func setupStorageTest(t *testing.T) *env {
	ctx := testdb.New(t)
	setting_repo.RegisterSetting(setting_repo.NewSetting())
	admin_repo.RegisterAdmin(admin_repo.NewAdmin())
	session_repo.RegisterSession(session_repo.NewSession())
	token_repo.RegisterToken(token_repo.NewToken())
	storage_repo.RegisterStorage(storage_repo.NewStorage())
	_, err := secret_svc.Secret().Init(ctx, secret_svc.InitOptions{DataDir: t.TempDir()})
	require.NoError(t, err)
	dataDir := t.TempDir()
	storage_svc.SetDataDir(dataDir)

	setupCode, _ := auth_svc.Auth().PrepareSetupCode(ctx)
	_, issued, err := auth_svc.Auth().Setup(ctx, &authapi.SetupRequest{SetupCode: setupCode, Username: "admin", Password: adminPassword},
		auth_svc.ClientMeta{IP: "192.0.2.1"})
	require.NoError(t, err)
	p, _, err := auth_svc.Auth().AuthenticateSession(ctx, issued.Token)
	require.NoError(t, err)
	tok, err := token_svc.Token().Create(ctx, &tokenapi.CreateRequest{Name: "ci"})
	require.NoError(t, err)

	testMux := muxtest.NewTestMux(muxtest.WithBaseUrl("http://opsnap.test/api/v1"))
	ctr := NewStorage()
	authed := testMux.Group("/api/v1", middleware.SameOrigin()).Group("/", middleware.Auth())
	authed.Bind(ctr.List, ctr.Probe, ctr.Create, ctr.Update, ctr.Test, ctr.Unlock, ctr.Delete, ctr.Key)
	authed.Group("/", middleware.RequireSession()).Bind(ctr.Reveal, ctr.ListDirs, ctr.MakeDir)
	return &env{ctx: ctx, mux: testMux, token: tok.Token, session: issued.Token, sessionID: p.SessionID, dataDir: dataDir}
}

// do 用 API 令牌调用
func (e *env) do(req, resp any) error {
	return e.mux.Do(e.ctx, req, resp, muxclient.WithHeader(http.Header{"Authorization": {"Bearer " + e.token}}))
}

// browser 用浏览器会话调用
func (e *env) browser(req, resp any) error {
	return e.mux.Do(e.ctx, req, resp, muxclient.WithHeader(http.Header{"Cookie": {middleware.SessionCookie + "=" + e.session}}))
}

func errCode(err error) int {
	var he *httputils.Error
	if errors.As(err, &he) {
		return he.Code
	}
	return 0
}

func local(path string) api.Location {
	return api.Location{Kind: "local", Path: path}
}

func (e *env) create(t *testing.T, name, path, key string) *api.CreateResponse {
	t.Helper()
	resp := &api.CreateResponse{}
	require.NoError(t, e.do(&api.CreateRequest{Name: name, Location: local(path), Key: key, ConfirmSaved: true}, resp))
	return resp
}

func (e *env) list(t *testing.T) []*api.Item {
	t.Helper()
	resp := &api.ListResponse{}
	require.NoError(t, e.do(&api.ListRequest{}, resp))
	return resp.Items
}

func isRepo(path string) bool {
	_, err := os.Stat(filepath.Join(path, "kopia.repository.f"))
	return err == nil
}

func dirEntries(path string) int {
	entries, _ := os.ReadDir(path)
	return len(entries)
}

func TestKey(t *testing.T) {
	e := setupStorageTest(t)
	convey.Convey("生成密钥与计算指纹", t, func() {
		resp := &api.KeyResponse{}
		require.NoError(t, e.do(&api.KeyRequest{}, resp))
		assert.Regexp(t, `^[A-Za-z0-9]{4}(-[A-Za-z0-9]{4}){5}$`, resp.Key)
		assert.Equal(t, kopiarepo.Fingerprint(resp.Key), resp.Fingerprint)
		assert.Equal(t, "AES256-GCM-HMAC-SHA256", resp.Encryption)

		own := &api.KeyResponse{}
		require.NoError(t, e.do(&api.KeyRequest{Key: "my own password 1"}, own))
		assert.Equal(t, kopiarepo.Fingerprint("my own password 1"), own.Fingerprint)

		blank := &api.KeyResponse{}
		require.NoError(t, e.do(&api.KeyRequest{Key: "   "}, blank))
		assert.Regexp(t, `^[A-Za-z0-9]{4}(-[A-Za-z0-9]{4}){5}$`, blank.Key, "只含空白的自设密码按未填写处理，生成新密钥")
	})
}

func TestProbe(t *testing.T) {
	convey.Convey("测试连接", t, func() {
		e := setupStorageTest(t)

		convey.Convey("按目标位置区分为空、已是仓库、不为空", func() {
			resp := &api.ProbeResponse{}
			require.NoError(t, e.do(&api.ProbeRequest{Name: "a", Location: local(filepath.Join(t.TempDir(), "new"))}, resp))
			assert.Equal(t, "empty", resp.State)

			full := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(full, "x"), []byte("x"), 0o600))
			require.NoError(t, e.do(&api.ProbeRequest{Name: "a", Location: local(full)}, resp))
			assert.Equal(t, "not_empty", resp.State)

			repoDir := t.TempDir()
			require.NoError(t, kopiarepo.Create(e.ctx, kopiarepo.Location{Kind: kopiarepo.KindLocal, Path: repoDir}, keyA))
			resp = &api.ProbeResponse{}
			require.NoError(t, e.do(&api.ProbeRequest{Name: "a", Location: local(repoDir + "/")}, resp))
			assert.Equal(t, "repository", resp.State)
			assert.NotZero(t, resp.CreatedAt)
			assert.Equal(t, repoDir, resp.Location)
		})

		convey.Convey("字段校验", func() {
			cases := []struct {
				req  *api.ProbeRequest
				want int
			}{
				{&api.ProbeRequest{Name: " ", Location: local("/tmp/x")}, code.StorageNameInvalid},
				{&api.ProbeRequest{Name: strings.Repeat("名", 65), Location: local("/tmp/x")}, code.StorageNameInvalid},
				{&api.ProbeRequest{Name: "a", Location: local("data/x")}, code.StoragePathRelative},
				{&api.ProbeRequest{Name: "a", Location: api.Location{Kind: "s3", Endpoint: "minio:9000", Bucket: "b", AccessKey: "k"}}, code.StorageS3FieldRequired},
				{&api.ProbeRequest{Name: "a", Location: api.Location{Kind: "s3", Endpoint: "https://minio:9000", Bucket: "b", AccessKey: "k", SecretKey: "s"}}, code.StorageEndpointScheme},
			}
			for _, c := range cases {
				assert.Equal(t, c.want, errCode(e.do(c.req, &api.ProbeResponse{})), "%+v", c.req)
			}
		})

		convey.Convey("S3 无法连接时显示原因", func() {
			err := e.do(&api.ProbeRequest{Name: "a", Location: api.Location{Kind: "s3", Endpoint: "127.0.0.1:1", Bucket: "b", AccessKey: "k", SecretKey: "s"}}, &api.ProbeResponse{})
			assert.Equal(t, code.StorageUnreachable, errCode(err))
		})

		convey.Convey("名称与位置不能与已有存储重复", func() {
			dir := t.TempDir()
			e.create(t, "prod", dir, keyA)
			err := e.do(&api.ProbeRequest{Name: "prod", Location: local(t.TempDir())}, &api.ProbeResponse{})
			assert.Equal(t, code.StorageNameDuplicate, errCode(err))
			err = e.do(&api.ProbeRequest{Name: "other", Location: local(dir + "/./")}, &api.ProbeResponse{})
			assert.Equal(t, code.StorageLocationInUse, errCode(err))
			assert.Contains(t, err.Error(), "prod")
		})
	})
}

func TestCreate(t *testing.T) {
	convey.Convey("新建存储", t, func() {
		e := setupStorageTest(t)

		convey.Convey("空位置：未勾选确认或密码过短时不建库、不留记录", func() {
			dir := filepath.Join(t.TempDir(), "repo")
			err := e.do(&api.CreateRequest{Name: "a", Location: local(dir), Key: keyA}, &api.CreateResponse{})
			assert.Equal(t, code.StorageKeyNotConfirmed, errCode(err))
			err = e.do(&api.CreateRequest{Name: "a", Location: local(dir), Key: "short-pass", ConfirmSaved: true}, &api.CreateResponse{})
			assert.Equal(t, code.StorageKeyTooShort, errCode(err))
			err = e.do(&api.CreateRequest{Name: "a", Location: local(dir), ConfirmSaved: true}, &api.CreateResponse{})
			assert.Equal(t, code.StorageKeyRequired, errCode(err))
			assert.NoDirExists(t, dir)
			assert.Empty(t, e.list(t))
		})

		convey.Convey("空位置：用密钥建库并保存，状态正常，接口与数据库都不含明文密钥", func() {
			dir := filepath.Join(t.TempDir(), "repo")
			resp := e.create(t, "本地备份", dir, keyA)
			assert.True(t, isRepo(dir))
			assert.Equal(t, "本地备份", resp.Item.Name)
			assert.Equal(t, storage_entity.StatusOK, resp.Item.Status)
			assert.Equal(t, kopiarepo.Fingerprint(keyA), resp.Item.Fingerprint)
			assert.NotZero(t, resp.Item.CheckedAt)

			items := e.list(t)
			require.Len(t, items, 1)
			assert.Equal(t, dir, items[0].Location)

			var row storage_entity.Storage
			require.NoError(t, db.Ctx(e.ctx).First(&row).Error)
			assert.NotContains(t, row.RepoKey, keyA)
			assert.NotEmpty(t, row.RepoKey)
		})

		convey.Convey("不为空也不是仓库的位置被拒绝，目录保持原样", func() {
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "x"), []byte("x"), 0o600))
			err := e.do(&api.CreateRequest{Name: "a", Location: local(dir), Key: keyA, ConfirmSaved: true}, &api.CreateResponse{})
			assert.Equal(t, code.StorageLocationNotEmpty, errCode(err))
			assert.Equal(t, 1, dirEntries(dir))
			assert.Empty(t, e.list(t))
		})

		convey.Convey("删除后数据保留；重新添加同一位置需用正确密钥解锁", func() {
			dir := t.TempDir()
			resp := e.create(t, "a", dir, keyA)
			require.NoError(t, e.do(&api.DeleteRequest{ID: resp.Item.ID}, &api.DeleteResponse{}))
			assert.Empty(t, e.list(t))
			assert.True(t, isRepo(dir), "删除存储不删除仓库数据")

			err := e.do(&api.CreateRequest{Name: "a", Location: local(dir), Key: keyB}, &api.CreateResponse{})
			assert.Equal(t, code.StorageKeyInvalid, errCode(err))
			assert.Empty(t, e.list(t))

			again := &api.CreateResponse{}
			require.NoError(t, e.do(&api.CreateRequest{Name: "a", Location: local(dir), Key: "  " + keyA + "\n"}, again))
			assert.Equal(t, 0, again.Snapshots)
			assert.Equal(t, kopiarepo.Fingerprint(keyA), again.Item.Fingerprint)
		})

		convey.Convey("删除不存在的存储返回 404 码", func() {
			assert.Equal(t, code.StorageNotFound, errCode(e.do(&api.DeleteRequest{ID: 99}, &api.DeleteResponse{})))
		})
	})
}

func TestUpdate(t *testing.T) {
	convey.Convey("编辑存储", t, func() {
		e := setupStorageTest(t)
		dir := t.TempDir()
		id := e.create(t, "a", dir, keyA).Item.ID

		convey.Convey("位置不变时只改名称，复测后保存", func() {
			resp := &api.UpdateResponse{}
			require.NoError(t, e.do(&api.UpdateRequest{ID: id, Name: "renamed", Location: local(dir + "/")}, resp))
			assert.Equal(t, "renamed", resp.Item.Name)
			assert.Equal(t, "renamed", e.list(t)[0].Name)
		})

		convey.Convey("位置变化需要确认；确认后空位置用托管密钥新建仓库", func() {
			newDir := filepath.Join(t.TempDir(), "moved")
			err := e.do(&api.UpdateRequest{ID: id, Name: "a", Location: local(newDir)}, &api.UpdateResponse{})
			assert.Equal(t, code.StorageLocationChangeConfirm, errCode(err))
			assert.NoDirExists(t, newDir)

			probe := &api.ProbeResponse{}
			require.NoError(t, e.do(&api.ProbeRequest{ID: id, Name: "a", Location: local(newDir)}, probe))
			assert.True(t, probe.LocationChanged)

			resp := &api.UpdateResponse{}
			require.NoError(t, e.do(&api.UpdateRequest{ID: id, Name: "a", Location: local(newDir), ConfirmLocationChange: true}, resp))
			assert.True(t, isRepo(newDir))
			assert.Equal(t, newDir, resp.Item.Location)
			assert.Equal(t, kopiarepo.Fingerprint(keyA), resp.Item.Fingerprint)
			assert.True(t, isRepo(dir), "原位置的仓库保持不动")
		})

		convey.Convey("新位置已是仓库：需提供正确密钥，成功后改用它", func() {
			other := t.TempDir()
			require.NoError(t, kopiarepo.Create(e.ctx, kopiarepo.Location{Kind: kopiarepo.KindLocal, Path: other}, keyB))
			req := &api.UpdateRequest{ID: id, Name: "a", Location: local(other), ConfirmLocationChange: true}
			assert.Equal(t, code.StorageKeyRequired, errCode(e.do(req, &api.UpdateResponse{})))
			req.Key = keyA
			assert.Equal(t, code.StorageKeyInvalid, errCode(e.do(req, &api.UpdateResponse{})))
			assert.Equal(t, dir, e.list(t)[0].Location, "失败时存储保持原样")

			req.Key = keyB
			resp := &api.UpdateResponse{}
			require.NoError(t, e.do(req, resp))
			assert.Equal(t, kopiarepo.Fingerprint(keyB), resp.Item.Fingerprint)
		})

		convey.Convey("新位置不为空时拒绝，存储保持原样", func() {
			full := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(full, "x"), []byte("x"), 0o600))
			err := e.do(&api.UpdateRequest{ID: id, Name: "a", Location: local(full), ConfirmLocationChange: true}, &api.UpdateResponse{})
			assert.Equal(t, code.StorageLocationNotEmpty, errCode(err))
			assert.Equal(t, dir, e.list(t)[0].Location)
		})

		convey.Convey("仓库密码在 OpsNap 之外被换过：测试为密钥不正确，编辑被拒绝，重新解锁后恢复正常", func() {
			swapRepo(t, e.ctx, dir, keyB)

			test := &api.TestResponse{}
			require.NoError(t, e.do(&api.TestRequest{ID: id}, test))
			assert.Equal(t, storage_entity.StatusWrongKey, test.Item.Status)
			assert.NotEmpty(t, test.Item.StatusMessage)
			assert.Equal(t, storage_entity.StatusWrongKey, e.list(t)[0].Status, "测试结果写入列表")

			err := e.do(&api.UpdateRequest{ID: id, Name: "renamed", Location: local(dir)}, &api.UpdateResponse{})
			assert.Equal(t, code.StorageManagedKeyInvalid, errCode(err))

			assert.Equal(t, code.StorageKeyInvalid, errCode(e.do(&api.UnlockRequest{ID: id, Key: keyA}, &api.UnlockResponse{})))
			unlock := &api.UnlockResponse{}
			require.NoError(t, e.do(&api.UnlockRequest{ID: id, Key: keyB}, unlock))
			assert.Equal(t, storage_entity.StatusOK, unlock.Item.Status)
			assert.Equal(t, kopiarepo.Fingerprint(keyB), unlock.Item.Fingerprint)
		})

		convey.Convey("目录被移走后测试为无法连接", func() {
			require.NoError(t, os.RemoveAll(dir))
			test := &api.TestResponse{}
			require.NoError(t, e.do(&api.TestRequest{ID: id}, test))
			assert.Equal(t, storage_entity.StatusUnreachable, test.Item.Status)
			assert.NotEmpty(t, test.Item.StatusMessage)
		})
	})
}

// swapRepo 把 dir 换成用另一把密钥新建的仓库，模拟仓库密码在 OpsNap 之外被修改
func swapRepo(t *testing.T, ctx context.Context, dir, key string) {
	t.Helper()
	require.NoError(t, os.RemoveAll(dir))
	require.NoError(t, kopiarepo.Create(ctx, kopiarepo.Location{Kind: kopiarepo.KindLocal, Path: dir}, key))
}

func TestSessionOnly(t *testing.T) {
	convey.Convey("查看密钥与浏览目录只允许浏览器会话", t, func() {
		e := setupStorageTest(t)
		id := e.create(t, "a", t.TempDir(), keyA).Item.ID
		for _, req := range []any{
			&api.RevealRequest{ID: id, Password: adminPassword},
			&api.ListDirsRequest{Path: "/"},
			&api.MakeDirRequest{Parent: t.TempDir(), Name: "x"},
		} {
			err := e.do(req, &struct{}{})
			assert.Equal(t, code.SessionRequired, errCode(err), "%T", req)
		}
	})
}

func TestReveal(t *testing.T) {
	convey.Convey("查看密钥", t, func() {
		e := setupStorageTest(t)
		id := e.create(t, "a", t.TempDir(), keyA).Item.ID

		convey.Convey("密码登录开启时：密码错误被拒绝，正确时返回密钥与指纹", func() {
			err := e.browser(&api.RevealRequest{ID: id, Password: "wrong"}, &api.RevealResponse{})
			assert.Equal(t, code.ReauthPasswordWrong, errCode(err))

			resp := &api.RevealResponse{}
			require.NoError(t, e.browser(&api.RevealRequest{ID: id, Password: adminPassword}, resp))
			assert.Equal(t, keyA, resp.Key)
			assert.Equal(t, kopiarepo.Fingerprint(keyA), resp.Fingerprint)
		})

		convey.Convey("密码登录关闭时：需要本会话在 5 分钟内完成 OIDC 重新验证，每次查看用掉一次", func() {
			require.NoError(t, auth_svc.Auth().SetPasswordLoginEnabled(e.ctx, false))
			err := e.browser(&api.RevealRequest{ID: id, Password: adminPassword}, &api.RevealResponse{})
			assert.Equal(t, code.StorageReauthRequired, errCode(err))

			auth_svc.Auth().GrantReauth(e.sessionID)
			resp := &api.RevealResponse{}
			require.NoError(t, e.browser(&api.RevealRequest{ID: id}, resp))
			assert.Equal(t, keyA, resp.Key)

			err = e.browser(&api.RevealRequest{ID: id}, &api.RevealResponse{})
			assert.Equal(t, code.StorageReauthRequired, errCode(err))
		})

		convey.Convey("不存在的存储", func() {
			err := e.browser(&api.RevealRequest{ID: 99, Password: adminPassword}, &api.RevealResponse{})
			assert.Equal(t, code.StorageNotFound, errCode(err))
		})
	})
}

func TestDirs(t *testing.T) {
	convey.Convey("浏览本地目录", t, func() {
		e := setupStorageTest(t)

		convey.Convey("未指定或路径不存在时打开数据目录的上级目录", func() {
			for _, p := range []string{"", filepath.Join(e.dataDir, "missing", "x")} {
				resp := &api.ListDirsResponse{}
				require.NoError(t, e.browser(&api.ListDirsRequest{Path: p}, resp))
				assert.Equal(t, filepath.Dir(e.dataDir), resp.Path)
				assert.Equal(t, filepath.Dir(filepath.Dir(e.dataDir)), resp.Parent)
				names := make([]string, 0, len(resp.Dirs))
				for _, d := range resp.Dirs {
					names = append(names, d.Name)
				}
				assert.Contains(t, names, filepath.Base(e.dataDir))
			}
		})

		convey.Convey("列出子目录及状态；根目录没有上一级", func() {
			root := t.TempDir()
			require.NoError(t, os.Mkdir(filepath.Join(root, "empty"), 0o700))
			resp := &api.ListDirsResponse{}
			require.NoError(t, e.browser(&api.ListDirsRequest{Path: root}, resp))
			require.Len(t, resp.Dirs, 1)
			assert.Equal(t, "empty", resp.Dirs[0].Name)
			assert.Equal(t, filepath.Join(root, "empty"), resp.Dirs[0].Path)
			assert.Equal(t, "empty", resp.Dirs[0].Status)

			require.NoError(t, e.browser(&api.ListDirsRequest{Path: "/"}, resp))
			assert.Equal(t, "/", resp.Path)
			assert.Equal(t, "", resp.Parent)
		})

		convey.Convey("新建文件夹", func() {
			root := t.TempDir()
			resp := &api.MakeDirResponse{}
			require.NoError(t, e.browser(&api.MakeDirRequest{Parent: root, Name: "backups"}, resp))
			assert.Equal(t, filepath.Join(root, "backups"), resp.Path)
			assert.DirExists(t, resp.Path)

			err := e.browser(&api.MakeDirRequest{Parent: root, Name: "backups"}, &api.MakeDirResponse{})
			assert.Equal(t, code.StorageDirExists, errCode(err))
			err = e.browser(&api.MakeDirRequest{Parent: root, Name: "a/b"}, &api.MakeDirResponse{})
			assert.Equal(t, code.StorageDirNameInvalid, errCode(err))
			err = e.browser(&api.MakeDirRequest{Parent: "relative", Name: "x"}, &api.MakeDirResponse{})
			assert.Equal(t, code.StoragePathRelative, errCode(err))
		})
	})
}
