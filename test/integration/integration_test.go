//go:build integration

package integration

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

const adminUser = "it-admin"

// suite 各场景共用的环境：已登录的客户端、经两跳代理链的目标服务器、本地目录仓库
type suite struct {
	api      *apiClient
	jump     *serverItem
	target   *serverItem
	local    *repoItem
	localDir string
	localKey string // 本地仓库的恢复密钥：测试用它独立打开仓库，核对 opsnap 之外看到的快照
}

// login 首次运行时创建管理员，然后登录
func login(t testing.TB) *apiClient {
	t.Helper()
	api := newAPIClient(t, env(t, "OPSNAP_IT_URL"))
	var status struct {
		Initialized bool `json:"initialized"`
	}
	waitFor(t, "opsnap 启动", 3*time.Minute, func(context.Context) error {
		return api.call(http.MethodGet, "/auth/status", nil, &status)
	})
	creds := map[string]string{"username": adminUser, "password": env(t, "IT_ADMIN_PASSWORD")}
	if !status.Initialized {
		api.mustCall(t, http.MethodPost, "/auth/setup", creds, nil)
	}
	api.mustCall(t, http.MethodPost, "/auth/login", creds, nil)
	return api
}

// TestIntegration 主流程：每种数据源 备份 → 快照 → 下载 → 与源数据一致，以及各失败场景。
// 子测试共用同一套服务器和仓库，按顺序执行。
func TestIntegration(t *testing.T) {
	s := &suite{api: login(t)}

	t.Log("写入种子数据")
	seedFiles(t)
	seedMySQL(t)
	seedPostgres(t)
	seedRedis(t)
	seedMongo(t)

	s.setupServers(t)
	s.local, s.localDir, s.localKey = s.newLocalRepo(t, "it-local", "main")

	t.Run("chain", s.testChain)
	t.Run("mysql", s.testMySQL)
	t.Run("postgres", s.testPostgres)
	t.Run("redis", s.testRedis)
	t.Run("mongodb", s.testMongo)
	t.Run("files", s.testFiles)
	t.Run("midway_failure", s.testMidwayFailure)
	t.Run("missing_marker", s.testMissingMarker)
	t.Run("sftp", s.testSFTP)
}

func socksHop(t testing.TB) hopInput {
	return hopInput{Type: "socks5", Host: hostSOCKS, Port: 1080, Username: env(t, "IT_SOCKS_USER"), Password: env(t, "IT_SOCKS_PASSWORD")}
}

// confirmFingerprint 走面板的两步流程：先测试拿到主机出示的指纹（不发送凭据），核对后带着指纹保存
func (s *suite) confirmFingerprint(t testing.TB, in serverInput) *serverItem {
	t.Helper()
	var probe serverTest
	s.api.mustCall(t, http.MethodPost, "/servers/test", in, &probe)
	if probe.OK || probe.Fingerprint == "" {
		t.Fatalf("首次测试 %s 应停在指纹待核对并返回指纹：%+v", in.Name, hopsString(probe.Hops))
	}
	last := probe.Hops[len(probe.Hops)-1]
	if last.Reason != "fingerprint_unconfirmed" {
		t.Fatalf("首次测试 %s 的最后一跳应是 fingerprint_unconfirmed：%s", in.Name, hopsString(probe.Hops))
	}
	in.HostKeyFingerprint = probe.Fingerprint
	var srv serverItem
	s.api.mustCall(t, http.MethodPost, "/servers", in, &srv)
	return &srv
}

// setupServers 跳板 jump 只能经 SOCKS5 代理到达；目标 target 只能经 SOCKS5 → jump 到达
func (s *suite) setupServers(t testing.TB) {
	t.Helper()
	s.jump = s.confirmFingerprint(t, serverInput{
		Name: "it-jump", Host: hostJump, Port: 22, Username: "itjump", AuthType: "password",
		Password: env(t, "IT_JUMP_PASSWORD"), Hops: []hopInput{socksHop(t)},
	})
	s.target = s.confirmFingerprint(t, targetInput(t, s.jump.ID))
}

func targetInput(t testing.TB, jumpID int64) serverInput {
	return serverInput{
		Name: "it-target", Host: hostTarget, Port: 22, Username: "ittarget", AuthType: "password",
		Password: env(t, "IT_TARGET_PASSWORD"),
		Hops:     []hopInput{socksHop(t), {Type: "ssh", ServerID: jumpID}},
	}
}

func hopsString(hops []*hopResult) string {
	b, _ := json.Marshal(hops)
	return string(b)
}

