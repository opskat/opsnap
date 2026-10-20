package repo_svc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	repoapi "github.com/opskat/opsnap/internal/api/repo"
	"github.com/opskat/opsnap/internal/model/entity/repo_entity"
	"github.com/opskat/opsnap/internal/pkg/cmdbuild"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/executor"
	"github.com/opskat/opsnap/internal/pkg/repolock"
	"github.com/opskat/opsnap/internal/pkg/secret"
	"github.com/opskat/opsnap/internal/repository/credential_repo"
	"github.com/opskat/opsnap/internal/repository/repo_repo"
	"github.com/opskat/opsnap/internal/repository/server_repo"
	"github.com/opskat/opsnap/migrations"
)

type fixture struct {
	ctx  context.Context
	db   *gorm.DB
	fake *executor.Fake
	svc  *repoSvc
	now  time.Time
}

func setupRepoTest(t *testing.T) *fixture {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, migrations.RunMigrations(gdb))

	s, err := secret.New("repo-test-master-key")
	require.NoError(t, err)
	secret.SetDefault(s)
	t.Cleanup(func() { secret.SetDefault(nil) })

	// 后台维护不带请求的 ctx，经默认数据库访问
	db.SetDefault(gdb)
	repo_repo.RegisterRepo(repo_repo.NewRepo())
	server_repo.RegisterServer(server_repo.NewServer())
	credential_repo.RegisterCredential(credential_repo.NewCredential())
	RegisterJobCounter(nil)
	t.Cleanup(func() { RegisterJobCounter(nil) })
	f := &fixture{
		ctx:  db.WithContextDB(context.Background(), gdb),
		db:   gdb,
		fake: executor.NewFake(),
		now:  time.Unix(1_800_000_000, 0),
	}
	f.svc = NewRepo(f.fake, func() time.Time { return f.now }).(*repoSvc)
	t.Cleanup(f.svc.wait)
	return f
}

// codeOf 取出业务错误码
func codeOf(t *testing.T, err error) int {
	t.Helper()
	var he *httputils.Error
	require.ErrorAs(t, err, &he, "%v", err)
	return he.Code
}

// passwordOf 取出命令的 RESTIC_PASSWORD
func passwordOf(c cmdbuild.Cmd) string {
	for _, e := range c.Env {
		if v, ok := strings.CutPrefix(e, "RESTIC_PASSWORD="); ok {
			return v
		}
	}
	return ""
}

// restic 匹配 restic 子命令
func restic(sub ...string) func(cmdbuild.Cmd) bool { return executor.Match("restic", sub...) }

// resticWith 匹配用指定密码执行的 restic 子命令
func resticWith(password string, sub ...string) func(cmdbuild.Cmd) bool {
	m := restic(sub...)
	return func(c cmdbuild.Cmd) bool { return m(c) && passwordOf(c) == password }
}

// resticWithout 匹配不是用指定密码执行的 restic 子命令
func resticWithout(password string, sub ...string) func(cmdbuild.Cmd) bool {
	m := restic(sub...)
	return func(c cmdbuild.Cmd) bool { return m(c) && passwordOf(c) != password }
}

// keyList 生成 restic key list --json 的输出，current 是当前密码对应的密钥
func keyList(current string, ids ...string) executor.Step {
	type k struct {
		Current bool   `json:"current"`
		ID      string `json:"id"`
	}
	list := make([]k, 0, len(ids))
	for _, id := range ids {
		list = append(list, k{Current: id == current, ID: id})
	}
	b, _ := json.Marshal(list)
	return executor.Step{Output: string(b)}
}

// callsOf 筛选出匹配的调用
func callsOf(f *executor.Fake, m func(cmdbuild.Cmd) bool) []cmdbuild.Cmd {
	var out []cmdbuild.Cmd
	for _, c := range f.Calls() {
		if m(c) {
			out = append(out, c)
		}
	}
	return out
}

func yes() *bool { b := true; return &b }
func no() *bool  { b := false; return &b }

// testRecoveryKey 管理员在保存前看到并存进密码管理器的恢复密钥
const testRecoveryKey = "RECOV-ERYKE-YABCD-EFGHI-JKLMN-OPQRS-TUVWX-YZ234"

func localReq(dir string) *repoapi.CreateRequest {
	return &repoapi.CreateRequest{
		Name: "飞牛 NAS", Type: repoapi.TypeLocal,
		Params:           repoapi.Params{Path: dir},
		RecoveryKey:      yes(),
		RecoveryKeyValue: testRecoveryKey,
		RecoveryKeySaved: true,
	}
}

// raw 直接读库里保存的行
func (f *fixture) raw(t *testing.T, id int64) *repo_entity.Repo {
	t.Helper()
	got := &repo_entity.Repo{}
	require.NoError(t, f.db.Where("id = ?", id).First(got).Error)
	return got
}

// rawRow 以字符串形式读出整行，用于断言某个值没有被存下来
func (f *fixture) rawRow(t *testing.T, id int64) string {
	t.Helper()
	row := map[string]any{}
	require.NoError(t, f.db.Table("repos").Where("id = ?", id).Take(&row).Error)
	b, err := json.Marshal(row)
	require.NoError(t, err)
	return string(b)
}

func (f *fixture) opsnapKey(t *testing.T, id int64) string {
	t.Helper()
	k, err := secret.Default().Decrypt(f.raw(t, id).OpsnapKey)
	require.NoError(t, err)
	return k
}

// createEmptyLocal 在空目录上建一个本地仓库：opsnap 密钥 op1，恢复密钥 rc1（如果生成）
func (f *fixture) createEmptyLocal(t *testing.T, dir string, recovery bool) *repoapi.CreateResponse {
	t.Helper()
	f.fake.On(restic("snapshots"), executor.Step{ExitCode: 10, Output: "Fatal: repository does not exist"})
	f.fake.On(restic("init"))
	f.fake.On(restic("key", "add"))
	f.fake.On(restic("key", "list"), keyList("op1", "op1"), keyList("op1", "op1"), keyList("rc1", "op1", "rc1"))
	req := localReq(dir)
	if !recovery {
		req.RecoveryKey, req.RecoveryKeyValue, req.RecoveryKeySaved = no(), "", false
	}
	resp, err := f.svc.Create(f.ctx, req)
	require.NoError(t, err)
	return resp
}

