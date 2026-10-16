package snapshot_ctr

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
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

	snapapi "github.com/opskat/opsnap/internal/api/snapshot"
	"github.com/opskat/opsnap/internal/model/entity/job_entity"
	"github.com/opskat/opsnap/internal/model/entity/repo_entity"
	"github.com/opskat/opsnap/internal/model/entity/run_entity"
	"github.com/opskat/opsnap/internal/pkg/cmdbuild"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/executor"
	"github.com/opskat/opsnap/internal/pkg/repolock"
	"github.com/opskat/opsnap/internal/pkg/secret"
	"github.com/opskat/opsnap/internal/repository/job_repo"
	"github.com/opskat/opsnap/internal/repository/repo_repo"
	"github.com/opskat/opsnap/internal/repository/run_repo"
	"github.com/opskat/opsnap/internal/repository/server_repo"
	"github.com/opskat/opsnap/internal/repository/setting_repo"
	"github.com/opskat/opsnap/internal/service/repo_svc"
	"github.com/opskat/opsnap/internal/service/snapshot_svc"
	"github.com/opskat/opsnap/migrations"
)

const (
	snapA = "aaaaaaaa11112222333344445555666677778888999900001111222233334444"
	snapB = "bbbbbbbb11112222333344445555666677778888999900001111222233334444"
)

type env struct {
	ctx      context.Context
	fake     *executor.Fake
	mux      *muxtest.TestMux
	srv      *httptest.Server
	repoID   int64
	fileJob  int64
	mysqlJob int64
	pgJob    int64
	marker   string
}

func setup(t *testing.T) *env {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, migrations.RunMigrations(gdb))
	s, err := secret.New("snapshot-ctr-test-key")
	require.NoError(t, err)
	secret.SetDefault(s)
	t.Cleanup(func() { secret.SetDefault(nil) })

	db.SetDefault(gdb)
	job_repo.RegisterJob(job_repo.NewJob())
	repo_repo.RegisterRepo(repo_repo.NewRepo())
	server_repo.RegisterServer(server_repo.NewServer())
	setting_repo.RegisterSetting(setting_repo.NewSetting())
	run_repo.RegisterRun(run_repo.NewRun())
	fake := executor.NewFake()
	repo_svc.RegisterRepo(repo_svc.NewRepo(fake, time.Now))
	snapshot_svc.RegisterSnapshot(snapshot_svc.NewSnapshot(snapshot_svc.Deps{Runner: fake}))
	ctx := db.WithContextDB(context.Background(), gdb)

	dir := t.TempDir()
	marker := filepath.Join(dir, repo_entity.MarkerFile)
	require.NoError(t, os.WriteFile(marker, nil, 0o600))
	key, err := secret.Default().Encrypt("SECRET-REPO-KEY")
	require.NoError(t, err)
	rp := &repo_entity.Repo{Name: "nas", Type: repo_entity.TypeLocal, Params: repo_entity.Params{Path: dir}, OpsnapKey: key, Status: repo_entity.StatusOK, FreeSpace: -1}
	require.NoError(t, repo_repo.Repo().Create(ctx, rp))
	mk := func(name, src string) int64 {
		j := &job_entity.Job{
			Name: name, SourceType: src, ConnMode: job_entity.ConnDirect, DBHost: "10.0.0.1", DBPort: 3306,
			RepoID: rp.ID, ScheduleKind: "daily", ScheduleTime: "03:00", RetentionPreset: "long",
			TimeoutSec: 3600, Compression: "auto", Databases: []string{}, Paths: []string{}, Excludes: []string{}, ExtraArgs: []string{},
		}
		require.NoError(t, job_repo.Job().Create(ctx, j))
		return j.ID
	}
	e := &env{ctx: ctx, fake: fake, repoID: rp.ID, marker: marker}
	e.fileJob = mk("站点文件", job_entity.SourceFiles)
	e.mysqlJob = mk("订单库", job_entity.SourceMySQL)
	e.pgJob = mk("报表库", job_entity.SourcePostgres)

	e.mux = muxtest.NewTestMux()
	ctr := NewSnapshot()
	e.mux.Group("/api/v1").Bind(ctr.List, ctr.Files, ctr.Exports, ctr.Download, ctr.CheckDownload)
	engine, ok := e.mux.IRouter.(*gin.Engine)
	require.True(t, ok)
	e.srv = httptest.NewServer(engine)
	t.Cleanup(e.srv.Close)
	return e
}

