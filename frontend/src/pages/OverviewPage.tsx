import { Plus, RotateCcw, Search } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";

import { errorMessage } from "@/components/jobs/loadable";
import { PageHeader } from "@/components/layout/PageHeader";
import { ComingSoon, comingSoonClass } from "@/components/overview/ComingSoon";
import { DailyRunsChart } from "@/components/overview/DailyRunsChart";
import { RecentRuns } from "@/components/overview/RecentRuns";
import { StartGuide } from "@/components/overview/StartGuide";
import { StatCards } from "@/components/overview/StatCards";
import { StorageTargets } from "@/components/overview/StorageTargets";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { TooltipProvider } from "@/components/ui/tooltip";
import { browserTimeZone, getOverview, type Overview } from "@/lib/overview";
import { cn } from "@/lib/utils";

/** 自动刷新间隔（spec 决策 6） */
const REFRESH_INTERVAL_MS = 30000;

function ErrorBlock({ title, message, onRetry }: { title?: string; message: string; onRetry: () => void }) {
  const { t } = useTranslation();
  return (
    <div role="alert" className="flex items-center justify-between gap-3 rounded-md bg-destructive-soft px-4 py-3">
      <div className="flex min-w-0 flex-col gap-1 text-sm text-destructive">
        {title && <p className="font-semibold">{title}</p>}
        <p>{message}</p>
      </div>
      <Button variant="outline" size="sm" onClick={onRetry}>
        <RotateCcw />
        {t("common.retry")}
      </Button>
    </div>
  );
}

/**
 * 本轮的完整概览；data 为空表示加载中，各区块显示占位。
 * 一个任务都没有时（spec「概览」→「空状态」），最近运行 / 14 天运行 / 存储目标换成三步引导。
 */
function Dashboard({ data }: { data?: Overview }) {
  const empty = data ? data.counts.jobs === 0 : false;
  return (
    <>
      <StatCards data={data} />
      {empty && data ? (
        <StartGuide datasources={data.counts.datasources} storages={data.counts.storages} />
      ) : (
        <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_24rem]">
          <RecentRuns data={data?.recent} />
          <div className="flex flex-col gap-4">
            <DailyRunsChart data={data?.daily} />
            <StorageTargets data={data?.storages} />
          </div>
        </div>
      )}
    </>
  );
}

export function OverviewPage() {
  const { t, i18n } = useTranslation();
  const [data, setData] = useState<Overview>();
  const [error, setError] = useState<string>();
  // 每次发出请求加一：较早发出的请求（例如切换语言前按原语言发出的）返回时已过时，丢弃其结果
  const seq = useRef(0);

  const load = useCallback(() => {
    const mine = ++seq.current;
    getOverview(browserTimeZone())
      .then((d) => {
        if (mine !== seq.current) return;
        setData(d);
        setError(undefined);
      })
      .catch((err: unknown) => mine === seq.current && setError(errorMessage(err)));
  }, []);

  // 打开期间每 30 秒刷新；标签页不可见时暂停，回到页面时立即刷新一次
  useEffect(() => {
    let timer: number | undefined;
    const schedule = () => {
      window.clearTimeout(timer);
      timer = window.setTimeout(() => {
        if (!document.hidden) load();
        schedule();
      }, REFRESH_INTERVAL_MS);
    };
    const onVisibility = () => {
      if (document.hidden) {
        window.clearTimeout(timer);
        return;
      }
      load();
      schedule();
    };
    load();
    schedule();
    document.addEventListener("visibilitychange", onVisibility);
    return () => {
      window.clearTimeout(timer);
      document.removeEventListener("visibilitychange", onVisibility);
    };
  }, [load]);

  // 切换界面语言后立即重新读取：存储的原因由服务端按请求的语言给出
  const language = i18n.language;
  const shownLanguage = useRef(language);
  useEffect(() => {
    if (shownLanguage.current === language) return;
    shownLanguage.current = language;
    load();
  }, [language, load]);

  const retryFirstLoad = () => {
    setError(undefined);
    load();
  };

  return (
    <TooltipProvider>
      <PageHeader
        title={t("nav.overview")}
        subtitle={t("overview.subtitle")}
        actions={
          <div className="flex items-center gap-2">
            <div className="relative w-64">
              <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
              <ComingSoon>
                <Input
                  readOnly
                  aria-label={t("overview.searchPlaceholder")}
                  placeholder={t("overview.searchPlaceholder")}
                  className={cn("pl-9", comingSoonClass)}
                />
              </ComingSoon>
            </div>
            <ComingSoon>
              <Button variant="outline" className={cn(comingSoonClass, "hover:bg-background")}>
                <RotateCcw />
                {t("overview.restore")}
              </Button>
            </ComingSoon>
            <Button asChild>
              <Link to="/jobs/new">
                <Plus />
                {t("jobs.list.create")}
              </Link>
            </Button>
          </div>
        }
      />
      <section className="flex flex-col gap-4 px-8 py-6" aria-live="polite">
        {!data && error ? (
          <ErrorBlock title={t("overview.loadFailedTitle")} message={error} onRetry={retryFirstLoad} />
        ) : (
          <>
            {error && <ErrorBlock message={t("overview.refreshFailed", { message: error })} onRetry={load} />}
            <Dashboard data={data} />
          </>
        )}
      </section>
    </TooltipProvider>
  );
}