func TestCreateInitializesEmptyLocation(t *testing.T) {
	f := setupRepoTest(t)
	dir := t.TempDir()
	resp := f.createEmptyLocal(t, dir, true)

	inits := callsOf(f.fake, restic("init"))
	require.Len(t, inits, 1, "空位置初始化新仓库")
	opsnapKey := passwordOf(inits[0])
	require.NotEmpty(t, opsnapKey)
	assert.Equal(t, opsnapKey, f.opsnapKey(t, resp.ID), "opsnap 密钥用主密钥加密后存入 SQLite")
	assert.NotContains(t, f.raw(t, resp.ID).OpsnapKey, opsnapKey)
	assert.Equal(t, "op1", f.raw(t, resp.ID).OpsnapKeyID)

	// 恢复密钥：保存前已展示给管理员，作为第二把 restic 密钥加入，不保存、不回传
	adds := callsOf(f.fake, restic("key", "add"))
	require.Len(t, adds, 1)
	assert.Equal(t, testRecoveryKey, string(adds[0].Stdin))
	assert.Equal(t, opsnapKey, passwordOf(adds[0]))
	assert.NotContains(t, f.rawRow(t, resp.ID), testRecoveryKey)
	assert.NotContains(t, mustJSON(t, resp), testRecoveryKey, "创建响应不再回传恢复密钥")
	assert.Equal(t, "rc1", f.raw(t, resp.ID).RecoveryKeyID)
	assert.False(t, resp.NoRecoveryKey)
	assert.False(t, resp.Adopted)

	// 凭据不进参数
	for _, c := range f.fake.Calls() {
		assert.NotContains(t, c.String(), opsnapKey)
		assert.NotContains(t, c.String(), testRecoveryKey)
	}

	// 本地目录：初始化时写入标记文件
	_, err := os.Stat(filepath.Join(dir, repo_entity.MarkerFile))
	assert.NoError(t, err)

	got, err := f.svc.Get(f.ctx, resp.ID)
	require.NoError(t, err)
	assert.NotContains(t, mustJSON(t, got), opsnapKey)
	assert.NotContains(t, mustJSON(t, got), testRecoveryKey)
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

func TestNewRecoveryKey(t *testing.T) {
	f := setupRepoTest(t)
	a, err := f.svc.NewRecoveryKey(f.ctx)
	require.NoError(t, err)
	b, err := f.svc.NewRecoveryKey(f.ctx)
	require.NoError(t, err)
	assert.NotEqual(t, a.RecoveryKeyValue, b.RecoveryKeyValue, "每次都是新的随机值")
	assert.True(t, repo_entity.ValidRecoveryKey(a.RecoveryKeyValue), a.RecoveryKeyValue)
	assert.Empty(t, f.fake.Calls())
	f.assertNoRepos(t)
}

func TestCreateRequiresRecoveryKeyAcknowledgement(t *testing.T) {
	f := setupRepoTest(t)
	dir := t.TempDir()
	cases := []struct {
		name string
		edit func(r *repoapi.CreateRequest)
		want int
	}{
		{"没有确认已存进密码管理器", func(r *repoapi.CreateRequest) { r.RecoveryKeySaved = false }, code.RepoRecoveryKeyNotSaved},
		{"默认生成恢复密钥，同样需要确认", func(r *repoapi.CreateRequest) { r.RecoveryKey, r.RecoveryKeySaved = nil, false }, code.RepoRecoveryKeyNotSaved},
		{"没有提交恢复密钥", func(r *repoapi.CreateRequest) { r.RecoveryKeyValue = "" }, code.RepoRecoveryKeyInvalid},
		{"默认生成但没有提交恢复密钥", func(r *repoapi.CreateRequest) { r.RecoveryKey, r.RecoveryKeyValue = nil, "" }, code.RepoRecoveryKeyInvalid},
		{"恢复密钥太短", func(r *repoapi.CreateRequest) { r.RecoveryKeyValue = "short-key-1234" }, code.RepoRecoveryKeyInvalid},
		{"恢复密钥含换行", func(r *repoapi.CreateRequest) { r.RecoveryKeyValue = testRecoveryKey + "\nX" }, code.RepoRecoveryKeyInvalid},
	}
	for _, c := range cases {
		req := localReq(dir)
		c.edit(req)
		_, err := f.svc.Create(f.ctx, req)
		assert.Equal(t, c.want, codeOf(t, err), c.name)
	}
	assert.Empty(t, f.fake.Calls(), "未确认或没有恢复密钥就不访问仓库")
	entries, _ := os.ReadDir(dir)
	assert.Empty(t, entries)
	f.assertNoRepos(t)
}

func TestCreateWithoutRecoveryKeyShowsWarning(t *testing.T) {
	f := setupRepoTest(t)
	resp := f.createEmptyLocal(t, t.TempDir(), false)
	assert.True(t, resp.NoRecoveryKey)
	assert.Empty(t, callsOf(f.fake, restic("key", "add")))

	list, err := f.svc.List(f.ctx)
	require.NoError(t, err)
	require.Len(t, list.List, 1)
	assert.True(t, list.List[0].NoRecoveryKey, "列表显示「没有恢复密钥」")
	keys, err := f.svc.Keys(f.ctx, resp.ID)
	require.NoError(t, err)
	assert.True(t, keys.NoRecoveryKey)
	assert.True(t, keys.OpsnapKey.Present)
	assert.Equal(t, f.now.Unix(), keys.OpsnapKey.CreatedAt)
}

func s3Req() *repoapi.CreateRequest {
	return &repoapi.CreateRequest{
		Name: "MinIO", Type: repoapi.TypeS3,
		Params: repoapi.Params{ //nolint:gosec // 测试用的虚构凭据
			Endpoint: "http://nas:9000", Bucket: "opsnap", AccessKeyID: "AKID",
			SecretAccessKey: "SECRET-s3-key", PathStyle: true,
		},
		RecoveryKey: no(),
	}
}

func TestCreateAdoptsExistingRepository(t *testing.T) {
	t.Run("已是 restic 仓库且没有提供原密码：要求输入，不保存", func(t *testing.T) {
		f := setupRepoTest(t)
		f.fake.On(restic("snapshots"), executor.Step{ExitCode: 12, Output: "Fatal: wrong password or no key found"})
		_, err := f.svc.Create(f.ctx, s3Req())
		assert.Equal(t, code.RepoExistingPasswordRequired, codeOf(t, err))
		assert.Empty(t, callsOf(f.fake, restic("init")), "不能在已有仓库上初始化")
		assert.Empty(t, callsOf(f.fake, restic("key", "add")))
		f.assertNoRepos(t)
	})

	t.Run("原密码错误：拒绝接入", func(t *testing.T) {
		f := setupRepoTest(t)
		f.fake.On(restic("snapshots"), executor.Step{ExitCode: 12})
		f.fake.On(resticWith("SECRET-old", "key", "list"), executor.Step{ExitCode: 12})
		req := s3Req()
		req.ExistingPassword = "SECRET-old"
		_, err := f.svc.Create(f.ctx, req)
		assert.Equal(t, code.RepoExistingPasswordWrong, codeOf(t, err))
		assert.Empty(t, callsOf(f.fake, restic("key", "add")))
		f.assertNoRepos(t)
	})

	t.Run("原密码正确：验证后加入 opsnap 密钥并保存", func(t *testing.T) {
		f := setupRepoTest(t)
		f.fake.On(restic("snapshots"), executor.Step{ExitCode: 12})
		f.fake.On(resticWith("SECRET-old", "key", "list"), keyList("old", "old"))
		f.fake.On(resticWith("SECRET-old", "key", "add"))
		f.fake.On(restic("key", "list"), keyList("op1", "old", "op1"))
		req := s3Req()
		req.ExistingPassword = "SECRET-old"
		resp, err := f.svc.Create(f.ctx, req)
		require.NoError(t, err)
		assert.True(t, resp.Adopted)

		adds := callsOf(f.fake, restic("key", "add"))
		require.Len(t, adds, 1)
		assert.Equal(t, string(adds[0].Stdin), f.opsnapKey(t, resp.ID), "接入时加入的新密钥就是保存的 opsnap 密钥")
		assert.Equal(t, "op1", f.raw(t, resp.ID).OpsnapKeyID)
		assert.Empty(t, callsOf(f.fake, restic("init")))
		row := f.rawRow(t, resp.ID)
		assert.NotContains(t, row, "SECRET-old", "原密码不保存")
		assert.NotContains(t, row, "SECRET-s3-key", "S3 secret 加密保存")
		assert.Equal(t, secret.Masked, resp.Params.SecretAccessKey)
	})
}

func TestCreateAdoptWithRecoveryKey(t *testing.T) {
	f := setupRepoTest(t)
	f.fake.On(restic("snapshots"), executor.Step{ExitCode: 12})
	f.fake.On(resticWith("SECRET-old", "key", "list"), keyList("old", "old"))
	f.fake.On(resticWith(testRecoveryKey, "key", "list"), keyList("rc1", "old", "op1", "rc1"))
	f.fake.On(restic("key", "add"))
	f.fake.On(restic("key", "list"), keyList("op1", "old", "op1"))
	req := s3Req()
	req.ExistingPassword = "SECRET-old"
	req.RecoveryKey, req.RecoveryKeyValue, req.RecoveryKeySaved = yes(), testRecoveryKey, true
	resp, err := f.svc.Create(f.ctx, req)
	require.NoError(t, err)
	assert.True(t, resp.Adopted)
	assert.False(t, resp.NoRecoveryKey)
	assert.Equal(t, "rc1", f.raw(t, resp.ID).RecoveryKeyID)

	adds := callsOf(f.fake, restic("key", "add"))
	require.Len(t, adds, 2)
	opsnap := f.opsnapKey(t, resp.ID)
	assert.Equal(t, opsnap, string(adds[0].Stdin), "先用原密码加入 opsnap 密钥")
	assert.Equal(t, testRecoveryKey, string(adds[1].Stdin), "再用 opsnap 密钥加入管理员已保存的恢复密钥")
	assert.Equal(t, opsnap, passwordOf(adds[1]))
	assert.NotContains(t, f.rawRow(t, resp.ID), testRecoveryKey)
}

func (f *fixture) assertNoRepos(t *testing.T) {
	t.Helper()
	var n int64
	require.NoError(t, f.db.Table("repos").Count(&n).Error)
	assert.Zero(t, n)
}

func TestCreateFailsWhenLocationUnreachable(t *testing.T) {
	t.Run("restic 无法访问：返回原因，不初始化不保存", func(t *testing.T) {
		f := setupRepoTest(t)
		f.fake.On(restic("snapshots"), executor.Step{ExitCode: 1, Output: "Fatal: unable to open config file: connection refused"})
		_, err := f.svc.Create(f.ctx, s3Req())
		assert.Equal(t, code.RepoAccessFailed, codeOf(t, err))
		assert.Contains(t, err.Error(), "connection refused")
		assert.Empty(t, callsOf(f.fake, restic("init")))
		f.assertNoRepos(t)
	})

	t.Run("本地目录不存在：不创建目录、不访问", func(t *testing.T) {
		f := setupRepoTest(t)
		dir := filepath.Join(t.TempDir(), "mnt", "nas")
		_, err := f.svc.Create(f.ctx, localReq(dir))
		assert.Equal(t, code.RepoPathMissing, codeOf(t, err))
		assert.Empty(t, f.fake.Calls())
		_, statErr := os.Stat(dir)
		assert.True(t, os.IsNotExist(statErr))
	})

	t.Run("本地目录不为空也不是仓库：拒绝初始化，不写标记", func(t *testing.T) {
		f := setupRepoTest(t)
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "photo.jpg"), []byte("x"), 0o600))
		f.fake.On(restic("snapshots"), executor.Step{ExitCode: 10})
		_, err := f.svc.Create(f.ctx, localReq(dir))
		assert.Equal(t, code.RepoNotEmpty, codeOf(t, err))
		assert.Empty(t, callsOf(f.fake, restic("init")))
		_, statErr := os.Stat(filepath.Join(dir, repo_entity.MarkerFile))
		assert.True(t, os.IsNotExist(statErr))
	})

	t.Run("初始化失败：撤掉标记，不保存", func(t *testing.T) {
		f := setupRepoTest(t)
		dir := t.TempDir()
		f.fake.On(restic("snapshots"), executor.Step{ExitCode: 10})
		f.fake.On(restic("init"), executor.Step{ExitCode: 1, Output: "Fatal: create repository failed: permission denied"})
		_, err := f.svc.Create(f.ctx, localReq(dir))
		assert.Equal(t, code.RepoAccessFailed, codeOf(t, err))
		entries, _ := os.ReadDir(dir)
		assert.Empty(t, entries)
		f.assertNoRepos(t)
	})
}