func codeOf(t *testing.T, err error) int {
	t.Helper()
	var he *httputils.Error
	require.ErrorAs(t, err, &he, "%v", err)
	return he.Code
}

// 仅匹配 restic 的某个子命令且最后一个参数等于 last
func resticOn(sub, last string) func(cmdbuild.Cmd) bool {
	return func(c cmdbuild.Cmd) bool {
		return c.Name == "restic" && len(c.Args) > 0 && c.Args[0] == sub && c.Args[len(c.Args)-1] == last
	}
}

func resticSub(sub string) func(cmdbuild.Cmd) bool { return executor.Match("restic", sub) }

type tarFile struct {
	name string
	data []byte
	dir  bool
}

func makeTar(t *testing.T, files ...tarFile) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, f := range files {
		h := &tar.Header{Name: f.name, Mode: 0o644, Size: int64(len(f.data)), ModTime: time.Unix(1700000000, 0), Typeflag: tar.TypeReg}
		if f.dir {
			h.Typeflag, h.Size, h.Mode = tar.TypeDir, 0, 0o755
		}
		require.NoError(t, tw.WriteHeader(h))
		if !f.dir {
			_, err := tw.Write(f.data)
			require.NoError(t, err)
		}
	}
	require.NoError(t, tw.Close())
	return buf.Bytes()
}

func snapshotsJSON(jobID int64, ids ...string) string {
	return fmt.Sprintf(`[{"id":%q,"short_id":%q,"time":"2026-10-14T03:00:00Z","paths":["/data/mirror/%d"],"hostname":"opsnap","tags":["job:%d","run:11"],"summary":{"data_added":1000,"total_bytes_processed":9000}},`+
		`{"id":%q,"short_id":%q,"time":"2026-10-13T03:00:00Z","paths":["/data/mirror/%d"],"hostname":"opsnap","tags":["job:%d","run:10"]}]`,
		snapA, snapA[:8], jobID, jobID, snapB, snapB[:8], jobID, jobID)
}

func oneSnapshot(jobID int64) string {
	return fmt.Sprintf(`[{"id":%q,"short_id":%q,"time":"2026-10-14T03:00:00Z","paths":["/x"],"hostname":"opsnap","tags":["job:%d","run:11"]}]`, snapA, snapA[:8], jobID)
}

const lsHeader = `{"message_type":"snapshot","id":"` + snapA + `","short_id":"aaaaaaaa"}` + "\n"

func lsNode(typ, p string, size int64) string {
	return fmt.Sprintf(`{"name":%q,"type":%q,"path":%q,"size":%d,"mtime":"2026-10-01T08:00:00Z","struct_type":"node"}`+"\n", filepath.Base(p), typ, p, size)
}

