// 概览页：一句话说明状态、需要处理的任务、运行时间线、存储位置。
// 宽度小于 768px 时时间线改为每天一格的 14 天运行带。
import * as React from "react";
import { Link, useNavigate } from "react-router";
import { ArrowRight, HardDrive, Play, Plus, RefreshCw, ScrollText } from "lucide-react";
import { toast } from "sonner";
import { Section, Sheet, Skeleton, Status, TypeTile } from "@/components/bits";
import { RunStrip } from "@/components/run-strip";
import { useShellRefresh } from "@/components/shell-status";
import { Timeline } from "@/components/timeline";
import { Button } from "@/components/ui/button";
import {
  api,
  ApiError,
  ErrorCode,
  errorMessage,
  type AttentionItem,
  type Overview,
  type OverviewRepo,
  type OverviewTimeline,
  type OverviewWindow,
} from "@/lib/api";
import { fmtBytes, fmtNext } from "@/lib/format";
import { sourceOf } from "@/lib/jobs";
import { attentionView, overviewTone, statusSentence } from "@/lib/overview";
import { REPO_TYPE_LABEL } from "@/lib/repos";
import { dayStrip, runStatusView } from "@/lib/runs";
import { NARROW_QUERY, useMediaQuery } from "@/lib/use-media-query";
import { useLoad } from "@/lib/use-load";
import { cn } from "@/lib/utils";

const REFRESH_MS = 60_000;
const WEEK = ["周日", "周一", "周二", "周三", "周四", "周五", "周六"];

export function OverviewPage() {
  const narrow = useMediaQuery(NARROW_QUERY);
  const [range, setRange] = React.useState<OverviewWindow>("7d");
  // 窄屏的运行带要 14 天，取 30 天窗口
  const window = narrow ? "30d" : range;
  const fetcher = React.useCallback(() => api.overview.get(window), [window]);
  const { data, error, reload } = useLoad(fetcher);

  // 「现在」线和状态要跟上时间：定时重新读取，读取期间保留旧内容
  React.useEffect(() => {
    const timer = globalThis.setInterval(reload, REFRESH_MS);
    return () => globalThis.clearInterval(timer);
  }, [reload]);

  if (!data) return error ? <ErrorState message={error} onRetry={reload} /> : <Loading />;
  if (data.summary.jobs === 0) return <Onboarding data={data} />;
  return <Dashboard data={data} narrow={narrow} range={range} onRange={setRange} />;
}

