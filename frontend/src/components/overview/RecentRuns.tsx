import { useState, type ComponentProps } from "react";
import { useTranslation } from "react-i18next";
import { Link, useNavigate } from "react-router";

import { formatDuration } from "@/components/jobs/runFormat";
import { formatBytes, type RunStatus } from "@/lib/jobs";
import type { RecentRun, RecentRuns as RecentRunsData } from "@/lib/overview";
import { cn } from "@/lib/utils";

import { ComingSoon, comingSoonClass } from "./ComingSoon";
import { runTimeLabel } from "./format";
import { Panel, Placeholder } from "./Panel";

type Filter = "all" | "backup" | "failed";

const STATUS_STYLE: Record<RunStatus, { badge: string; dot: string }> = {
  queued: { badge: "bg-pending-soft text-pending", dot: "bg-pending" },
  running: { badge: "bg-running-soft text-running", dot: "bg-running" },
  success: { badge: "bg-success-soft text-success", dot: "bg-success" },
  failed: { badge: "bg-destructive-soft text-destructive", dot: "bg-destructive" },
  canceled: { badge: "bg-accent text-muted-foreground", dot: "bg-muted-foreground" },
  skipped: { badge: "bg-accent text-muted-foreground", dot: "bg-muted-foreground" },
};

const ENGINE: Record<string, string> = { mysql: "MY", postgres: "PG" };

function StatusBadge({ status }: { status: RunStatus }) {
  const { t } = useTranslation();
  const style = STATUS_STYLE[status];
  return (
    <span className={cn("inline-flex items-center gap-1.5 rounded-sm px-2 py-0.5 text-xs font-medium", style.badge)}>
      <span className={cn("size-1.5 rounded-full", style.dot)} />
      {t(`overview.recent.status.${status}`)}
    </span>
  );
}

/** 耗时：运行中为已运行时长，已开始并结束的为总耗时，从未开始的为“—” */
function duration(run: RecentRun) {
  if (run.status === "running" || run.started_at > 0) return formatDuration(run.duration_ms);
  return "—";
}

/** 大小：成功为导出量，运行中为已导出量，其余为“—” */
function size(run: RecentRun) {
  return run.status === "success" || run.status === "running" ? formatBytes(run.exported_bytes) : "—";
}

function FilterButton({ pressed, className, ...props }: ComponentProps<"button"> & { pressed: boolean }) {
  return (
    <button
      type="button"
      aria-pressed={pressed}
      className={cn(
        "rounded-sm px-3 py-1 text-sm outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50",
        pressed ? "bg-card font-semibold text-foreground" : "text-muted-foreground hover:text-foreground",
        className
      )}
      {...props}
    />
  );
}

const COLUMNS = ["job", "type", "status", "duration", "size", "time"] as const;

/** 最近运行（spec「概览」→「最近运行」）；data 为空表示加载中 */
export function RecentRuns({ data }: { data?: RecentRunsData }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [filter, setFilter] = useState<Filter>("all");

  const rows = !data
    ? []
    : filter === "failed"
      ? data.failed
      : data.items.filter((r) => filter === "all" || r.job_type === "backup");

  const filters = (
    <div
      role="group"
      aria-label={t("overview.recent.filterLabel")}
      className="inline-flex gap-0.5 rounded-md bg-accent p-0.75"
    >
      <FilterButton pressed={filter === "all"} onClick={() => setFilter("all")}>
        {t("overview.recent.all")}
      </FilterButton>
      <FilterButton pressed={filter === "backup"} onClick={() => setFilter("backup")}>
        {t("overview.recent.backup")}
      </FilterButton>
      <ComingSoon>
        <FilterButton pressed={false} className={cn(comingSoonClass, "hover:text-muted-foreground")}>
          {t("overview.recent.sync")}
        </FilterButton>
      </ComingSoon>
      <FilterButton pressed={filter === "failed"} onClick={() => setFilter("failed")}>
        {t("overview.recent.failed", { count: data?.failed_24h ?? 0 })}
      </FilterButton>
    </div>
  );

  return (
    <Panel title={t("overview.recent.title")} actions={filters} loading={!data}>
      <div className="overflow-x-auto">
        <table className="w-full text-sm">
          <thead className="bg-muted/50 text-left text-xs text-muted-foreground">
            <tr>
              {COLUMNS.map((c) => (
                <th key={c} scope="col" className="px-3 py-2.5 font-normal whitespace-nowrap first:pl-5 last:pr-5">
                  {t(`overview.recent.columns.${c}`)}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {!data &&
              Array.from({ length: 5 }, (_, i) => (
                <tr key={i} className="border-t">
                  <td colSpan={COLUMNS.length} className="px-5 py-3.5">
                    <Placeholder className="h-8 w-full" />
                  </td>
                </tr>
              ))}
            {rows.map((run) => (
              <tr
                key={run.id}
                className="cursor-pointer border-t hover:bg-accent/60"
                onClick={() => void navigate(`/jobs/${run.job_id}`)}
              >
                <td className="w-full max-w-0 min-w-40 py-3 pr-3 pl-5">
                  <div className="flex min-w-0 items-center gap-3">
                    <span className="flex size-7 shrink-0 items-center justify-center rounded-md bg-accent font-mono text-2xs font-semibold text-muted-foreground">
                      {ENGINE[run.datasource_kind] ?? "—"}
                    </span>
                    <div className="flex min-w-0 flex-col">
                      <Link
                        to={`/jobs/${run.job_id}`}
                        className="truncate font-medium outline-none hover:underline focus-visible:underline"
                        onClick={(e) => e.stopPropagation()}
                      >
                        {run.job_name}
                      </Link>
                      <span className="truncate font-mono text-xs text-muted-foreground">{run.datasource_address}</span>
                    </div>
                  </div>
                </td>
                <td className="px-3 py-3 whitespace-nowrap text-muted-foreground">
                  {t(`overview.recent.type.${run.job_type}`)}
                </td>
                <td className="px-3 py-3 whitespace-nowrap">
                  <StatusBadge status={run.status} />
                </td>
                <td className="px-3 py-3 font-mono whitespace-nowrap">{duration(run)}</td>
                <td className="px-3 py-3 font-mono whitespace-nowrap">{size(run)}</td>
                <td className="py-3 pr-5 pl-3 font-mono whitespace-nowrap text-muted-foreground">
                  {runTimeLabel(t, run.started_at || run.created_at)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {data && rows.length === 0 && (
        <p className="border-t px-5 py-10 text-center text-sm text-muted-foreground">
          {filter === "failed" ? t("overview.recent.emptyFailed") : t("overview.recent.empty")}
        </p>
      )}
    </Panel>
  );
}
