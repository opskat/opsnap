import * as React from "react";
import { ChevronLeft, FolderOpen, Link2, TriangleAlert } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { api, errorMessage, type JobPathEntry, type JobPathsResult } from "@/lib/api";
import { buildInput, type JobForm } from "@/lib/jobs";
import { fmtAgo } from "@/lib/format";
import { cn } from "@/lib/utils";

interface LoadState {
  status: "loading" | "done";
  result: JobPathsResult | null;
  error: string | null;
}

/** 把路径拆成面包屑："/srv/www" → ["/", "srv", "www"] */
export function crumbs(path: string): { label: string; path: string }[] {
  const out = [{ label: "/", path: "/" }];
  let acc = "";
  for (const seg of path.split("/")) {
    if (!seg) continue;
    acc += `/${seg}`;
    out.push({ label: seg, path: acc });
  }
  return out;
}

/** 上一级；根目录下返回 null */
export function parentOf(path: string): string | null {
  if (path === "/") return null;
  const i = path.lastIndexOf("/");
  return i <= 0 ? "/" : path.slice(0, i);
}

export function childPath(dir: string, name: string): string {
  return dir === "/" ? `/${name}` : `${dir}/${name}`;
}

/**
 * 目录选择器：从服务器上逐层浏览并选中目录。
 *
 * 路径输入框仍是唯一的事实来源，浏览只负责把正确的路径填进去。
 * 读取失败不是接口错误，原地写明是哪个目录、为什么读不了，并给一条退路。
 */
