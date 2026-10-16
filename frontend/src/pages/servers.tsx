import * as React from "react";
import { AlertTriangle, Pencil, Plus, RefreshCw, ShieldCheck, Trash2 } from "lucide-react";
import { toast } from "sonner";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { FormError, Mono, PageTitle, Sheet, Skeleton } from "@/components/bits";
import { api, errorMessage, type HopView, type ServerItem } from "@/lib/api";
import { fmtDate } from "@/lib/format";
import { AUTH_LABEL, reasonText } from "@/lib/servers";
import { useLoad } from "@/lib/use-load";
import { cn } from "@/lib/utils";
import { ServerDrawer } from "./server-drawer";
import { LoadError } from "./settings";

const COLS = "lg:grid-cols-[180px_220px_1fr_200px_auto]";

/** 抽屉的状态：null 关闭，"new" 添加，否则编辑这一台 */
type DrawerState = null | "new" | ServerItem;

export function ServersPage() {
  const load = React.useCallback(async () => (await api.servers.list()).list ?? [], []);
  const { data: servers, error, loading, reload } = useLoad(load);
  const [drawer, setDrawer] = React.useState<DrawerState>(null);
  const [deleting, setDeleting] = React.useState<ServerItem | null>(null);
  const [testingId, setTestingId] = React.useState<number | null>(null);

  async function testSaved(s: ServerItem) {
    setTestingId(s.id);
    try {
      const r = await api.servers.testSaved(s.id);
      if (r.ok) toast.success(`${s.name} 连接正常`);
      else {
        const bad = r.hops?.find((h) => !h.ok);
        toast.error(`${s.name} 连不上${bad ? `：第 ${bad.index} 跳${reasonText(bad)}` : ""}`);
      }
      reload();
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setTestingId(null);
    }
  }

  const add = (
    <Button onClick={() => setDrawer("new")}>
      <Plus />
      添加服务器
    </Button>
  );

  const body = (() => {
    if (error && !servers) return <LoadError what="服务器" message={error} onRetry={reload} />;
    if (!servers || (loading && !servers.length)) {
      return (
        <div className="grid gap-3" aria-busy="true" aria-label="正在读取服务器">
          <Skeleton className="h-16" />
          <Skeleton className="h-16" />
        </div>
      );
    }
    if (!servers.length) {
      return (
        <Sheet className="px-5 py-10 text-center text-[13.5px] text-ink-2">
          还没有服务器。添加后才能在备份任务里选用。
        </Sheet>
      );
    }
    return (
      <Sheet className="overflow-hidden">
        <div className={cn("hidden gap-6 border-b border-line px-5 py-2.5 text-xs text-ink-3 lg:grid", COLS)}>
          <span>名称</span>
          <span>地址</span>
          <span>连接路径</span>
          <span>状态</span>
          <span className="w-[108px]" />
        </div>
        <ul className="divide-y divide-line">
          {servers.map((s) => (
            <li key={s.id} className={cn("grid grid-cols-[1fr_auto] items-center gap-x-6 gap-y-2 px-5 py-3.5", COLS)}>
              <div className="min-w-0">
                <div className="font-semibold">{s.name}</div>
                <div className="text-xs text-ink-3">
                  {AUTH_LABEL[s.auth_type]}，{s.job_count} 个任务
                </div>
              </div>
              {/* 窄屏时地址排在第二行，不省略 */}
              <Mono className="col-span-2 row-start-2 block truncate lg:col-span-1 lg:row-start-auto">
                {s.username}@{s.host}:{s.port}
              </Mono>
              <div className="col-span-2 row-start-3 min-w-0 lg:col-span-1 lg:row-start-auto">
                <ChainInline hops={s.hops ?? []} />
              </div>
              <div className="col-span-2 text-[13px] lg:col-span-1">
                <ServerStatus s={s} />
              </div>
              <div className="col-start-2 row-start-1 flex justify-end gap-1 lg:col-start-auto lg:row-start-auto">
                <Button
                  variant="ghost"
                  size="icon-sm"
                  aria-label={`测试 ${s.name}`}
                  title="测试连接"
                  disabled={testingId === s.id}
                  onClick={() => void testSaved(s)}
                >
                  <RefreshCw className={cn(testingId === s.id && "animate-spin")} />
                </Button>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  aria-label={`编辑 ${s.name}`}
                  title="编辑"
                  onClick={() => setDrawer(s)}
                >
                  <Pencil />
                </Button>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  aria-label={`删除 ${s.name}`}
                  title="删除"
                  onClick={() => setDeleting(s)}
                >
                  <Trash2 />
                </Button>
              </div>
            </li>
          ))}
        </ul>
      </Sheet>
    );
  })();

  return (
    <>
      <PageTitle title="服务器" actions={add} />
      {body}
      {drawer && (
        <ServerDrawer
          key={drawer === "new" ? "new" : drawer.id}
          item={drawer === "new" ? null : drawer}
          servers={servers ?? []}
          onClose={() => setDrawer(null)}
          onSaved={() => {
            setDrawer(null);
            reload();
          }}
        />
      )}
      <DeleteDialog
        server={deleting}
        onClose={() => setDeleting(null)}
        onDeleted={() => {
          setDeleting(null);
          reload();
        }}
      />
    </>
  );
}

