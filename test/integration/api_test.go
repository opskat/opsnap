//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"
)

// apiClient 只经 /api/v1 访问 opsnap，用会话 Cookie 认证
type apiClient struct {
	base string
	hc   *http.Client
}

// apiError opsnap 返回的错误：HTTP 状态码 + 错误码 + 文案
type apiError struct {
	Status int
	Code   int
	Msg    string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("HTTP %d 错误码 %d：%s", e.Status, e.Code, e.Msg)
}

func newAPIClient(t testing.TB, base string) *apiClient {
	t.Helper()
	jar, err := cookiejar.New(nil)
	check(t, err, "创建 cookie jar")
	return &apiClient{base: strings.TrimRight(base, "/") + "/api/v1", hc: &http.Client{Jar: jar, Timeout: 10 * time.Minute}}
}

// call 发送 JSON 请求并把 data 解到 out；code 非 0 或 HTTP 非 2xx 时返回 *apiError
func (c *apiClient) call(method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	var env struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("%s %s：HTTP %d，响应不是 JSON：%.300s", method, path, resp.StatusCode, raw)
	}
	if resp.StatusCode/100 != 2 || env.Code != 0 {
		return &apiError{Status: resp.StatusCode, Code: env.Code, Msg: env.Msg}
	}
	if out != nil && len(env.Data) > 0 && string(env.Data) != "null" {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return fmt.Errorf("%s %s：解析 data 失败：%w；%.300s", method, path, err, env.Data)
		}
	}
	return nil
}

// mustCall 同 call，失败时终止测试
func (c *apiClient) mustCall(t testing.TB, method, path string, body, out any) {
	t.Helper()
	check(t, c.call(method, path, body, out), "%s %s", method, path)
}

// download 请求下载接口，返回响应头和完整内容；非 200 时按 JSON 错误返回
func (c *apiClient) download(path string, q url.Values) (http.Header, []byte, error) {
	resp, err := c.hc.Get(c.base + path + "?" + q.Encode())
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("读取下载内容中断（已读 %d 字节）：%w", len(raw), err)
	}
	if resp.StatusCode != http.StatusOK || strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		return resp.Header, raw, fmt.Errorf("下载失败：HTTP %d：%.300s", resp.StatusCode, raw)
	}
	return resp.Header, raw, nil
}

// ---- 请求与响应结构：只声明测试用到的字段，字段名与 internal/api 下的 json 标签一致 ----

type hopInput struct {
	Type     string `json:"type"`
	ServerID int64  `json:"server_id,omitempty"`
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

type serverInput struct {
	ID                 int64      `json:"id,omitempty"`
	Name               string     `json:"name"`
	Host               string     `json:"host"`
	Port               int        `json:"port"`
	Username           string     `json:"username"`
	AuthType           string     `json:"auth_type"`
	Password           string     `json:"password"`
	Hops               []hopInput `json:"hops"`
	HostKeyFingerprint string     `json:"host_key_fingerprint"`
}

type serverItem struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	HostKeyFingerprint string `json:"host_key_fingerprint"`
}

type hopResult struct {
	Index       int    `json:"index"`
	Type        string `json:"type"`
	Name        string `json:"name"`
	Addr        string `json:"addr"`
	OK          bool   `json:"ok"`
	Reason      string `json:"reason"`
	ReasonLabel string `json:"reason_label"`
	Message     string `json:"message"`
	Fingerprint string `json:"fingerprint"`
}

type serverTest struct {
	OK          bool         `json:"ok"`
	Hops        []*hopResult `json:"hops"`
	Fingerprint string       `json:"fingerprint"`
}

type repoParams struct {
	Path     string `json:"path,omitempty"`
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	HostKey  string `json:"host_key,omitempty"`
}

type repoCreate struct {
	Name             string     `json:"name"`
	Type             string     `json:"type"`
	Params           repoParams `json:"params"`
	RecoveryKeyValue string     `json:"recovery_key_value"`
	RecoveryKeySaved bool       `json:"recovery_key_saved"`
}

type repoItem struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Status        string `json:"status"`
	StatusMessage string `json:"status_message"`
}

type repoCreated struct {
	repoItem
	RecoveryKeyError string `json:"recovery_key_error"`
}

type scheduleInput struct {
	Kind string `json:"kind"`
	Time string `json:"time,omitempty"`
}

type retentionInput struct {
	Preset string `json:"preset"`
}

type advancedInput struct {
	ExtraArgs []string `json:"extra_args,omitempty"`
}

type jobInput struct {
	Name       string         `json:"name"`
	SourceType string         `json:"source_type"`
	ServerID   int64          `json:"server_id,omitempty"`
	ConnMode   string         `json:"conn_mode,omitempty"`
	DBHost     string         `json:"db_host,omitempty"`
	DBPort     int            `json:"db_port,omitempty"`
	DBUser     string         `json:"db_user,omitempty"`
	DBPassword string         `json:"db_password,omitempty"`
	Databases  []string       `json:"databases,omitempty"`
	AuthDB     string         `json:"auth_db,omitempty"`
	Paths      []string       `json:"paths,omitempty"`
	Excludes   []string       `json:"excludes,omitempty"`
	RepoID     int64          `json:"repo_id"`
	Schedule   scheduleInput  `json:"schedule"`
	Retention  retentionInput `json:"retention"`
	Advanced   advancedInput  `json:"advanced"`
	Paused     bool           `json:"paused"`
}

