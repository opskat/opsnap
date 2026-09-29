import { ArrowLeft, Lock, Pause, Play } from "lucide-react";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link, useParams } from "react-router";

import type { JobRowBusy } from "@/components/jobs/JobRowActions";
import { errorMessage, type Loadable } from "@/components/jobs/loadable";
import { lastRunDetail } from "@/components/jobs/runFormat";
import { RunsTable } from "@/components/jobs/RunsTable";
import { scheduleDescription } from "@/components/jobs/schedule";
import type { SchedulePreview } from "@/components/jobs/StepSchedule";
import { Button } from "@/components/ui/button";
import { ApiError } from "@/lib/api";
import { ErrorCode } from "@/lib/auth";
import { formatDateTime, joinNames } from "@/lib/format";
import {
  cancelRun,
  enableJob,
  formatBytes,
  formatScheduleTime,
  getJob,
  getJobStats,
  isRunActive,
  listRuns,
  pauseJob,
  runJobNow,
  schedulePreview,
  type JobItem,
  type JobStats,
  type Run,
} from "@/lib/jobs";
import { listStorages, type Storage } from "@/lib/storage";
import { cn } from "@/lib/utils";

/** 运行中或排队中时轮询的间隔（与任务列表页、数据源详情页一致） */
const POLL_INTERVAL_MS = 3000;
/** 没有运行中、排队中的运行时的轮询间隔：计划触发的运行开始后不需要手动刷新页面也能看到 */
const IDLE_POLL_INTERVAL_MS = 30000;

type LoadState =
  | { status: "loading" }
  | { status: "not_found" }
  | { status: "error"; message: string }
  | { status: "ready"; item: JobItem };

/** 最近一次运行的标识：变化（新的运行，或同一运行的状态变化）时刷新统计 */
const runKey = (run?: Run | null) => (run ? `${run.id}:${run.status}` : "");

