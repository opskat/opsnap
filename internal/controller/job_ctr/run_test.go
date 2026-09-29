package job_ctr

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	api "github.com/opskat/opsnap/internal/api/job"
	"github.com/opskat/opsnap/internal/model/entity/job_entity"
	"github.com/opskat/opsnap/internal/model/entity/storage_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/dsconn"
	"github.com/opskat/opsnap/internal/repository/job_repo"
	"github.com/opskat/opsnap/internal/repository/storage_repo"
)

const (
	pgPassword = "pg-S3cret!pw" //nolint:gosec // 假数据源的测试密码
	pgHeader   = "PGDMP\x01\x0e\x00\x04\x08\x01"
	pgDumpOK   = `printf 'PGDMP\001\016\000\004\010\001'; printf 'archive app\n'`
	pgDumpHang = `printf 'PGDMP\001\016\000\004\010\001'; exec sleep 30`
	pgDumpFail = `echo 'FATAL: password authentication failed (pw=` + pgPassword + `)' >&2; exit 1`
)

// fakeConnector 代替数据库协议：连接“成功”并返回服务端版本，不需要真实数据库
type fakeConnector struct{}

func (fakeConnector) Test(context.Context, dsconn.Dialer, dsconn.Config) (dsconn.Info, error) {
	return dsconn.Info{Version: "16.4"}, nil
}

func (fakeConnector) Open(context.Context, dsconn.Dialer, dsconn.Config) (*dsconn.Conn, error) {
	return &dsconn.Conn{Info: dsconn.Info{Version: "16.4"}}, nil
}

// tool 写假 pg_dump
func (e *env) tool(body string) {
	script := "#!/bin/sh\nPATH=/usr/bin:/bin\n" +
		"if [ \"$1\" = \"--version\" ]; then echo 'pg_dump (PostgreSQL) 16.4'; exit 0; fi\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(e.bin, "pg_dump"), []byte(script), 0o755); err != nil { //nolint:gosec // 测试用假可执行文件
		panic(err)
	}
}

// pgCreate 一份备份单个库 app 的 PostgreSQL 任务
func (e *env) pgCreate(name, prefix string) *api.CreateRequest {
	req := e.validCreate(name, prefix)
	req.DataSourceID = e.pg.ID
	req.Scope, req.Databases = "databases", []string{"app"}
	req.Options = api.Options{}
	return req
}

func (e *env) runs(t *testing.T, jobID int64, page int) *api.RunsResponse {
	t.Helper()
	resp := &api.RunsResponse{}
	require.NoError(t, e.do(&api.RunsRequest{ID: jobID, Page: page}, resp))
	return resp
}

// waitFinished 等任务最近一次运行结束并返回它
func (e *env) waitFinished(t *testing.T, jobID int64) *api.Run {
	t.Helper()
	var last *api.Run
	require.Eventually(t, func() bool {
		items := e.runs(t, jobID, 1).Items
		if len(items) == 0 {
			return false
		}
		last = items[0]
		return last.Status != job_entity.RunQueued && last.Status != job_entity.RunRunning
	}, 60*time.Second, 50*time.Millisecond)
	return last
}

