import * as React from "react";
import {
  AlertTriangle,
  FileKey,
  KeyRound,
  Loader2,
  MoreHorizontal,
  Plus,
  RefreshCw,
  ScanSearch,
  ShieldCheck,
  Unlock,
  Wrench,
} from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Mono, PageTitle, Sheet, Skeleton } from "@/components/bits";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { api, errorMessage, type RepoItem } from "@/lib/api";
import { fmtAgo, fmtBytes } from "@/lib/format";
import { REPO_TYPE_LABEL } from "@/lib/repos";
import { useLoad } from "@/lib/use-load";
import { cn } from "@/lib/utils";
import { downloadRecoveryBundle, KeysDialog } from "./keys-dialog";
import { RepoDialog } from "./repo-dialog";
import { LoadError } from "./settings";

/** 维护进行中时刷新列表的间隔 */
const POLL_MS = 3000;

const MAINTENANCE_TEXT = { check: "正在校验数据", prune: "正在清理空间" } as const;

export function ReposPage() {
  const load = React.useCallback(async () => (await api.repos.list()).list ?? [], []);
  const { data: repos, error, loading, reload } = useLoad(load);
  const [adding, setAdding] = React.useState(false);
  const [keysFor, setKeysFor] = React.useState<number | null>(null);
  const [unlocking, setUnlocking] = React.useState<RepoItem | null>(null);
  const [busyId, setBusyId] = React.useState<number | null>(null);

  const running = (repos ?? []).some((r) => r.maintenance !== "");
  React.useEffect(() => {
    if (!running) return;
    const t = setInterval(reload, POLL_MS);
    return () => clearInterval(t);
  }, [running, reload]);

  /** 执行一个维护动作：成功提示并刷新；后端忙碌或失败时提示原因 */
  async function run(r: RepoItem, fn: () => Promise<unknown>, done: string) {
    setBusyId(r.id);
    try {
      await fn();
      toast.success(done);
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setBusyId(null);
      reload();
    }
  }

  const keysRepo = repos?.find((r) => r.id === keysFor) ?? null;

  const body = (() => {
    if (error && !repos) return <LoadError what="存储位置" message={error} onRetry={reload} />;
    if (!repos || (loading && !repos.length)) {
      return (
        <div className="grid gap-3" aria-busy="true" aria-label="正在读取存储位置">
          <Skeleton className="h-28" />
          <Skeleton className="h-28" />
        </div>
      );
    }
    if (!repos.length) {
      return (
        <Sheet className="px-5 py-10 text-center text-[13.5px] text-ink-2">
          还没有存储位置。添加后，备份任务才有地方存放快照。
        </Sheet>
      );
    }
    return (
      <div className="flex flex-col gap-4">
        {repos.map((r) => (
          <RepoCard
            key={r.id}
            repo={r}
            busy={busyId === r.id}
            onKeys={() => setKeysFor(r.id)}
            onRecheck={() => void run(r, () => api.repos.recheck(r.id), `${r.name} 已重新检查`)}
            onCheck={() => void run(r, () => api.repos.check(r.id), `${r.name} 开始校验数据`)}
            onPrune={() => void run(r, () => api.repos.prune(r.id), `${r.name} 开始清理空间`)}
            onBundle={() => void run(r, () => downloadRecoveryBundle(r.id), "恢复包已下载")}
            onUnlock={() => setUnlocking(r)}
          />
        ))}
      </div>
    );
  })();

  return (
    <>
      <PageTitle
        title="存储位置"
        actions={
          <Button onClick={() => setAdding(true)}>
            <Plus />
            添加存储位置
          </Button>
        }
      />
      {body}
      {adding && (
        <RepoDialog
          onClose={() => setAdding(false)}
          onCreated={() => {
            setAdding(false);
            reload();
          }}
        />
      )}
      {keysRepo && <KeysDialog repo={keysRepo} onClose={() => setKeysFor(null)} onChanged={reload} />}
      <ConfirmDialog
        open={unlocking !== null}
        title={`解除「${unlocking?.name ?? ""}」的仓库锁？`}
        description="只在确认没有备份或维护正在使用这个存储位置时解除，否则可能损坏仓库。"
        confirmLabel="确认解除"
        onClose={() => setUnlocking(null)}
        onConfirm={async () => {
          if (!unlocking) return;
          await api.repos.unlock(unlocking.id);
          toast.success(`已解除 ${unlocking.name} 的仓库锁`);
          setUnlocking(null);
          reload();
        }}
      />
    </>
  );
}