function Dashboard({
  data,
  narrow,
  range,
  onRange,
}: {
  data: Overview;
  narrow: boolean;
  range: OverviewWindow;
  onRange: (r: OverviewWindow) => void;
}) {
  const now = new Date(data.now * 1000);
  const sentence = statusSentence(data.summary);
  const pad = (n: number) => String(n).padStart(2, "0");
  return (
    <>
      <div className="mb-10 grid gap-8 lg:grid-cols-[1fr_auto] lg:items-end">
        <div>
          <p className="mb-3 flex items-center gap-2 text-[13px] text-ink-2">
            <span
              className={cn(
                "size-2 rounded-full",
                { failed: "bg-fail-fill", warning: "bg-warn-fill", success: "bg-ok-fill", running: "", none: "" }[
                  overviewTone(data.attention)
                ]
              )}
              aria-hidden
            />
            {now.getMonth() + 1}月{now.getDate()}日 {WEEK[now.getDay()]}，{pad(now.getHours())}:{pad(now.getMinutes())}
          </p>
          <h1 className="text-[34px] leading-[1.15] font-semibold tracking-[-0.02em] md:text-[40px]">
            {sentence.lead}
            {sentence.rest && (
              <>
                <br className="hidden md:block" />
                <span className="text-ink-3">{sentence.rest}</span>
              </>
            )}
          </h1>
        </div>
        <dl className="flex gap-8 text-[13px] lg:pb-1.5">
          <Total label="受保护的数据" value={fmtBytes(data.totals.raw_bytes)} />
          <Total label="NAS 上实际占用" value={fmtBytes(data.totals.stored_bytes)} />
          <Total label="快照" value={String(data.totals.snapshots)} />
        </dl>
      </div>

      {data.attention.length > 0 && <AttentionList items={data.attention} now={now} />}

      <Section
        title="运行时间线"
        aside={
          !narrow && (
            <div className="flex items-center gap-5">
              <div className="flex items-center gap-4 text-xs text-ink-2">
                <Legend cls="bg-ok-fill" label="成功" />
                <Legend cls="bg-warn-fill" label="有警告" />
                <Legend cls="bg-fail-fill" label="失败" />
                <Legend cls="border-[1.5px] border-ink-3 bg-surface" label="计划" />
              </div>
              <div className="flex rounded-lg bg-sunken p-0.5 text-[13px]" role="radiogroup" aria-label="时间范围">
                {(["7d", "30d"] as const).map((r) => (
                  <button
                    key={r}
                    type="button"
                    role="radio"
                    aria-checked={range === r}
                    onClick={() => onRange(r)}
                    className={cn(
                      "rounded-md px-2.5 py-0.5 text-ink-2",
                      range === r && "bg-surface font-medium text-ink shadow-xs"
                    )}
                  >
                    {r === "7d" ? "7 天" : "30 天"}
                  </button>
                ))}
              </div>
            </div>
          )
        }
      >
        {narrow ? (
          <StripList timeline={data.timeline} now={now} />
        ) : (
          <Sheet className="px-5 pt-9 pb-2">
            <Timeline data={data.timeline} now={data.now} />
          </Sheet>
        )}
      </Section>

      <Section
        title="存储位置"
        aside={
          <Link to="/repos" className="flex items-center gap-1 text-[13px] text-ink-2 hover:text-ink">
            管理存储位置
            <ArrowRight className="size-3.5" />
          </Link>
        }
      >
        <ul className="grid gap-4 md:grid-cols-3">
          {data.repos.map((r) => (
            <VaultCard key={r.id} repo={r} />
          ))}
        </ul>
      </Section>
    </>
  );
}

function Total({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <dt className="text-ink-3">{label}</dt>
      <dd className="mt-0.5 text-[20px] font-semibold num">{value}</dd>
    </div>
  );
}

function Legend({ cls, label }: { cls: string; label: string }) {
  return (
    <span className="flex items-center gap-1.5">
      <span className={`size-2 rounded-full ${cls}`} aria-hidden />
      {label}
    </span>
  );
}

function AttentionList({ items, now }: { items: AttentionItem[]; now: Date }) {
  return (
    <Section title="需要处理">
      <Sheet className="overflow-hidden">
        <ul className="divide-y divide-line">
          {items.map((it) => (
            <AttentionRow key={it.job_id} item={it} now={now} />
          ))}
        </ul>
      </Sheet>
    </Section>
  );
}

function AttentionRow({ item, now }: { item: AttentionItem; now: Date }) {
  const navigate = useNavigate();
  const refreshShell = useShellRefresh();
  const [busy, setBusy] = React.useState(false);
  const view = attentionView(item, now);

  async function rerun() {
    setBusy(true);
    try {
      const { run_id } = await api.jobs.run(item.job_id);
      refreshShell();
      navigate(`/runs/${run_id}`);
    } catch (err) {
      toast.error(
        err instanceof ApiError && err.code === ErrorCode.RunAlreadyRunning
          ? `${item.job_name} 正在运行，结束后才能再次运行`
          : errorMessage(err)
      );
    } finally {
      setBusy(false);
    }
  }

  return (
    <li className="relative grid gap-4 py-4 pr-5 pl-6 md:grid-cols-[1fr_auto] md:items-center">
      <span
        className={cn("absolute inset-y-0 left-0 w-1", item.kind === "warning" ? "bg-warn-fill" : "bg-fail-fill")}
        aria-hidden
      />
      <div className="flex gap-4">
        <TypeTile type={sourceOf(item.source_type).tile} />
        <div className="min-w-0">
          <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
            <span className="font-semibold">{view.title}</span>
            <span className="text-[13px] text-ink-2">
              <span className="font-medium text-ink">{item.job_name}</span>，
              <Status status={item.kind === "warning" ? "warning" : "failed"} className="text-[13px]">
                {item.kind === "warning" ? "有警告" : item.kind === "stale" ? "太久没有成功" : "失败"}
              </Status>
              ，{view.since}
            </span>
          </div>
          <p className="mt-1 max-w-[80ch] text-[13.5px] text-ink-2">{view.body}</p>
        </div>
      </div>
      <div className="flex gap-2 pl-12 md:pl-0">
        {view.actions.map((a) =>
          a.key === "rerun" ? (
            <Button key={a.key} size="sm" variant="outline" disabled={busy} onClick={() => void rerun()}>
              <Play />
              {a.label}
            </Button>
          ) : (
            <Button key={a.key} size="sm" variant="ghost" asChild>
              <Link to={a.to}>
                {a.key === "log" ? <ScrollText /> : <HardDrive />}
                {a.label}
              </Link>
            </Button>
          )
        )}
      </div>
    </li>
  );
}