// newLocalRepo 在共享卷 /nas 下建一个空目录（属主改成 opsnap 用户），经 API 初始化为本地目录仓库并加入恢复密钥
func (s *suite) newLocalRepo(t testing.TB, name, dir string) (*repoItem, string, string) {
	t.Helper()
	path := filepath.Join(nasDir, dir)
	check(t, os.MkdirAll(path, 0o700), "创建仓库目录")
	check(t, os.Chown(path, opsnapUID, opsnapUID), "仓库目录交给 opsnap 用户")
	key := s.recoveryKey(t)
	var repo repoCreated
	s.api.mustCall(t, http.MethodPost, "/repos", repoCreate{
		Name: name, Type: "local", Params: repoParams{Path: path},
		RecoveryKeyValue: key, RecoveryKeySaved: true,
	}, &repo)
	if repo.RecoveryKeyError != "" {
		t.Fatalf("恢复密钥没有加入仓库 %s：%s", name, repo.RecoveryKeyError)
	}
	if _, err := os.Stat(filepath.Join(path, ".opsnap-repo")); err != nil {
		t.Fatalf("初始化后应写入标记文件：%v", err)
	}
	return &repo.repoItem, path, key
}

func (s *suite) recoveryKey(t testing.TB) string {
	t.Helper()
	var out struct {
		Value string `json:"recovery_key_value"`
	}
	s.api.mustCall(t, http.MethodPost, "/repos/recovery-key", struct{}{}, &out)
	if out.Value == "" {
		t.Fatal("没有拿到恢复密钥")
	}
	return out.Value
}

// testChain 两跳代理链：经 SOCKS5 → jump 连到 target，每一跳都通；不经代理链时 opsnap 到不了 jump 和 target
func (s *suite) testChain(t *testing.T) {
	var res serverTest
	s.api.mustCall(t, http.MethodPost, fmt.Sprintf("/servers/%d/test", s.target.ID), struct{}{}, &res)
	if !res.OK || len(res.Hops) != 3 {
		t.Fatalf("经两跳代理链连接 target 应成功且有 3 段结果：%s", hopsString(res.Hops))
	}
	wantTypes := []string{"socks5", "ssh", "ssh"}
	for i, h := range res.Hops {
		if !h.OK || h.Type != wantTypes[i] {
			t.Fatalf("第 %d 段应为成功的 %s：%s", i+1, wantTypes[i], hopsString(res.Hops))
		}
	}
	if res.Fingerprint != s.target.HostKeyFingerprint {
		t.Fatalf("target 出示的指纹 %s 与保存的 %s 不一致", res.Fingerprint, s.target.HostKeyFingerprint)
	}

	// 隔离性：去掉代理链直连，jump 与 target 都到不了，说明上面确实走了两跳
	for _, in := range []serverInput{
		{Name: "it-jump-direct", Host: hostJump, Port: 22, Username: "itjump", AuthType: "password", Password: env(t, "IT_JUMP_PASSWORD"), HostKeyFingerprint: s.jump.HostKeyFingerprint},
		{Name: "it-target-direct", Host: hostTarget, Port: 22, Username: "ittarget", AuthType: "password", Password: env(t, "IT_TARGET_PASSWORD"), HostKeyFingerprint: s.target.HostKeyFingerprint},
	} {
		var direct serverTest
		s.api.mustCall(t, http.MethodPost, "/servers/test", in, &direct)
		if direct.OK {
			t.Fatalf("opsnap 不应能直连 %s：%s", in.Host, hopsString(direct.Hops))
		}
	}
}

// dbJob 经 SSH（两跳代理链 → target → 数据库）的数据库任务
func (s *suite) dbJob(name, source, host string, port int, user, password string, dbs []string) jobInput {
	return jobInput{
		Name: name, SourceType: source, ServerID: s.target.ID, ConnMode: "ssh",
		DBHost: host, DBPort: port, DBUser: user, DBPassword: password, Databases: dbs, RepoID: s.local.ID,
	}
}

// backupOne 运行一次任务，确认恰好生成一个属于这次运行的快照，返回快照 ID
func (s *suite) backupOne(t *testing.T, jobID int64) (*runItem, string) {
	t.Helper()
	run := s.api.runJob(t, jobID)
	s.api.mustSucceed(t, run)
	snaps := s.api.snapshotsOfRun(t, jobID, run.ID)
	if len(snaps) != 1 {
		t.Fatalf("运行 %d 应恰好生成 1 个快照，实际 %d 个", run.ID, len(snaps))
	}
	return run, snaps[0].ID
}