// createWithin 在 d 内等 Create 返回；超时直接判失败（探测卡住时 Create 不会自己返回）
func createWithin(t *testing.T, f *fixture, req *repoapi.CreateRequest, d time.Duration) (*repoapi.CreateResponse, error) {
	t.Helper()
	type out struct {
		resp *repoapi.CreateResponse
		err  error
	}
	ch := make(chan out, 1)
	go func() {
		resp, err := f.svc.Create(f.ctx, req)
		ch <- out{resp, err}
	}()
	select {
	case o := <-ch:
		return o.resp, o.err
	case <-time.After(d):
		t.Fatalf("Create 没有在 %v 内返回", d)
		return nil, nil
	}
}

// restic 0.19 遇到 bucket 不存在时把 NoSuchBucket 当成可重试错误，每次重试在标准错误打印一行，然后一直重试
const bucketMissingRetry = "Load(<config/0000000000>, 0, 0) returned error, retrying after 1.07s: The specified bucket does not exist\n"

func TestCreateS3BucketMissingInitializes(t *testing.T) {
	f := setupRepoTest(t)
	f.svc.probeLimit = time.Minute
	f.fake.On(restic("snapshots"), executor.Step{Stderr: bucketMissingRetry, Block: true})
	f.fake.On(restic("init"))
	f.fake.On(restic("key", "list"), keyList("op1", "op1"))

	resp, err := createWithin(t, f, s3Req(), 5*time.Second)
	require.NoError(t, err)
	assert.False(t, resp.Adopted)
	inits := callsOf(f.fake, restic("init"))
	require.Len(t, inits, 1, "bucket 不存在按空位置处理：初始化新仓库（restic init 会建 bucket）")
	assert.Equal(t, passwordOf(inits[0]), f.opsnapKey(t, resp.ID))
	assert.Equal(t, "op1", f.raw(t, resp.ID).OpsnapKeyID)
	for _, c := range f.fake.Calls() {
		assert.NotContains(t, c.String(), "SECRET-s3-key", "S3 secret 不进参数")
	}
}

// restic 打出 bucket 不存在后自己就退出了（没有重试、或恰好在被终止前结束）：结果不能取决于谁先结束，同样按空位置初始化
func TestCreateS3BucketMissingResticExits(t *testing.T) {
	f := setupRepoTest(t)
	f.fake.On(restic("snapshots"), executor.Step{Stderr: "Fatal: unable to open config file: Stat: The specified bucket does not exist.\n", ExitCode: 1})
	f.fake.On(restic("init"))
	f.fake.On(restic("key", "list"), keyList("op1", "op1"))

	resp, err := createWithin(t, f, s3Req(), 5*time.Second)
	require.NoError(t, err)
	assert.False(t, resp.Adopted)
	require.Len(t, callsOf(f.fake, restic("init")), 1)
}