func TestListSnapshots(t *testing.T) {
	e := setup(t)
	// 运行 10 只有一个快照：新增量取运行记录
	require.NoError(t, run_repo.Run().Create(e.ctx, &run_entity.Run{
		ID: 10, JobID: e.fileJob, JobName: "站点文件", Trigger: "schedule", Status: "success", BytesAdded: 5000,
		SnapshotIDs: []string{snapB[:8]}, Steps: []run_entity.Step{}, Log: []run_entity.LogLine{},
	}))
	e.fake.On(resticSub("snapshots"), executor.Step{Output: snapshotsJSON(e.fileJob)})
	e.fake.On(resticSub("forget"), executor.Step{Output: fmt.Sprintf(`[{"tags":["job:%d"],"host":"opsnap","paths":["/data/mirror/%d"],`+
		`"keep":[{"id":%q},{"id":%q}],"remove":null,"reasons":[`+
		`{"snapshot":{"id":%q},"matches":["last snapshot","daily snapshot","weekly snapshot"],"counters":{"daily":6}},`+
		`{"snapshot":{"id":%q},"matches":["daily snapshot"],"counters":{"daily":5}}]}]`, e.fileJob, e.fileJob, snapA, snapB, snapA, snapB)})

	resp := &snapapi.ListResponse{}
	require.NoError(t, e.mux.Do(e.ctx, &snapapi.ListRequest{ID: e.fileJob}, resp))
	require.Len(t, resp.Snapshots, 2)
	a, b := resp.Snapshots[0], resp.Snapshots[1]
	assert.Equal(t, snapA, a.ID)
	assert.Equal(t, "aaaaaaaa", a.ShortID)
	assert.Greater(t, a.Time, b.Time, "按时间倒序")
	assert.Equal(t, int64(1000), a.BytesAdded, "restic 记录了本快照新增量时取它")
	assert.Equal(t, int64(11), a.RunID)
	assert.Equal(t, []string{"last", "daily", "weekly"}, a.Reasons)
	assert.Equal(t, int64(5000), b.BytesAdded, "没有 summary 时取运行记录的新增量")
	assert.Equal(t, []string{"daily"}, b.Reasons)
	assert.Equal(t, "files", resp.SourceType)

	var forget cmdbuild.Cmd
	var snaps cmdbuild.Cmd
	for _, c := range e.fake.Calls() {
		if len(c.Args) > 0 && c.Args[0] == "forget" {
			forget = c
		}
		if len(c.Args) > 0 && c.Args[0] == "snapshots" {
			snaps = c
		}
	}
	assert.Contains(t, snaps.Args, fmt.Sprintf("job:%d", e.fileJob), "只列本任务的快照")
	assert.Contains(t, forget.Args, "--dry-run", "只演算，不能真的删除")
	assert.NotContains(t, forget.Args, "--prune")
	assert.Contains(t, forget.Args, "--keep-daily", "演算用任务自己的保留规则")

	t.Run("多个快照的运行无法拆分新增量", func(t *testing.T) {
		e2 := setup(t)
		require.NoError(t, run_repo.Run().Create(e2.ctx, &run_entity.Run{
			ID: 10, JobID: e2.fileJob, JobName: "x", Trigger: "schedule", Status: "success", BytesAdded: 5000,
			SnapshotIDs: []string{snapB[:8], "cccccccc"}, Steps: []run_entity.Step{}, Log: []run_entity.LogLine{},
		}))
		e2.fake.On(resticSub("snapshots"), executor.Step{Output: snapshotsJSON(e2.fileJob)})
		e2.fake.On(resticSub("forget"), executor.Step{ExitCode: 1, Stderr: "boom"})
		r := &snapapi.ListResponse{}
		require.NoError(t, e2.mux.Do(e2.ctx, &snapapi.ListRequest{ID: e2.fileJob}, r))
		assert.Equal(t, int64(-1), r.Snapshots[1].BytesAdded)
		assert.Empty(t, r.Snapshots[0].Reasons, "演算失败只丢掉保留原因，不影响列表")
	})

	t.Run("仓库被锁时返回错误", func(t *testing.T) {
		e2 := setup(t)
		e2.fake.On(resticSub("snapshots"), executor.Step{ExitCode: 11, Stderr: "Fatal: unable to create lock in backend: repository is already locked exclusively by PID 9"})
		err := e2.mux.Do(e2.ctx, &snapapi.ListRequest{ID: e2.fileJob}, &snapapi.ListResponse{})
		assert.Equal(t, code.SnapshotRepoLocked, codeOf(t, err))
	})

	t.Run("任务不存在", func(t *testing.T) {
		err := e.mux.Do(e.ctx, &snapapi.ListRequest{ID: 999}, &snapapi.ListResponse{})
		assert.Equal(t, code.JobNotFound, codeOf(t, err))
	})
}