type jobItem struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type runItem struct {
	ID          int64    `json:"id"`
	JobID       int64    `json:"job_id"`
	Status      string   `json:"status"`
	Reason      string   `json:"reason"`
	SnapshotIDs []string `json:"snapshot_ids"`
}

type logLine struct {
	Level string `json:"level"`
	Text  string `json:"text"`
}

type snapshotItem struct {
	ID    string   `json:"id"`
	Paths []string `json:"paths"`
	RunID int64    `json:"run_id"`
}

type snapshotList struct {
	SourceType string          `json:"source_type"`
	Snapshots  []*snapshotItem `json:"snapshots"`
}

type exportFile struct {
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	Gzip     bool   `json:"gzip"`
	Filename string `json:"filename"`
}

// ---- 常用操作 ----

// newJob 用默认的计划和保留规则建任务；任务保持暂停，避免调度器在测试期间插入运行
func (c *apiClient) newJob(t testing.TB, in jobInput) *jobItem {
	t.Helper()
	in.Schedule = scheduleInput{Kind: "daily", Time: "03:00"}
	in.Retention = retentionInput{Preset: "7d"}
	in.Paused = true
	var job jobItem
	c.mustCall(t, http.MethodPost, "/jobs", in, &job)
	return &job
}

// runJob 手动触发一次运行并等到结束
func (c *apiClient) runJob(t testing.TB, jobID int64) *runItem {
	t.Helper()
	var started struct {
		RunID int64 `json:"run_id"`
	}
	c.mustCall(t, http.MethodPost, fmt.Sprintf("/jobs/%d/run", jobID), struct{}{}, &started)
	deadline := time.Now().Add(10 * time.Minute)
	for time.Now().Before(deadline) {
		var run runItem
		c.mustCall(t, http.MethodGet, fmt.Sprintf("/runs/%d", started.RunID), nil, &run)
		if run.Status != "running" {
			return &run
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("运行 %d 10 分钟内没有结束", started.RunID)
	return nil
}

// runLog 运行日志全文，失败时附在断言信息里方便定位
func (c *apiClient) runLog(t testing.TB, runID int64) string {
	t.Helper()
	var out struct {
		Lines []*logLine `json:"lines"`
	}
	if err := c.call(http.MethodGet, fmt.Sprintf("/runs/%d/log", runID), nil, &out); err != nil {
		return "（读取日志失败：" + err.Error() + "）"
	}
	var b strings.Builder
	for _, l := range out.Lines {
		fmt.Fprintf(&b, "[%s] %s\n", l.Level, l.Text)
	}
	return b.String()
}

// mustSucceed 运行成功（不接受警告：测试数据里没有会消失的文件）
func (c *apiClient) mustSucceed(t testing.TB, run *runItem) {
	t.Helper()
	if run.Status != "success" {
		t.Fatalf("运行 %d 状态 %s，原因 %q；日志：\n%s", run.ID, run.Status, run.Reason, c.runLog(t, run.ID))
	}
}

func (c *apiClient) snapshots(t testing.TB, jobID int64) *snapshotList {
	t.Helper()
	var out snapshotList
	c.mustCall(t, http.MethodGet, fmt.Sprintf("/jobs/%d/snapshots", jobID), nil, &out)
	return &out
}

// snapshotsOfRun 这个任务里由指定运行产生的快照
func (c *apiClient) snapshotsOfRun(t testing.TB, jobID, runID int64) []*snapshotItem {
	t.Helper()
	var out []*snapshotItem
	for _, s := range c.snapshots(t, jobID).Snapshots {
		if s.RunID == runID {
			out = append(out, s)
		}
	}
	return out
}

func (c *apiClient) exports(t testing.TB, jobID int64, sid string) []*exportFile {
	t.Helper()
	var out struct {
		Files []*exportFile `json:"files"`
	}
	c.mustCall(t, http.MethodGet, fmt.Sprintf("/jobs/%d/snapshots/%s/exports", jobID, sid), nil, &out)
	return out.Files
}

// downloadSnapshot 经下载接口取回快照里的内容：先走预检，再真正下载
func (c *apiClient) downloadSnapshot(t testing.TB, jobID int64, sid string, paths []string, gzip bool) []byte {
	t.Helper()
	q := url.Values{"path": paths}
	if gzip {
		q.Set("gzip", "true")
	}
	base := fmt.Sprintf("/jobs/%d/snapshots/%s/download", jobID, sid)
	c.mustCall(t, http.MethodGet, base+"/check?"+q.Encode(), nil, nil)
	_, body, err := c.download(base, q)
	check(t, err, "下载快照 %s 的 %v", sid, paths)
	return body
}
