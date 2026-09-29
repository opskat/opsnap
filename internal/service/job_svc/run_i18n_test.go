package job_svc

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	jobapi "github.com/opskat/opsnap/internal/api/job"
	"github.com/opskat/opsnap/internal/model/entity/datasource_entity"
	"github.com/opskat/opsnap/internal/model/entity/job_entity"
	"github.com/opskat/opsnap/internal/model/entity/storage_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/repository/channel_repo"
	"github.com/opskat/opsnap/internal/repository/datasource_repo"
	"github.com/opskat/opsnap/internal/repository/job_repo"
	"github.com/opskat/opsnap/internal/service/secret_svc"
)

// chineseText 中文字符与全角标点：英文界面中 OpsNap 自己的文字不应出现它们
var chineseText = regexp.MustCompile(`[\p{Han}\x{3000}-\x{303F}\x{FF00}-\x{FFEF}“”‘’……]`)

// apiLog 以指定的界面语言经接口读取一次运行的执行日志，每行为“[步骤] 内容”
func apiLog(t *testing.T, e *runEnv, runID int64, lang string) string {
	t.Helper()
	resp, err := Job().RunLog(i18n.WithLanguage(e.ctx, lang), &jobapi.RunLogRequest{ID: e.job.ID, RunID: runID})
	require.NoError(t, err)
	var b strings.Builder
	for _, l := range resp.Lines {
		b.WriteString("[" + l.Step + "] " + l.Message + "\n")
	}
	return b.String()
}

// pgDumpDenied 导出时缺少表的权限：错误输出（原文，带着密码）由导出工具给出
const pgDumpDenied = `echo 'pg_dump: error: query failed: ERROR:  permission denied for table secret (pw=` + pgPassword + `)' >&2; exit 1`

