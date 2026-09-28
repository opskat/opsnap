import { Database } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";

import { formatDateTime } from "@/lib/format";
import type { JobItem } from "@/lib/jobs";
import { cn } from "@/lib/utils";

import { JobRowActions, type JobRowActionHandlers, type JobRowBusy } from "./JobRowActions";
import { lastRunDetail } from "./runFormat";
import { RunStatusTag } from "./RunStatusTag";
import { scheduleDescription } from "./schedule";

const columns = "grid grid-cols-[minmax(0,1.3fr)_330px_170px_250px_auto] items-center gap-4 px-4.5";

function scopeText(t: (key: string, opts?: Record<string, unknown>) => string, job: JobItem) {
  return job.scope === "instance"
    ? t("jobs.list.scope.instance")
    : t("jobs.list.scope.databases", { count: job.databases.length });
}

/** “类型 · 范围 · 启用状态”：拼成一个字符串渲染成单个文本节点，避免多个表达式散落成相邻文本节点 */
function metaLine(t: (key: string, opts?: Record<string, unknown>) => string, job: JobItem) {
  const kind = t(`sources.dataSource.kind.${job.datasource_kind}`);
  const enabled = job.enabled ? t("jobs.list.enabled") : t("jobs.list.paused");
  return [kind, scopeText(t, job), enabled].join(" · ");
}

export function JobTable({
  items,
  busy,
  actions,
}: {
  items: JobItem[];
  /** 正在进行行内操作的任务 ID → 操作种类 */
  busy: Record<number, JobRowBusy>;
  actions: JobRowActionHandlers;
}) {
  const { t, i18n } = useTranslation();
  return (
    <div role="table" aria-label={t("nav.jobs")} className="overflow-hidden rounded-lg border bg-card">
      <div role="row" className={cn(columns, "bg-accent py-2.25 text-xs text-muted-foreground")}>
        <span role="columnheader">{t("jobs.list.columns.name")}</span>
        <span role="columnheader">{t("jobs.list.columns.route")}</span>
        <span role="columnheader">{t("jobs.list.columns.schedule")}</span>
        <span role="columnheader">{t("jobs.list.columns.lastRun")}</span>
        <span role="columnheader" className="text-right">
          {t("jobs.list.columns.actions")}
        </span>
      </div>
      {items.map((job) => {
        const snapshotCount = job.snapshot_count ?? 0;
        const lastRun = job.last_run ?? null;
        return (
          <div key={job.id} role="row" className={cn(columns, "border-t py-3 text-sm")}>
            <span role="cell" className="flex min-w-0 items-center gap-3">
              <span className="flex size-8 shrink-0 items-center justify-center rounded-md border bg-background text-muted-foreground [&_svg]:size-4">
                <Database />
              </span>
              <span className="flex min-w-0 flex-col">
                <Link to={`/jobs/${job.id}`} className="truncate font-medium hover:underline">
                  {job.name}
                </Link>
                <span className="truncate text-xs text-muted-foreground">{metaLine(t, job)}</span>
              </span>
            </span>
            <span role="cell" className="flex min-w-0 flex-col">
              <span className="truncate text-sm">{`${job.datasource_name} → ${job.storage_name}`}</span>
              <span className="truncate font-mono text-xs text-muted-foreground">{job.location}</span>
            </span>
            <span role="cell" className="flex min-w-0 flex-col gap-0.5">
              <span className="truncate text-sm">{scheduleDescription(t, i18n.language, job.schedule)}</span>
              <span className="truncate text-xs text-muted-foreground">
                {job.enabled
                  ? job.next_run_at > 0 && t("jobs.list.nextRun", { time: formatDateTime(job.next_run_at) })
                  : t("jobs.list.paused")}
              </span>
            </span>
            <span role="cell" className="flex min-w-0 flex-col gap-1">
              <span className="flex min-w-0 items-center gap-2">
                {lastRun ? (
                  <RunStatusTag status={lastRun.status} />
                ) : (
                  <span className="text-xs text-muted-foreground">{t("jobs.list.noRun")}</span>
                )}
                <span className="truncate text-2xs text-muted-foreground">
                  {t("jobs.list.snapshotCount", { count: snapshotCount })}
                </span>
              </span>
              {lastRun && <span className="truncate text-xs text-muted-foreground">{lastRunDetail(t, lastRun)}</span>}
            </span>
            <span role="cell">
              <JobRowActions job={job} busy={busy[job.id]} actions={actions} />
            </span>
          </div>
        );
      })}
    </div>
  );
}