func TestAmbiguousSnapshotPrefix(t *testing.T) {
	e := setup(t)
	twin := "aaaaaaaa99998888777766665555444433332222111100009999888877776666"
	e.fake.On(resticSub("snapshots"), executor.Step{Output: fmt.Sprintf(
		`[{"id":%q,"short_id":"aaaaaaaa","time":"2026-10-14T03:00:00Z","paths":["/x"],"tags":["job:%d"]},`+
			`{"id":%q,"short_id":"aaaaaaaa","time":"2026-10-13T03:00:00Z","paths":["/x"],"tags":["job:%d"]}]`,
		snapA, e.fileJob, twin, e.fileJob)})
	// 前缀同时对上两个快照时不能随便挑一个：下载或浏览的可能不是用户要的那个
	err := e.mux.Do(e.ctx, &snapapi.FilesRequest{ID: e.fileJob, SID: "aaaaaaaa", Path: "/x"}, &snapapi.FilesResponse{})
	assert.Equal(t, code.SnapshotNotFound, codeOf(t, err))
	for _, c := range e.fake.Calls() {
		assert.NotEqual(t, "ls", c.Args[0], "没有读取任何一个快照")
	}
}

func TestListFiles(t *testing.T) {
	e := setup(t)
	e.fake.On(resticSub("snapshots"), executor.Step{Output: oneSnapshot(e.fileJob)})
	e.fake.On(resticOn("ls", "/data/site"), executor.Step{Output: lsHeader +
		lsNode("dir", "/data/site", 0) +
		lsNode("dir", "/data/site/assets", 0) +
		lsNode("file", "/data/site/index.html", 2048) +
		lsNode("file", "/data/site/assets/deep.js", 7)})
	resp := &snapapi.FilesResponse{}
	require.NoError(t, e.mux.Do(e.ctx, &snapapi.FilesRequest{ID: e.fileJob, SID: snapA, Path: "/data/site"}, resp))
	require.Len(t, resp.Entries, 2, "只返回直接子项，不含目录自身和更深层")
	assert.Equal(t, "assets", resp.Entries[0].Name, "目录排在文件前")
	assert.Equal(t, "dir", resp.Entries[0].Type)
	assert.Equal(t, "/data/site/index.html", resp.Entries[1].Path)
	assert.Equal(t, int64(2048), resp.Entries[1].Size)
	assert.Equal(t, time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC).Unix(), resp.Entries[1].MTime)

	err := e.mux.Do(e.ctx, &snapapi.FilesRequest{ID: e.fileJob, SID: "--bad", Path: "/"}, &snapapi.FilesResponse{})
	assert.Equal(t, code.SnapshotNotFound, codeOf(t, err), "非法快照 ID 不会交给 restic")
	err = e.mux.Do(e.ctx, &snapapi.FilesRequest{ID: e.fileJob, SID: snapA, Path: "relative"}, &snapapi.FilesResponse{})
	assert.Equal(t, code.SnapshotPathNotFound, codeOf(t, err))
}

// dlResult 一次下载的结果：响应头，读完正文得到的字节，以及读取过程中的错误（连接被中止时非空）
type dlResult struct {
	Status int
	Header http.Header
	Body   []byte
	Err    error
}

func (e *env) download(t *testing.T, jobID int64, sid string, gz bool, paths ...string) *dlResult {
	t.Helper()
	return e.fetch(t, "download", jobID, sid, gz, paths...)
}

func (e *env) fetch(t *testing.T, action string, jobID int64, sid string, gz bool, paths ...string) *dlResult {
	t.Helper()
	q := url.Values{}
	for _, p := range paths {
		q.Add("path", p)
	}
	if gz {
		q.Set("gzip", "true")
	}
	u := fmt.Sprintf("%s/api/v1/jobs/%d/snapshots/%s/%s?%s", e.srv.URL, jobID, sid, action, q.Encode())
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, u, nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, readErr := io.ReadAll(resp.Body)
	return &dlResult{Status: resp.StatusCode, Header: resp.Header, Body: body, Err: readErr}
}