/** 窄屏：每个任务一行，14 天运行带 */
function StripList({ timeline, now }: { timeline: OverviewTimeline; now: Date }) {
  const runs = timeline.jobs.flatMap((j) => j.runs.map((r) => ({ job_id: j.job_id, ...r })));
  return (
    <Sheet>
      <ul className="divide-y divide-line">
        {timeline.jobs.map((j) => {
          const last = j.last_status ? runStatusView(j.last_status) : { status: "none" as const, text: "未运行" };
          return (
            <li key={j.job_id}>
              <Link to={`/jobs/${j.job_id}/edit`} className="flex w-full items-center gap-3 px-4 py-3">
                <TypeTile type={sourceOf(j.source_type).tile} />
                <span className="min-w-0 flex-1">
                  <span className="block truncate font-medium">{j.name}</span>
                  <RunStrip days={dayStrip(runs, j.job_id, now)} className="mt-1.5" />
                </span>
                <span className="text-right text-xs text-ink-3 num">
                  <Status status={last.status} className="text-xs">
                    {last.text}
                  </Status>
                  <span className="mt-0.5 block">{j.paused ? "已暂停" : `下次 ${fmtNext(j.next_run_at, now)}`}</span>
                </span>
              </Link>
            </li>
          );
        })}
      </ul>
    </Sheet>
  );
}

function VaultCard({ repo: r }: { repo: OverviewRepo }) {
  const missing = r.status === "not_mounted";
  return (
    <li>
      <Sheet className={cn("flex h-full flex-col gap-5 p-5", missing && "border-fail/40")}>
        <Link to="/repos" className="flex items-start justify-between text-left">
          <span>
            <span className="block font-semibold">{r.name}</span>
            <span className="text-xs text-ink-3">
              {REPO_TYPE_LABEL[r.type]}，{r.jobs} 个任务
            </span>
          </span>
          {missing ? (
            <span className="rounded-full bg-fail-wash px-2 py-0.5 text-xs font-medium text-fail">未挂载</span>
          ) : r.ratio > 0 ? (
            <span className="text-right">
              <span className="block text-[22px] leading-none font-semibold tracking-tight num">
                {r.ratio.toFixed(1)}×
              </span>
              <span className="text-[11px] text-ink-3">去重压缩比</span>
            </span>
          ) : null}
        </Link>
        <div className="flex flex-col gap-1.5">
          <div className="relative h-2.5 overflow-hidden rounded-full bg-[repeating-linear-gradient(90deg,var(--line-strong)_0_2px,transparent_2px_5px)]">
            <div
              className={cn("absolute inset-y-0 left-0 rounded-full", missing ? "bg-fail-fill/60" : "bg-ink")}
              style={{ width: `${r.ratio > 0 ? Math.max(3, 100 / r.ratio) : 0}%` }}
            />
          </div>
          <div className="flex justify-between text-xs num">
            <span>
              <span className="font-medium">{fmtBytes(r.stored_bytes)}</span>{" "}
              <span className="text-ink-3">实际占用</span>
            </span>
            <span className="text-ink-3">原始 {fmtBytes(r.raw_bytes)}</span>
          </div>
        </div>
        <div className="mt-auto flex justify-between border-t border-line pt-3 text-xs text-ink-3 num">
          <span>{r.snapshots} 个快照</span>
          {r.free_space >= 0 && <span>所在卷剩余 {fmtBytes(r.free_space)}</span>}
        </div>
      </Sheet>
    </li>
  );
}