export function PathPicker({
  open,
  onOpenChange,
  form,
  jobId,
  startPath,
  onSelect,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  form: JobForm;
  /** 编辑中的任务 id，新建为 0 */
  jobId: number;
  /** 打开时停在哪个目录；为空表示根 */
  startPath: string;
  onSelect: (path: string, entryCount: number) => void;
}) {
  const [path, setPath] = React.useState(startPath || "/");
  const [state, setState] = React.useState<LoadState>({ status: "loading", result: null, error: null });
  // 关闭状态下不读：打开时由 path 的 effect 负责第一次加载
  const [wasOpen, setWasOpen] = React.useState(open);
  // 丢弃过期响应：快速连点时只认最后一次请求
  const pathRef = React.useRef(path);
  pathRef.current = path;

  // 打开的那一刻从输入框里的路径重新开始
  if (open && !wasOpen) {
    setWasOpen(true);
    setPath(startPath || "/");
  } else if (!open && wasOpen) {
    setWasOpen(false);
  }

  const load = React.useEffectEvent(async (forPath: string) => {
    setState({ status: "loading", result: null, error: null });
    try {
      const result = await api.jobs.paths(jobId, buildInput(form).input, forPath);
      // 快速连点时只认最后一次请求的结果
      setState((s) => (forPath === pathRef.current ? { status: "done", result, error: null } : s));
    } catch (err) {
      setState((s) => (forPath === pathRef.current ? { status: "done", result: null, error: errorMessage(err) } : s));
    }
  });

  React.useEffect(() => {
    if (!open) return;
    // 定时器让状态更新离开 effect 的同步阶段
    const timer = setTimeout(() => void load(path), 0);
    return () => clearTimeout(timer);
  }, [open, path]);

  const list = state.result?.ok ? (state.result.list ?? []) : [];
  const failure = state.error ?? (state.result && !state.result.ok ? failureText(state.result) : null);
  const loading = state.status === "loading";

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>选择目录</DialogTitle>
          <DialogDescription>浏览这台服务器上的目录，选中后会把路径填进输入框。</DialogDescription>
        </DialogHeader>

        <nav aria-label="路径" className="flex flex-wrap items-center gap-1 text-[13px]">
          {crumbs(path).map((c, i) => (
            <React.Fragment key={c.path}>
              {i > 0 && <span className="text-ink-3">/</span>}
              <button
                type="button"
                className={cn(
                  "rounded px-1.5 py-0.5 font-mono hover:bg-accent",
                  i === crumbs(path).length - 1 ? "text-ink" : "text-brand-ink"
                )}
                aria-current={i === crumbs(path).length - 1 ? "page" : undefined}
                onClick={() => setPath(c.path)}
              >
                {c.label}
              </button>
            </React.Fragment>
          ))}
        </nav>

        <div className="min-h-[16rem] rounded-lg border border-line bg-surface">
          {failure ? (
            <div role="alert" className="flex flex-col items-start gap-3 p-4">
              <p className="flex items-start gap-2 text-[13px] text-fail">
                <TriangleAlert className="mt-0.5 size-4 shrink-0" />
                <span className="min-w-0">{failure}</span>
              </p>
              <div className="flex flex-wrap gap-2">
                {parentOf(path) && (
                  <Button type="button" size="sm" variant="outline" onClick={() => setPath(parentOf(path)!)}>
                    <ChevronLeft />
                    上一级
                  </Button>
                )}
                {path !== "/" && (
                  <Button type="button" size="sm" variant="outline" onClick={() => setPath("/")}>
                    回到根目录
                  </Button>
                )}
                <Button type="button" size="sm" variant="ghost" onClick={() => void load(path)}>
                  重试
                </Button>
              </div>
            </div>
          ) : loading ? (
            <p role="status" className="p-4 text-[13px] text-ink-3">
              正在读取 <span className="font-mono">{path}</span>…
            </p>
          ) : list.length === 0 ? (
            <p className="p-4 text-[13px] text-ink-3">这个目录下没有子目录。仍然可以选它——里面的文件会被备份。</p>
          ) : (
            <ul className="max-h-[22rem] overflow-y-auto p-1">
              {parentOf(path) && (
                <li>
                  <button
                    type="button"
                    className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-[13px] hover:bg-accent focus-visible:ring-2 focus-visible:ring-brand focus-visible:outline-none"
                    onClick={() => setPath(parentOf(path)!)}
                  >
                    <ChevronLeft className="size-4 text-ink-3" />
                    <span className="text-ink-2">上一级</span>
                  </button>
                </li>
              )}
              {list.map((e) => (
                <li key={e.name}>
                  <button
                    type="button"
                    disabled={!e.selectable}
                    title={e.selectable ? undefined : "名称含特殊字符，不能在面板里选，请直接填路径"}
                    className={cn(
                      "flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-[13px]",
                      "hover:bg-accent focus-visible:ring-2 focus-visible:ring-brand focus-visible:outline-none",
                      !e.selectable && "cursor-not-allowed opacity-50"
                    )}
                    onClick={() => setPath(childPath(path, e.name))}
                  >
                    <FolderOpen className="size-4 shrink-0 text-ink-3" />
                    <span className="min-w-0 flex-1 truncate font-mono">{e.name}</span>
                    {e.symlink && (
                      <span className="flex items-center gap-1 rounded bg-warn-wash px-1.5 py-0.5 text-[11px] text-warn">
                        <Link2 className="size-3" />
                        链接
                      </span>
                    )}
                    <span className="shrink-0 text-[12px] text-ink-3">{fmtAgo(e.mtime)}</span>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>

        {state.result?.truncated && (
          <p className="text-[13px] text-ink-3">条目太多，只显示前 2000 个。可以直接在输入框里填完整路径。</p>
        )}

        <div className="flex flex-wrap items-center justify-between gap-3">
          <p className="min-w-0 text-[13px] text-ink-2">
            将备份 <span className="font-mono">{path}</span>
            {state.result?.ok && list.length > 0 && <span className="text-ink-3">（{list.length} 个子目录）</span>}
          </p>
          <div className="flex gap-2">
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              取消
            </Button>
            <Button type="button" disabled={loading || !!failure} onClick={() => onSelect(path, list.length)}>
              选择此目录
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}

function failureText(r: JobPathsResult): string {
  const label = r.reason_label || "读取目录失败";
  return r.message ? `${label}：${r.message}` : label;
}

export type { JobPathEntry };