func jsonCode(t *testing.T, r *dlResult) int {
	t.Helper()
	assert.Contains(t, r.Header.Get("Content-Type"), "application/json", "失败必须是 JSON，不能是半截文件")
	assert.Empty(t, r.Header.Get("Content-Disposition"))
	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &out), string(r.Body))
	return out.Code
}

func (e *env) prepareFileDownload(t *testing.T) {
	t.Helper()
	e.fake.On(resticSub("snapshots"), executor.Step{Output: oneSnapshot(e.fileJob)})
	e.fake.On(resticOn("ls", "/data/a"), executor.Step{Output: lsHeader + lsNode("dir", "/data/a", 0)})
	e.fake.On(resticOn("ls", "/data/b.txt"), executor.Step{Output: lsHeader + lsNode("file", "/data/b.txt", 3)})
}

func dumpCalls(f *executor.Fake) []cmdbuild.Cmd {
	var out []cmdbuild.Cmd
	for _, c := range f.Calls() {
		if c.Name == "restic" && len(c.Args) > 0 && c.Args[0] == "dump" {
			out = append(out, c)
		}
	}
	return out
}

func TestDownloadTarGz(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	e := setup(t)
	e.prepareFileDownload(t)
	e.fake.On(resticOn("dump", "/data/a"), executor.Step{Output: string(makeTar(t,
		tarFile{name: "data/a/", dir: true}, tarFile{name: "data/a/x.txt", data: []byte("hello")}))})
	e.fake.On(resticOn("dump", "/data/b.txt"), executor.Step{Output: string(makeTar(t,
		tarFile{name: "data/b.txt", data: []byte("bee")}))})

	resp := e.download(t, e.fileJob, snapA, false, "/data/a", "/data/b.txt", "/data/a/x.txt")
	require.Equal(t, http.StatusOK, resp.Status)
	require.NoError(t, resp.Err)
	assert.Contains(t, resp.Header.Get("Content-Disposition"), "attachment")
	assert.Contains(t, resp.Header.Get("Content-Disposition"), ".tar.gz")
	assert.Equal(t, "application/gzip", resp.Header.Get("Content-Type"))

	gz, err := gzip.NewReader(bytes.NewReader(resp.Body))
	require.NoError(t, err)
	tr := tar.NewReader(gz)
	got := map[string]string{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		b, err := io.ReadAll(tr)
		require.NoError(t, err)
		got[h.Name] = string(b)
	}
	assert.Equal(t, "hello", got["data/a/x.txt"])
	assert.Equal(t, "bee", got["data/b.txt"])
	assert.Len(t, dumpCalls(e.fake), 2, "被其他所选目录包含的 /data/a/x.txt 不重复打包")
	for _, c := range dumpCalls(e.fake) {
		assert.Contains(t, c.Args, "--archive")
		assert.Contains(t, c.Args, "tar")
	}
	entries, err := os.ReadDir(tmp)
	require.NoError(t, err)
	assert.Empty(t, entries, "不能落临时文件")
}

