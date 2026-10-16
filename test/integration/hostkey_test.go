//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
)

// TestHostKeyChanged 在 scripts/it-remote.sh 用新的主机密钥重建 target 之后运行（IT_PHASE=rekeyed）：
// opsnap 必须拒绝连接并报「主机指纹已变化」，已保存的指纹不被改写，运行失败且不生成快照。
func TestHostKeyChanged(t *testing.T) {
	if os.Getenv("IT_PHASE") != "rekeyed" {
		t.Skip("IT_PHASE 不是 rekeyed")
	}
	api := login(t)

	var servers struct {
		List []*serverItem `json:"list"`
	}
	api.mustCall(t, http.MethodGet, "/servers", nil, &servers)
	var target *serverItem
	for _, s := range servers.List {
		if s.Name == "it-target" {
			target = s
		}
	}
	if target == nil {
		t.Fatal("找不到主流程创建的 it-target")
	}
	var jobs struct {
		List []*jobItem `json:"list"`
	}
	api.mustCall(t, http.MethodGet, "/jobs?filter=all", nil, &jobs)
	var job *jobItem
	for _, j := range jobs.List {
		if j.Name == "it-files" {
			job = j
		}
	}
	if job == nil {
		t.Fatal("找不到主流程创建的 it-files")
	}
	before := len(api.snapshots(t, job.ID).Snapshots)

	var res serverTest
	api.mustCall(t, http.MethodPost, fmt.Sprintf("/servers/%d/test", target.ID), struct{}{}, &res)
	if res.OK || len(res.Hops) != 3 {
		t.Fatalf("target 换了主机密钥后测试应失败，且前两跳仍有结果：%s", hopsString(res.Hops))
	}
	last := res.Hops[2]
	if !res.Hops[0].OK || !res.Hops[1].OK || last.Reason != "fingerprint_mismatch" || !strings.Contains(last.Message, "主机指纹已变化") {
		t.Fatalf("应只在 target 这一跳以「主机指纹已变化」拒绝：%s", hopsString(res.Hops))
	}
	if last.Fingerprint == "" || last.Fingerprint == target.HostKeyFingerprint {
		t.Fatalf("target 应出示与已核对的 %s 不同的新指纹：%s", target.HostKeyFingerprint, hopsString(res.Hops))
	}

	run := api.runJob(t, job.ID)
	if run.Status != "failed" || !strings.Contains(run.Reason, "主机指纹已变化") {
		t.Fatalf("指纹变化后运行应失败且原因含「主机指纹已变化」，实际 %s / %q；日志：\n%s", run.Status, run.Reason, api.runLog(t, run.ID))
	}
	if after := len(api.snapshots(t, job.ID).Snapshots); after != before {
		t.Fatalf("被拒绝的运行不应生成快照：之前 %d 个，之后 %d 个", before, after)
	}
	var saved serverItem
	api.mustCall(t, http.MethodGet, fmt.Sprintf("/servers/%d", target.ID), nil, &saved)
	if saved.HostKeyFingerprint != target.HostKeyFingerprint {
		t.Fatalf("已核对的指纹不应被自动改写：%s → %s", target.HostKeyFingerprint, saved.HostKeyFingerprint)
	}
}