// 运行记录的失败原因、修复方法与执行日志中由 OpsNap 生成的文字按查看者的界面语言显示；导出工具与数据库
// 返回的原文原样保留且去掉秘密；中文界面的文字与原来一致
// （docs/specs/2026-09-27-backup-jobs.md「界面」：所有文案提供中文与英文）
func TestRunMessagesFollowViewerLanguage(t *testing.T) {
	cases := []struct {
		name  string
		setup func(e *runEnv)
		step  string
		// zhReason 中文界面的原因（与改动前一致）；为空时只检查 zhContains
		zhReason   string
		zhContains string
		// enContains 英文界面的原因中应有的内容（含原样保留的原文）
		enContains []string
		// enLog 英文界面的日志中应有的内容
		enLog []string
	}{
		{name: "prepare_storage_not_ok", step: job_entity.StepPrepare,
			setup:      func(e *runEnv) { e.setStorageStatus(storage_entity.StatusUnreachable) },
			zhReason:   "存储“primary”的状态不是“正常”（unreachable），请先测试存储连接",
			enContains: []string{`"primary"`, "unreachable"},
			enLog:      []string{"[prepare] Checking storage, data source and export tools"}},
		{name: "prepare_pg_dump_too_old", step: job_entity.StepPrepare,
			setup: func(e *runEnv) {
				e.ds.Version = "16.4 (Debian 16.4-1)"
				require.NoError(e.t, datasource_repo.DataSource().Save(e.ctx, e.ds))
				e.tool("pg_dump", "pg_dump (PostgreSQL) 15.2", pgDumpOK)
			},
			zhReason:   "导出工具版本过低：pg_dump 大版本 15 低于服务端 16。修复：在主控端安装 PostgreSQL 16 或更新版本的客户端，放在 PATH 或 tools.dir 中",
			enContains: []string{"pg_dump", "15", "16", "Fix:", "tools.dir"}},
		{name: "prepare_mariadb_tls", step: job_entity.StepPrepare,
			setup: func(e *runEnv) {
				e.ds.Kind, e.ds.TLSMode, e.ds.Version = datasource_entity.KindMySQL, "require", "8.0.40"
				require.NoError(e.t, datasource_repo.DataSource().Save(e.ctx, e.ds))
				e.job.OptGlobals = false
				require.NoError(e.t, job_repo.Job().Save(e.ctx, e.job))
				e.tool("mysqldump", "mysqldump  Ver 10.19 Distrib 10.11.14-MariaDB, for debian-linux-gnu (x86_64)", "exit 0")
			},
			zhContains: "导出工具无法按数据源的 TLS 设置连接：主控端的 mysqldump 来自 MariaDB（mysqldump  Ver 10.19 Distrib 10.11.14-MariaDB",
			// 工具的版本原文原样保留
			enContains: []string{"mysqldump  Ver 10.19 Distrib 10.11.14-MariaDB, for debian-linux-gnu (x86_64)", `"require"`, "Fix:"}},
		{name: "connect_hop_auth_failed", step: job_entity.StepConnect,
			setup: func(e *runEnv) {
				ch, err := channel_repo.Channel().Find(e.ctx, e.ds.ChannelID)
				require.NoError(e.t, err)
				ch.Password, err = secret_svc.Secret().Encrypt(e.ctx, "wrong-password")
				require.NoError(e.t, err)
				require.NoError(e.t, channel_repo.Channel().Save(e.ctx, ch))
			},
			zhContains: "连接数据源失败: 第 1 跳 jump（SSH）：认证失败: ssh: handshake failed: ssh: unable to authenticate",
			// SSH 库的原文原样保留
			enContains: []string{"Hop 1 jump (SSH)", "ssh: unable to authenticate"}},
		{name: "connect_missing_database", step: job_entity.StepConnect,
			setup:      func(e *runEnv) { e.setDatabases("app", "postgres") },
			zhReason:   "指定的库在数据源中不存在（或不允许连接）：reports",
			enContains: []string{"reports"},
			enLog:      []string{"[connect] Connected, server version 16.4"}},
		{name: "export_privilege", step: job_entity.StepExport,
			setup: func(e *runEnv) { e.tool("pg_dump", "pg_dump (PostgreSQL) 16.4", pgDumpDenied) },
			zhReason: "数据源账号缺少权限：pg_dump 失败（退出码 1）: " +
				"pg_dump: error: query failed: ERROR:  permission denied for table secret (pw=******)",
			// 导出工具的错误输出原样保留，其中的密码已去掉
			enContains: []string{"pg_dump", "exit code 1", "pg_dump: error: query failed: ERROR:  permission denied for table secret (pw=******)"}},
	}
	// 子测试名只用 ASCII：t.TempDir() 带着测试名，导出工具的路径会出现在日志中
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newRunEnv(t)
			c.setup(e)
			run := e.manual()
			require.Equal(t, job_entity.RunFailed, run.Status)
			require.Equal(t, c.step, run.FailedStep, "%s\n%s", run.Reason, logText(run))

			zh := apiReasons(t, e.ctx, e.job.ID, code.LangZhCN)[run.ID]
			if c.zhReason != "" {
				assert.Equal(t, c.zhReason, zh, "中文界面的原因与原来一致")
			}
			assert.Contains(t, zh, c.zhContains)
			zhLog := apiLog(t, e, run.ID, code.LangZhCN)
			assert.Contains(t, zhLog, "[prepare] 检查存储、数据源与导出工具")
			assert.Contains(t, zhLog, zh, "失败原因写进日志")

			en := apiReasons(t, e.ctx, e.job.ID, code.LangEn)[run.ID]
			assert.NotEmpty(t, en)
			assert.Falsef(t, chineseText.MatchString(en), "英文界面的原因中不应有中文：%s", en)
			for _, s := range c.enContains {
				assert.Contains(t, en, s)
			}
			enLog := apiLog(t, e, run.ID, code.LangEn)
			assert.Falsef(t, chineseText.MatchString(enLog), "英文界面的日志中不应有中文：\n%s", enLog)
			assert.Contains(t, enLog, en, "失败原因写进日志")
			for _, s := range c.enLog {
				assert.Contains(t, enLog, s)
			}
			for _, text := range []string{zh, zhLog, en, enLog} {
				assert.NotContains(t, text, pgPassword)
				assert.NotContains(t, text, sshPassword)
			}
		})
	}

	t.Run("timeout", func(t *testing.T) {
		e := newRunEnv(t)
		e.tool("pg_dump", "pg_dump (PostgreSQL) 16.4", pgDumpHang)
		timeoutUnit = 500 * time.Millisecond
		run := e.manual()
		require.Equal(t, job_entity.RunFailed, run.Status)
		assert.Contains(t, apiLog(t, e, run.ID, code.LangZhCN), "[export] 超时（超过 10 分钟）：已终止导出工具，未形成快照")
		enLog := apiLog(t, e, run.ID, code.LangEn)
		assert.Contains(t, enLog, "[export] Timed out (exceeded 10 min): ")
		assert.Falsef(t, chineseText.MatchString(enLog), "英文界面的日志中不应有中文：\n%s", enLog)
	})

	t.Run("success", func(t *testing.T) {
		e := newRunEnv(t)
		run := e.manual()
		require.Equal(t, job_entity.RunSuccess, run.Status, run.Reason)
		zhLog := apiLog(t, e, run.ID, code.LangZhCN)
		assert.Contains(t, zhLog, "[export] 导出并流式写入仓库，路径前缀 pg/analytics，压缩 zstd")
		enLog := apiLog(t, e, run.ID, code.LangEn)
		assert.Contains(t, enLog, "pg/analytics")
		assert.Falsef(t, chineseText.MatchString(enLog), "英文界面的日志中不应有中文：\n%s", enLog)
	})
}