func TestDownloadFailsBeforeHeaders(t *testing.T) {
	t.Run("仓库被锁", func(t *testing.T) {
		e := setup(t)
		e.fake.On(resticSub("snapshots"), executor.Step{ExitCode: 11, Stderr: "Fatal: unable to create lock in backend: repository is already locked exclusively"})
		resp := e.download(t, e.fileJob, snapA, false, "/data/a")
		assert.Equal(t, code.SnapshotRepoLocked, jsonCode(t, resp))
		assert.Empty(t, dumpCalls(e.fake))
	})
	t.Run("快照不存在", func(t *testing.T) {
		e := setup(t)
		e.fake.On(resticSub("snapshots"), executor.Step{Output: "[]"})
		resp := e.download(t, e.fileJob, snapA, false, "/data/a")
		assert.Equal(t, code.SnapshotNotFound, jsonCode(t, resp))
		assert.Empty(t, dumpCalls(e.fake))
	})
	t.Run("快照 ID 非法", func(t *testing.T) {
		e := setup(t)
		resp := e.download(t, e.fileJob, "--password-file", false, "/data/a")
		assert.Equal(t, code.SnapshotNotFound, jsonCode(t, resp))
		assert.Empty(t, e.fake.Calls())
	})
	t.Run("路径不存在", func(t *testing.T) {
		e := setup(t)
		e.fake.On(resticSub("snapshots"), executor.Step{Output: oneSnapshot(e.fileJob)})
		e.fake.On(resticSub("ls"), executor.Step{Output: lsHeader})
		resp := e.download(t, e.fileJob, snapA, false, "/data/missing")
		assert.Equal(t, code.SnapshotPathNotFound, jsonCode(t, resp))
		assert.Empty(t, dumpCalls(e.fake))
	})
	t.Run("没有选择路径", func(t *testing.T) {
		e := setup(t)
		resp := e.download(t, e.fileJob, snapA, false)
		assert.Equal(t, code.SnapshotPathNotFound, jsonCode(t, resp))
	})
	t.Run("NAS 没有挂载", func(t *testing.T) {
		e := setup(t)
		require.NoError(t, os.Remove(e.marker))
		resp := e.download(t, e.fileJob, snapA, false, "/data/a")
		assert.Equal(t, code.RepoNotMounted, jsonCode(t, resp))
		assert.Empty(t, e.fake.Calls(), "没有挂载时不运行 restic")
	})
	t.Run("仓库正在维护", func(t *testing.T) {
		e := setup(t)
		release, err := repolock.Acquire(context.Background(), e.repoID, repolock.Exclusive)
		require.NoError(t, err)
		defer release()
		resp := e.download(t, e.fileJob, snapA, false, "/data/a")
		assert.Equal(t, code.RepoBusy, jsonCode(t, resp))
		assert.Empty(t, e.fake.Calls())
	})
	t.Run("restic dump 在输出任何数据前就失败", func(t *testing.T) {
		e := setup(t)
		e.prepareFileDownload(t)
		e.fake.On(resticOn("dump", "/data/a"), executor.Step{ExitCode: 1, Stderr: "Fatal: unable to load blob"})
		resp := e.download(t, e.fileJob, snapA, false, "/data/a")
		assert.Equal(t, code.SnapshotReadFailed, jsonCode(t, resp))
	})
	t.Run("下载期间持有共享锁，结束后释放", func(t *testing.T) {
		e := setup(t)
		probe := &lockProbe{Fake: e.fake, repoID: e.repoID}
		snapshot_svc.RegisterSnapshot(snapshot_svc.NewSnapshot(snapshot_svc.Deps{Runner: probe}))
		e.prepareFileDownload(t)
		e.fake.On(resticOn("dump", "/data/a"), executor.Step{Output: string(makeTar(t, tarFile{name: "a", data: []byte("x")}))})
		resp := e.download(t, e.fileJob, snapA, false, "/data/a")
		require.NoError(t, resp.Err)
		assert.True(t, probe.dumpSeen, "restic dump 确实执行了")
		assert.False(t, probe.exclusiveDuringDump, "dump 期间独占锁被共享锁挡住")
		release, ok := repolock.TryAcquire(e.repoID, repolock.Exclusive)
		require.True(t, ok, "下载结束后锁必须释放")
		release()
	})
}

// lockProbe 在 restic dump 执行期间尝试取独占锁，检验下载持有共享锁
type lockProbe struct {
	*executor.Fake
	repoID              int64
	dumpSeen            bool
	exclusiveDuringDump bool
}

func (p *lockProbe) RunSplit(ctx context.Context, cmd cmdbuild.Cmd, out, errOut io.Writer) (int, error) {
	if len(cmd.Args) > 0 && cmd.Args[0] == "dump" {
		p.dumpSeen = true
		if release, ok := repolock.TryAcquire(p.repoID, repolock.Exclusive); ok {
			p.exclusiveDuringDump = true
			release()
		}
	}
	return p.Fake.RunSplit(ctx, cmd, out, errOut)
}

