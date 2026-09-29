import type { TFunction } from "i18next";

import { formatDateTime } from "@/lib/format";
import { formatBytes, type Run } from "@/lib/jobs";

/** 耗时的简写形式：1h05m / 4m12s / 45s */
export function formatDuration(ms: number): string {
  const totalSeconds = Math.max(0, Math.round(ms / 1000));
  const h = Math.floor(totalSeconds / 3600);
  const m = Math.floor((totalSeconds % 3600) / 60);
  const s = totalSeconds % 60;
  if (h > 0) return `${h}h${String(m).padStart(2, "0")}m`;
  if (m > 0) return `${m}m${String(s).padStart(2, "0")}s`;
  return `${s}s`;
}

/** 触发方式的一句话描述，重试带上第几次/共几次 */
export function triggerLabel(t: TFunction, run: Run): string {
  if (run.trigger === "retry") {
    return t("jobs.list.trigger.retry", { attempt: run.retry_attempt, total: run.retry_total });
  }
  return t(`jobs.list.trigger.${run.trigger}`);
}

/** 任务列表 / 详情页共用：最近一次运行状态标签下方的说明行 */
export function lastRunDetail(t: TFunction, run: Run): string {
  switch (run.status) {
    case "running":
      return [
        t("jobs.list.exported", { size: formatBytes(run.exported_bytes) }),
        t("jobs.list.runningFor", { duration: formatDuration(run.duration_ms) }),
      ].join(" · ");
    case "queued":
      return [triggerLabel(t, run), t("jobs.list.queuedHint")].join(" · ");
    case "success":
      return [
        formatDateTime(run.started_at),
        t("jobs.list.tookTime", { duration: formatDuration(run.duration_ms) }),
        t("jobs.list.uploaded", { size: formatBytes(run.uploaded_bytes) }),
      ].join(" · ");
    case "failed":
      return [formatDateTime(run.started_at || run.created_at), run.reason].filter(Boolean).join(" · ");
    case "canceled":
      return [formatDateTime(run.started_at || run.created_at), run.reason].filter(Boolean).join(" · ");
    case "skipped":
      return [formatDateTime(run.created_at), run.reason].filter(Boolean).join(" · ");
    default:
      return "";
  }
}