func TestCreateProbeBounded(t *testing.T) {
	const retry = "Load(<config/0000000000>, 0, 0) returned error, retrying after 2.3s: " +
		"Get \"http://nas:9000/opsnap/config\": dial tcp 10.9.9.9:9000: connect: connection refused (SECRET-s3-key)\n"

	t.Run("探测一直重试：到上限后说明发生了什么、怎么处理，带上 restic 最后一条错误", func(t *testing.T) {
		f := setupRepoTest(t)
		f.svc.probeLimit = 50 * time.Millisecond
		f.fake.On(restic("snapshots"), executor.Step{Stderr: "Load(<config/0000000000>, 0, 0) returned error, retrying after 1s: i/o timeout\n" + retry, Block: true})

		_, err := createWithin(t, f, s3Req(), 5*time.Second)
		require.Error(t, err)
		assert.Equal(t, code.RepoProbeTimeout, codeOf(t, err))
		msg := err.Error()
		assert.Contains(t, msg, "connection refused", "带上 restic 最后一条错误")
		assert.NotContains(t, msg, "i/o timeout", "只带最后一条")
		assert.Contains(t, msg, "请检查")
		assert.NotContains(t, msg, "context deadline exceeded")
		assert.NotContains(t, msg, "SECRET-s3-key", "错误信息里遮盖凭据")
		probes := callsOf(f.fake, restic("snapshots"))
		require.Len(t, probes, 1)
		assert.NotContains(t, msg, passwordOf(probes[0]), "错误信息里遮盖 opsnap 密钥")
		for _, c := range f.fake.Calls() {
			assert.NotContains(t, c.String(), "SECRET-s3-key", "S3 secret 不进参数")
		}
		assert.Empty(t, callsOf(f.fake, restic("init")))
		f.assertNoRepos(t)
	})

	t.Run("restic 被取消后还会输出 exit_error：带上的是重试时的真实错误，不是 context canceled", func(t *testing.T) {
		f := setupRepoTest(t)
		f.svc.probeLimit = 50 * time.Millisecond
		// restic 0.19 实测：被取消后最后一行是 exit_error，只说 context canceled
		exitErr := `{"message_type":"exit_error","code":130,"message":"Fatal: unable to open config file: context canceled\ns3:http://nas:9000/opsnap"}` + "\n"
		f.fake.On(restic("snapshots"), executor.Step{Stderr: retry + exitErr, Block: true})

		_, err := createWithin(t, f, s3Req(), 5*time.Second)
		require.Error(t, err)
		assert.Equal(t, code.RepoProbeTimeout, codeOf(t, err))
		msg := err.Error()
		assert.Contains(t, msg, "connection refused")
		assert.NotContains(t, msg, "context canceled")
		assert.NotContains(t, msg, "retrying after", "只带错误本身")
		assert.NotContains(t, msg, "SECRET-s3-key")
	})

	t.Run("不是 S3：同样的输出不当作 bucket 不存在", func(t *testing.T) {
		f := setupRepoTest(t)
		f.svc.probeLimit = 50 * time.Millisecond
		f.fake.On(restic("snapshots"), executor.Step{Stderr: bucketMissingRetry, Block: true})
		req := &repoapi.CreateRequest{
			Name: "REST", Type: repoapi.TypeREST,
			Params:      repoapi.Params{URL: "http://nas:8000/opsnap/"},
			RecoveryKey: no(),
		}
		_, err := createWithin(t, f, req, 5*time.Second)
		assert.Equal(t, code.RepoProbeTimeout, codeOf(t, err))
		assert.Empty(t, callsOf(f.fake, restic("init")))
		f.assertNoRepos(t)
	})
}

func TestPreflightMissingMarker(t *testing.T) {
	f := setupRepoTest(t)
	dir := t.TempDir()
	resp := f.createEmptyLocal(t, dir, false)
	marker := filepath.Join(dir, repo_entity.MarkerFile)
	require.NoError(t, Preflight(f.ctx, resp.ID), "有标记文件时预检通过")

	// NAS 没有挂载：目录还在，但是空的
	require.NoError(t, os.Remove(marker))
	callsBefore := len(f.fake.Calls())
	err := f.svc.Preflight(f.ctx, resp.ID)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotMounted))
	assert.Contains(t, err.Error(), "没有挂载")
	entries, _ := os.ReadDir(dir)
	assert.Empty(t, entries, "不向该路径写入任何数据")
	assert.Equal(t, callsBefore, len(f.fake.Calls()), "预检失败不运行 restic")

	got, err := f.svc.Get(f.ctx, resp.ID)
	require.NoError(t, err)
	assert.Equal(t, repoapi.StatusNotMounted, got.Status, "面板标红")
	assert.Contains(t, got.StatusMessage, "没有挂载")

	// 重新检查：仍然缺少标记时不运行 restic，也不写入
	rc, err := f.svc.Recheck(f.ctx, resp.ID)
	require.NoError(t, err)
	assert.Equal(t, repoapi.StatusNotMounted, rc.Status)
	assert.Equal(t, callsBefore, len(f.fake.Calls()))
	entries, _ = os.ReadDir(dir)
	assert.Empty(t, entries)

	// 挂载恢复后重新检查：刷新统计并恢复正常
	require.NoError(t, os.WriteFile(marker, []byte("x"), 0o600))
	f.fake.On(restic("stats", "--json", "--mode", "raw-data"), executor.Step{Output: `{"total_size":1000,"total_uncompressed_size":3000,"snapshots_count":4}`})
	f.fake.On(restic("stats", "--json", "--mode", "restore-size"), executor.Step{Output: `{"total_size":5000,"total_file_count":7,"snapshots_count":4}`})
	rc, err = f.svc.Recheck(f.ctx, resp.ID)
	require.NoError(t, err)
	assert.Equal(t, repoapi.StatusOK, rc.Status)
	assert.Equal(t, int64(1000), rc.StoredSize)
	assert.Equal(t, int64(5000), rc.RawSize)
	assert.InDelta(t, 5.0, rc.DedupRatio, 0.001)
	assert.Equal(t, 4, rc.SnapshotCount)
	assert.Equal(t, 1, rc.KeyCount)
	require.NotNil(t, rc.FreeSpace, "本地目录能取到剩余空间")
	assert.Positive(t, *rc.FreeSpace)
	assert.Equal(t, f.now.Unix(), rc.StatsAt)
}

func TestRecheckRemoteStatsAndFailure(t *testing.T) {
	f := setupRepoTest(t)
	f.fake.On(restic("snapshots"), executor.Step{ExitCode: 10})
	f.fake.On(restic("init"))
	f.fake.On(restic("key", "list"), keyList("op1", "op1"))
	resp, err := f.svc.Create(f.ctx, s3Req())
	require.NoError(t, err)

	f.fake.On(restic("stats"), executor.Step{ExitCode: 1, Output: "Fatal: unable to open repository: timeout"})
	rc, err := f.svc.Recheck(f.ctx, resp.ID)
	require.NoError(t, err)
	assert.Equal(t, repoapi.StatusError, rc.Status)
	assert.Contains(t, rc.StatusMessage, "timeout")
	assert.Nil(t, rc.FreeSpace, "S3 的剩余空间未知")
}

// rotateFixture 建好一个 opsnap 密钥 ID 为 op1 的仓库，返回 ID 和原 opsnap 密钥
func rotateFixture(t *testing.T) (*fixture, int64, string) {
	t.Helper()
	f := setupRepoTest(t)
	resp := f.createEmptyLocal(t, t.TempDir(), false)
	f.fake = executor.NewFake()
	f.svc.runner = f.fake
	return f, resp.ID, f.opsnapKey(t, resp.ID)
}

