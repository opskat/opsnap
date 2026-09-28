import { useTranslation } from "react-i18next";

import type { RunStatus } from "@/lib/jobs";
import { cn } from "@/lib/utils";

const STATUS_STYLE: Record<RunStatus, string> = {
  queued: "bg-pending-soft text-pending",
  running: "bg-running-soft text-running",
  success: "bg-success-soft text-success",
  failed: "bg-destructive-soft text-destructive",
  canceled: "bg-accent text-muted-foreground",
  skipped: "bg-accent text-muted-foreground",
};

/** 运行状态标签：等待中 / 运行中 / 成功 / 失败 / 已取消 / 跳过（docs/specs/2026-09-27-backup-jobs.md「运行记录」） */
export function RunStatusTag({ status }: { status: RunStatus }) {
  const { t } = useTranslation();
  return (
    <span className={cn("shrink-0 rounded-sm px-1.75 py-0.5 text-2xs font-medium", STATUS_STYLE[status])}>
      {t(`jobs.list.status.${status}`)}
    </span>
  );
}
