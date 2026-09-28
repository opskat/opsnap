import { Loader2, RefreshCw } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

import { FixBlock, TierIcon } from "@/components/sources/ProbeParts";
import { Button } from "@/components/ui/button";
import { relativeTime } from "@/lib/format";
import {
  dataSourceVersionLabel,
  getDataSource,
  pickProbeText,
  reprobeDataSource,
  type DataSourceItem,
  type ProbeText,
} from "@/lib/sources";
import { cn } from "@/lib/utils";

import { errorMessage } from "./loadable";

/** 探测进行中时的轮询间隔，与数据源详情页一致 */
const POLL_INTERVAL_MS = 3000;

/**
 * 第 2 步的探测面板：所选数据源的探测结果与探测时间，可以重新探测；探测进行中时定时刷新。
 * 数据源的新状态通过 onChange 交给向导，用于判断导出工具是否可用。
 */
export function ProbePanel({ item, onChange }: { item: DataSourceItem; onChange: (item: DataSourceItem) => void }) {
  const { t, i18n } = useTranslation();
  const [reprobing, setReprobing] = useState(false);
  const [error, setError] = useState<string>();
  const probe = item.probe;
  // 只有探测进行中才轮询、禁用重新探测；还没有探测结果（null，如探测进行中 OpsNap 重启过）时可以重新探测
  const probing = probe?.state === "probing";

  useEffect(() => {
    if (!probing) return;
    let cancelled = false;
    let timer: number | undefined;
    const poll = () => {
      timer = window.setTimeout(() => {
        getDataSource(item.id)
          .then((r) => {
            if (cancelled) return;
            onChange(r.item);
            if (r.item.probe?.state === "probing") poll();
          })
          .catch(() => {
            if (!cancelled) poll();
          });
      }, POLL_INTERVAL_MS);
    };
    poll();
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [probing, item.id, onChange]);

  const reprobe = async () => {
    setReprobing(true);
    setError(undefined);
    try {
      const res = await reprobeDataSource(item.id);
      onChange(res.item);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setReprobing(false);
    }
  };

  const pick = (text: ProbeText) => pickProbeText(text, i18n.language);
  const title = t("jobs.wizard.content.probeTitle", { name: item.name });
  const meta = [dataSourceVersionLabel(item), probe?.time ? relativeTime(t, probe.time) : undefined]
    .filter(Boolean)
    .join(" · ");

  return (
    <section role="region" aria-label={title} className="rounded-lg border bg-card">
      <header className="flex items-center justify-between gap-3 border-b px-4 py-2.5">
        <p className="flex min-w-0 items-baseline gap-2">
          <span className="truncate text-sm font-semibold">{title}</span>
          {meta && <span className="shrink-0 font-mono text-xs text-muted-foreground">{meta}</span>}
        </p>
        <Button variant="ghost" size="sm" disabled={reprobing || probing} onClick={() => void reprobe()}>
          <RefreshCw className={cn("size-4", reprobing && "animate-spin")} />
          {reprobing ? t("sources.dataSource.detail.reprobing") : t("sources.dataSource.detail.reprobe")}
        </Button>
      </header>
      {error && (
        <p role="alert" className="mx-4 mt-3 rounded-md bg-destructive-soft px-3 py-2 text-sm text-destructive">
          {error}
        </p>
      )}
      {/* 与数据源详情页一致：还没有探测结果时也显示“探测中” */}
      {(!probe || probing) && (
        <p className="flex items-center gap-2 px-4 py-4 text-sm text-muted-foreground">
          <Loader2 className="size-4 shrink-0 animate-spin" />
          {t("sources.dataSource.list.probing")}
        </p>
      )}
      {probe?.state === "unprobeable" && (
        <p className="flex flex-wrap items-center gap-2 px-4 py-4 text-sm text-muted-foreground">
          <span>{t("sources.dataSource.list.unprobeable")}</span>
          {probe.error && <span className="text-destructive">{probe.error}</span>}
        </p>
      )}
      {probe?.state === "done" && (
        <ul className="grid grid-cols-1 gap-x-6 gap-y-2.5 px-4 py-3 md:grid-cols-2">
          {(probe.items ?? []).map((p) => (
            <li key={p.key} className="flex min-w-0 flex-col gap-1.5">
              <span className="flex items-start gap-2 text-sm">
                <TierIcon tier={p.tier} />
                <span className="flex min-w-0 flex-col">
                  <span className="font-medium">{pick(p.title)}</span>
                  {pick(p.detail) && <span className="text-xs text-muted-foreground">{pick(p.detail)}</span>}
                </span>
              </span>
              {p.tier !== "ok" && (
                <div className="pl-6">
                  <FixBlock fix={p.fix} />
                </div>
              )}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