func TestRotateOpsnapKey(t *testing.T) {
	t.Run("成功：先加新密钥并验证，再删旧密钥，保存新密钥", func(t *testing.T) {
		f, id, old := rotateFixture(t)
		f.fake.On(resticWith(old, "key", "list"), keyList("op1", "op1"))
		f.fake.On(resticWith(old, "key", "add"))
		f.fake.On(resticWithout(old, "key", "list"), keyList("op2", "op1", "op2"))
		f.fake.On(resticWithout(old, "key", "remove", "op1"))
		resp, err := f.svc.RotateKey(f.ctx, id)
		require.NoError(t, err)
		assert.Equal(t, "op2", resp.OpsnapKey.ID)

		newKey := f.opsnapKey(t, id)
		assert.NotEqual(t, old, newKey)
		adds := callsOf(f.fake, restic("key", "add"))
		require.Len(t, adds, 1)
		assert.Equal(t, newKey, string(adds[0].Stdin))
		removes := callsOf(f.fake, restic("key", "remove"))
		require.Len(t, removes, 1)
		assert.Equal(t, newKey, passwordOf(removes[0]), "用新密钥删除旧密钥")
		assert.Equal(t, "op2", f.raw(t, id).OpsnapKeyID)
	})

	steps := []struct {
		name         string
		script       func(f *fixture, old string)
		wantRollback bool
	}{
		{"读取现有密钥失败", func(f *fixture, old string) {
			f.fake.On(resticWith(old, "key", "list"), executor.Step{ExitCode: 1})
		}, false},
		{"添加新密钥失败", func(f *fixture, old string) {
			f.fake.On(resticWith(old, "key", "list"), keyList("op1", "op1"))
			f.fake.On(restic("key", "add"), executor.Step{ExitCode: 1, Output: "Fatal: unable to save key"})
		}, false},
		{"验证新密钥失败", func(f *fixture, old string) {
			f.fake.On(resticWith(old, "key", "list"), keyList("op1", "op1"), keyList("op1", "op1", "op2"))
			f.fake.On(restic("key", "add"))
			f.fake.On(resticWithout(old, "key", "list"), executor.Step{ExitCode: 12})
			f.fake.On(resticWith(old, "key", "remove", "op2"))
		}, true},
		{"删除旧密钥失败", func(f *fixture, old string) {
			f.fake.On(resticWith(old, "key", "list"), keyList("op1", "op1"))
			f.fake.On(restic("key", "add"))
			f.fake.On(resticWithout(old, "key", "list"), keyList("op2", "op1", "op2"))
			f.fake.On(resticWithout(old, "key", "remove", "op1"), executor.Step{ExitCode: 11, Output: "Fatal: unable to create lock"})
			f.fake.On(resticWith(old, "key", "remove", "op2"))
		}, true},
		{"执行器无法启动", func(f *fixture, old string) {
			f.fake.On(restic("key"), executor.Step{Err: errors.New("exec: restic not found")})
		}, false},
	}
	for _, s := range steps {
		t.Run(s.name+"：保持原状", func(t *testing.T) {
			f, id, old := rotateFixture(t)
			s.script(f, old)
			before := f.raw(t, id)
			_, err := f.svc.RotateKey(f.ctx, id)
			require.Error(t, err)
			assert.Equal(t, code.RepoKeyOperationFailed, codeOf(t, err))
			assert.Equal(t, old, f.opsnapKey(t, id), "保存的 opsnap 密钥不变")
			after := f.raw(t, id)
			assert.Equal(t, before.OpsnapKeyID, after.OpsnapKeyID)
			assert.Equal(t, before.OpsnapKeyCreatedAt, after.OpsnapKeyCreatedAt)
			rollbacks := callsOf(f.fake, resticWith(old, "key", "remove", "op2"))
			if s.wantRollback {
				assert.Len(t, rollbacks, 1, "撤掉已加入的新密钥")
			} else {
				assert.Empty(t, callsOf(f.fake, restic("key", "remove")))
			}
		})
	}

	t.Run("维护进行中：拒绝更换", func(t *testing.T) {
		f, id, _ := rotateFixture(t)
		rel, ok := repolock.TryAcquire(id, repolock.Shared)
		require.True(t, ok)
		defer rel()
		_, err := f.svc.RotateKey(f.ctx, id)
		assert.Equal(t, code.RepoBusy, codeOf(t, err))
		assert.Empty(t, f.fake.Calls())
	})
}

// cancelOnRunner 执行到匹配的命令时取消请求（客户端断开），命令本身照常在仓库里生效；
// 收到的 ctx 已结束时像真实执行器一样返回 -1
type cancelOnRunner struct {
	executor.Runner
	match  func(cmdbuild.Cmd) bool
	cancel context.CancelFunc
}

func (c *cancelOnRunner) Run(ctx context.Context, cmd cmdbuild.Cmd, out io.Writer) (int, error) {
	if c.match(cmd) {
		c.cancel()
		code, err := c.Runner.Run(context.Background(), cmd, out)
		if ctx.Err() != nil {
			return -1, context.Cause(ctx)
		}
		return code, err
	}
	return c.Runner.Run(ctx, cmd, out)
}

func TestRotateKeyNotAbortedByClientDisconnect(t *testing.T) {
	f, id, old := rotateFixture(t)
	f.fake.On(resticWith(old, "key", "list"), keyList("op1", "op1"))
	f.fake.On(resticWith(old, "key", "add"))
	f.fake.On(resticWithout(old, "key", "list"), keyList("op2", "op1", "op2"))
	f.fake.On(resticWithout(old, "key", "remove", "op1"))
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	// 删除旧密钥的同时客户端断开：旧密钥已经从仓库删掉，保存的必须是新密钥，否则之后再也打不开仓库
	f.svc.runner = &cancelOnRunner{Runner: f.fake, match: restic("key", "remove", "op1"), cancel: cancel}

	_, err := f.svc.RotateKey(ctx, id)
	require.NoError(t, err)
	assert.NotEqual(t, old, f.opsnapKey(t, id))
	assert.Equal(t, "op2", f.raw(t, id).OpsnapKeyID)
	assert.Empty(t, callsOf(f.fake, restic("key", "remove", "op2")), "没有撤掉仍在使用的新密钥")
}

func TestMaintainUsesKeyCurrentAfterWaiting(t *testing.T) {
	f, id, old := rotateFixture(t)
	// 有备份持有共享锁，维护排队等待；等待期间 opsnap 密钥换了
	rel, ok := repolock.TryAcquire(id, repolock.Shared)
	require.True(t, ok)
	done := make(chan error, 1)
	go func() { done <- f.svc.Maintain(f.ctx, id) }()
	time.Sleep(100 * time.Millisecond)
	newKey, err := secret.Default().Encrypt("SECRET-new-opsnap-key")
	require.NoError(t, err)
	require.NoError(t, f.db.Model(&repo_entity.Repo{}).Where("id = ?", id).Update("opsnap_key", newKey).Error)
	rel()
	require.NoError(t, <-done)
	prunes := callsOf(f.fake, restic("prune"))
	require.Len(t, prunes, 1)
	assert.Equal(t, "SECRET-new-opsnap-key", passwordOf(prunes[0]), "用拿到锁之后的密钥，而不是排队前读到的 %s", old)
}

func TestJSONOutputIgnoresStderr(t *testing.T) {
	f, id, old := rotateFixture(t)
	// restic 在标准错误上打印的重试提示不能混进要解析的 JSON
	warn := func(s executor.Step) executor.Step {
		s.Stderr = "Load(<lock/0123abcd>, 0, 0) returned error, retrying after 552ms: connection reset\n"
		return s
	}
	f.fake.On(resticWith(old, "key", "list"), warn(keyList("op1", "op1")))
	f.fake.On(resticWith(old, "key", "add"))
	f.fake.On(resticWithout(old, "key", "list"), warn(keyList("op2", "op1", "op2")))
	f.fake.On(resticWithout(old, "key", "remove", "op1"))
	resp, err := f.svc.RotateKey(f.ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "op2", resp.OpsnapKey.ID)
}