func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	_, err := rand.Read(b)
	require.NoError(t, err)
	return b
}

func TestDownloadAbortsMidStream(t *testing.T) {
	t.Run("文件快照：数据流中断后连接被中止", func(t *testing.T) {
		e := setup(t)
		e.prepareFileDownload(t)
		full := makeTar(t, tarFile{name: "data/a/big.bin", data: randomBytes(t, 400<<10)})
		// 只给出一部分正文就以非零退出：gzip 已经输出过数据，响应头已发送
		e.fake.On(resticOn("dump", "/data/a"), executor.Step{Output: string(full[:300<<10]), ExitCode: 1, Stderr: "Fatal: pack missing"})
		resp := e.download(t, e.fileJob, snapA, false, "/data/a")
		require.Equal(t, http.StatusOK, resp.Status)
		require.Error(t, resp.Err, "连接被中止，客户端必须读到错误而不是正常结束")
		gz, gzErr := gzip.NewReader(bytes.NewReader(resp.Body))
		if gzErr == nil {
			_, gzErr = io.ReadAll(gz)
		}
		assert.Error(t, gzErr, "收到的内容不是完整的 gzip")
	})
	t.Run("文件快照：第二个路径失败，第一个已完整也不能正常结束", func(t *testing.T) {
		e := setup(t)
		e.prepareFileDownload(t)
		e.fake.On(resticOn("dump", "/data/a"), executor.Step{Output: string(makeTar(t, tarFile{name: "data/a/big.bin", data: randomBytes(t, 400<<10)}))})
		e.fake.On(resticOn("dump", "/data/b.txt"), executor.Step{ExitCode: 1, Stderr: "Fatal: gone"})
		resp := e.download(t, e.fileJob, snapA, false, "/data/a", "/data/b.txt")
		assert.Error(t, resp.Err)
	})
	t.Run("数据库导出：中断后连接被中止", func(t *testing.T) {
		e := setup(t)
		e.fake.On(resticSub("snapshots"), executor.Step{Output: oneSnapshot(e.pgJob)})
		e.fake.On(resticSub("ls"), executor.Step{Output: lsHeader + lsNode("file", "/postgres/app.dump", 400<<10)})
		e.fake.On(resticSub("dump"), executor.Step{Output: string(randomBytes(t, 300<<10)), ExitCode: 1, Stderr: "Fatal: x"})
		resp := e.download(t, e.pgJob, snapA, false, "/postgres/app.dump")
		assert.Error(t, resp.Err)
	})
}

func TestDatabaseExports(t *testing.T) {
	e := setup(t)
	e.fake.On(resticSub("snapshots"), executor.Step{Output: oneSnapshot(e.mysqlJob)})
	e.fake.On(resticSub("ls"), executor.Step{Output: lsHeader + lsNode("dir", "/mysql", 0) + lsNode("file", "/mysql/shop.sql", 4096)})
	resp := &snapapi.ExportsResponse{}
	require.NoError(t, e.mux.Do(e.ctx, &snapapi.ExportsRequest{ID: e.mysqlJob, SID: snapA}, resp))
	require.Len(t, resp.Files, 1)
	f := resp.Files[0]
	assert.Equal(t, "/mysql/shop.sql", f.Path)
	assert.Equal(t, int64(4096), f.Size)
	assert.True(t, f.Gzip)
	assert.Equal(t, "shop.sql.gz", f.Filename)
	assert.Contains(t, f.RestoreCommand, "gunzip < shop.sql.gz | mysql")

	t.Run("文件任务没有导出文件", func(t *testing.T) {
		err := e.mux.Do(e.ctx, &snapapi.ExportsRequest{ID: e.fileJob, SID: snapA}, &snapapi.ExportsResponse{})
		assert.Equal(t, code.SnapshotNotDatabase, codeOf(t, err))
	})
}

