// 运行记录列表与运行详情（步骤、实时日志、停止运行）。
import * as React from "react";
import { Link, useParams, useSearchParams } from "react-router";
import { Check, ChevronLeft, Square, X } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { FormError, Mono, PageTitle, Sheet, Skeleton, Status, TypeTile } from "@/components/bits";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { useShellRefresh } from "@/components/shell-status";
import {
  API_BASE,
  api,
  errorMessage,
  type JobItem,
  type LogLine,
  type RunItem,
  type RunStatusValue,
  type RunStep,
} from "@/lib/api";
import { fmtBytes } from "@/lib/format";
import { sourceOf } from "@/lib/jobs";
import {
  fmtClock,
  fmtDurationMs,
  fmtLogTime,
  fmtStart,
  groupByDay,
  runStatusView,
  speedBetween,
  type Sample,
} from "@/lib/runs";
import { useLoad } from "@/lib/use-load";
import { useNow } from "@/lib/use-now";
import { cn } from "@/lib/utils";
import { LoadError } from "./settings";

const STATUS_TABS: { id: "" | RunStatusValue; label: string }[] = [
  { id: "", label: "全部" },
  { id: "running", label: "运行中" },
  { id: "success", label: "成功" },
  { id: "warning", label: "有警告" },
  { id: "failed", label: "失败" },
  { id: "skipped", label: "已跳过" },
];

const PAGE_SIZE = 30;
const ROW_COLS = "md:grid-cols-[52px_minmax(200px,300px)_110px_1fr_90px_90px]";

// ---------------------------------------------------------------- 列表