func TestJobRunNow(t *testing.T) {
	e := setupTest(t)
	item := e.create(t, e.pgCreate("analytics 全量备份", "pg/analytics"))
	var lastID int64

	// 立即执行：经 API 令牌触发，完成后可以查看运行记录、日志与统计（只执行一次，不放在会被重复执行的 Convey 中）
	{
		started := &api.RunNowResponse{}
		require.NoError(t, e.do(&api.RunNowRequest{ID: item.ID}, started))
		assert.Equal(t, "manual", started.Run.Trigger)
		assert.Equal(t, item.ID, started.Run.JobID)

		run := e.waitFinished(t, item.ID)
		require.Equal(t, "success", run.Status, run.Reason)
		assert.Equal(t, started.Run.ID, run.ID)
		assert.NotEmpty(t, run.SnapshotID)
		assert.EqualValues(t, len(pgHeader+"archive app\n"), run.ExportedBytes)
		assert.Positive(t, run.UploadedBytes)
		assert.Positive(t, run.StartedAt)
		assert.GreaterOrEqual(t, run.FinishedAt, run.StartedAt)
		assert.GreaterOrEqual(t, run.DurationMs, int64(0))
		assert.Empty(t, run.FailedStep)

		logResp := &api.RunLogResponse{}
		require.NoError(t, e.do(&api.RunLogRequest{ID: item.ID, RunID: run.ID}, logResp))
		steps := map[string]bool{}
		for _, l := range logResp.Lines {
			steps[l.Step] = true
			assert.Positive(t, l.Time)
		}
		assert.True(t, steps["prepare"] && steps["connect"] && steps["export"] && steps["verify"] && steps["retention"], "%v", steps)

		stats := &api.StatsResponse{}
		require.NoError(t, e.do(&api.StatsRequest{ID: item.ID}, stats))
		assert.Equal(t, 1, stats.SnapshotCount)
		assert.Positive(t, stats.EarliestSnapshotAt)
		assert.Equal(t, run.ExportedBytes, stats.ExportBytes)
		assert.Positive(t, stats.PackedBytes)
		assert.Empty(t, stats.StorageError)
		require.NotNil(t, stats.LastSuccess)
		assert.Equal(t, run.ID, stats.LastSuccess.ID)
		assert.Equal(t, api.RecentStats{Runs: 1, Success: 1, Failed: 0, SuccessRate: 1}, stats.Recent)

		list := &api.ListResponse{}
		require.NoError(t, e.do(&api.ListRequest{}, list))
		require.Len(t, list.Items, 1)
		require.NotNil(t, list.Items[0].LastRun)
		assert.Equal(t, run.ID, list.Items[0].LastRun.ID)
		assert.Equal(t, "success", list.Items[0].LastRun.Status)
		assert.Equal(t, 1, list.Items[0].SnapshotCount)
		got := &api.GetResponse{}
		require.NoError(t, e.do(&api.GetRequest{ID: item.ID}, got))
		assert.Equal(t, list.Items[0], got.Item)
		lastID = run.ID
	}

	convey.Convey("立即执行之后", t, func() {
		convey.Convey("失败的运行计入统计；错误输出中的密码不出现在任何响应中", func() {
			e.tool(pgDumpFail)
			defer e.tool(pgDumpOK)
			require.NoError(t, e.do(&api.RunNowRequest{ID: item.ID}, &api.RunNowResponse{}))
			failed := e.waitFinished(t, item.ID)
			assert.Equal(t, "failed", failed.Status)
			assert.Equal(t, "export", failed.FailedStep)
			assert.Contains(t, failed.Reason, "pg_dump")
			assert.NotContains(t, failed.Reason, pgPassword)
			logResp := &api.RunLogResponse{}
			require.NoError(t, e.do(&api.RunLogRequest{ID: item.ID, RunID: failed.ID}, logResp))
			for _, l := range logResp.Lines {
				assert.NotContains(t, l.Message, pgPassword)
			}
			stats := &api.StatsResponse{}
			require.NoError(t, e.do(&api.StatsRequest{ID: item.ID}, stats))
			assert.Equal(t, api.RecentStats{Runs: 2, Success: 1, Failed: 1, SuccessRate: 0.5}, stats.Recent)
			assert.Equal(t, 1, stats.SnapshotCount, "失败不形成快照")
			list := &api.ListResponse{}
			require.NoError(t, e.do(&api.ListRequest{}, list))
			assert.NotContains(t, list.Items[0].LastRun.Reason, pgPassword)
		})

		convey.Convey("存储无法打开时统计只给出提示，运行统计照常", func() {
			st, err := storage_repo.Storage().Find(e.ctx, e.primary)
			require.NoError(t, err)
			st.Status = storage_entity.StatusUnreachable
			require.NoError(t, storage_repo.Storage().Save(e.ctx, st))
			defer func() {
				st.Status = storage_entity.StatusOK
				require.NoError(t, storage_repo.Storage().Save(e.ctx, st))
			}()
			stats := &api.StatsResponse{}
			require.NoError(t, e.do(&api.StatsRequest{ID: item.ID}, stats))
			assert.Equal(t, i18n.T(e.ctx, code.JobStatsStorageUnreadable), stats.StorageError)
			assert.Zero(t, stats.SnapshotCount)
			assert.NotNil(t, stats.LastSuccess)
		})

		convey.Convey("不存在的任务或运行", func() {
			assert.Equal(t, code.JobNotFound, errCode(e.do(&api.RunNowRequest{ID: 999}, &api.RunNowResponse{})))
			assert.Equal(t, code.JobNotFound, errCode(e.do(&api.RunsRequest{ID: 999}, &api.RunsResponse{})))
			assert.Equal(t, code.JobNotFound, errCode(e.do(&api.StatsRequest{ID: 999}, &api.StatsResponse{})))
			assert.Equal(t, code.JobRunNotFound, errCode(e.do(&api.RunLogRequest{ID: item.ID, RunID: 999}, &api.RunLogResponse{})))
			other := e.create(t, e.pgCreate("另一个任务", "pg/other"))
			assert.Equal(t, code.JobRunNotFound, errCode(e.do(&api.RunLogRequest{ID: other.ID, RunID: lastID}, &api.RunLogResponse{})),
				"运行不属于该任务")
			assert.Equal(t, code.JobRunNotFound, errCode(e.do(&api.CancelRunRequest{ID: other.ID, RunID: lastID}, &api.CancelRunResponse{})))
			assert.Equal(t, code.JobRunFinished, errCode(e.do(&api.CancelRunRequest{ID: item.ID, RunID: lastID}, &api.CancelRunResponse{})))
		})
	})
}

