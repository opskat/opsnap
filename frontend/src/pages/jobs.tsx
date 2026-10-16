// 备份任务列表：筛选、搜索、14 天运行带、上次结果、立即运行和更多操作。
import * as React from "react";
import { Link, useNavigate } from "react-router";
import { Archive, ArrowRight, Copy, MoreHorizontal, Pause, Pencil, Play, Plus, Search, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { PageTitle, Sheet, Skeleton, Status, TypeTile } from "@/components/bits";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { RunStrip } from "@/components/run-strip";
import { useRunningJobs, useShellRefresh } from "@/components/shell-status";
import { ApiError, ErrorCode, api, errorMessage, type JobItem, type JobListFilter, type JobRunBrief } from "@/lib/api";
import { fmtAgo, fmtBytes, fmtNext } from "@/lib/format";
import { jobOrigin, jobTarget, scheduleText, sourceOf } from "@/lib/jobs";
import { dayStrip, runStatusView } from "@/lib/runs";
import { useLoad } from "@/lib/use-load";
import { cn } from "@/lib/utils";
import { LoadError } from "./settings";

const TABS: { id: JobListFilter; label: string }[] = [
  { id: "all", label: "全部" },
  { id: "attention", label: "需要处理" },
  { id: "paused", label: "已暂停" },
];

const COLS = "lg:grid-cols-[minmax(300px,1.6fr)_140px_130px_170px_96px]";

export function JobsPage() {
  const navigate = useNavigate();
  const refreshShell = useShellRefresh();
  const [filter, setFilter] = React.useState<JobListFilter>("all");
  const [query, setQuery] = React.useState("");
  const [q, setQ] = React.useState("");
  const [deleting, setDeleting] = React.useState<JobItem | null>(null);
  const [busyId, setBusyId] = React.useState<number | null>(null);

  // 输入停顿后再查询
  React.useEffect(() => {
    const t = window.setTimeout(() => setQ(query), 250);
    return () => window.clearTimeout(t);
  }, [query]);

  const loadList = React.useCallback(async () => (await api.jobs.list(filter, q)).list ?? [], [filter, q]);
  const loadAll = React.useCallback(async () => (await api.jobs.list("all")).list ?? [], []);
  const { data: jobs, error, reload: reloadList } = useLoad(loadList);
  const { data: all, reload: reloadAll } = useLoad(loadAll);
  // 侧边栏每 5 秒轮询正在运行的任务：集合变化（开始或结束了运行）时刷新列表，上次结果、运行带随之更新
  const live = useRunningJobs();
  const [prevLive, setPrevLive] = React.useState(live);
  if (live !== prevLive) {
    setPrevLive(live);
    if (prevLive !== null) {
      reloadList();
      reloadAll();
    }
  }

  function reload() {
    reloadList();
    reloadAll();
    refreshShell();
  }

  const counts = {
    all: all?.length,
    attention: all?.filter((j) => j.attention).length,
    paused: all?.filter((j) => j.paused).length,
  };

  async function act(j: JobItem, fn: () => Promise<unknown>, done: string) {
    setBusyId(j.id);
    try {
      await fn();
      toast.success(done);
      reload();
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setBusyId(null);
    }
  }

  async function runNow(j: JobItem) {
    setBusyId(j.id);
    try {
      const { run_id } = await api.jobs.run(j.id);
      refreshShell();
      navigate(`/runs/${run_id}`);
    } catch (err) {
      toast.error(
        err instanceof ApiError && err.code === ErrorCode.RunAlreadyRunning
          ? `${j.name} 正在运行，结束后才能再次运行`
          : errorMessage(err)
      );
      reloadList();
    } finally {
      setBusyId(null);
    }
  }

  const newButton = (
    <Button asChild>
      <Link to="/jobs/new">
        <Plus />
        新建任务
      </Link>
    </Button>
  );

  const body = (() => {
    // 已有列表时某次刷新失败（侧边栏轮询会触发刷新）不替换列表，与服务器、存储位置页一致
    if (error && !jobs) return <LoadError what="备份任务" message={error} onRetry={reload} />;
    if (!jobs) return <ListSkeleton />;
    if (jobs.length === 0) {
      if (filter === "all" && !q.trim()) {
        return (
          <Sheet className="px-6 py-20 text-center">
            <h2 className="text-[17px] font-semibold">还没有备份任务</h2>
            <p className="mx-auto mt-2 max-w-sm text-ink-2">
              新建任务时要选一台服务器和一个存储位置，请先确认它们已经添加。
            </p>
            <div className="mt-6">{newButton}</div>
          </Sheet>
        );
      }
      return (
        <Sheet className="px-6 py-14 text-center">
          <h2 className="text-[15px] font-semibold">没有符合条件的任务</h2>
          <p className="mt-1.5 text-ink-2">换一个筛选或关键词试试。</p>
        </Sheet>
      );
    }
    return (
      <Sheet className="overflow-hidden">
        <div
          className={cn("hidden grid-cols-1 gap-6 border-b border-line px-5 py-2.5 text-xs text-ink-3 lg:grid", COLS)}
        >
          <span>任务</span>
          <span>计划</span>
          <span>最近 14 天</span>
          <span>上次运行</span>
          <span />
        </div>
        <ul className="divide-y divide-line">
          {jobs.map((j) => {
            const recent = (j.recent_runs ?? []).map((r) => ({ ...r, job_id: j.id }));
            // 侧边栏轮询到的结果比列表更新；还没轮询到时用列表里的状态
            const running = live ? live.has(j.id) : !!j.running;
            return (
              <JobRow
                key={j.id}
                job={j}
                last={j.last_run ?? undefined}
                strip={dayStrip(recent, j.id)}
                running={running}
                busy={busyId === j.id}
                onRun={() => void runNow(j)}
                onDuplicate={() =>
                  void act(j, () => api.jobs.duplicate(j.id), `已复制为「${j.name} 副本」，新任务处于暂停状态`)
                }
                onTogglePause={() =>
                  void act(
                    j,
                    () => (j.paused ? api.jobs.resume(j.id) : api.jobs.pause(j.id)),
                    j.paused ? `${j.name} 已恢复计划` : `${j.name} 已暂停`
                  )
                }
                onDelete={() => setDeleting(j)}
              />
            );
          })}
        </ul>
      </Sheet>
    );
  })();

  return (
    <>
      <PageTitle title="备份任务" actions={newButton} />

      <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
        <div className="flex gap-1" role="tablist" aria-label="任务筛选">
          {TABS.map((t) => (
            <button
              key={t.id}
              role="tab"
              aria-selected={filter === t.id}
              onClick={() => setFilter(t.id)}
              className={cn(
                "flex items-center gap-1.5 rounded-full px-3 py-1 text-[13px] text-ink-2 hover:text-ink",
                filter === t.id && "bg-ink text-paper hover:text-paper"
              )}
            >
              {t.label}
              {counts[t.id] !== undefined && (
                <span className={cn("num", filter === t.id ? "text-paper/60" : "text-ink-3")}>{counts[t.id]}</span>
              )}
            </button>
          ))}
        </div>
        <div className="relative w-full sm:w-72">
          <Search
            className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-ink-3"
            aria-hidden
          />
          <Input
            type="search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="按名称、服务器或路径查找"
            className="h-9 bg-surface pl-9"
            aria-label="查找任务"
          />
        </div>
      </div>

      {body}

      <ConfirmDialog
        open={deleting !== null}
        title={`删除「${deleting?.name ?? ""}」？`}
        description="删除后不再按计划备份。删除任务不删除已有快照，它们仍留在存储位置里。"
        confirmLabel="删除"
        onClose={() => setDeleting(null)}
        onConfirm={async () => {
          if (!deleting) return;
          await api.jobs.remove(deleting.id);
          toast.success(`${deleting.name} 已删除，已有快照保留`);
          setDeleting(null);
          reload();
        }}
      />
    </>
  );
}

function ListSkeleton() {
  return (
    <Sheet className="p-5" aria-busy>
      {[0, 1, 2, 3].map((i) => (
        <div key={i} className="flex items-center gap-4 py-3">
          <Skeleton className="size-8" />
          <Skeleton className="h-4 w-48" />
          <Skeleton className="ml-auto h-4 w-32" />
        </div>
      ))}
    </Sheet>
  );
}

function JobRow({
  job: j,
  last,
  strip,
  running,
  busy,
  onRun,
  onDuplicate,
  onTogglePause,
  onDelete,
}: {
  job: JobItem;
  last?: JobRunBrief;
  strip: ReturnType<typeof dayStrip>;
  running: boolean;
  busy: boolean;
  onRun: () => void;
  onDuplicate: () => void;
  onTogglePause: () => void;
  onDelete: () => void;
}) {
  const navigate = useNavigate();
  const view = last ? runStatusView(last.status) : null;
  return (
    <li
      className={cn(
        "grid grid-cols-[1fr_auto] items-center gap-x-6 gap-y-2 px-5 py-4 hover:bg-sunken/40",
        COLS,
        j.paused && "opacity-60"
      )}
    >
      <div className="col-start-1 row-start-1 flex min-w-0 items-center gap-3">
        <TypeTile type={sourceOf(j.source_type).tile} />
        <div className="min-w-0">
          <Link to={`/jobs/${j.id}/edit`} className="block truncate font-semibold hover:underline">
            {j.name}
          </Link>
          <div className="flex min-w-0 items-center gap-1.5 text-[12.5px] text-ink-3">
            <span className="truncate">
              {jobOrigin(j)}，{jobTarget(j)}
            </span>
            <ArrowRight className="size-3 shrink-0" aria-hidden />
            <span className="shrink-0 text-ink-2">{j.repo_name}</span>
          </div>
        </div>
      </div>
      <div className="col-span-2 row-start-2 text-[13px] lg:col-span-1 lg:col-start-2 lg:row-start-1">
        {j.paused ? <span className="text-ink-3">已暂停</span> : scheduleText(j.schedule)}
        {!j.paused && j.next_run_at > 0 && <div className="text-xs text-ink-3 num">下次 {fmtNext(j.next_run_at)}</div>}
      </div>
      {/* 窄屏时运行带与上次结果排在第三行，不省略 */}
      <RunStrip days={strip} className="col-start-1 row-start-3 lg:col-start-3 lg:row-start-1" />
      <div className="col-start-2 row-start-3 text-right lg:col-start-4 lg:row-start-1 lg:text-left">
        {last && view ? (
          <>
            <Status status={view.status}>{view.text}</Status>
            <div className="mt-0.5 text-xs text-ink-3 num">
              {fmtAgo(last.started_at)}
              {last.bytes_added > 0 && `，新增 ${fmtBytes(last.bytes_added)}`}
            </div>
          </>
        ) : (
          <span className="text-[13px] text-ink-3">还没有运行过</span>
        )}
      </div>
      <div className="col-start-2 row-start-1 flex items-center justify-end gap-1 lg:col-start-5">
        <Button
          size="icon-sm"
          variant="outline"
          disabled={running || busy}
          aria-label={`立即运行 ${j.name}`}
          title={running ? "正在运行，结束后才能再次运行" : "立即运行"}
          onClick={onRun}
        >
          <Play />
        </Button>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon-sm" aria-label={`${j.name} 的更多操作`}>
              <MoreHorizontal />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-44">
            <DropdownMenuItem onSelect={() => navigate(`/jobs/${j.id}/edit`)}>
              <Pencil />
              编辑
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={() => navigate(`/snapshots?job=${j.id}`)}>
              <Archive />
              查看快照
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={onDuplicate}>
              <Copy />
              复制为新任务
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={onTogglePause}>
              {j.paused ? <Play /> : <Pause />}
              {j.paused ? "恢复计划" : "暂停计划"}
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem variant="destructive" onSelect={onDelete}>
              <Trash2 />
              删除任务
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </li>
  );
}
