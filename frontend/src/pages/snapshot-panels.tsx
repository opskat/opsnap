// 快照页右侧：文件快照的目录树（懒加载、勾选、打包下载）和数据库快照的导出文件（下载、恢复命令）。
import * as React from "react";
import { AlertTriangle, ChevronRight, Download, File, Folder, FolderOpen } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { CodeBlock, Mono, Sheet, Skeleton } from "@/components/bits";
import { api, errorMessage, type SnapshotEntry, type SnapshotItem } from "@/lib/api";
import { downloadUrl } from "@/lib/download";
import { fmtBytes } from "@/lib/format";
import { fileSuffix, fmtSnapshotTime, pickedSummary, rootPath, withoutDescendants } from "@/lib/snapshots";
import { useLoad } from "@/lib/use-load";
import { cn } from "@/lib/utils";
import { LoadError } from "./settings";

const COLS = "grid-cols-[1fr_100px_120px]";

/**
 * 下载：先调预检接口，通过后才让浏览器发起真正的流式下载。
 * 这样仓库被锁、NAS 不可访问、快照或路径不存在时，原因显示在面板上，浏览器不会保存任何半截文件；
 * 也不用 fetch + blob，那会把整个文件缓冲在浏览器内存里。
 */
function useDownload(jobId: number, sid: string) {
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const start = React.useCallback(
    async (paths: string[], gzip = false) => {
      setBusy(true);
      setError(null);
      try {
        await api.snapshots.checkDownload(jobId, sid, paths, gzip);
        downloadUrl(api.snapshots.downloadUrl(jobId, sid, paths, gzip));
      } catch (err) {
        setError(errorMessage(err));
      } finally {
        setBusy(false);
      }
    },
    [jobId, sid]
  );
  return { busy, error, start };
}

function DownloadError({ message }: { message: string | null }) {
  if (!message) return null;
  return (
    <div role="alert" className="flex items-start gap-2.5 border-t border-fail/30 bg-fail-wash px-5 py-3 text-[13px]">
      <AlertTriangle className="mt-0.5 size-4 shrink-0 text-fail" aria-hidden />
      <p>下载失败：{message}</p>
    </div>
  );
}

// ---------------------------------------------------------------- 文件快照

interface DirState {
  entries?: SnapshotEntry[];
  error?: string;
  loading: boolean;
}