func TestRecoveryKeyLifecycle(t *testing.T) {
	f, id, op := rotateFixture(t)
	f.fake.On(resticWith(op, "key", "list"), keyList("op1", "op1"))
	f.fake.On(restic("key", "add"))
	f.fake.On(resticWithout(op, "key", "list"), keyList("rc9", "op1", "rc9"))
	f.fake.On(resticWith(op, "key", "remove", "rc9"))

	genReq := func(value string, saved bool) *repoapi.GenerateRecoveryKeyRequest {
		return &repoapi.GenerateRecoveryKeyRequest{ID: id, RecoveryKeyValue: value, RecoveryKeySaved: saved}
	}
	// 密钥对话框同样先展示再加入：没有确认或值不合法时不访问仓库
	_, err := f.svc.GenerateRecoveryKey(f.ctx, genReq(testRecoveryKey, false))
	assert.Equal(t, code.RepoRecoveryKeyNotSaved, codeOf(t, err))
	_, err = f.svc.GenerateRecoveryKey(f.ctx, genReq("", true))
	assert.Equal(t, code.RepoRecoveryKeyInvalid, codeOf(t, err))
	assert.Empty(t, f.fake.Calls())

	gen, err := f.svc.GenerateRecoveryKey(f.ctx, genReq(testRecoveryKey, true))
	require.NoError(t, err)
	assert.True(t, gen.RecoveryKey.Present)
	assert.Equal(t, "rc9", gen.RecoveryKey.ID)
	assert.False(t, gen.NoRecoveryKey)
	adds := callsOf(f.fake, restic("key", "add"))
	require.Len(t, adds, 1)
	assert.Equal(t, testRecoveryKey, string(adds[0].Stdin))
	assert.NotContains(t, f.rawRow(t, id), testRecoveryKey, "恢复密钥不保存")
	assert.NotContains(t, mustJSON(t, gen), testRecoveryKey, "不回传恢复密钥")
	got, err := f.svc.Get(f.ctx, id)
	require.NoError(t, err)
	assert.Equal(t, 2, got.KeyCount, "列表里的密钥数量随之更新")

	_, err = f.svc.GenerateRecoveryKey(f.ctx, genReq(testRecoveryKey, true))
	assert.Equal(t, code.RepoRecoveryKeyExists, codeOf(t, err))

	rm, err := f.svc.RemoveRecoveryKey(f.ctx, id)
	require.NoError(t, err)
	assert.False(t, rm.RecoveryKey.Present)
	assert.True(t, rm.NoRecoveryKey)
	assert.Len(t, callsOf(f.fake, resticWith(op, "key", "remove", "rc9")), 1)
	got, err = f.svc.Get(f.ctx, id)
	require.NoError(t, err)
	assert.Equal(t, 1, got.KeyCount)

	_, err = f.svc.RemoveRecoveryKey(f.ctx, id)
	assert.Equal(t, code.RepoRecoveryKeyMissing, codeOf(t, err))
}

func TestRecoveryBundleHasNoSecrets(t *testing.T) {
	for _, req := range []*repoapi.CreateRequest{
		s3Req(),
		{Name: "rest", Type: repoapi.TypeREST, Params: repoapi.Params{URL: "https://nas:8000/opsnap/", Username: "u", Password: "SECRET-rest-pw"}, RecoveryKey: yes(), RecoveryKeyValue: testRecoveryKey, RecoveryKeySaved: true},
		{Name: "dav", Type: repoapi.TypeWebDAV, Params: repoapi.Params{URL: "https://nas:5006", Path: "/backup/opsnap", Username: "u", Password: "SECRET-dav-pw"}, RecoveryKey: yes(), RecoveryKeyValue: testRecoveryKey, RecoveryKeySaved: true}, //nolint:gosec // 测试用的虚构凭据
	} {
		t.Run(req.Type, func(t *testing.T) {
			f := setupRepoTest(t)
			f.fake.On(executor.Match("rclone", "obscure"), executor.Step{Output: "OBSCURED-pw\n"})
			f.fake.On(restic("snapshots"), executor.Step{ExitCode: 10})
			f.fake.On(restic("init"))
			f.fake.On(restic("key", "add"))
			f.fake.On(restic("key", "list"), keyList("op1", "op1"), keyList("op1", "op1"), keyList("rc1", "op1", "rc1"))
			resp, err := f.svc.Create(f.ctx, req)
			require.NoError(t, err)
			opsnap := f.opsnapKey(t, resp.ID)

			b, err := f.svc.RecoveryBundle(f.ctx, resp.ID)
			require.NoError(t, err)
			assert.NotEmpty(t, b.Filename)
			assert.Contains(t, b.Content, resp.Address, "包含仓库地址")
			assert.Contains(t, b.Content, "restic restore")
			for _, s := range []string{opsnap, testRecoveryKey, "SECRET-", "OBSCURED"} {
				assert.NotContains(t, b.Content, s, "恢复包不能包含任何密钥或密码")
			}
		})
	}
}

func TestMaintenance(t *testing.T) {
	t.Run("校验与清理在后台独占执行并记录结果", func(t *testing.T) {
		f, id, _ := rotateFixture(t)
		f.fake.On(restic("check"), executor.Step{Output: "no errors were found"})
		f.fake.On(restic("prune"), executor.Step{ExitCode: 1, Output: "Fatal: unable to create lock in backend"})
		gate := make(chan struct{})
		f.svc.runner = &gatedRunner{Runner: f.fake, gate: gate}

		item, err := f.svc.StartCheck(f.ctx, id)
		require.NoError(t, err)
		assert.Equal(t, repoapi.MaintenanceCheck, item.Maintenance)
		_, err = f.svc.StartPrune(f.ctx, id)
		assert.Equal(t, code.RepoBusy, codeOf(t, err), "同一存储位置同时只做一项维护")
		close(gate)
		f.svc.wait()

		got, err := f.svc.Get(f.ctx, id)
		require.NoError(t, err)
		assert.True(t, got.LastCheckOK)
		assert.Equal(t, f.now.Unix(), got.LastCheckAt)
		assert.Empty(t, got.Maintenance)
		checks := callsOf(f.fake, restic("check"))
		require.Len(t, checks, 1)
		assert.Contains(t, checks[0].Args, "--read-data-subset")

		_, err = f.svc.StartPrune(f.ctx, id)
		require.NoError(t, err)
		f.svc.wait()
		got, err = f.svc.Get(f.ctx, id)
		require.NoError(t, err)
		assert.False(t, got.LastPruneOK)
		assert.Contains(t, got.LastPruneMessage, "unable to create lock")
	})

	t.Run("缺少标记文件时不做维护", func(t *testing.T) {
		f := setupRepoTest(t)
		dir := t.TempDir()
		resp := f.createEmptyLocal(t, dir, false)
		require.NoError(t, os.Remove(filepath.Join(dir, repo_entity.MarkerFile)))
		n := len(f.fake.Calls())
		_, err := f.svc.StartPrune(f.ctx, resp.ID)
		assert.Equal(t, code.RepoNotMounted, codeOf(t, err))
		require.Error(t, f.svc.Maintain(f.ctx, resp.ID))
		assert.Len(t, f.fake.Calls(), n)
	})

	t.Run("解除仓库锁需要二次确认，执行 unlock --remove-all", func(t *testing.T) {
		f, id, _ := rotateFixture(t)
		err := f.svc.Unlock(f.ctx, id, false)
		assert.Equal(t, code.RepoUnlockNotConfirmed, codeOf(t, err))
		assert.Empty(t, f.fake.Calls())

		require.NoError(t, f.svc.Unlock(f.ctx, id, true))
		unlocks := callsOf(f.fake, restic("unlock"))
		require.Len(t, unlocks, 1)
		assert.Contains(t, unlocks[0].Args, "--remove-all")

		rel, ok := repolock.TryAcquire(id, repolock.Shared)
		require.True(t, ok)
		err = f.svc.Unlock(f.ctx, id, true)
		rel()
		assert.Equal(t, code.RepoBusy, codeOf(t, err), "备份正在写入时不能解锁")

		f.fake.On(restic("unlock"), executor.Step{ExitCode: 1, Output: "Fatal: permission denied"})
		err = f.svc.Unlock(f.ctx, id, true)
		assert.Equal(t, code.RepoUnlockFailed, codeOf(t, err))
	})

	t.Run("每周维护：等备份结束后独占执行清理和抽样校验", func(t *testing.T) {
		f, id, _ := rotateFixture(t)
		backup, err := repolock.Acquire(context.Background(), id, repolock.Shared)
		require.NoError(t, err)

		ctx, cancel := context.WithTimeout(f.ctx, 50*time.Millisecond)
		defer cancel()
		err = Maintain(ctx, id)
		assert.ErrorIs(t, err, context.DeadlineExceeded, "备份写入期间维护等待，ctx 结束后放弃")
		assert.Empty(t, f.fake.Calls())
		backup()

		require.NoError(t, f.svc.Maintain(f.ctx, id))
		calls := f.fake.Calls()
		subs := make([]string, 0, len(calls))
		for _, c := range calls {
			subs = append(subs, c.Args[0])
		}
		require.GreaterOrEqual(t, len(subs), 2)
		assert.Equal(t, []string{"prune", "check"}, subs[:2])
		got, err := f.svc.Get(f.ctx, id)
		require.NoError(t, err)
		assert.True(t, got.LastPruneOK)
		assert.True(t, got.LastCheckOK)
	})
}