function hopText(h: HopView) {
  return h.type === "ssh" ? (h.server_name ?? `服务器 ${h.server_id}`) : `${h.host}:${h.port}`;
}

/** 连接路径：直连，或依次列出各跳 */
function ChainInline({ hops }: { hops: HopView[] }) {
  if (!hops.length) return <span className="text-[13px] text-ink-3">直连</span>;
  return (
    <span
      className="flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-0.5 text-[13px]"
      title={hops
        .map((h) => `${h.type === "ssh" ? "SSH" : h.type === "socks5" ? "SOCKS5" : "HTTP"} ${hopText(h)}`)
        .join(" → ")}
    >
      {hops.map((h, i) => (
        <React.Fragment key={i}>
          {i > 0 && <span className="text-ink-3">→</span>}
          <span className={cn("min-w-0 break-all", h.type !== "ssh" && "font-mono text-[12.5px]")}>
            {h.type !== "ssh" && (
              <span className="mr-1 rounded bg-sunken px-1 font-sans text-[11px] text-ink-2">
                {h.type === "socks5" ? "SOCKS5" : "HTTP"}
              </span>
            )}
            {hopText(h)}
          </span>
        </React.Fragment>
      ))}
    </span>
  );
}

function ServerStatus({ s }: { s: ServerItem }) {
  if (s.status === "ok") {
    return (
      <span className="flex items-center gap-1.5 text-ok">
        <ShieldCheck className="size-4" aria-hidden />
        <span>
          正常 <span className="text-ink-3 num">{s.latency_ms} ms</span>
        </span>
      </span>
    );
  }
  if (s.status === "failed") {
    return (
      <span className="flex items-center gap-1.5 text-fail" title={s.last_error || undefined}>
        <AlertTriangle className="size-4" aria-hidden />
        {s.fail_since ? `${fmtDate(s.fail_since)}起连不上` : "连不上"}
      </span>
    );
  }
  return <span className="text-ink-3">还没有测试过</span>;
}

function DeleteDialog({
  server,
  onClose,
  onDeleted,
}: {
  server: ServerItem | null;
  onClose: () => void;
  onDeleted: () => void;
}) {
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  // 换一台服务器时清掉上一次的错误：在渲染期间对比
  const [prev, setPrev] = React.useState(server);
  if (prev !== server) {
    setPrev(server);
    setError(null);
  }

  async function remove() {
    if (!server) return;
    setBusy(true);
    setError(null);
    try {
      await api.servers.remove(server.id);
      toast.success(`已删除 ${server.name}`);
      onDeleted();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <AlertDialog open={server !== null} onOpenChange={(v) => !v && onClose()}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>删除服务器「{server?.name}」？</AlertDialogTitle>
          <AlertDialogDescription>删除后不能恢复；被任务使用或被其他服务器用作跳板时不能删除。</AlertDialogDescription>
        </AlertDialogHeader>
        <FormError>{error}</FormError>
        <AlertDialogFooter>
          <AlertDialogCancel>取消</AlertDialogCancel>
          <AlertDialogAction
            disabled={busy}
            className="bg-destructive text-white hover:bg-destructive/90"
            onClick={(e) => {
              e.preventDefault();
              void remove();
            }}
          >
            删除
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