export function FilesPanel({ jobId, snapshot }: { jobId: number; snapshot: SnapshotItem }) {
  const root = rootPath(snapshot);
  const [dirs, setDirs] = React.useState<Record<string, DirState>>({ [root]: { loading: true } });
  const [open, setOpen] = React.useState<Set<string>>(new Set());
  const [picked, setPicked] = React.useState<Map<string, SnapshotEntry>>(new Map());
  const dl = useDownload(jobId, snapshot.id);

  const fetchDir = React.useCallback(
    (path: string) =>
      api.snapshots
        .files(jobId, snapshot.id, path === "/" ? "" : path)
        .then((res): DirState => ({ entries: res.entries ?? [], loading: false }))
        .catch((err): DirState => ({ error: errorMessage(err), loading: false }))
        .then((state) => setDirs((d) => ({ ...d, [path]: state }))),
    [jobId, snapshot.id]
  );

  React.useEffect(() => {
    void fetchDir(root);
  }, [fetchDir, root]);

  function loadDir(path: string) {
    setDirs((d) => ({ ...d, [path]: { loading: true } }));
    void fetchDir(path);
  }

  function toggleOpen(e: SnapshotEntry) {
    const next = new Set(open);
    if (next.has(e.path)) {
      next.delete(e.path);
    } else {
      next.add(e.path);
      if (!dirs[e.path]?.entries) loadDir(e.path);
    }
    setOpen(next);
  }

  function togglePick(e: SnapshotEntry) {
    const next = new Map(picked);
    if (next.has(e.path)) next.delete(e.path);
    else next.set(e.path, e);
    setPicked(next);
  }

  const chosen = withoutDescendants([...picked.values()]);
  const summary = pickedSummary(chosen);

  return (
    <Sheet className="flex min-h-[520px] flex-col overflow-hidden">
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-line px-5 py-3.5">
        <nav className="flex min-w-0 items-center gap-1 text-[13px]" aria-label="路径">
          <span className="text-ink-2 num">{fmtSnapshotTime(snapshot.time)}</span>
          <ChevronRight className="size-3.5 text-ink-3" aria-hidden />
          <Mono className="truncate font-medium">{root}</Mono>
        </nav>
        <Mono className="text-xs text-ink-3">{snapshot.short_id}</Mono>
      </div>
      <div className={cn("grid border-b border-line px-5 py-2 text-xs text-ink-3", COLS)}>
        <span className="pl-12">名称</span>
        <span className="text-right">大小</span>
        <span className="text-right">修改时间</span>
      </div>
      <div className="flex-1 overflow-auto py-1">
        <Rows
          dir={root}
          depth={0}
          dirs={dirs}
          open={open}
          picked={picked}
          onOpen={toggleOpen}
          onPick={togglePick}
          onRetry={loadDir}
        />
      </div>
      <div
        className={cn(
          "flex flex-wrap items-center justify-between gap-3 border-t px-5 py-3 transition-colors",
          summary.count ? "border-brand/30 bg-brand-wash" : "border-line"
        )}
      >
        <span className="text-[13px]">
          {summary.count ? (
            <>
              已选 <b className="num">{summary.count}</b> 项
              {summary.bytes !== null && `，约 ${fmtBytes(summary.bytes)}`}
            </>
          ) : (
            <span className="text-ink-2">勾选文件或目录后可以打包下载</span>
          )}
        </span>
        <div className="flex gap-2">
          {summary.count > 0 && (
            <Button variant="ghost" size="sm" onClick={() => setPicked(new Map())}>
              清除
            </Button>
          )}
          <Button
            size="sm"
            disabled={!summary.count || dl.busy}
            onClick={() => void dl.start(chosen.map((e) => e.path))}
          >
            <Download />
            下载所选 (.tar.gz)
          </Button>
        </div>
      </div>
      <DownloadError message={dl.error} />
    </Sheet>
  );
}

function Rows({
  dir,
  depth,
  dirs,
  open,
  picked,
  onOpen,
  onPick,
  onRetry,
}: {
  dir: string;
  depth: number;
  dirs: Record<string, DirState>;
  open: Set<string>;
  picked: Map<string, SnapshotEntry>;
  onOpen: (e: SnapshotEntry) => void;
  onPick: (e: SnapshotEntry) => void;
  onRetry: (path: string) => void;
}) {
  const state = dirs[dir];
  const indent = { paddingLeft: 20 + depth * 22 };
  if (!state || state.loading) {
    return (
      <div className="px-5 py-2" style={indent} aria-busy>
        <Skeleton className="h-5 w-48" />
      </div>
    );
  }
  if (state.error) {
    return (
      <div className="flex items-center gap-3 px-5 py-2 text-[13px]" style={indent} role="alert">
        <span className="text-fail">读取失败：{state.error}</span>
        <Button size="xs" variant="outline" onClick={() => onRetry(dir)}>
          重试
        </Button>
      </div>
    );
  }
  if (!state.entries || state.entries.length === 0) {
    return (
      <div className="px-5 py-2 text-[13px] text-ink-3" style={indent}>
        空目录
      </div>
    );
  }
  return (
    <ul role={depth === 0 ? "tree" : "group"}>
      {state.entries.map((e) => {
        const isDir = e.type === "dir";
        const expanded = open.has(e.path);
        const on = picked.has(e.path);
        const Icon = isDir ? (expanded ? FolderOpen : Folder) : File;
        return (
          <li key={e.path} role="treeitem" aria-expanded={isDir ? expanded : undefined} aria-selected={on}>
            <div className={cn("grid h-9 items-center px-5 hover:bg-sunken/50", COLS, on && "bg-brand-wash/60")}>
              <span className="flex min-w-0 items-center gap-2" style={{ paddingLeft: depth * 22 }}>
                <Checkbox checked={on} onCheckedChange={() => onPick(e)} aria-label={`选择 ${e.name}`} />
                {isDir ? (
                  <button
                    type="button"
                    onClick={() => onOpen(e)}
                    aria-label={`${expanded ? "收起" : "展开"} ${e.name}`}
                    className="rounded text-ink-3 hover:text-ink"
                  >
                    <ChevronRight className={cn("size-3.5 transition-transform", expanded && "rotate-90")} />
                  </button>
                ) : (
                  <span className="w-3.5" />
                )}
                <Icon className={cn("size-4 shrink-0", isDir ? "text-ink-2" : "text-ink-3")} aria-hidden />
                <Mono className="truncate text-[13px]">{e.name}</Mono>
              </span>
              <span className="text-right text-[12.5px] text-ink-2 num">{isDir ? "" : fmtBytes(e.size)}</span>
              <span className="text-right text-[12.5px] text-ink-3 num">{e.mtime ? fmtSnapshotTime(e.mtime) : ""}</span>
            </div>
            {isDir && expanded && (
              <Rows
                dir={e.path}
                depth={depth + 1}
                dirs={dirs}
                open={open}
                picked={picked}
                onOpen={onOpen}
                onPick={onPick}
                onRetry={onRetry}
              />
            )}
          </li>
        );
      })}
    </ul>
  );
}