func TestJobRunActive(t *testing.T) {
	e := setupTest(t)
	e.tool(pgDumpHang)
	item := e.create(t, e.pgCreate("运行中", "pg/busy"))

	convey.Convey("运行中：不能再次立即执行、不能删除；取消后可以删除，运行记录一并删除", t, func() {
		started := &api.RunNowResponse{}
		require.NoError(t, e.do(&api.RunNowRequest{ID: item.ID}, started))
		var running *api.Run
		require.Eventually(t, func() bool {
			list := &api.ListResponse{}
			require.NoError(t, e.do(&api.ListRequest{}, list))
			running = list.Items[0].LastRun
			return running != nil && running.Status == "running" && running.ExportedBytes > 0
		}, 30*time.Second, 20*time.Millisecond, "列表中显示运行中与实时的已导出量")
		assert.EqualValues(t, len(pgHeader), running.ExportedBytes)

		err := e.do(&api.RunNowRequest{ID: item.ID}, &api.RunNowResponse{})
		assert.Equal(t, code.JobRunAlreadyActive, errCode(err))
		err = e.do(&api.DeleteRequest{ID: item.ID}, &api.DeleteResponse{})
		assert.Equal(t, code.JobRunActive, errCode(err), "正在运行时不能删除")

		canceled := &api.CancelRunResponse{}
		require.NoError(t, e.do(&api.CancelRunRequest{ID: item.ID, RunID: started.Run.ID}, canceled))
		assert.Equal(t, "canceled", canceled.Run.Status)
		assert.Empty(t, canceled.Run.SnapshotID)

		require.NoError(t, e.do(&api.DeleteRequest{ID: item.ID}, &api.DeleteResponse{}))
		_, total, err := job_repo.Run().Page(e.ctx, item.ID, 0, 10)
		require.NoError(t, err)
		assert.Zero(t, total, "删除任务时删除它的运行记录")
	})
}

func TestJobRunNowOnCreate(t *testing.T) {
	e := setupTest(t)

	convey.Convey("创建时选择立即执行一次：执行一次并清除标记", t, func() {
		req := e.pgCreate("立即执行", "pg/now")
		req.RunNow = true
		item := e.create(t, req)
		run := e.waitFinished(t, item.ID)
		assert.Equal(t, "manual", run.Trigger)
		assert.EqualValues(t, 1, e.runs(t, item.ID, 1).Total, "只执行一次")
		saved, err := job_repo.Job().Find(e.ctx, item.ID)
		require.NoError(t, err)
		assert.False(t, saved.RunNow)
	})
}

