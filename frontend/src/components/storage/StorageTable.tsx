import { Cloud, Download, Ellipsis, Folder, KeyRound, Lock, Trash2 } from "lucide-react";
import { useTranslation } from "react-i18next";

import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { relativeTime } from "@/lib/format";
import type { Storage, StorageStatus } from "@/lib/storage";
import { cn } from "@/lib/utils";

const columns = "grid grid-cols-[minmax(0,1.2fr)_minmax(0,1.6fr)_9rem_minmax(0,1.3fr)_auto] items-center gap-4 px-4.5";

const statusStyle: Record<StorageStatus, string> = {
  ok: "bg-success-soft text-success",
  wrong_key: "bg-warning-soft text-warning",
  unreachable: "bg-destructive-soft text-destructive",
};

export interface RowActions {
  onTest: (s: Storage) => void;
  onEdit: (s: Storage) => void;
  onUnlock: (s: Storage) => void;
  onDelete: (s: Storage) => void;
  /** 查看密钥与下载密钥文件；不传时菜单中不显示 */
  onRevealKey?: (s: Storage) => void;
  onDownloadKey?: (s: Storage) => void;
}

export function StorageTable({ items, testing, actions }: { items: Storage[]; testing?: number; actions: RowActions }) {
  const { t } = useTranslation();
  return (
    <div role="table" aria-label={t("nav.storage")} className="overflow-hidden rounded-lg border bg-card">
      <div role="row" className={cn(columns, "bg-accent py-2.25 text-xs text-muted-foreground")}>
        <span role="columnheader">{t("storage.list.name")}</span>
        <span role="columnheader">{t("storage.list.location")}</span>
        <span role="columnheader">{t("storage.list.key")}</span>
        <span role="columnheader">{t("storage.list.status")}</span>
        <span role="columnheader" className="text-right">
          {t("storage.list.actions")}
        </span>
      </div>
      {items.map((s) => (
        <div key={s.id} role="row" className={cn(columns, "border-t py-3 text-sm")}>
          <span role="cell" className="flex min-w-0 items-center gap-3">
            <span className="flex size-8 shrink-0 items-center justify-center rounded-md border bg-background text-muted-foreground [&_svg]:size-4">
              {s.kind === "local" ? <Folder /> : <Cloud />}
            </span>
            <span className="flex min-w-0 flex-col">
              <span className="truncate font-medium">{s.name}</span>
              <span className="text-xs text-muted-foreground">{t(`storage.kind.${s.kind}`)}</span>
            </span>
          </span>
          <span role="cell" className="flex min-w-0 flex-col font-mono text-xs">
            <span className="truncate">{s.location}</span>
            <span className="truncate text-muted-foreground">
              {s.kind === "local" ? t("storage.list.host") : s.endpoint}
            </span>
          </span>
          <span role="cell" className="flex items-center gap-1.5 font-mono text-xs text-muted-foreground">
            <Lock className="size-3.5" />
            {s.fingerprint}
          </span>
          <span role="cell" className="flex min-w-0 flex-col items-start gap-1">
            <span className={cn("rounded-sm px-1.75 py-0.5 text-2xs font-medium", statusStyle[s.status])}>
              {t(`storage.status.${s.status}`)}
            </span>
            <span className="max-w-full truncate text-xs text-muted-foreground" title={s.status_message || undefined}>
              {s.status === "ok"
                ? t("storage.list.checked", { time: relativeTime(t, s.checked_at) })
                : s.status_message}
            </span>
          </span>
          <span role="cell" className="flex items-center justify-end gap-1">
            {s.status === "wrong_key" ? (
              <Button variant="ghost" size="sm" className="text-warning" onClick={() => actions.onUnlock(s)}>
                {t("storage.list.unlock")}
              </Button>
            ) : (
              <Button
                variant="ghost"
                size="sm"
                disabled={testing === s.id}
                aria-label={t("storage.list.testNamed", { name: s.name })}
                onClick={() => actions.onTest(s)}
              >
                {testing === s.id ? t("storage.form.testing") : t("storage.form.test")}
              </Button>
            )}
            <Button
              variant="ghost"
              size="sm"
              aria-label={t("storage.list.editNamed", { name: s.name })}
              onClick={() => actions.onEdit(s)}
            >
              {t("storage.list.edit")}
            </Button>
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="ghost" size="icon-sm" aria-label={t("storage.list.more", { name: s.name })}>
                  <Ellipsis />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                {actions.onRevealKey && (
                  <DropdownMenuItem onSelect={() => actions.onRevealKey?.(s)}>
                    <KeyRound />
                    {t("storage.list.revealKey")}
                  </DropdownMenuItem>
                )}
                {actions.onDownloadKey && (
                  <DropdownMenuItem onSelect={() => actions.onDownloadKey?.(s)}>
                    <Download />
                    {t("storage.key.download")}
                  </DropdownMenuItem>
                )}
                {(actions.onRevealKey || actions.onDownloadKey) && <DropdownMenuSeparator />}
                <DropdownMenuItem variant="destructive" onSelect={() => actions.onDelete(s)}>
                  <Trash2 />
                  {t("storage.delete.confirm")}
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </span>
        </div>
      ))}
    </div>
  );
}