function Loading() {
  return (
    <div aria-busy="true" aria-label="正在加载">
      <Skeleton className="h-4 w-40" />
      <Skeleton className="mt-4 h-10 w-[520px] max-w-full" />
      <Skeleton className="mt-3 h-10 w-[380px] max-w-full" />
      <Sheet className="mt-12 p-5">
        {[0, 1, 2, 3, 4, 5].map((i) => (
          <div key={i} className="flex items-center gap-4 py-3">
            <Skeleton className="size-8" />
            <Skeleton className="h-4 w-36" />
            <Skeleton className="ml-6 h-px flex-1" />
          </div>
        ))}
      </Sheet>
    </div>
  );
}

function ErrorState({ message, onRetry }: { message: string; onRetry: () => void }) {
  return (
    <div className="mx-auto max-w-lg py-24 text-center">
      <div className="mx-auto mb-6 flex size-12 items-center justify-center rounded-xl border border-line-strong bg-surface">
        <RefreshCw className="size-5 text-ink-2" aria-hidden />
      </div>
      <h1 className="text-[22px] font-semibold">暂时读不到任务状态</h1>
      <p className="mt-2 text-ink-2">
        没有拿到 GET /api/v1/overview 的结果：{message}。服务刚重启的话，几秒后再试；一直这样，请查看容器日志。
      </p>
      <Button variant="outline" className="mt-6" onClick={onRetry}>
        <RefreshCw />
        立即重试
      </Button>
    </div>
  );
}

/** 没有任何任务：三步引导，已有的资源标为完成 */
function Onboarding({ data }: { data: Overview }) {
  const { servers, repos } = data.summary;
  const steps = [
    {
      title: "添加服务器",
      body: "opsnap 通过 SSH 登录线上服务器拉取数据，需要地址和密钥。",
      to: "/servers",
      done: servers > 0,
      doneText: `已添加 ${servers} 台服务器`,
      manage: "管理服务器",
    },
    {
      title: "添加存储位置",
      body: "NAS 上的一个目录、SFTP 或 S3 地址。备份会加密后存进去。",
      to: "/repos",
      done: repos > 0,
      doneText: `已添加 ${repos} 个存储位置`,
      manage: "管理存储位置",
    },
    {
      title: "创建备份任务",
      body: "选一个数据库或几个目录，定好多久备份一次、保留多久。",
      to: "/jobs/new",
      done: false,
      doneText: "",
      manage: "",
    },
  ];
  const current = steps.findIndex((s) => !s.done);
  return (
    <div className="max-w-3xl">
      <h1 className="text-[34px] leading-tight font-semibold tracking-[-0.02em]">还没有任何备份。</h1>
      <p className="mt-3 text-ink-2">三步完成第一次备份，大约 5 分钟。</p>
      <ol className="mt-10 border-l border-line-strong">
        {steps.map((s, i) => (
          <li key={s.title} className="relative pb-10 pl-10 last:pb-0">
            <span
              className={cn(
                "absolute top-0 -left-[13px] flex size-[26px] items-center justify-center rounded-full border text-[12px] font-semibold num",
                s.done
                  ? "border-ok-fill bg-ok-fill text-white"
                  : i === current
                    ? "border-brand bg-brand text-white"
                    : "border-line-strong bg-surface text-ink-3"
              )}
            >
              {s.done ? (
                <>
                  <span aria-hidden>✓</span>
                  <span className="sr-only">已完成</span>
                </>
              ) : (
                i + 1
              )}
            </span>
            <h2 className={cn("text-[17px] font-semibold", !s.done && i !== current && "text-ink-3")}>{s.title}</h2>
            <p className="mt-1 text-ink-2">{s.body}</p>
            {s.done ? (
              <p className="mt-1 flex items-center gap-3 text-[13px]">
                <span className="text-ok">{s.doneText}</span>
                <Link to={s.to} className="text-ink-2 hover:text-ink">
                  {s.manage}
                </Link>
              </p>
            ) : (
              <Button className="mt-4" variant={i === current ? "default" : "outline"} asChild>
                <Link to={s.to}>
                  <Plus />
                  {s.title}
                </Link>
              </Button>
            )}
          </li>
        ))}
      </ol>
    </div>
  );
}