// downloadExport 快照里只有一个导出文件，经下载接口原样取回；支持 gzip 的再取一次压缩版，解压后必须相同
func (s *suite) downloadExport(t *testing.T, jobID int64, sid, wantSuffix string) []byte {
	t.Helper()
	files := s.api.exports(t, jobID, sid)
	if len(files) != 1 || !strings.HasSuffix(files[0].Path, wantSuffix) {
		b, _ := json.Marshal(files)
		t.Fatalf("快照 %s 应恰好有一个 *%s 导出：%s", sid, wantSuffix, b)
	}
	f := files[0]
	raw := s.api.downloadSnapshot(t, jobID, sid, []string{f.Path}, false)
	if int64(len(raw)) != f.Size || len(raw) == 0 {
		t.Fatalf("下载的 %s 长度 %d，快照里记录的大小 %d", f.Path, len(raw), f.Size)
	}
	if f.Gzip {
		gz := s.api.downloadSnapshot(t, jobID, sid, []string{f.Path}, true)
		zr, err := gzip.NewReader(bytes.NewReader(gz))
		check(t, err, "gzip 下载不是 gzip")
		plain, err := io.ReadAll(zr)
		check(t, err, "解压 gzip 下载")
		if !bytes.Equal(plain, raw) {
			t.Fatalf("gzip 下载解压后与原样下载不一致（%d / %d 字节）", len(plain), len(raw))
		}
	}
	t.Logf("下载 %s：%d 字节", f.Path, len(raw))
	return raw
}

