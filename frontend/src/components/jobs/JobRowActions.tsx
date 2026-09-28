import { Ellipsis, Eye, Pause, Play, Trash2 } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";

import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import type { JobItem } from "@/lib/jobs";

/** 一行正在进行的操作；用于禁用对应按钮并显示加载文案，防止重复提交 */
export type JobRowBusy = "run" | "cancel" | "pause" | "enable" | undefined;

export interface JobRowActionHandlers {
  onRun: (job: JobItem) => void;
  onCancel: (job: JobItem) => void;
  onPause: (job: JobItem) => void;
  onEnable: (job: JobItem) => void;
  onDelete: (job: JobItem) => void;
}

/**
 * 任务的行操作：立即执行（运行中变为取消运行，运行中或排队中不可用）、编辑、更多菜单
 * （暂停/启用、查看详情、删除任务）。任务列表与详情页头部共用（docs/specs/2026-09-27-backup-jobs.md「任务列表」「执行」）。
 */
export function JobRowActions({
  job,
  busy,
  actions,
}: {
  job: JobItem;
  busy: JobRowBusy;
  actions: JobRowActionHandlers;
}) {
  const { t } = useTranslation();
  const status = job.last_run?.status;
  const isRunning = status === "running";
  const isQueued = status === "queued";
  const isActive = isRunning || isQueued;

  return (
    <div className="flex items-center justify-end gap-1">
      {isRunning ? (
        <Button
          variant="ghost"
          size="sm"
          disabled={busy === "cancel"}
          aria-label={t("jobs.list.actions.cancelNamed", { name: job.name })}
          onClick={() => actions.onCancel(job)}
        >
          {busy === "cancel" ? t("common.submitting") : t("jobs.list.actions.cancel")}
        </Button>
      ) : (
        <Button
          variant="ghost"
          size="sm"
          disabled={isQueued || busy === "run"}
          aria-label={t("jobs.list.actions.runNamed", { name: job.name })}
          onClick={() => actions.onRun(job)}
        >
          {busy === "run" ? t("common.submitting") : t("jobs.list.actions.run")}
        </Button>
      )}
      <Button asChild variant="ghost" size="sm" aria-label={t("jobs.list.actions.editNamed", { name: job.name })}>
        <Link to={`/jobs/${job.id}/edit`}>{t("jobs.list.actions.edit")}</Link>
      </Button>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="ghost" size="icon-sm" aria-label={t("jobs.list.actions.more", { name: job.name })}>
            <Ellipsis />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuItem
            disabled={busy === "pause" || busy === "enable"}
            onSelect={() => (job.enabled ? actions.onPause(job) : actions.onEnable(job))}
          >
            {job.enabled ? <Pause /> : <Play />}
            {job.enabled ? t("jobs.list.actions.pause") : t("jobs.list.actions.enable")}
          </DropdownMenuItem>
          <DropdownMenuItem asChild>
            <Link to={`/jobs/${job.id}`}>
              <Eye />
              {t("jobs.list.actions.viewDetail")}
            </Link>
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem
            variant="destructive"
            disabled={isActive}
            onSelect={() => !isActive && actions.onDelete(job)}
          >
            <Trash2 />
            <span className="flex min-w-0 flex-col">
              {t("jobs.list.actions.delete")}
              {isActive && (
                <span className="text-xs font-normal text-muted-foreground">
                  {t("jobs.list.actions.deleteBlocked")}
                </span>
              )}
            </span>
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  );
}