func TestDatabaseDownload(t *testing.T) {
	e := setup(t)
	dump := randomBytes(t, 2048)
	e.fake.On(resticSub("snapshots"), executor.Step{Output: oneSnapshot(e.pgJob)})
	e.fake.On(resticOn("ls", "/postgres/app.dump"), executor.Step{Output: lsHeader + lsNode("file", "/postgres/app.dump", 2048)})
	e.fake.On(resticOn("dump", "/postgres/app.dump"), executor.Step{Output: string(dump)})

	resp := e.download(t, e.pgJob, snapA, false, "/postgres/app.dump")
	require.Equal(t, http.StatusOK, resp.Status)
	assert.Contains(t, resp.Header.Get("Content-Disposition"), `filename=app.dump`)
	require.NoError(t, resp.Err)
	assert.Equal(t, dump, resp.Body, "按存储的格式原样下载")
	for _, c := range dumpCalls(e.fake) {
		assert.NotContains(t, c.Args, "--archive", "数据库导出是单个文件，不打包")
	}

	resp = e.download(t, e.pgJob, snapA, true, "/postgres/app.dump")
	require.Equal(t, http.StatusOK, resp.Status)
	assert.Contains(t, resp.Header.Get("Content-Disposition"), "app.dump.gz")
	gz, err := gzip.NewReader(bytes.NewReader(resp.Body))
	require.NoError(t, err)
	plain, err := io.ReadAll(gz)
	require.NoError(t, err)
	assert.Equal(t, dump, plain)

	t.Run("数据库任务一次只能下载一个导出文件", func(t *testing.T) {
		r := e.download(t, e.pgJob, snapA, false, "/postgres/a.dump", "/postgres/b.dump")
		assert.Equal(t, code.SnapshotPathNotFound, jsonCode(t, r))
	})
}

func TestTarEntryNamesAreSafe(t *testing.T) {
	// 备份内容来自线上服务器，条目名不能带着 .. 逃出解压目录
	e := setup(t)
	e.prepareFileDownload(t)
	e.fake.On(resticOn("dump", "/data/a"), executor.Step{Output: string(makeTar(t, tarFile{name: "/data/a/../../etc/x", data: []byte("1")}))})
	resp := e.download(t, e.fileJob, snapA, false, "/data/a")
	gz, err := gzip.NewReader(bytes.NewReader(resp.Body))
	require.NoError(t, err)
	tr := tar.NewReader(gz)
	h, err := tr.Next()
	require.NoError(t, err)
	assert.False(t, strings.HasPrefix(h.Name, "/"))
	assert.NotContains(t, h.Name, "..")
}

func TestCheckDownload(t *testing.T) {
	t.Run("预检通过：不运行 restic dump，返回成功", func(t *testing.T) {
		e := setup(t)
		e.prepareFileDownload(t)
		r := e.fetch(t, "download/check", e.fileJob, snapA, false, "/data/a", "/data/b.txt")
		require.Equal(t, http.StatusOK, r.Status)
		assert.Equal(t, 0, jsonCode(t, r))
		assert.Empty(t, dumpCalls(e.fake), "预检不读取数据")
		release, ok := repolock.TryAcquire(e.repoID, repolock.Exclusive)
		require.True(t, ok, "预检结束后锁必须释放")
		release()
	})
	t.Run("仓库被锁", func(t *testing.T) {
		e := setup(t)
		e.fake.On(resticSub("snapshots"), executor.Step{ExitCode: 11, Stderr: "repository is already locked exclusively"})
		r := e.fetch(t, "download/check", e.fileJob, snapA, false, "/data/a")
		assert.Equal(t, code.SnapshotRepoLocked, jsonCode(t, r))
	})
	t.Run("路径不存在", func(t *testing.T) {
		e := setup(t)
		e.fake.On(resticSub("snapshots"), executor.Step{Output: oneSnapshot(e.fileJob)})
		e.fake.On(resticSub("ls"), executor.Step{Output: lsHeader})
		r := e.fetch(t, "download/check", e.fileJob, snapA, false, "/gone")
		assert.Equal(t, code.SnapshotPathNotFound, jsonCode(t, r))
	})
}