/** 任务详情页：/jobs/:id（docs/specs/2026-09-27-backup-jobs.md「任务详情」） */
export function JobDetailPage() {
  const { t, i18n } = useTranslation();
  const params = useParams<{ id: string }>();
  const numericId = Number(params.id);
  const validId = Number.isInteger(numericId) && numericId > 0;

  const [job, setJob] = useState<LoadState>(() => (validId ? { status: "loading" } : { status: "not_found" }));
  const [jobAttempt, setJobAttempt] = useState(0);
  const [busy, setBusy] = useState<JobRowBusy>();
  // 请求进行中：头部的操作在上一个请求返回前不能再次提交
  const inflight = useRef(false);
  // 每次操作改动任务后加一：在它之前发出的轮询请求返回时已过时，丢弃其任务数据，避免把刚做的修改改回去
  const mutations = useRef(0);
  const [actionError, setActionError] = useState<string>();

  const [stats, setStats] = useState<Loadable<JobStats>>({ status: "loading" });
  const [statsAttempt, setStatsAttempt] = useState(0);
  const [storages, setStorages] = useState<Storage[]>([]);
  const [preview, setPreview] = useState<Loadable<SchedulePreview>>({ status: "loading" });

  const [page, setPage] = useState(1);
  const [runs, setRuns] = useState<Loadable<{ items: Run[]; total: number }>>({ status: "loading" });
  const [runsAttempt, setRunsAttempt] = useState(0);

  const jobId = job.status === "ready" ? job.item.id : undefined;

  // 加载任务本身；404 与其他错误分开处理，出错不出现空白页
  useEffect(() => {
    if (!validId) return;
    let cancelled = false;
    getJob(numericId)
      .then((r) => !cancelled && setJob({ status: "ready", item: r.item }))
      .catch((err: unknown) => {
        if (cancelled) return;
        if (err instanceof ApiError && err.status === 404) setJob({ status: "not_found" });
        else setJob({ status: "error", message: errorMessage(err) });
      });
    return () => {
      cancelled = true;
    };
  }, [numericId, validId, jobAttempt]);

  // 统计、存储（用于加密指纹）与计划预览：任务加载成功后各自拉取一次
  useEffect(() => {
    if (!jobId) return;
    let cancelled = false;
    getJobStats(jobId)
      .then((r) => !cancelled && setStats({ status: "ready", data: r }))
      .catch((err: unknown) => !cancelled && setStats({ status: "error", message: errorMessage(err) }));
    return () => {
      cancelled = true;
    };
  }, [jobId, statsAttempt]);

  useEffect(() => {
    if (job.status !== "ready") return;
    let cancelled = false;
    listStorages()
      .then((r) => !cancelled && setStorages(r.items))
      .catch(() => {
        // 仅用于展示加密指纹，加载失败时该字段显示“未知”，不影响页面其余内容
      });
    schedulePreview({ schedule: job.item.schedule, retention: job.item.retention })
      .then(
        (r) =>
          !cancelled && setPreview({ status: "ready", data: { nextRuns: r.next_runs, maxSnapshots: r.max_snapshots } })
      )
      .catch((err: unknown) => !cancelled && setPreview({ status: "error", message: errorMessage(err) }));
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps -- 只在任务首次就绪时取一次，任务对象之后的刷新不需要重新计算预览
  }, [jobId]);

  // 运行记录：按页拉取；切换界面语言后重新拉取（失败原因中 OpsNap 的文字由服务端按请求的语言给出）
  const language = i18n.language;
  useEffect(() => {
    if (!jobId) return;
    let cancelled = false;
    listRuns(jobId, page)
      .then((r) => !cancelled && setRuns({ status: "ready", data: r }))
      .catch((err: unknown) => !cancelled && setRuns({ status: "error", message: errorMessage(err) }));
    return () => {
      cancelled = true;
    };
  }, [jobId, page, runsAttempt, language]);

  // 自动刷新任务与运行记录：运行中或排队中时每 3 秒，其余时候低频刷新（计划触发的运行开始后也能看到）；
  // 有运行结束（包括在两次低频刷新之间开始又结束的运行）时同时刷新统计（快照数、占用、最近成功与成功率）
  const active = job.status === "ready" && isRunActive(job.item.last_run);
  const lastRunKey = useRef("");
  const currentRunKey = job.status === "ready" ? runKey(job.item.last_run) : "";
  useEffect(() => {
    lastRunKey.current = currentRunKey;
  }, [currentRunKey]);
  useEffect(() => {
    if (!jobId) return;
    let cancelled = false;
    let timer: number | undefined;
    const poll = (wasActive: boolean) => {
      timer = window.setTimeout(
        () => {
          if (cancelled) return;
          const seq = mutations.current;
          const before = lastRunKey.current;
          Promise.all([getJob(jobId), listRuns(jobId, page)])
            .then(([j, r]) => {
              if (cancelled) return;
              const nowActive = isRunActive(j.item.last_run);
              if (seq === mutations.current) setJob({ status: "ready", item: j.item });
              setRuns({ status: "ready", data: r });
              if (!nowActive && runKey(j.item.last_run) !== before) setStatsAttempt((n) => n + 1);
              poll(nowActive);
            })
            .catch((err: unknown) => {
              if (cancelled) return;
              // 任务已在别处被删除：显示“不存在”，不再轮询
              if (err instanceof ApiError && err.status === 404) setJob({ status: "not_found" });
              else poll(wasActive);
            });
        },
        wasActive ? POLL_INTERVAL_MS : IDLE_POLL_INTERVAL_MS
      );
    };
    poll(active);
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
    // 切换语言时重新开始轮询：丢弃按原来的语言发出、尚未返回的请求
  }, [jobId, active, page, language]);

  const retryJob = () => {
    setJob({ status: "loading" });
    setJobAttempt((n) => n + 1);
  };

  const refreshRuns = () => {
    setRuns({ status: "loading" });
    setPage(1);
    setRunsAttempt((n) => n + 1);
  };

  const goToRunsPage = (next: number) => {
    setRuns({ status: "loading" });
    setPage(next);
  };

  const retryRuns = () => {
    setRuns({ status: "loading" });
    setRunsAttempt((n) => n + 1);
  };

  const retryStats = () => {
    setStats({ status: "loading" });
    setStatsAttempt((n) => n + 1);
  };

  /** 按操作结果更新任务（基于当前的任务，而不是发起操作时的快照） */
  const updateJob = (update: (item: JobItem) => JobItem) => {
    mutations.current++;
    setJob((j) => (j.status === "ready" ? { status: "ready", item: update(j.item) } : j));
  };

  /** 立即重新读取任务（操作被拒绝时，界面上的状态已经过时） */
  const reloadJob = (id: number) => {
    const seq = ++mutations.current;
    getJob(id)
      .then((r) => seq === mutations.current && setJob({ status: "ready", item: r.item }))
      .catch(() => {
        // 下一次自动刷新会再试
      });
  };

  const withBusy = async (kind: NonNullable<JobRowBusy>, fn: (item: JobItem) => Promise<void>) => {
    if (job.status !== "ready" || inflight.current) return;
    inflight.current = true;
    setBusy(kind);
    setActionError(undefined);
    try {
      await fn(job.item);
    } catch (err) {
      setActionError(errorMessage(err));
      // 已在运行或排队、运行已结束：按服务端的当前状态刷新，换成正确的操作
      if (
        err instanceof ApiError &&
        (err.code === ErrorCode.JobRunAlreadyActive || err.code === ErrorCode.JobRunFinished)
      ) {
        reloadJob(job.item.id);
        retryRuns();
      }
    } finally {
      inflight.current = false;
      setBusy(undefined);
    }
  };

  const runJob = () =>
    withBusy("run", async (item) => {
      const { run } = await runJobNow(item.id);
      updateJob((j) => ({ ...j, last_run: run }));
      refreshRuns();
    });

  const cancel = () =>
    withBusy("cancel", async (item) => {
      if (!item.last_run) return;
      const { run } = await cancelRun(item.id, item.last_run.id);
      updateJob((j) => (j.last_run?.id === run.id ? { ...j, last_run: run } : j));
      retryRuns();
    });

  const togglePause = () =>
    withBusy(job.status === "ready" && job.item.enabled ? "pause" : "enable", async (item) => {
      const { item: updated } = item.enabled ? await pauseJob(item.id) : await enableJob(item.id);
      updateJob(() => updated);
    });

  if (job.status === "loading") {
    return (
      <section className="px-8 py-6">
        <p className="text-sm text-muted-foreground">{t("common.loading")}</p>
      </section>
    );
  }

  if (job.status === "not_found") {
    return (
      <section className="flex flex-col items-center gap-3 px-8 py-16 text-center">
        <p className="text-lg font-medium">{t("jobs.detail.notFoundTitle")}</p>
        <p className="text-sm text-muted-foreground">{t("jobs.detail.notFoundHint")}</p>
        <Button asChild variant="outline">
          <Link to="/jobs">{t("jobs.detail.backToList")}</Link>
        </Button>
      </section>
    );
  }

  if (job.status === "error") {
    return (
      <section className="px-8 py-6">
        <div className="flex items-center justify-between gap-3 rounded-md bg-destructive-soft px-4 py-3">
          <p className="text-sm text-destructive">{t("jobs.detail.loadFailed", { message: job.message })}</p>
          <Button variant="outline" size="sm" onClick={retryJob}>
            {t("common.retry")}
          </Button>
        </div>
      </section>
    );
  }

  const item = job.item;
  const lang = i18n.language;

  const nextRun =
    item.enabled && item.next_run_at > 0
      ? t("jobs.list.nextRun", { time: formatDateTime(item.next_run_at) })
      : undefined;
  const subtitle = [
    `${item.datasource_name} → ${item.storage_name}`,
    scheduleDescription(t, lang, item.schedule),
    item.enabled ? undefined : t("jobs.list.paused"),
  ]
    .filter(Boolean)
    .join(" · ");

  const scopeText =
    item.scope === "instance"
      ? t("jobs.wizard.confirm.scopeInstance")
      : t("jobs.wizard.confirm.scopeDatabases", { count: item.databases.length });
  const databasesList =
    item.scope === "databases" && item.databases.length > 0 ? joinNames(item.databases, lang) : undefined;

  const optionKeys = (
    item.datasource_kind === "postgres" ? ["globals"] : ["routines", "triggers", "events", "users"]
  ) as ("routines" | "triggers" | "events" | "users" | "globals")[];
  const included = optionKeys.filter((k) => item.options[k]).map((k) => t(`jobs.wizard.content.options.${k}`));
  const includedText = included.length > 0 ? joinNames(included, lang) : t("jobs.detail.config.includedNone");

  const storage = storages.find((s) => s.id === item.storage_id);

  return (
    <>
      <header className="flex flex-col gap-3 border-b px-8 py-5">
        <Link
          to="/jobs"
          className="inline-flex w-fit items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground"
        >
          <ArrowLeft className="size-4" />
          {t("jobs.detail.back", { name: t("nav.jobs") })}
        </Link>
        <div className="flex items-start justify-between gap-4">
          <div className="flex flex-col gap-1">
            <div className="flex items-center gap-2">
              <h1 className="text-2xl font-bold">{item.name}</h1>
              <span
                className={cn(
                  "rounded-sm px-1.75 py-0.5 text-2xs font-medium",
                  item.enabled ? "bg-success-soft text-success" : "bg-accent text-muted-foreground"
                )}
              >
                {item.enabled ? t("jobs.list.enabled") : t("jobs.list.paused")}
              </span>
            </div>
            <p className="text-sm text-muted-foreground">
              {subtitle}
              {nextRun && (
                <>
                  {" · "}
                  <span className="font-mono">{nextRun}</span>
                </>
              )}
            </p>
          </div>
          <div className="flex items-center gap-2">
            <Button variant="outline" disabled={busy !== undefined} onClick={() => void togglePause()}>
              {item.enabled ? <Pause /> : <Play />}
              {item.enabled ? t("jobs.list.actions.pause") : t("jobs.list.actions.enable")}
            </Button>
            <Button asChild variant="outline">
              <Link to={`/jobs/${item.id}/edit`}>{t("jobs.list.actions.edit")}</Link>
            </Button>
            {/* 运行中或排队中：立即执行换成取消运行 */}
            {active ? (
              <Button variant="outline" disabled={busy !== undefined} onClick={() => void cancel()}>
                {busy === "cancel" ? t("common.submitting") : t("jobs.list.actions.cancel")}
              </Button>
            ) : (
              <Button disabled={busy !== undefined} onClick={() => void runJob()}>
                {busy === "run" ? t("common.submitting") : t("jobs.list.actions.run")}
              </Button>
            )}
          </div>
        </div>
        {actionError && (
          <p role="alert" className="rounded-md bg-destructive-soft px-3 py-2.5 text-sm text-destructive">
            {actionError}
          </p>
        )}
      </header>

      <section className="grid grid-cols-1 items-start gap-6 px-8 py-6 lg:grid-cols-2">
        <div role="region" aria-label={t("jobs.detail.config.title")} className="rounded-lg border bg-card">
          <h2 className="border-b px-4 py-3 text-sm font-medium">{t("jobs.detail.config.title")}</h2>
          <dl className="flex flex-col gap-3 px-4 py-3.5 text-sm">
            <DetailRow label={t("jobs.wizard.source.title")}>
              {t("jobs.detail.config.sourceWithKind", {
                name: item.datasource_name,
                kind: t(`sources.dataSource.kind.${item.datasource_kind}`),
              })}
            </DetailRow>
            <DetailRow label={t("jobs.wizard.content.scope")}>
              <span>{scopeText}</span>
              {databasesList && <span className="text-xs text-muted-foreground">{databasesList}</span>}
            </DetailRow>
            <DetailRow label={t("jobs.wizard.content.alsoBackup")}>{includedText}</DetailRow>
            <DetailRow label={t("jobs.wizard.content.method")}>{t("jobs.wizard.content.full")}</DetailRow>
            <DetailRow label={t("jobs.wizard.destination.storage")}>{item.storage_name}</DetailRow>
            <DetailRow label={t("jobs.detail.config.location")}>
              <span className="font-mono">{item.location}</span>
            </DetailRow>
            <DetailRow label={t("jobs.wizard.destination.compression")}>
              {item.compression === "none" ? t("jobs.wizard.destination.compressionNone") : item.compression}
            </DetailRow>
            <DetailRow label={t("jobs.wizard.destination.encryption")}>
              {storage ? (
                <>
                  <span className="flex items-center gap-1.5 text-success">
                    <Lock className="size-3.5" />
                    {t("jobs.wizard.destination.encrypted")}
                  </span>
                  <span className="font-mono text-xs text-muted-foreground">
                    {t("jobs.wizard.destination.fingerprint", { fingerprint: storage.fingerprint })}
                  </span>
                </>
              ) : (
                <span className="text-xs text-muted-foreground">{t("jobs.detail.config.encryptionUnknown")}</span>
              )}
            </DetailRow>
            <DetailRow label={t("jobs.detail.config.schedule")}>
              <span>
                {scheduleDescription(t, lang, item.schedule)} ·{" "}
                <span className="font-mono">{item.schedule.timezone}</span>
              </span>
              {preview.status === "ready" && (
                <span className="flex flex-col items-end gap-0.5 font-mono text-xs text-muted-foreground">
                  {preview.data.nextRuns.map((iso) => (
                    <span key={iso}>{formatScheduleTime(iso)}</span>
                  ))}
                </span>
              )}
            </DetailRow>
            <DetailRow label={t("jobs.wizard.schedule.retentionTitle")}>
              {t("jobs.wizard.confirm.retentionSummary", {
                days: item.retention.days,
                weeks: item.retention.weeks,
                months: item.retention.months,
              })}
            </DetailRow>
            <DetailRow label={t("jobs.wizard.schedule.failureTitle")}>
              {t("jobs.wizard.confirm.failureSummary", {
                retries: item.failure.retries,
                interval: item.failure.retry_interval,
                timeout: item.failure.timeout,
              })}
            </DetailRow>
          </dl>
        </div>

        <div role="region" aria-label={t("jobs.detail.stats.title")} className="rounded-lg border bg-card">
          <h2 className="border-b px-4 py-3 text-sm font-medium">{t("jobs.detail.stats.title")}</h2>
          <div className="flex flex-col gap-3 px-4 py-3.5 text-sm">
            {stats.status === "loading" && <p className="text-sm text-muted-foreground">{t("common.loading")}</p>}
            {stats.status === "error" && (
              <div className="flex items-center justify-between gap-3 rounded-md bg-destructive-soft px-3 py-2.5">
                <p className="text-sm text-destructive">{t("jobs.detail.loadFailed", { message: stats.message })}</p>
                <Button variant="outline" size="sm" onClick={retryStats}>
                  {t("common.retry")}
                </Button>
              </div>
            )}
            {stats.status === "ready" && (
              <>
                {stats.data.storage_error && (
                  <p role="alert" className="rounded-md bg-warning-soft px-3 py-2.5 text-xs text-warning">
                    {stats.data.storage_error}
                  </p>
                )}
                <p className="font-mono">
                  {stats.data.snapshot_count > 0
                    ? `${t("jobs.list.snapshotCount", { count: stats.data.snapshot_count })} · ${t("jobs.detail.stats.earliestSnapshot", { time: formatDateTime(stats.data.earliest_snapshot_at) })}`
                    : t("jobs.detail.stats.noSnapshot")}
                </p>
                <p>
                  <span className="text-muted-foreground">{t("jobs.detail.stats.storageUsage")}</span>{" "}
                  <span className="font-mono">{formatBytes(stats.data.packed_bytes)}</span>{" "}
                  <span className="text-xs text-muted-foreground">
                    ({t("jobs.detail.stats.exportTotal", { size: formatBytes(stats.data.export_bytes) })} ·{" "}
                    {t("jobs.detail.stats.savings", { percent: `${Math.round(stats.data.savings * 100)}%` })})
                  </span>
                </p>
                <p>
                  <span className="text-muted-foreground">{t("jobs.detail.stats.lastSuccessTitle")}</span>{" "}
                  {stats.data.last_success ? (
                    <span className="font-mono">{lastRunDetail(t, stats.data.last_success)}</span>
                  ) : (
                    t("jobs.detail.stats.noSuccess")
                  )}
                </p>
                <p className="text-xs text-muted-foreground">
                  {t("jobs.detail.stats.recentTitle")} ·{" "}
                  <span className="font-mono">
                    {t("jobs.detail.stats.recentSummary", {
                      success: stats.data.recent.success,
                      failed: stats.data.recent.failed,
                      rate: `${Math.round(stats.data.recent.success_rate * 100)}%`,
                    })}
                  </span>
                </p>
              </>
            )}
          </div>
        </div>
      </section>

      <section className="flex flex-col gap-2.5 px-8 pb-8">
        <h2 className="text-base font-semibold">{t("jobs.detail.runs.title")}</h2>
        <RunsTable jobId={item.id} state={runs} page={page} onPageChange={goToRunsPage} onRetry={retryRuns} />
      </section>
    </>
  );
}

function DetailRow({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex items-start justify-between gap-3">
      <dt className="pt-0.5 text-muted-foreground">{label}</dt>
      <dd className="flex flex-col items-end gap-0.5 text-right">{children}</dd>
    </div>
  );
}
