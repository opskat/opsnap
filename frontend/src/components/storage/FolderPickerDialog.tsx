import { ArrowUp, ChevronRight, Folder, FolderPlus } from "lucide-react";
import { useEffect, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { listDirs, makeDir, type Dir, type DirStatus } from "@/lib/storage";
import { cn } from "@/lib/utils";

type Listing = { path: string; parent: string; dirs: Dir[] };

const statusStyle: Record<DirStatus, string> = {
  empty: "bg-success-soft text-success",
  repository: "bg-running-soft text-running",
  not_empty: "bg-accent text-muted-foreground",
  not_writable: "bg-destructive-soft text-destructive",
  no_access: "bg-destructive-soft text-destructive",
};

/** 把绝对路径拆成可点击的面包屑：/、/srv、/srv/backups… */
function crumbs(path: string) {
  const parts = path.split("/").filter(Boolean);
  return parts.map((name, i) => ({ name, path: `/${parts.slice(0, i + 1).join("/")}` }));
}

/**
 * 选择 OpsNap 主机上的目录：只列子目录并标出状态，可以新建文件夹。
 * 点目录进入它，“选择此目录”选中当前所在的目录。
 */
export function FolderPickerDialog({
  start,
  onPick,
  onClose,
}: {
  /** 为 undefined 时对话框关闭；空字符串表示从默认位置开始 */
  start?: string;
  onPick: (path: string) => void;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const [target, setTarget] = useState<string>();
  const [listing, setListing] = useState<Listing>();
  const [error, setError] = useState<string>();
  const [newName, setNewName] = useState("");
  const [mkdirError, setMkdirError] = useState<string>();
  const [creating, setCreating] = useState(false);
  const open = start !== undefined;
  const path = target ?? start;

  useEffect(() => {
    if (path === undefined) return;
    let cancelled = false;
    listDirs(path)
      .then((l) => {
        if (cancelled) return;
        setListing(l);
        setError(undefined);
      })
      .catch((err: unknown) => !cancelled && setError(err instanceof Error ? err.message : String(err)));
    return () => {
      cancelled = true;
    };
  }, [path]);

  const go = (p: string) => {
    setMkdirError(undefined);
    setTarget(p);
  };

  const close = () => {
    setTarget(undefined);
    setListing(undefined);
    setError(undefined);
    setNewName("");
    setMkdirError(undefined);
    onClose();
  };

  const create = async (e: FormEvent) => {
    e.preventDefault();
    if (!listing) return;
    setCreating(true);
    setMkdirError(undefined);
    try {
      const res = await makeDir(listing.path, newName);
      setNewName("");
      go(res.path);
    } catch (err) {
      setMkdirError(err instanceof Error ? err.message : String(err));
    } finally {
      setCreating(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(next) => !next && close()}>
      <DialogContent className="gap-0 bg-card p-0 sm:max-w-lg">
        <DialogHeader className="border-b px-5 py-4 text-left">
          <DialogTitle>{t("storage.picker.title")}</DialogTitle>
        </DialogHeader>
        <nav aria-label={t("storage.picker.path")} className="flex items-center gap-1.5 border-b px-5 py-2.5">
          <Button
            type="button"
            variant="outline"
            size="icon-sm"
            aria-label={t("storage.picker.up")}
            disabled={!listing?.parent}
            onClick={() => listing?.parent && go(listing.parent)}
          >
            <ArrowUp />
          </Button>
          <ol className="flex min-w-0 flex-wrap items-center gap-1 font-mono text-xs">
            <li>
              <button
                type="button"
                className="px-1 text-muted-foreground hover:text-foreground"
                onClick={() => go("/")}
              >
                /
              </button>
            </li>
            {listing &&
              crumbs(listing.path).map((c, i, all) => (
                <li key={c.path} className="flex items-center gap-1">
                  {i > 0 && <ChevronRight aria-hidden className="size-3 text-faint-foreground" />}
                  <button
                    type="button"
                    aria-current={i === all.length - 1 ? "location" : undefined}
                    className={cn(
                      "px-1 hover:text-foreground",
                      i === all.length - 1 ? "font-semibold text-foreground" : "text-muted-foreground"
                    )}
                    onClick={() => go(c.path)}
                  >
                    {c.name}
                  </button>
                </li>
              ))}
          </ol>
        </nav>
        <div className="flex flex-col gap-3 p-3">
          {error && (
            <p role="alert" className="rounded-md bg-destructive-soft px-3 py-2.5 text-sm text-destructive">
              {error}
            </p>
          )}
          <ul aria-label={t("storage.picker.dirs")} className="flex max-h-72 flex-col gap-0.5 overflow-y-auto">
            {listing?.dirs.length === 0 && (
              <li className="px-3 py-4 text-center text-sm text-muted-foreground">{t("storage.picker.noDirs")}</li>
            )}
            {listing?.dirs.map((d) => {
              const blocked = d.status === "no_access";
              return (
                <li key={d.path}>
                  <button
                    type="button"
                    disabled={blocked}
                    onClick={() => go(d.path)}
                    className="flex w-full items-center gap-2.5 rounded-md px-3 py-2 text-left text-sm hover:bg-accent disabled:cursor-not-allowed disabled:opacity-60"
                  >
                    <Folder className="size-4 shrink-0 text-muted-foreground" />
                    <span className="min-w-0 flex-1 truncate font-mono">{d.name}</span>
                    <span className={cn("rounded-sm px-1.75 py-0.5 text-2xs font-medium", statusStyle[d.status])}>
                      {t(`storage.picker.status.${d.status}`)}
                    </span>
                    {!blocked && <ChevronRight className="size-4 shrink-0 text-faint-foreground" />}
                  </button>
                </li>
              );
            })}
          </ul>
          <form onSubmit={(e) => void create(e)} className="flex flex-col gap-1.5 px-2">
            <div className="flex gap-2">
              <Input
                value={newName}
                placeholder={t("storage.picker.newFolderName")}
                aria-label={t("storage.picker.newFolderName")}
                aria-invalid={mkdirError ? true : undefined}
                className="bg-background"
                onChange={(e) => setNewName(e.target.value)}
              />
              <Button type="submit" variant="outline" disabled={!newName.trim() || creating || !listing}>
                <FolderPlus />
                {t("storage.picker.newFolder")}
              </Button>
            </div>
            {mkdirError && (
              <p role="alert" className="text-xs text-destructive">
                {mkdirError}
              </p>
            )}
          </form>
        </div>
        <DialogFooter className="items-center border-t bg-sidebar px-5 py-3.5 sm:justify-between">
          <code
            className="min-w-0 truncate font-mono text-xs text-muted-foreground"
            aria-label={t("storage.picker.selected")}
          >
            {listing?.path}
          </code>
          <div className="flex gap-2">
            <Button type="button" variant="outline" onClick={close}>
              {t("common.cancel")}
            </Button>
            <Button
              type="button"
              disabled={!listing}
              onClick={() => {
                if (!listing) return;
                onPick(listing.path);
                close();
              }}
            >
              {t("storage.picker.choose")}
            </Button>
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