// statsSteps 让 restic stats 返回给定的占用、原始数据量和快照数
func statsSteps(f *fixture, stored, raw int64, snapshots int) {
	f.fake.On(restic("stats", "--json", "--mode", "raw-data"), executor.Step{Output: fmt.Sprintf(`{"total_size":%d,"snapshots_count":%d}`, stored, snapshots)})
	f.fake.On(restic("stats", "--json", "--mode", "restore-size"), executor.Step{Output: fmt.Sprintf(`{"total_size":%d,"snapshots_count":%d}`, raw, snapshots)})
	f.fake.On(restic("key", "list"), keyList("op1", "op1"))
}

func TestStatsStayCurrent(t *testing.T) {
	t.Run("刚添加就有密钥数量", func(t *testing.T) {
		f := setupRepoTest(t)
		assert.Equal(t, 2, f.createEmptyLocal(t, t.TempDir(), true).KeyCount)
		f = setupRepoTest(t)
		assert.Equal(t, 1, f.createEmptyLocal(t, t.TempDir(), false).KeyCount)
	})

	t.Run("备份后刷新统计：占用、快照数、剩余空间随之更新", func(t *testing.T) {
		f, id, _ := rotateFixture(t)
		statsSteps(f, 1000, 5000, 4)
		require.NoError(t, f.svc.RefreshStats(f.ctx, id))
		got, err := f.svc.Get(f.ctx, id)
		require.NoError(t, err)
		assert.Equal(t, int64(1000), got.StoredSize)
		assert.Equal(t, int64(5000), got.RawSize)
		assert.Equal(t, 4, got.SnapshotCount)
		assert.Equal(t, 1, got.KeyCount)
		require.NotNil(t, got.FreeSpace)
		assert.Equal(t, f.now.Unix(), got.StatsAt)
	})

	t.Run("统计失败只返回错误，不把存储位置标红", func(t *testing.T) {
		f, id, _ := rotateFixture(t)
		f.fake.On(restic("stats"), executor.Step{ExitCode: 1, Output: "Fatal: timeout"})
		require.Error(t, f.svc.RefreshStats(f.ctx, id))
		got, err := f.svc.Get(f.ctx, id)
		require.NoError(t, err)
		assert.Equal(t, repoapi.StatusOK, got.Status)
	})

	t.Run("WebDAV/SFTP 经 rclone about 取 NAS 剩余空间，取不到时显示未知", func(t *testing.T) {
		f := setupRepoTest(t)
		f.fake.On(executor.Match("rclone", "obscure", "-"), executor.Step{Output: "OBSCURED-pw\n"})
		f.fake.On(restic("snapshots"), executor.Step{ExitCode: 10})
		f.fake.On(restic("init"))
		statsSteps(f, 1000, 5000, 4)
		resp, err := f.svc.Create(f.ctx, &repoapi.CreateRequest{Name: "dav", Type: repoapi.TypeWebDAV, RecoveryKey: no(),
			Params: repoapi.Params{URL: "https://nas:5006", Path: "/opsnap", Username: "u", Password: "SECRET-dav-pw"}}) //nolint:gosec // 测试用的虚构凭据
		require.NoError(t, err)
		f.fake.On(executor.Match("rclone", "about"), executor.Step{Output: "{\n\t\"total\": 100000,\n\t\"used\": 40000,\n\t\"free\": 60000\n}\n"}, executor.Step{ExitCode: 1})

		rc, err := f.svc.Recheck(f.ctx, resp.ID)
		require.NoError(t, err)
		require.NotNil(t, rc.FreeSpace)
		assert.Equal(t, int64(60000), *rc.FreeSpace)
		about := callsOf(f.fake, executor.Match("rclone", "about"))[0]
		assert.NotContains(t, about.String(), "SECRET-dav-pw")

		rc, err = f.svc.Recheck(f.ctx, resp.ID)
		require.NoError(t, err)
		assert.Nil(t, rc.FreeSpace, "rclone about 失败：剩余空间未知")
		assert.Equal(t, repoapi.StatusOK, rc.Status, "取不到剩余空间不算检查失败")
	})

	t.Run("维护（清理空间后）刷新统计", func(t *testing.T) {
		f, id, _ := rotateFixture(t)
		statsSteps(f, 700, 5000, 3)
		require.NoError(t, f.svc.Maintain(f.ctx, id))
		got, err := f.svc.Get(f.ctx, id)
		require.NoError(t, err)
		assert.Equal(t, int64(700), got.StoredSize)
		assert.Equal(t, 3, got.SnapshotCount)

		f.fake = executor.NewFake()
		f.svc.runner = f.fake
		statsSteps(f, 600, 5000, 3)
		_, err = f.svc.StartPrune(f.ctx, id)
		require.NoError(t, err)
		f.svc.wait()
		got, err = f.svc.Get(f.ctx, id)
		require.NoError(t, err)
		assert.Equal(t, int64(600), got.StoredSize, "手动清理空间后同样刷新")
	})
}

