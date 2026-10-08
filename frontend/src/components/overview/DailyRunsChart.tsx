import { useTranslation } from "react-i18next";

import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import type { DayRuns } from "@/lib/overview";
import { cn } from "@/lib/utils";

import { shortDate } from "./format";
import { Panel, Placeholder } from "./Panel";

function Legend() {
  const { t } = useTranslation();
  return (
    <div className="flex items-center gap-3 text-xs text-muted-foreground">
      <span className="flex items-center gap-1.5">
        <span className="size-2 rounded-xs bg-chart-bar" />
        {t("overview.daily.legendSuccess")}
      </span>
      <span className="flex items-center gap-1.5">
        <span className="size-2 rounded-xs bg-destructive" />
        {t("overview.daily.legendFailed")}
      </span>
    </div>
  );
}

/** 14 天运行（spec「概览」→「14 天运行」）：每天一根柱，成功与失败叠放；data 为空表示加载中 */
export function DailyRunsChart({ data }: { data?: DayRuns[] }) {
  const { t } = useTranslation();
  const max = Math.max(1, ...(data ?? []).map((d) => d.success + d.failed));
  const empty = !!data && data.every((d) => d.success + d.failed === 0);

  return (
    <Panel title={t("overview.daily.title")} actions={<Legend />} loading={!data}>
      <div className="flex flex-col gap-2 px-5 pb-4">
        <div className="relative flex h-32 items-end gap-1.5">
          {!data && <Placeholder className="h-full w-full" />}
          {data?.map((d) => (
            <Tooltip key={d.date}>
              <TooltipTrigger asChild>
                <div
                  role="img"
                  tabIndex={0}
                  aria-label={t("overview.daily.bar", { date: d.date, success: d.success, failed: d.failed })}
                  className="flex h-full min-w-0 flex-1 flex-col justify-end rounded-xs outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50"
                >
                  {d.failed > 0 && (
                    <span
                      className="block rounded-t-xs bg-destructive"
                      style={{ height: `${(d.failed / max) * 100}%` }}
                    />
                  )}
                  {d.success > 0 && (
                    <span
                      className={cn("block bg-chart-bar", d.failed === 0 && "rounded-t-xs")}
                      style={{ height: `${(d.success / max) * 100}%` }}
                    />
                  )}
                </div>
              </TooltipTrigger>
              <TooltipContent className="flex flex-col gap-0.5">
                <span className="font-mono">{d.date}</span>
                <span>{t("overview.daily.success", { count: d.success })}</span>
                <span>{t("overview.daily.failed", { count: d.failed })}</span>
              </TooltipContent>
            </Tooltip>
          ))}
          {empty && (
            <p className="absolute inset-0 flex items-center justify-center text-sm text-muted-foreground">
              {t("overview.daily.empty")}
            </p>
          )}
        </div>
        <div className="flex min-h-6 justify-between border-t pt-2 text-xs text-muted-foreground">
          <span className="font-mono">{data?.[0] && shortDate(data[0].date)}</span>
          <span>{t("overview.daily.today")}</span>
        </div>
      </div>
    </Panel>
  );
}