func TestJobRunsPagination(t *testing.T) {
	e := setupTest(t)
	item := e.create(t, e.pgCreate("分页", "pg/pages"))
	for i := 0; i < 25; i++ {
		require.NoError(t, job_repo.Run().Create(e.ctx, &job_entity.Run{JobID: item.ID, Status: job_entity.RunSuccess,
			Trigger: job_entity.TriggerSchedule, StartedAt: int64(i+1) * 1000, FinishedAt: int64(i+1)*1000 + 1500, Log: "[]"}))
	}

	convey.Convey("运行记录按触发顺序倒序，每页 20 条", t, func() {
		first := e.runs(t, item.ID, 1)
		assert.EqualValues(t, 25, first.Total)
		require.Len(t, first.Items, 20)
		assert.EqualValues(t, 25, first.Items[0].StartedAt, "最新的在前")
		assert.EqualValues(t, 1500, first.Items[0].DurationMs)
		for i := 1; i < len(first.Items); i++ {
			assert.Greater(t, first.Items[i-1].ID, first.Items[i].ID)
		}
		second := e.runs(t, item.ID, 2)
		assert.Len(t, second.Items, 5)
		assert.Equal(t, first.Items, e.runs(t, item.ID, 0).Items, "缺省为第 1 页")
		assert.Empty(t, e.runs(t, item.ID, 3).Items)
		// 极大的页码不能因为偏移量溢出而回到第 1 页
		assert.Empty(t, e.runs(t, item.ID, 1<<62).Items, "超出范围的页码没有记录")
	})
}

// 运行记录的失败原因与执行日志按请求的 Accept-Language 显示：OpsNap 自己的文字随语言变化，
// 导出工具的错误输出原样保留且去掉秘密（docs/specs/2026-09-27-backup-jobs.md「界面」）
func TestJobRunMessagesFollowAcceptLanguage(t *testing.T) {
	e := setupTest(t)
	e.tool(`echo 'pg_dump: error: query failed: ERROR:  permission denied for table secret (pw=` + pgPassword + `)' >&2; exit 1`)
	item := e.create(t, e.pgCreate("权限不足", "pg/denied"))
	require.NoError(t, e.do(&api.RunNowRequest{ID: item.ID}, &api.RunNowResponse{}))
	run := e.waitFinished(t, item.ID)
	require.Equal(t, job_entity.RunFailed, run.Status)
	raw := "pg_dump: error: query failed: ERROR:  permission denied for table secret (pw=******)"
	chinese := regexp.MustCompile(`[\p{Han}\x{3000}-\x{303F}\x{FF00}-\x{FFEF}“”]`)

	read := func(lang string) (string, string) {
		runs := &api.RunsResponse{}
		require.NoError(t, e.doLang(lang, &api.RunsRequest{ID: item.ID, Page: 1}, runs))
		require.Len(t, runs.Items, 1)
		lg := &api.RunLogResponse{}
		require.NoError(t, e.doLang(lang, &api.RunLogRequest{ID: item.ID, RunID: run.ID}, lg))
		var b strings.Builder
		for _, l := range lg.Lines {
			b.WriteString(l.Message + "\n")
		}
		return runs.Items[0].Reason, b.String()
	}

	convey.Convey("Accept-Language: en 时原因与日志为英文，原文保留", t, func() {
		reason, log := read("en-US,en;q=0.9")
		assert.Contains(t, reason, raw)
		assert.Contains(t, reason, "exit code 1")
		assert.False(t, chinese.MatchString(reason), reason)
		assert.False(t, chinese.MatchString(log), log)
		assert.Contains(t, log, raw)
		assert.NotContains(t, reason+log, pgPassword)
	})
	convey.Convey("Accept-Language: zh-CN 时为中文，与原来一致", t, func() {
		reason, log := read("zh-CN,zh;q=0.9")
		assert.Equal(t, "数据源账号缺少权限：pg_dump 失败（退出码 1）: "+raw, reason)
		assert.Contains(t, log, "检查存储、数据源与导出工具")
		assert.NotContains(t, reason+log, pgPassword)
	})
}