export function RunsPage() {
  const [params, setParams] = useSearchParams();
  const jobId = Number(params.get("job_id")) || 0;
  const status = params.get("status") ?? "";

  const loadJobs = React.useCallback(async () => (await api.jobs.list("all")).list ?? [], []);
  const { data: jobs } = useLoad(loadJobs);

  function setFilter(patch: { job_id?: number; status?: string }) {
    const next = new URLSearchParams(params);
    for (const [k, v] of Object.entries(patch)) {
      if (v) next.set(k, String(v));
      else next.delete(k);
    }
    setParams(next, { replace: true });
  }

  return (
    <>
      <PageTitle title="运行记录" />
      <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
        <div className="flex flex-wrap gap-1" role="tablist" aria-label="状态筛选">
          {STATUS_TABS.map((t) => (
            <button
              key={t.id}
              role="tab"
              aria-selected={status === t.id}
              onClick={() => setFilter({ status: t.id })}
              className={cn(
                "rounded-full px-3 py-1 text-[13px] text-ink-2 hover:text-ink",
                status === t.id && "bg-ink text-paper hover:text-paper"
              )}
            >
              {t.label}
            </button>
          ))}
        </div>
        <Select
          value={jobId ? String(jobId) : "all"}
          onValueChange={(v) => setFilter({ job_id: v === "all" ? 0 : Number(v) })}
        >
          <SelectTrigger className="h-9 w-full bg-surface sm:w-56" aria-label="按任务筛选">
            <SelectValue placeholder="全部任务" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">全部任务</SelectItem>
            {jobsForSelect(jobs, jobId).map((j) => (
              <SelectItem key={j.id} value={String(j.id)}>
                {j.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
      <RunsList key={`${jobId}|${status}`} jobId={jobId} status={status} />
    </>
  );
}

/** 地址里的任务不在列表里时（例如任务已删除）也要能显示出来 */
function jobsForSelect(jobs: JobItem[] | undefined, current: number): Pick<JobItem, "id" | "name">[] {
  const list = jobs ?? [];
  if (current && !list.some((j) => j.id === current)) return [...list, { id: current, name: `任务 ${current}` }];
  return list;
}

/** 一组筛选条件下的分页列表；筛选变化时由 key 重建 */
function RunsList({ jobId, status }: { jobId: number; status: string }) {
  const [items, setItems] = React.useState<RunItem[] | null>(null);
  const [total, setTotal] = React.useState(0);
  /** 正在读取（或上次读取失败）的页；loaded 是已经成功读到的页数 */
  const [page, setPage] = React.useState(1);
  const [loaded, setLoaded] = React.useState(0);
  const [retry, setRetry] = React.useState(0);
  const [error, setError] = React.useState<string | null>(null);
  const [busy, setBusy] = React.useState(true);

  React.useEffect(() => {
    let cancelled = false;
    api.runs
      .list({ job_id: jobId, status, page, size: PAGE_SIZE })
      .then((res) => {
        if (cancelled) return;
        const list = res.list ?? [];
        // 按偏移分页，期间有新运行时下一页会重复已显示的行，按 id 去重
        setItems((prev) => {
          if (page === 1 || !prev) return list;
          const seen = new Set(prev.map((r) => r.id));
          return [...prev, ...list.filter((r) => !seen.has(r.id))];
        });
        setTotal(res.total);
        setLoaded(page);
        setError(null);
      })
      .catch((err) => !cancelled && setError(errorMessage(err)))
      .finally(() => !cancelled && setBusy(false));
    return () => {
      cancelled = true;
    };
  }, [jobId, status, page, retry]);

  if (error && !items) {
    return (
      <LoadError
        what="运行记录"
        message={error}
        onRetry={() => {
          setBusy(true);
          setRetry((n) => n + 1);
        }}
      />
    );
  }
  if (!items) {
    return (
      <Sheet className="p-5" aria-busy>
        {[0, 1, 2, 3].map((i) => (
          <div key={i} className="flex items-center gap-4 py-3">
            <Skeleton className="h-4 w-10" />
            <Skeleton className="h-4 w-48" />
            <Skeleton className="ml-auto h-4 w-32" />
          </div>
        ))}
      </Sheet>
    );
  }
  if (items.length === 0) {
    return (
      <Sheet className="px-6 py-14 text-center">
        <h2 className="text-[15px] font-semibold">{jobId || status ? "没有符合条件的运行记录" : "还没有运行记录"}</h2>
        <p className="mt-1.5 text-ink-2">任务按计划运行或手动运行后，记录会出现在这里。</p>
      </Sheet>
    );
  }

  const maxMs = Math.max(1, ...items.map((r) => r.duration_ms));
  return (
    <div className="flex flex-col gap-10">
      {groupByDay(items).map((g) => (
        <section key={g.label}>
          <h2 className="mb-2 text-[13px] font-medium text-ink-3">{g.label}</h2>
          <Sheet className="overflow-hidden">
            <ul className="divide-y divide-line">
              {g.rows.map((r) => (
                <RunRow key={r.id} run={r} maxMs={maxMs} />
              ))}
            </ul>
          </Sheet>
        </section>
      ))}
      {items.length < total && (
        <div className="flex flex-col items-center gap-2">
          <FormError>{error && `读取更多运行记录失败：${error}`}</FormError>
          <Button
            variant="outline"
            disabled={busy}
            onClick={() => {
              setBusy(true);
              // 总是读已加载的下一页：上次失败时重新读同一页，不跳过
              setPage(loaded + 1);
              setRetry((n) => n + 1);
            }}
          >
            加载更多
          </Button>
        </div>
      )}
    </div>
  );
}

function RunRow({ run: r, maxMs }: { run: RunItem; maxMs: number }) {
  const view = runStatusView(r.status);
  const running = r.status === "running";
  return (
    <li>
      <Link
        to={`/runs/${r.id}`}
        className={cn(
          "grid grid-cols-[48px_1fr_auto] items-center gap-4 px-5 py-3 text-left hover:bg-sunken/40",
          ROW_COLS
        )}
      >
        <span className="text-[13px] text-ink-2 num">{fmtClock(r.started_at)}</span>
        <span className="flex min-w-0 items-center gap-3">
          <span className="min-w-0">
            <span className="block truncate font-medium">{r.job_name}</span>
            <span className="block text-xs text-ink-3 num">
              #{r.id}，{r.trigger_label}
            </span>
          </span>
        </span>
        <Status status={view.status}>{view.text}</Status>
        <span className="hidden items-center gap-3 md:flex">
          <span className="h-1.5 flex-1 overflow-hidden rounded-full bg-sunken">
            <span
              className={cn(
                "block h-full rounded-full",
                running ? "w-full bg-brand" : r.status === "failed" ? "bg-fail-fill" : "bg-ink-3"
              )}
              style={running ? undefined : { width: `${Math.max(2, (r.duration_ms / maxMs) * 100)}%` }}
            />
          </span>
          <span className="w-20 text-right text-xs text-ink-2 num">
            {running ? "进行中" : r.status === "skipped" ? "—" : fmtDurationMs(r.duration_ms)}
          </span>
        </span>
        <span className="hidden text-right text-[13px] num md:block">
          {r.bytes_processed > 0 ? fmtBytes(r.bytes_processed) : "—"}
        </span>
        <span className="hidden text-right text-[13px] text-ink-2 num md:block">
          {r.bytes_added > 0 ? `+${fmtBytes(r.bytes_added)}` : "—"}
        </span>
      </Link>
    </li>
  );
}

// ---------------------------------------------------------------- 详情

const DETAIL_POLL_MS = 3000;

export function RunDetailPage() {
  const id = Number(useParams().id);
  const refreshShell = useShellRefresh();
  const [run, setRun] = React.useState<RunItem | null>(null);
  const [speed, setSpeed] = React.useState<number | null>(null);
  const [error, setError] = React.useState<string | null>(null);
  const [tick, setTick] = React.useState(0);
  const [stopping, setStopping] = React.useState(false);
  const sampleRef = React.useRef<(Sample & { runId: number }) | undefined>(undefined);
  const reload = React.useCallback(() => setTick((n) => n + 1), []);

  // 换了一条运行记录：清掉旧数据，在渲染期间对比
  const [prevId, setPrevId] = React.useState(id);
  if (prevId !== id) {
    setPrevId(id);
    setRun(null);
    setSpeed(null);
    setError(null);
  }

  React.useEffect(() => {
    let cancelled = false;
    api.runs
      .get(id)
      .then((r) => {
        if (cancelled) return;
        const now = Date.now();
        const last = sampleRef.current;
        setSpeed(speedBetween(last && last.runId === id ? last : undefined, r.bytes_processed, now));
        sampleRef.current = r.status === "running" ? { runId: id, bytes: r.bytes_processed, at: now } : undefined;
        setRun(r);
        setError(null);
      })
      .catch((err) => !cancelled && setError(errorMessage(err)));
    return () => {
      cancelled = true;
    };
  }, [id, tick]);

  const running = run?.status === "running";
  // 运行中每 3 秒刷新一次数据量和步骤
  React.useEffect(() => {
    if (!running) return;
    const timer = window.setInterval(reload, DETAIL_POLL_MS);
    return () => window.clearInterval(timer);
  }, [running, reload]);

  const now = useNow(running);

  const back = (
    <Link to="/runs" className="mb-4 inline-flex items-center gap-1 text-[13px] text-ink-2 hover:text-ink">
      <ChevronLeft className="size-4" aria-hidden />
      运行记录
    </Link>
  );

  if (error && !run) {
    return (
      <>
        {back}
        <LoadError what="运行记录" message={error} onRetry={reload} />
      </>
    );
  }
  if (!run) {
    return (
      <>
        {back}
        <Skeleton className="mb-8 h-10 w-72" />
        <Skeleton className="h-24 w-full" />
      </>
    );
  }

  const view = runStatusView(run.status);
  const elapsed = running ? Math.max(0, now - run.started_at * 1000) : run.duration_ms;
  const cells: [string, React.ReactNode][] = [
    [
      "状态",
      <Status key="s" status={view.status} className="text-[15px]">
        {view.text}
      </Status>,
    ],
    ["已处理", run.bytes_processed > 0 || running ? fmtBytes(run.bytes_processed) : "—"],
    running
      ? ["速度", speed === null ? "—" : `${fmtBytes(speed)}/s`]
      : ["新增存储", run.bytes_added > 0 ? `+${fmtBytes(run.bytes_added)}` : "—"],
    [running ? "已用时" : "耗时", run.status === "skipped" ? "—" : fmtDurationMs(elapsed)],
  ];
  const snapshots = run.snapshot_ids ?? [];

  return (
    <>
      {back}
      <div className="mb-8 flex flex-wrap items-start justify-between gap-4">
        <div className="flex items-center gap-4">
          <JobTile jobId={run.job_id} />
          <div>
            <h1 className="text-[26px] leading-tight font-semibold tracking-[-0.01em]">{run.job_name}</h1>
            <p className="mt-1 text-[13px] text-ink-2">
              第 {run.id} 次运行，{run.trigger_label}开始于 {fmtStart(run.started_at)}
            </p>
          </div>
        </div>
        {running && (
          <Button variant="outline" className="text-fail hover:text-fail" onClick={() => setStopping(true)}>
            <Square />
            停止运行
          </Button>
        )}
      </div>

      <Sheet className="mb-6 grid grid-cols-2 divide-line md:grid-cols-4 md:divide-x">
        {cells.map(([k, v]) => (
          <div key={k} className="px-5 py-4">
            <div className="text-xs text-ink-3">{k}</div>
            <div className="mt-1 text-[18px] font-semibold num">{v}</div>
          </div>
        ))}
      </Sheet>

      {(run.reason || run.notify_failed) && !running && (
        <Sheet
          className={cn("mb-6 px-5 py-3.5 text-[13.5px]", run.status === "failed" && "border-fail/40 bg-fail-wash")}
          role="status"
        >
          {run.reason && <p className="font-medium">{run.reason}</p>}
          {run.notify_failed && <p className="text-ink-2">通知发送失败，请到通知页检查 Webhook。</p>}
        </Sheet>
      )}

      {snapshots.length > 0 && (
        <div className="mb-6 flex flex-wrap items-center gap-2 text-[13px]">
          <span className="text-ink-3">快照</span>
          {snapshots.map((s) => (
            <Mono key={s} className="rounded-md border border-line bg-surface px-2 py-0.5">
              {s}
            </Mono>
          ))}
        </div>
      )}

      <div className="grid gap-6 lg:grid-cols-[280px_1fr]">
        <ol className="flex flex-col">
          {(run.steps ?? []).map((s, i, all) => (
            <StepItem key={s.key} step={s} last={i === all.length - 1} index={i + 1} />
          ))}
        </ol>
        <LogPanel key={run.id} runId={run.id} running={running} onEnd={reload} />
      </div>

      <ConfirmDialog
        open={stopping}
        title="停止这次运行？"
        description="会终止整个导出进程，本次运行记为失败，原因写「手动停止」，不留下快照。"
        confirmLabel="停止"
        onClose={() => setStopping(false)}
        onConfirm={async () => {
          await api.runs.stop(run.id);
          setStopping(false);
          toast.success("已发出停止指令");
          reload();
          refreshShell();
        }}
      />
    </>
  );
}

function JobTile({ jobId }: { jobId: number }) {
  const load = React.useCallback(() => api.jobs.get(jobId), [jobId]);
  const { data } = useLoad(load);
  return <TypeTile type={data ? sourceOf(data.source_type).tile : "files"} size="lg" />;
}

// 已跳过也是完成态：这一步处理过了，只是没有要做的事（例如存储位置被别的备份占用）
const STEP_STATE = (s: RunStep["status"]) =>
  s === "success" || s === "warning" || s === "skipped"
    ? "done"
    : s === "running"
      ? "active"
      : s === "failed"
        ? "failed"
        : "todo";

function StepItem({ step: s, last, index }: { step: RunStep; last: boolean; index: number }) {
  const state = STEP_STATE(s.status);
  const label = {
    success: "成功",
    warning: "有警告",
    failed: "失败",
    running: "进行中",
    pending: "未开始",
    skipped: "已跳过",
  }[s.status];
  const took = s.started_at > 0 && s.finished_at > s.started_at ? fmtDurationMs(s.finished_at - s.started_at) : "";
  return (
    <li className="relative flex gap-4 pb-7 last:pb-0">
      {!last && (
        <span
          className={cn(
            "absolute top-7 left-[11px] h-[calc(100%-28px)] w-[2px] rounded",
            state === "done" ? "bg-ok-fill" : "bg-line-strong"
          )}
        />
      )}
      <span
        role="img"
        aria-label={label}
        className={cn(
          "relative flex size-6 shrink-0 items-center justify-center rounded-full border-2 text-[11px] font-semibold num",
          state === "done" &&
            (s.status === "warning"
              ? "border-warn-fill bg-warn-fill text-white"
              : s.status === "skipped"
                ? "border-line-strong bg-surface text-ink-3"
                : "border-ok-fill bg-ok-fill text-white"),
          state === "active" && "border-brand bg-surface text-brand",
          state === "failed" && "border-fail-fill bg-fail-fill text-white",
          state === "todo" && "border-line-strong bg-surface text-ink-3"
        )}
      >
        {state === "done" ? <Check className="size-3.5" /> : state === "failed" ? <X className="size-3.5" /> : index}
        {state === "active" && (
          <span className="absolute inset-[-2px] rounded-full border-2 border-brand [animation:dot-ring_1.8s_ease-out_infinite]" />
        )}
      </span>
      <div className="min-w-0 pt-0.5">
        <div className={cn("text-[14px] font-medium", (state === "todo" || s.status === "skipped") && "text-ink-3")}>
          {s.label}
          {s.status === "skipped" && <span className="ml-1.5 text-[12.5px] font-normal text-ink-3">{label}</span>}
        </div>
        {s.message && <div className="mt-0.5 text-[12.5px] break-words text-ink-2">{s.message}</div>}
        {took && <div className="mt-0.5 text-[12.5px] text-ink-3 num">{took}</div>}
      </div>
    </li>
  );
}

/**
 * 日志。运行中用 SSE：服务端先补发已有的行再推送新行（重连时会从头补发，所以每次连上先清空）；
 * 已结束的运行直接读取完整日志。「只看警告」在本地过滤警告和错误。
 */
function LogPanel({ runId, running, onEnd }: { runId: number; running: boolean; onEnd: () => void }) {
  const [lines, setLines] = React.useState<LogLine[]>([]);
  const [warnOnly, setWarnOnly] = React.useState(false);
  const [loadError, setLoadError] = React.useState<string | null>(null);
  const boxRef = React.useRef<HTMLDivElement>(null);

  React.useEffect(() => {
    if (running) {
      const es = new EventSource(`${API_BASE}/runs/${runId}/log/stream`);
      es.addEventListener("open", () => setLines([]));
      es.addEventListener("line", (ev) => {
        try {
          const line = JSON.parse((ev as MessageEvent<string>).data) as LogLine;
          setLines((prev) => [...prev, line]);
        } catch {
          // 无法解析的行直接丢弃
        }
      });
      es.addEventListener("end", () => {
        es.close();
        onEnd();
      });
      return () => es.close();
    }
    let cancelled = false;
    api.runs
      .log(runId)
      .then((res) => {
        if (cancelled) return;
        setLines(res.lines ?? []);
        setLoadError(null);
      })
      .catch((err) => !cancelled && setLoadError(errorMessage(err)));
    return () => {
      cancelled = true;
    };
  }, [runId, running, onEnd]);

  // 运行中新增一行时滚到底部
  React.useEffect(() => {
    const el = boxRef.current;
    if (running && el) el.scrollTop = el.scrollHeight;
  }, [lines.length, running]);

  const warnCount = lines.filter((l) => l.level !== "info").length;
  const shown = warnOnly ? lines.filter((l) => l.level !== "info") : lines;
  return (
    <div className="flex min-h-[440px] flex-col overflow-hidden rounded-xl bg-log-bg text-log-ink">
      <div className="flex items-center justify-between border-b border-white/10 px-4 py-2.5">
        <div className="flex gap-1 text-[12.5px]">
          <button
            onClick={() => setWarnOnly(false)}
            aria-pressed={!warnOnly}
            className={cn(
              "rounded-md px-2 py-0.5 text-white/55 hover:text-white",
              !warnOnly && "bg-white/10 text-white"
            )}
          >
            全部日志
          </button>
          <button
            onClick={() => setWarnOnly(true)}
            aria-pressed={warnOnly}
            className={cn(
              "rounded-md px-2 py-0.5 text-white/55 hover:text-white",
              warnOnly && "bg-white/10 text-white"
            )}
          >
            只看警告 {warnCount}
          </button>
        </div>
        {running && (
          <span className="flex items-center gap-2 text-[12px] text-white/55">
            <span className="size-1.5 rounded-full bg-brand [animation:now-pulse_1.4s_ease-in-out_infinite]" />
            实时
          </span>
        )}
      </div>
      <div
        ref={boxRef}
        className="max-h-[70vh] flex-1 overflow-auto px-4 py-3 font-mono text-[12.5px] leading-[1.9] select-text"
      >
        {loadError && <p className="text-fail">读取日志失败：{loadError}</p>}
        {shown.length === 0 && !loadError && (
          <p className="text-white/45">{warnOnly ? "没有警告" : running ? "等待日志…" : "没有日志"}</p>
        )}
        {shown.map((l, i) => (
          <div key={i} className="flex gap-4">
            <span className="shrink-0 text-white/35 num">{fmtLogTime(l.t)}</span>
            <span
              className={cn(
                "break-all",
                l.level === "warn" && "text-warn-fill",
                l.level === "error" && "text-fail-fill"
              )}
            >
              {l.text}
            </span>
          </div>
        ))}
        {running && (
          <span
            className="mt-1 inline-block h-4 w-2 bg-log-ink/80 [animation:now-pulse_1s_steps(2)_infinite]"
            aria-hidden
          />
        )}
      </div>
    </div>
  );
}