// ---------------------------------------------------------------- 数据库快照

export function ExportsPanel({ jobId, snapshot }: { jobId: number; snapshot: SnapshotItem }) {
  const load = React.useCallback(() => api.snapshots.exports(jobId, snapshot.id), [jobId, snapshot.id]);
  const { data, error, loading, reload } = useLoad(load);
  const dl = useDownload(jobId, snapshot.id);

  if (error && !data) return <LoadError what="导出文件" message={error} onRetry={reload} />;
  if (loading && !data) return <Skeleton className="h-60" />;
  const files = data?.files ?? [];

  return (
    <Sheet className="overflow-hidden">
      <div className="flex items-center justify-between border-b border-line px-5 py-3.5 text-[13px]">
        <span className="font-medium num">{fmtSnapshotTime(snapshot.time)}</span>
        <Mono className="text-xs text-ink-3">{snapshot.short_id}</Mono>
      </div>
      {files.length === 0 ? (
        <p className="px-5 py-10 text-center text-ink-2">这个快照里没有导出文件</p>
      ) : (
        <ul className="divide-y divide-line">
          {files.map((f) => (
            <li key={f.path} className="flex flex-wrap items-center gap-4 px-5 py-4">
              <File className="size-4 text-ink-3" aria-hidden />
              <Mono className="min-w-0 flex-1 truncate text-[13.5px]">{f.filename}</Mono>
              <span className="text-[13px] text-ink-2 num">{fmtBytes(f.size)}</span>
              <Button size="sm" variant="outline" disabled={dl.busy} onClick={() => void dl.start([f.path], f.gzip)}>
                <Download />
                下载 {fileSuffix(f.filename)}
              </Button>
            </li>
          ))}
        </ul>
      )}
      <DownloadError message={dl.error} />
      {files.length > 0 && (
        <div className="flex flex-col gap-3 border-t border-line bg-sunken/50 p-5">
          {files.map((f) => (
            <CodeBlock
              key={f.path}
              title={files.length > 1 ? `恢复 ${f.filename}` : "恢复到数据库"}
              text={f.restore_command}
            />
          ))}
          <p className="text-xs text-ink-2">opsnap 不会写回线上库，请手动执行；地址、账号和库名按目标环境替换。</p>
        </div>
      )}
    </Sheet>
  );
}