function RepoCard({
  repo: r,
  busy,
  onKeys,
  onRecheck,
  onCheck,
  onPrune,
  onBundle,
  onUnlock,
}: {
  repo: RepoItem;
  busy: boolean;
  onKeys: () => void;
  onRecheck: () => void;
  onCheck: () => void;
  onPrune: () => void;
  onBundle: () => void;
  onUnlock: () => void;
}) {
  const bad = r.status === "not_mounted" || r.status === "error";
  const maintaining = r.maintenance !== "";
  const hasStats = r.stats_at > 0;
  return (
    <Sheet className={cn("overflow-hidden", bad && "border-fail/40")} data-repo={r.id} data-state={r.status}>
      <div className="grid gap-6 p-5 lg:grid-cols-[minmax(220px,1fr)_minmax(240px,1.1fr)_minmax(220px,0.9fr)_190px] lg:items-center">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <span className="text-[16px] font-semibold">{r.name}</span>
            <span className="rounded-md border border-line-strong px-1.5 text-[11px] text-ink-2">
              {REPO_TYPE_LABEL[r.type] ?? r.type}
            </span>
          </div>
          <Mono className="mt-1 block truncate text-ink-3" title={r.address}>
            {r.address}
          </Mono>
        </div>

        <Usage repo={r} hasStats={hasStats} />

        <ul className="flex flex-col gap-1 text-[13px]">
          <li className="flex items-center gap-1.5">
            <ShieldCheck className="size-4 text-ok" aria-hidden />
            AES-256 加密
          </li>
          <li className={cn("flex items-center gap-1.5", r.no_recovery_key ? "text-warn" : "text-ink-2")}>
            {r.no_recovery_key ? (
              <AlertTriangle className="size-4" aria-hidden />
            ) : (
              <KeyRound className="size-4 text-ink-3" aria-hidden />
            )}
            {r.no_recovery_key ? `${r.key_count} 把密钥，没有恢复密钥` : `${r.key_count} 把密钥，含你保管的恢复密钥`}
          </li>
          <li className="flex items-center gap-1.5 text-ink-2">
            {maintaining ? (
              <Loader2 className="size-4 animate-spin text-primary" aria-hidden />
            ) : (
              <ScanSearch className="size-4 text-ink-3" aria-hidden />
            )}
            {maintaining ? MAINTENANCE_TEXT[r.maintenance as "check" | "prune"] : checkText(r)}
          </li>
        </ul>

        <div className="flex gap-2 lg:justify-end">
          <Button size="sm" variant="outline" onClick={onKeys}>
            <KeyRound />
            密钥
          </Button>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="ghost" size="icon-sm" aria-label={`${r.name} 的维护操作`} disabled={busy}>
                <MoreHorizontal />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-48">
              <DropdownMenuItem disabled={maintaining} onSelect={onCheck}>
                <ScanSearch />
                校验数据
              </DropdownMenuItem>
              <DropdownMenuItem disabled={maintaining} onSelect={onPrune}>
                <Wrench />
                清理空间
              </DropdownMenuItem>
              <DropdownMenuItem onSelect={onBundle}>
                <FileKey />
                下载恢复包
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem onSelect={onUnlock}>
                <Unlock />
                解除仓库锁
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </div>

      {bad && (
        <div
          role="alert"
          className="flex flex-col gap-3 border-t border-fail/25 bg-fail-wash px-5 py-3 sm:flex-row sm:items-center"
        >
          <AlertTriangle className="size-4 shrink-0 text-fail" aria-hidden />
          <p className="flex-1 text-[13px]">
            {r.status === "not_mounted" ? (
              <>
                <b>没有挂载</b>，找不到 <Mono>{markerPath(r)}</Mono>。写入已暂停。
              </>
            ) : (
              <>
                <b>检查未通过</b>
                {r.status_message && `：${r.status_message}`}
              </>
            )}
          </p>
          <Button size="sm" variant="outline" className="bg-surface" disabled={busy} onClick={onRecheck}>
            <RefreshCw className={cn(busy && "animate-spin")} />
            重新检查
          </Button>
        </div>
      )}
    </Sheet>
  );
}

function Usage({ repo: r, hasStats }: { repo: RepoItem; hasStats: boolean }) {
  if (!hasStats) return <div className="text-[13px] text-ink-3">还没有统计，下次备份后显示占用</div>;
  const ratio = r.dedup_ratio;
  return (
    <div>
      <div className="mb-2 flex items-baseline justify-between text-[13px] num">
        <span>
          <b className="text-[17px] font-semibold">{fmtBytes(r.stored_size)}</b>
          <span className="text-ink-3"> / 原始 {fmtBytes(r.raw_size)}</span>
        </span>
        <span className="font-medium">{ratio > 0 ? `${ratio.toFixed(1)}×` : "—"}</span>
      </div>
      <div className="relative h-2 overflow-hidden rounded-full bg-[repeating-linear-gradient(90deg,var(--line-strong)_0_2px,transparent_2px_5px)]">
        <div
          className="absolute inset-y-0 left-0 rounded-full bg-ink"
          style={{ width: `${ratio > 0 ? Math.max(3, 100 / ratio) : 100}%` }}
        />
      </div>
      <div className="mt-1.5 text-xs text-ink-3 num">
        {r.snapshot_count} 个快照，NAS 剩余 {r.free_space === null ? "未知" : fmtBytes(r.free_space)}
      </div>
    </div>
  );
}

function checkText(r: RepoItem): string {
  if (!r.last_check_at) return "还没有校验过";
  return `${fmtAgo(r.last_check_at)} ${r.last_check_ok ? "校验无误" : "校验未通过"}`;
}

/** 本地目录的挂载标记文件路径 */
function markerPath(r: RepoItem): string {
  return `${r.params.path.replace(/\/+$/, "")}/.opsnap-repo`;
}