// sameContent 逐项比较源与恢复后的语义内容；minRows 防止两边都为空时误判为一致
func sameContent(t *testing.T, want, got map[string][]string, minRows map[string]int) {
	t.Helper()
	for k, n := range minRows {
		if len(want[k]) < n {
			t.Fatalf("源数据 %s 只有 %d 项，少于种子的 %d 项", k, len(want[k]), n)
		}
	}
	keys := make([]string, 0, len(want)+len(got))
	for k := range want {
		keys = append(keys, k)
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	diffs := 0
	for _, k := range keys {
		w, g := want[k], got[k]
		if len(w) != len(g) {
			t.Errorf("%s：源 %d 项，恢复后 %d 项", k, len(w), len(g))
			diffs++
			continue
		}
		for i := range w {
			if w[i] != g[i] {
				t.Errorf("%s 第 %d 项不同：\n源    %.200q\n恢复后 %.200q", k, i, w[i], g[i])
				diffs++
				break
			}
		}
	}
	if diffs > 0 {
		t.FailNow()
	}
	t.Logf("内容一致：%d 组", len(keys))
}

func (s *suite) testMySQL(t *testing.T) {
	job := s.api.newJob(t, s.dbJob("it-mysql", "mysql", hostMySQL, 3306, "root", env(t, "IT_MYSQL_PASSWORD"), []string{mysqlDB}))
	_, sid := s.backupOne(t, job.ID)
	dump := s.downloadExport(t, job.ID, sid, ".sql")
	restoreMySQL(t, dump)
	sameContent(t, mysqlContent(t, hostMySQL), mysqlContent(t, hostMySQLScratch),
		map[string]int{"rows:customers": 300, "rows:orders": 800, "rows:order_audit": 800, "views": 1, "triggers": 1, "routines": 1})
}

func (s *suite) testPostgres(t *testing.T) {
	job := s.api.newJob(t, s.dbJob("it-postgres", "postgres", hostPostgres, 5432, "postgres", env(t, "IT_PG_PASSWORD"), []string{pgDB}))
	_, sid := s.backupOne(t, job.ID)
	dump := s.downloadExport(t, job.ID, sid, ".dump")
	restorePostgres(t, dump)
	sameContent(t, pgContent(t, hostPostgres), pgContent(t, hostPostgresScratch),
		map[string]int{"rows:public.products": 400, "rows:public.stock_moves": 1500, "rows:archive.old_products": 60, "views": 1, "functions": 1})
}

func (s *suite) testRedis(t *testing.T) {
	in := s.dbJob("it-redis", "redis", hostRedis, 6379, "", env(t, "IT_REDIS_PASSWORD"), nil)
	job := s.api.newJob(t, in)
	_, sid := s.backupOne(t, job.ID)
	rdb := s.downloadExport(t, job.ID, sid, ".rdb")
	restoreRedis(t, rdb)
	sameContent(t, redisContent(t, hostRedis, env(t, "IT_REDIS_PASSWORD")), redisContent(t, hostRedisScratch, ""),
		map[string]int{"db0:keys": 10, "db3:keys": 50})
}

func (s *suite) testMongo(t *testing.T) {
	in := s.dbJob("it-mongodb", "mongodb", hostMongo, 27017, "root", env(t, "IT_MONGO_PASSWORD"), []string{mongoDB})
	in.AuthDB = "admin"
	job := s.api.newJob(t, in)
	_, sid := s.backupOne(t, job.ID)
	archive := s.downloadExport(t, job.ID, sid, ".archive")
	restoreMongo(t, archive)
	sameContent(t, mongoContent(t, hostMongo, true), mongoContent(t, hostMongoScratch, false),
		map[string]int{"docs:customers": 400, "docs:events": 1200, "indexes:customers": 2, "indexes:events": 2})
}

func (s *suite) testFiles(t *testing.T) {
	job := s.api.newJob(t, jobInput{
		Name: "it-files", SourceType: "files", ServerID: s.target.ID,
		Paths: []string{targetFilesDir}, Excludes: []string{filesExclude}, RepoID: s.local.ID,
	})
	_, sid := s.backupOne(t, job.ID)
	var snap *snapshotItem
	for _, sn := range s.api.snapshots(t, job.ID).Snapshots {
		if sn.ID == sid {
			snap = sn
		}
	}
	if snap == nil || len(snap.Paths) == 0 {
		t.Fatalf("快照 %s 没有记录路径", sid)
	}
	tgz := s.api.downloadSnapshot(t, job.ID, sid, snap.Paths, false)
	got := tarTree(t, tgz)
	want := seedTree(t)
	if _, ok := got["logs/app.log"]; ok {
		t.Fatal("排除规则 *.log 没有生效：logs/app.log 出现在快照里")
	}
	if len(want) < 10 {
		t.Fatalf("种子目录条目太少：%d", len(want))
	}
	toLines := func(m map[string]fileEntry) map[string][]string {
		out := map[string][]string{}
		for k, v := range m {
			out[k] = []string{v.String()}
		}
		return out
	}
	sameContent(t, toLines(want), toLines(got), nil)
}

// testMidwayFailure 导出中途失败：shop 先成功生成快照，shop_broken 导出了部分表数据后遇到失效视图，mysqldump 非零退出。
// 整次运行必须失败，且不留下任何快照（包括 shop 那个已经生成的）——opsnap 列表和独立用 restic 打开仓库都要看不到。
func (s *suite) testMidwayFailure(t *testing.T) {
	job := s.api.newJob(t, s.dbJob("it-midway", "mysql", hostMySQL, 3306, "root", env(t, "IT_MYSQL_PASSWORD"), []string{mysqlDB, mysqlBrokenDB}))
	run := s.api.runJob(t, job.ID)
	log := s.api.runLog(t, run.ID)
	if run.Status != "failed" {
		t.Fatalf("导出中途失败时运行应为 failed，实际 %s；日志：\n%s", run.Status, log)
	}
	// 证明是「中途」失败：mysqldump 已输出 a_items 的数据，随后在失效视图上报 1356 并非零退出；
	// 此前 shop 已生成的快照被回滚删除
	for _, want := range []string{"导出库 " + mysqlBrokenDB, "v_broken", "(1356)", "已删除它留下的快照"} {
		if !strings.Contains(log, want) {
			t.Fatalf("日志里应包含 %q：\n%s", want, log)
		}
	}
	t.Logf("失败原因：%s", run.Reason)
	if n := len(s.api.snapshots(t, job.ID).Snapshots); n != 0 {
		t.Fatalf("失败的运行不应留下快照，opsnap 列出了 %d 个；日志：\n%s", n, log)
	}
	for _, tag := range []string{fmt.Sprintf("run:%d", run.ID), fmt.Sprintf("job:%d", job.ID)} {
		if snaps := resticSnapshots(t, s.localDir, s.localKey, tag); len(snaps) != 0 {
			t.Fatalf("用恢复密钥直接打开仓库，仍有带 %s 标签的快照：%v", tag, snaps)
		}
	}
}

// testMissingMarker 本地目录仓库缺少 .opsnap-repo：运行失败、原因是「没有挂载」，仓库目录里一个字节都不变
func (s *suite) testMissingMarker(t *testing.T) {
	repo, dir, key := s.newLocalRepo(t, "it-marker", "marker")
	in := s.dbJob("it-marker-redis", "redis", hostRedis, 6379, "", env(t, "IT_REDIS_PASSWORD"), nil)
	in.RepoID = repo.ID
	job := s.api.newJob(t, in)
	s.backupOne(t, job.ID)
	if n := len(resticSnapshots(t, dir, key)); n != 1 {
		t.Fatalf("第一次运行后仓库里应有 1 个快照，实际 %d", n)
	}

	check(t, os.Remove(filepath.Join(dir, ".opsnap-repo")), "删除标记文件")
	before := dirState(t, dir)
	run := s.api.runJob(t, job.ID)
	if run.Status != "failed" || !strings.Contains(run.Reason, "没有挂载") {
		t.Fatalf("缺少标记文件时应失败且原因含「没有挂载」，实际 %s / %q；日志：\n%s", run.Status, run.Reason, s.api.runLog(t, run.ID))
	}
	after := dirState(t, dir)
	sameContent(t, wrap(before), wrap(after), nil)
	if len(before) < 5 {
		t.Fatalf("仓库目录条目太少（%d），比较没有意义", len(before))
	}
	var item repoItem
	s.api.mustCall(t, http.MethodGet, fmt.Sprintf("/repos/%d", repo.ID), nil, &item)
	if item.Status != "not_mounted" {
		t.Fatalf("存储位置状态应为 not_mounted，实际 %s（%s）", item.Status, item.StatusMessage)
	}
	if n := len(resticSnapshots(t, dir, key)); n != 1 {
		t.Fatalf("拒绝写入后仓库里应仍是 1 个快照，实际 %d", n)
	}
}

func wrap(m map[string]string) map[string][]string {
	out := make(map[string][]string, len(m))
	for k, v := range m {
		out[k] = []string{v}
	}
	return out
}

// testSFTP SFTP 仓库：核对主机公钥后添加，PostgreSQL 直连导出写入 SFTP 仓库，下载恢复后与源一致
func (s *suite) testSFTP(t *testing.T) {
	var hk struct {
		Fingerprint string `json:"fingerprint"`
		HostKey     string `json:"host_key"`
	}
	s.api.mustCall(t, http.MethodPost, "/repos/sftp-host-key", map[string]any{"host": hostSFTP, "port": 22}, &hk)
	if !strings.HasPrefix(hk.Fingerprint, "SHA256:") || hk.HostKey == "" {
		t.Fatalf("没有取回 SFTP 主机公钥：%+v", hk)
	}
	var repo repoCreated
	s.api.mustCall(t, http.MethodPost, "/repos", repoCreate{
		Name: "it-sftp", Type: "sftp",
		Params: repoParams{Host: hostSFTP, Port: 22, Username: "itsftp", Password: env(t, "IT_SFTP_PASSWORD"),
			Path: "/home/itsftp/restic-repo", HostKey: hk.HostKey},
		RecoveryKeyValue: s.recoveryKey(t), RecoveryKeySaved: true,
	}, &repo)
	if repo.RecoveryKeyError != "" {
		t.Fatalf("恢复密钥没有加入 SFTP 仓库：%s", repo.RecoveryKeyError)
	}
	job := s.api.newJob(t, jobInput{
		Name: "it-sftp-postgres", SourceType: "postgres", ConnMode: "direct",
		DBHost: hostPostgres, DBPort: 5432, DBUser: "postgres", DBPassword: env(t, "IT_PG_PASSWORD"),
		Databases: []string{pgDB}, RepoID: repo.ID,
	})
	_, sid := s.backupOne(t, job.ID)
	dump := s.downloadExport(t, job.ID, sid, ".dump")
	restorePostgres(t, dump)
	sameContent(t, pgContent(t, hostPostgres), pgContent(t, hostPostgresScratch),
		map[string]int{"rows:public.products": 400, "rows:public.stock_moves": 1500})
}

// resticSnapshots 不经 opsnap，直接用恢复密钥以只读方式列出本地仓库里的快照（--no-lock 不写锁文件）
func resticSnapshots(t testing.TB, repoDir, password string, tags ...string) []string {
	t.Helper()
	args := []string{"-r", repoDir, "snapshots", "--json", "--no-lock", "--no-cache"}
	for _, tag := range tags {
		args = append(args, "--tag", tag)
	}
	cmd := exec.Command("restic", args...)
	cmd.Env = append(os.Environ(), "RESTIC_PASSWORD="+password)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	check(t, err, "restic snapshots：%s", stderr.String())
	var snaps []struct {
		ID   string   `json:"id"`
		Tags []string `json:"tags"`
	}
	check(t, json.Unmarshal(out, &snaps), "解析 restic snapshots 输出")
	ids := make([]string, 0, len(snaps))
	for _, sn := range snaps {
		ids = append(ids, sn.ID[:8]+fmt.Sprint(sn.Tags))
	}
	return ids
}
