import { useCallback, useEffect, useRef, useState } from "react";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";

import { Button } from "@/components/ui/button";
import { formatDateTime } from "@/lib/format";
import { formatBytes, getRunLog, isRunActive, RUNS_PAGE_SIZE, type Run, type RunLogLine } from "@/lib/jobs";
import { cn } from "@/lib/utils";

import { errorMessage, type Loadable } from "./loadable";
import { formatDuration, triggerLabel } from "./runFormat";
import { RunStatusTag } from "./RunStatusTag";

const columns = "grid grid-cols-[100px_150px_150px_90px_1fr_140px] items-center gap-3 px-4";

const pad = (n: number) => String(n).padStart(2, "0");

/** 日志行的时间：毫秒 → 本地 HH:mm:ss */
function formatLogTime(ms: number): string {
  const d = new Date(ms);
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

/**
 * 运行记录表（docs/specs/2026-09-27-backup-jobs.md「运行记录」「任务详情」）：按开始时间倒序，每页 20 条，
 * 列为状态/触发方式/开始时间/耗时/导出量 · 新增量/快照 ID。点击一行在其下方展开失败步骤与原因（仅失败）以及
 * 执行日志（首次展开时懒加载）。
 */
export function RunsTable({
  jobId,
  state,
  page,
  onPageChange,
  onRetry,
}: {
  jobId: number;
  state: Loadable<{ items: Run[]; total: number }>;
  page: number;
  onPageChange: (page: number) => void;
  onRetry: () => void;
}) {
  const { t, i18n } = useTranslation();
  const [expandedId, setExpandedId] = useState<number>();
  const [logs, setLogs] = useState<Record<number, Loadable<RunLogLine[]>>>({});
  // 读取日志时运行的状态：运行中的记录或状态已变化的记录，在列表刷新时重新读取日志
  const logStatus = useRef<Record<number, Run["status"]>>({});
  // 每条运行最近一次读取日志的序号：较早发出的请求后返回时不覆盖较新的结果
  const logSeq = useRef<Record<number, number>>({});

  const fetchLog = useCallback(
    (run: Run) => {
      logStatus.current[run.id] = run.status;
      const seq = (logSeq.current[run.id] ?? 0) + 1;
      logSeq.current[run.id] = seq;
      const settle = (state: Loadable<RunLogLine[]>) => {
        if (logSeq.current[run.id] === seq) setLogs((l) => ({ ...l, [run.id]: state }));
      };
      getRunLog(jobId, run.id)
        .then((r) => settle({ status: "ready", data: r.lines }))
        .catch((err: unknown) => settle({ status: "error", message: errorMessage(err) }));
    },
    [jobId]
  );

  // 运行中的记录自动刷新：展开的运行仍在进行或刚刚结束时，随列表刷新重新读取它的日志
  useEffect(() => {
    if (state.status !== "ready" || expandedId === undefined) return;
    const run = state.data.items.find((r) => r.id === expandedId);
    if (!run || logStatus.current[run.id] === undefined) return;
    if (isRunActive(run) || logStatus.current[run.id] !== run.status) fetchLog(run);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- 只在列表数据刷新时检查，展开/收起由 toggle 处理
  }, [state]);

  // 切换界面语言后，日志中 OpsNap 的文字按新语言重新读取：丢弃已读到的日志与按原来的语言发出、尚未返回的请求
  // （再次展开时重新读取），展开中的立即重新读取
  const language = i18n.language;
  const shownLanguage = useRef(language);
  useEffect(() => {
    if (shownLanguage.current === language) return;
    shownLanguage.current = language;
    const run =
      state.status === "ready" && expandedId !== undefined
        ? state.data.items.find((r) => r.id === expandedId)
        : undefined;
    logStatus.current = {};
    for (const id of Object.keys(logSeq.current)) logSeq.current[Number(id)]++;
    setLogs((l) => (run && l[run.id] ? { [run.id]: l[run.id] } : {}));
    if (run) fetchLog(run);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- 只在语言变化时重新读取
  }, [language]);

  const toggle = (run: Run) => {
    if (expandedId === run.id) {
      setExpandedId(undefined);
      return;
    }
    setExpandedId(run.id);
    // 已读到或正在读取时不再请求；上一次读取失败时重新读取
    if (logs[run.id] && logs[run.id].status !== "error") return;
    setLogs((l) => ({ ...l, [run.id]: { status: "loading" } }));
    fetchLog(run);
  };

  if (state.status === "loading") {
    return <p className="px-4 py-8 text-sm text-muted-foreground">{t("common.loading")}</p>;
  }

  if (state.status === "error") {
    return (
      <div className="flex items-center justify-between gap-3 rounded-md bg-destructive-soft px-4 py-3">
        <p className="text-sm text-destructive">{t("jobs.detail.runs.loadFailed", { message: state.message })}</p>
        <Button variant="outline" size="sm" onClick={onRetry}>
          {t("common.retry")}
        </Button>
      </div>
    );
  }

  const { items, total } = state.data;
  if (items.length === 0 && total === 0) {
    return <p className="px-4 py-10 text-center text-sm text-muted-foreground">{t("jobs.detail.runs.empty")}</p>;
  }
  const totalPages = Math.max(1, Math.ceil(total / RUNS_PAGE_SIZE));

  return (
    <div className="overflow-hidden rounded-lg border bg-card">
      <div role="row" className={cn(columns, "bg-accent py-2.25 text-xs text-muted-foreground")}>
        <span role="columnheader">{t("jobs.detail.runs.columns.status")}</span>
        <span role="columnheader">{t("jobs.detail.runs.columns.trigger")}</span>
        <span role="columnheader">{t("jobs.detail.runs.columns.startedAt")}</span>
        <span role="columnheader">{t("jobs.detail.runs.columns.duration")}</span>
        <span role="columnheader">{t("jobs.detail.runs.columns.bytes")}</span>
        <span role="columnheader">{t("jobs.detail.runs.columns.snapshot")}</span>
      </div>
      {/* 这一页没有记录（例如期间被清理）：保留页码与翻页，可以回到有记录的页 */}
      {items.length === 0 && (
        <p className="border-t px-4 py-10 text-center text-sm text-muted-foreground">{t("jobs.detail.runs.empty")}</p>
      )}
      {items.map((run) => {
        const expanded = expandedId === run.id;
        const started = run.started_at > 0;
        return (
          <div key={run.id}>
            <div
              role="row"
              tabIndex={0}
              aria-expanded={expanded}
              onClick={() => toggle(run)}
              onKeyDown={(e) => {
                if (e.key === "Enter" || e.key === " ") {
                  e.preventDefault();
                  toggle(run);
                }
              }}
              className={cn(columns, "cursor-pointer border-t py-2.5 text-sm outline-none hover:bg-accent/50")}
            >
              <span role="cell">
                <RunStatusTag status={run.status} />
              </span>
              <span role="cell" className="truncate text-xs text-muted-foreground">
                {triggerLabel(t, run)}
              </span>
              <span role="cell" className="truncate font-mono text-xs">
                {started ? formatDateTime(run.started_at) : "—"}
              </span>
              <span role="cell" className="font-mono text-xs">
                {started ? formatDuration(run.duration_ms) : "—"}
              </span>
              <span role="cell" className="truncate font-mono text-xs text-muted-foreground">
                {formatBytes(run.exported_bytes)} / {formatBytes(run.uploaded_bytes)}
              </span>
              <span role="cell" className="truncate font-mono text-xs">
                {run.snapshot_id || "—"}
              </span>
            </div>
            {expanded && (
              <div role="row" className="border-t bg-accent/20 px-4 py-3.5">
                <div role="cell" className="flex flex-col gap-3">
                  {run.status === "failed" && <p className="text-sm text-destructive">{failureText(t, run)}</p>}
                  <div>
                    <h4 className="mb-1.5 text-xs font-medium text-muted-foreground">
                      {t("jobs.detail.runs.log.title")}
                    </h4>
                    <LogView state={logs[run.id]} />
                  </div>
                </div>
              </div>
            )}
          </div>
        );
      })}
      <div className="flex items-center justify-between gap-3 border-t px-4 py-2.5 text-xs text-muted-foreground">
        <span className="font-mono">{t("jobs.detail.runs.pageInfo", { page, total: totalPages })}</span>
        <div className="flex gap-2">
          <Button variant="outline" size="sm" disabled={page <= 1} onClick={() => onPageChange(page - 1)}>
            {t("jobs.detail.runs.prevPage")}
          </Button>
          <Button variant="outline" size="sm" disabled={page >= totalPages} onClick={() => onPageChange(page + 1)}>
            {t("jobs.detail.runs.nextPage")}
          </Button>
        </div>
      </div>
    </div>
  );
}

/** 失败在哪一步及原因；没有记录步骤的失败（如 OpsNap 重启时中断）只显示原因 */
function failureText(t: TFunction, run: Run): string {
  if (!run.failed_step) return t("jobs.detail.runs.failedReason", { reason: run.reason });
  const step = t(`jobs.detail.runs.step.${run.failed_step}`);
  return run.reason
    ? t("jobs.detail.runs.failedAtWithReason", { step, reason: run.reason })
    : t("jobs.detail.runs.failedAt", { step });
}

function LogView({ state }: { state?: Loadable<RunLogLine[]> }) {
  const { t } = useTranslation();
  if (!state || state.status === "loading") {
    return <p className="text-xs text-muted-foreground">{t("common.loading")}</p>;
  }
  if (state.status === "error") {
    return (
      <p className="text-xs text-destructive">{t("jobs.detail.runs.log.loadFailed", { message: state.message })}</p>
    );
  }
  if (state.data.length === 0) {
    return <p className="text-xs text-muted-foreground">{t("jobs.detail.runs.log.empty")}</p>;
  }
  return (
    <ol className="flex flex-col gap-1 rounded-md bg-background px-3 py-2.5 font-mono text-2xs text-muted-foreground">
      {state.data.map((line, i) =>
        line.omitted ? (
          <li key={i} className="py-0.5 text-center italic">
            {t("jobs.detail.runs.log.omitted", { count: line.omitted })}
          </li>
        ) : (
          <li key={i}>
            <span>{formatLogTime(line.time)}</span> <span>[{t(`jobs.detail.runs.step.${line.step}`)}]</span>{" "}
            <span className="text-foreground">{line.message}</span>
          </li>
        )
      )}
    </ol>
  );
}