func TestEnvAndCredentials(t *testing.T) {
	t.Run("S3：凭据加密保存，Env 解密后只放进环境变量", func(t *testing.T) {
		f := setupRepoTest(t)
		f.fake.On(restic("snapshots"), executor.Step{ExitCode: 10})
		f.fake.On(restic("init"))
		f.fake.On(restic("key", "list"), keyList("op1", "op1"))
		resp, err := f.svc.Create(f.ctx, s3Req())
		require.NoError(t, err)

		r, release, err := Env(f.ctx, resp.ID)
		require.NoError(t, err)
		defer release()
		assert.Equal(t, f.opsnapKey(t, resp.ID), r.Password)
		c, err := cmdbuild.ResticSnapshots(r, nil)
		require.NoError(t, err)
		assert.Contains(t, c.Env, "AWS_SECRET_ACCESS_KEY=SECRET-s3-key")
		assert.NotContains(t, c.String(), "SECRET-s3-key")

		// 修改时回传占位保留原值；访问验证用 opsnap 密钥
		f.fake.On(resticWith(r.Password, "key", "list"), keyList("op1", "op1"))
		up, err := f.svc.Update(f.ctx, &repoapi.UpdateRequest{ID: resp.ID, Name: "MinIO 2", Params: repoapi.Params{
			Endpoint: "http://nas:9000", Bucket: "opsnap", AccessKeyID: "AKID", SecretAccessKey: secret.Masked, PathStyle: true,
		}})
		require.NoError(t, err)
		assert.Equal(t, "MinIO 2", up.Name)
		r2, release2, err := Env(f.ctx, resp.ID)
		require.NoError(t, err)
		defer release2()
		c2, _ := cmdbuild.ResticSnapshots(r2, nil)
		assert.Contains(t, c2.Env, "AWS_SECRET_ACCESS_KEY=SECRET-s3-key")
	})

	t.Run("修改后打不开仓库：不保存", func(t *testing.T) {
		f := setupRepoTest(t)
		f.fake.On(restic("snapshots"), executor.Step{ExitCode: 10})
		f.fake.On(restic("init"))
		f.fake.On(restic("key", "list"), keyList("op1", "op1"), executor.Step{ExitCode: 12})
		resp, err := f.svc.Create(f.ctx, s3Req())
		require.NoError(t, err)
		_, err = f.svc.Update(f.ctx, &repoapi.UpdateRequest{ID: resp.ID, Name: "x", Params: repoapi.Params{
			Endpoint: "http://other:9000", Bucket: "b", AccessKeyID: "AKID", SecretAccessKey: secret.Masked,
		}})
		assert.Equal(t, code.RepoAccessFailed, codeOf(t, err))
		assert.Equal(t, "MinIO", f.raw(t, resp.ID).Name)
	})

	t.Run("WebDAV：密码经标准输入交给 rclone obscure", func(t *testing.T) {
		f := setupRepoTest(t)
		f.fake.On(executor.Match("rclone", "obscure", "-"), executor.Step{Output: "OBSCURED-pw\n"})
		f.fake.On(restic("snapshots"), executor.Step{ExitCode: 10})
		f.fake.On(restic("init"))
		f.fake.On(restic("key", "list"), keyList("op1", "op1"))
		_, err := f.svc.Create(f.ctx, &repoapi.CreateRequest{Name: "dav", Type: repoapi.TypeWebDAV, RecoveryKey: no(),
			Params: repoapi.Params{URL: "https://nas:5006", Path: "/opsnap", Username: "u", Password: "SECRET-dav-pw"}}) //nolint:gosec // 测试用的虚构凭据
		require.NoError(t, err)
		obs := callsOf(f.fake, executor.Match("rclone", "obscure"))
		require.NotEmpty(t, obs)
		assert.Equal(t, "SECRET-dav-pw", string(obs[0].Stdin))
		init := callsOf(f.fake, restic("init"))[0]
		assert.Contains(t, init.Env, "RCLONE_CONFIG_OPSNAP_PASS=OBSCURED-pw")
		assert.NotContains(t, init.String(), "SECRET-dav-pw")
	})

	t.Run("WebDAV：改了地址或用户时不能沿用已保存的密码", func(t *testing.T) {
		f := setupRepoTest(t)
		f.fake.On(executor.Match("rclone", "obscure", "-"), executor.Step{Output: "OBSCURED-pw\n"})
		f.fake.On(restic("snapshots"), executor.Step{ExitCode: 10})
		f.fake.On(restic("init"))
		f.fake.On(restic("key", "list"), keyList("op1", "op1"))
		resp, err := f.svc.Create(f.ctx, &repoapi.CreateRequest{Name: "dav", Type: repoapi.TypeWebDAV, RecoveryKey: no(),
			Params: repoapi.Params{URL: "https://nas:5006", Path: "/opsnap", Username: "u", Password: "SECRET-dav-pw"}}) //nolint:gosec // 测试用的虚构凭据
		require.NoError(t, err)
		before := len(f.fake.Calls())
		for _, p := range []repoapi.Params{
			{URL: "https://attacker.example:5006", Path: "/opsnap", Username: "u", Password: secret.Masked},
			{URL: "https://nas:5006", Path: "/opsnap", Username: "other", Password: secret.Masked},
		} {
			_, err = f.svc.Update(f.ctx, &repoapi.UpdateRequest{ID: resp.ID, Name: "dav", Params: p})
			assert.Equal(t, code.RepoCredentialRetarget, codeOf(t, err), p.URL+" "+p.Username)
		}
		assert.Len(t, f.fake.Calls(), before, "没有用已保存的密码访问新地址")
	})

	t.Run("缺少必填项或凭据", func(t *testing.T) {
		f := setupRepoTest(t)
		cases := []*repoapi.CreateRequest{
			{Name: "", Type: repoapi.TypeLocal, Params: repoapi.Params{Path: "/mnt/x"}, RecoveryKey: no()},
			{Name: "x", Type: "ftp", RecoveryKey: no()},
			{Name: "x", Type: repoapi.TypeLocal, Params: repoapi.Params{Path: "relative"}, RecoveryKey: no()},
			{Name: "x", Type: repoapi.TypeS3, Params: repoapi.Params{Endpoint: "http://nas", Bucket: "b", AccessKeyID: "a"}, RecoveryKey: no()},
			{Name: "x", Type: repoapi.TypeSFTP, Params: repoapi.Params{Host: "nas", Username: "u", Path: "/r", Password: "p"}, RecoveryKey: no()},
			{Name: "x", Type: repoapi.TypeS3, Params: repoapi.Params{Endpoint: "http://nas", Bucket: "b", AccessKeyID: "a", SecretAccessKey: secret.Masked}, RecoveryKey: no()},
		}
		for i, c := range cases {
			_, err := f.svc.Create(f.ctx, c)
			assert.Error(t, err, i)
		}
		assert.Empty(t, f.fake.Calls())
		f.assertNoRepos(t)
	})
}

// gatedRunner 在 gate 关闭前阻塞每一次执行，用来让后台维护停在进行中
type gatedRunner struct {
	executor.Runner
	gate chan struct{}
}

func (g *gatedRunner) Run(ctx context.Context, c cmdbuild.Cmd, out io.Writer) (int, error) {
	<-g.gate
	return g.Runner.Run(ctx, c, out)
}

func TestDeleteRefusesWhenUsed(t *testing.T) {
	f, id, _ := rotateFixture(t)
	RegisterJobCounter(func(ctx context.Context) (map[int64]int, error) { return map[int64]int{id: 2}, nil })
	err := f.svc.Delete(f.ctx, id)
	assert.Equal(t, code.RepoInUse, codeOf(t, err))
	RegisterJobCounter(nil)
	require.NoError(t, f.svc.Delete(f.ctx, id))
	_, err = f.svc.Get(f.ctx, id)
	assert.Equal(t, code.RepoNotFound, codeOf(t, err))
}

func TestBucketWatch(t *testing.T) {
	fired := 0
	w := &bucketWatch{onMissing: func() { fired++ }}
	_, _ = io.WriteString(w, "Load(<config/0000000000>, 0, 0) returned error, retrying after 1s: The specified bu")
	assert.Zero(t, fired, "关键字还不完整")
	_, _ = io.WriteString(w, "cket does not exist\n")
	assert.Equal(t, 1, fired, "关键字拆在两次写入之间也能认出")
	_, _ = io.WriteString(w, bucketMissingRetry)
	assert.Equal(t, 1, fired, "只通知一次")

	other := &bucketWatch{onMissing: func() { t.Fatal("不是 bucket 不存在") }}
	_, _ = io.WriteString(other, "Fatal: unable to open config file: Stat: The specified key does not exist.\n")
}
