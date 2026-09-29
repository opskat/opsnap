import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { formatBytes } from "@/lib/jobs";
import type { Overview } from "@/lib/overview";
import { cn } from "@/lib/utils";

import { nextRunRelative, nextRunTime, percent } from "./format";
import { Placeholder } from "./Panel";

function Stat({
  title,
  value,
  detail,
  loading,
  emphasis,
}: {
  title: string;
  value?: ReactNode;
  detail?: ReactNode;
  loading: boolean;
  emphasis?: boolean;
}) {
  return (
    <div
      role="group"
      aria-label={title}
      aria-busy={loading}
      className="flex min-w-0 flex-col gap-2 rounded-lg border bg-card px-5 py-4"
    >
      <p className="text-sm text-muted-foreground">{title}</p>
      {loading ? (
        <>
          <Placeholder className="h-8 w-24" />
          <Placeholder className="h-4 w-40" />
        </>
      ) : (
        <>
          <p className={cn("font-mono text-3xl font-semibold", emphasis && "text-brand-text")}>{value}</p>
          <p className="text-xs text-muted-foreground">{detail}</p>
        </>
      )}
    </div>
  );
}

/** 四个统计（spec「概览」→「四个统计」）；data 为空表示加载中 */
export function StatCards({ data }: { data?: Overview }) {
  const { t } = useTranslation();
  const loading = !data;

  const protectedDetail = () => {
    if (!data || data.protected.count === 0) return t("overview.stats.protectedNone");
    return data.protected.by_kind.map((k) => `${t(`sources.dataSource.kind.${k.kind}`)} ${k.count}`).join(" · ");
  };

  const rate = data?.success_24h;
  const usage = data?.storage_usage;
  const storageDetail = () => {
    if (!data || !usage) return undefined;
    if (data.storages.length === 0) return t("overview.stats.noStorage");
    if (usage.unreadable > 0) return t("overview.stats.unreadable", { count: usage.unreadable });
    return t("overview.stats.savings", { percent: percent(usage.savings) });
  };

  const next = data?.next_run;

  return (
    <div className="grid grid-cols-2 gap-4 xl:grid-cols-4">
      <Stat
        title={t("overview.stats.protected")}
        loading={loading}
        value={data?.protected.count}
        detail={protectedDetail()}
      />
      <Stat
        title={t("overview.stats.successRate")}
        loading={loading}
        emphasis={!!rate && rate.runs > 0}
        value={rate && rate.runs > 0 ? `${(rate.success_rate * 100).toFixed(1)}%` : "—"}
        detail={
          rate && rate.runs > 0
            ? `${t("overview.stats.runs", { count: rate.runs })} · ${t("overview.stats.failedRuns", { count: rate.failed })}`
            : t("overview.stats.noRuns")
        }
      />
      <Stat
        title={t("overview.stats.storageUsed")}
        loading={loading}
        value={formatBytes(usage?.packed_bytes ?? 0)}
        detail={storageDetail()}
      />
      <Stat
        title={t("overview.stats.nextRun")}
        loading={loading}
        value={next ? nextRunTime(next.at) : "—"}
        detail={next ? `${next.job_name} · ${nextRunRelative(t, next.at)}` : t("overview.stats.noEnabledJobs")}
      />
    </div>
  );
}
