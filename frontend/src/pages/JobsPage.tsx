import { ListChecks, Plus, X } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";

import { DeleteJobDialog } from "@/components/jobs/DeleteJobDialog";
import type { JobRowBusy } from "@/components/jobs/JobRowActions";
import { JobTable } from "@/components/jobs/JobTable";
import { errorMessage, type Loadable } from "@/components/jobs/loadable";
import { PageHeader } from "@/components/layout/PageHeader";
import { Button } from "@/components/ui/button";
import { ApiError } from "@/lib/api";
import { ErrorCode } from "@/lib/auth";
import { cancelRun, enableJob, isRunActive, listJobs, pauseJob, runJobNow, type JobItem } from "@/lib/jobs";

/** 有运行中或排队中的任务时刷新列表的间隔 */
const JOB_POLL_INTERVAL_MS = 3000;
/** 没有运行中、排队中的任务时的刷新间隔：计划触发的运行开始后不需要手动刷新页面也能看到 */
const JOB_IDLE_POLL_INTERVAL_MS = 30000;

const isActive = (job: JobItem) => isRunActive(job.last_run);

export function JobsPage() {
  const { t } = useTranslation();
  const [state, setState] = useState<Loadable<JobItem[]>>({ status: "loading" });
  const [attempt, setAttempt] = useState(0);
  const [busy, setBusy] = useState<Record<number, JobRowBusy>>({});
  const [actionError, setActionError] = useState<string>();
  const [notice, setNotice] = useState<string>();
  const [deleting, setDeleting] = useState<JobItem>();
  // 每次操作改动列表后加一：在它之前发出的刷新请求返回时已过时，丢弃其结果，避免把刚做的修改改回去
  const mutations = useRef(0);
  // 请求进行中的行：同一行的操作不能在上一个请求返回前再次提交
  const inflight = useRef(new Set<number>());

  useEffect(() => {
    let cancelled = false;
    listJobs()
      .then((r) => !cancelled && setState({ status: "ready", data: r.items }))
      .catch((err: unknown) => !cancelled && setState({ status: "error", message: errorMessage(err) }));
    return () => {
      cancelled = true;
    };
  }, [attempt]);

  const jobs = state.status === "ready" ? state.data : [];
  const ready = state.status === "ready";
  const anyActive = ready && jobs.some(isActive);

  // 自动刷新，不需要用户手动刷新页面：有运行中、排队中的任务时每 3 秒，其余时候低频刷新，
  // 以便看到计划触发后开始的运行
  useEffect(() => {
    if (!ready) return;
    let cancelled = false;
    let timer: number | undefined;
    const poll = (active: boolean) => {
      timer = window.setTimeout(
        () => {
          if (cancelled) return;
          const seq = mutations.current;
          listJobs()
            .then((r) => {
              if (cancelled) return;
              if (seq === mutations.current) setState({ status: "ready", data: r.items });
              poll(r.items.some(isActive));
            })
            .catch(() => {
              if (!cancelled) poll(active);
            });
        },
        active ? JOB_POLL_INTERVAL_MS : JOB_IDLE_POLL_INTERVAL_MS
      );
    };
    poll(anyActive);
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [ready, anyActive]);

  const retry = () => {
    setState({ status: "loading" });
    setAttempt((n) => n + 1);
  };

  /** 按操作结果更新一个任务（基于当前的行，而不是发起操作时的快照） */
  const updateJob = (id: number, update: (job: JobItem) => JobItem) => {
    mutations.current++;
    setState((s) => (s.status === "ready" ? { ...s, data: s.data.map((j) => (j.id === id ? update(j) : j)) } : s));
  };
  const removeJob = (id: number) => {
    mutations.current++;
    setState((s) => (s.status === "ready" ? { ...s, data: s.data.filter((j) => j.id !== id) } : s));
  };
  /** 立即重新读取列表（操作被拒绝时，界面上的状态已经过时） */
  const reload = () => {
    const seq = ++mutations.current;
    listJobs()
      .then((r) => seq === mutations.current && setState({ status: "ready", data: r.items }))
      .catch(() => {
        // 下一次自动刷新会再试
      });
  };

  const withBusy = async (
    job: JobItem,
    kind: NonNullable<JobRowBusy>,
    fn: () => Promise<(job: JobItem) => JobItem>
  ) => {
    if (inflight.current.has(job.id)) return;
    inflight.current.add(job.id);
    setBusy((b) => ({ ...b, [job.id]: kind }));
    setActionError(undefined);
    try {
      updateJob(job.id, await fn());
    } catch (err) {
      setActionError(errorMessage(err));
      // 已在运行或排队、运行已结束：按服务端的当前状态刷新，换成正确的操作
      if (
        err instanceof ApiError &&
        (err.code === ErrorCode.JobRunAlreadyActive || err.code === ErrorCode.JobRunFinished)
      ) {
        reload();
      }
    } finally {
      inflight.current.delete(job.id);
      setBusy((b) => {
        const rest = { ...b };
        delete rest[job.id];
        return rest;
      });
    }
  };

  const runJob = (job: JobItem) =>
    void withBusy(job, "run", async () => {
      const { run } = await runJobNow(job.id);
      return (j) => ({ ...j, last_run: run });
    });

  const cancelJob = (job: JobItem) =>
    void withBusy(job, "cancel", async () => {
      const runId = job.last_run?.id;
      if (runId === undefined) return (j) => j;
      const { run } = await cancelRun(job.id, runId);
      return (j) => (j.last_run?.id === run.id ? { ...j, last_run: run } : j);
    });

  const togglePause = (job: JobItem) =>
    void withBusy(job, job.enabled ? "pause" : "enable", async () => {
      const { item } = job.enabled ? await pauseJob(job.id) : await enableJob(job.id);
      return () => item;
    });

  return (
    <>
      <PageHeader
        title={t("nav.jobs")}
        subtitle={t("jobs.list.subtitle")}
        actions={
          <Button asChild>
            <Link to="/jobs/new">
              <Plus />
              {t("jobs.list.create")}
            </Link>
          </Button>
        }
      />
      <section className="flex flex-col gap-4 px-8 py-6" aria-live="polite">
        {actionError && (
          <p role="alert" className="rounded-md bg-destructive-soft px-3 py-2.5 text-sm text-destructive">
            {actionError}
          </p>
        )}
        {notice && (
          <div role="alert" className="flex items-center justify-between gap-3 rounded-md bg-warning-soft px-4 py-3">
            <p className="text-sm text-warning">{notice}</p>
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label={t("jobs.list.dismissNotice")}
              onClick={() => setNotice(undefined)}
            >
              <X />
            </Button>
          </div>
        )}

        {state.status === "loading" && <p className="text-sm text-muted-foreground">{t("common.loading")}</p>}

        {state.status === "error" && (
          <div className="flex items-center justify-between gap-3 rounded-md bg-destructive-soft px-4 py-3">
            <p className="text-sm text-destructive">{t("jobs.list.loadFailed", { message: state.message })}</p>
            <Button variant="outline" size="sm" onClick={retry}>
              {t("common.retry")}
            </Button>
          </div>
        )}

        {state.status === "ready" && jobs.length === 0 && (
          <div className="flex flex-col items-center gap-3 rounded-lg border border-dashed px-6 py-12 text-center">
            <ListChecks className="size-8 text-faint-foreground" />
            <p className="text-md font-medium">{t("jobs.list.empty")}</p>
            <p className="max-w-md text-sm text-muted-foreground">{t("jobs.list.emptyHint")}</p>
            <Button asChild>
              <Link to="/jobs/new">
                <Plus />
                {t("jobs.list.create")}
              </Link>
            </Button>
          </div>
        )}

        {state.status === "ready" && jobs.length > 0 && (
          <JobTable
            items={jobs}
            busy={busy}
            actions={{
              onRun: runJob,
              onCancel: cancelJob,
              onPause: togglePause,
              onEnable: togglePause,
              onDelete: setDeleting,
            }}
          />
        )}
      </section>

      <DeleteJobDialog
        job={deleting}
        onCancel={() => setDeleting(undefined)}
        onDeleted={(id, result) => {
          removeJob(id);
          setDeleting(undefined);
          // 快照未能全部删除（部分失败或无法打开存储）时，任务仍已删除，提示原因
          if (result.snapshots_message) setNotice(result.snapshots_message);
        }}
      />
    </>
  );
}
