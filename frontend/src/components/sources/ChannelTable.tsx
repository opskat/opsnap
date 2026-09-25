import { Ellipsis, Link2, ShieldAlert, Terminal, Trash2, Waypoints } from "lucide-react";
import { useTranslation } from "react-i18next";

import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { relativeTime } from "@/lib/format";
import { usedByNames, type ChannelItem, type ChannelStatus } from "@/lib/sources";
import { cn } from "@/lib/utils";

const columns =
  "grid grid-cols-[minmax(0,1.2fr)_minmax(0,1.6fr)_minmax(0,1fr)_minmax(0,1.2fr)_auto] items-center gap-4 px-4.5";

const statusStyle: Record<ChannelStatus, string> = {
  ok: "bg-success-soft text-success",
  unreachable: "bg-destructive-soft text-destructive",
  host_key_changed: "bg-warning-soft text-warning",
};

function authLabel(t: (key: string) => string, c: ChannelItem) {
  if (c.kind === "ssh")
    return t(c.auth_method === "key" ? "sources.channel.auth.key" : "sources.channel.auth.password");
  return t(c.auth_method === "password" ? "sources.channel.auth.socks5Password" : "sources.channel.auth.socks5None");
}

function usageLabel(t: (key: string, opts?: Record<string, unknown>) => string, c: ChannelItem) {
  const parts: string[] = [];
  if (c.used_by.data_sources.length > 0) {
    parts.push(t("sources.channel.list.dataSourceCount", { count: c.used_by.data_sources.length }));
  }
  if (c.used_by.channels.length > 0) {
    parts.push(t("sources.channel.list.channelCount", { count: c.used_by.channels.length }));
  }
  return parts.length > 0 ? parts.join(" · ") : t("sources.channel.list.unused");
}

export interface ChannelRowActions {
  onTest: (c: ChannelItem) => void;
  onEdit: (c: ChannelItem) => void;
  onReconfirm: (c: ChannelItem) => void;
  onDelete: (c: ChannelItem) => void;
}

export function ChannelTable({
  items,
  testing,
  actions,
}: {
  items: ChannelItem[];
  /** 正在测试连接的通道 ID */
  testing?: ReadonlySet<number>;
  actions: ChannelRowActions;
}) {
  const { t } = useTranslation();
  return (
    <div role="table" aria-label={t("sources.tabs.channels")} className="overflow-hidden rounded-lg border bg-card">
      <div role="row" className={cn(columns, "bg-accent py-2.25 text-xs text-muted-foreground")}>
        <span role="columnheader">{t("sources.channel.list.name")}</span>
        <span role="columnheader">{t("sources.channel.list.address")}</span>
        <span role="columnheader">{t("sources.channel.list.usage")}</span>
        <span role="columnheader">{t("sources.channel.list.status")}</span>
        <span role="columnheader" className="text-right">
          {t("sources.channel.list.actions")}
        </span>
      </div>
      {items.map((c) => {
        const viaHop = c.chain.length > 1 ? c.chain[c.chain.length - 2] : undefined;
        const usedBy = usedByNames(c.used_by);
        const canDelete = usedBy.length === 0;
        return (
          <div key={c.id} role="row" className={cn(columns, "border-t py-3 text-sm")}>
            <span role="cell" className="flex min-w-0 items-center gap-3">
              <span className="flex size-8 shrink-0 items-center justify-center rounded-md border bg-background text-muted-foreground [&_svg]:size-4">
                {c.kind === "ssh" ? <Terminal /> : <Waypoints />}
              </span>
              <span className="flex min-w-0 flex-col">
                <span className="truncate font-medium">{c.name}</span>
                <span className="text-xs text-muted-foreground">{t(`sources.channel.kind.${c.kind}`)}</span>
              </span>
            </span>
            <span role="cell" className="flex min-w-0 flex-col font-mono text-xs">
              <span className="truncate">{c.address}</span>
              <span className="truncate font-sans text-muted-foreground">
                {(viaHop ? t("sources.channel.list.via", { name: viaHop.name }) : t("sources.channel.list.direct")) +
                  " · " +
                  authLabel(t, c)}
              </span>
            </span>
            <span role="cell" className="flex items-center gap-1.5 text-xs text-muted-foreground">
              <Link2 className="size-3.5 shrink-0" />
              {usageLabel(t, c)}
            </span>
            <span role="cell" className="flex min-w-0 flex-col items-start gap-1">
              <span className={cn("rounded-sm px-1.75 py-0.5 text-2xs font-medium", statusStyle[c.status])}>
                {t(`sources.channel.status.${c.status}`)}
              </span>
              <span className="max-w-full truncate text-xs text-muted-foreground" title={c.status_message || undefined}>
                {c.status === "ok"
                  ? t("sources.channel.list.checked", { time: relativeTime(t, c.checked_at) })
                  : c.status_message}
              </span>
            </span>
            <span role="cell" className="flex items-center justify-end gap-1">
              {c.status === "host_key_changed" ? (
                <Button
                  variant="ghost"
                  size="sm"
                  className="text-warning"
                  aria-label={t("sources.channel.list.reconfirmNamed", { name: c.name })}
                  onClick={() => actions.onReconfirm(c)}
                >
                  <ShieldAlert />
                  {t("sources.channel.list.reconfirm")}
                </Button>
              ) : (
                <Button
                  variant="ghost"
                  size="sm"
                  disabled={testing?.has(c.id)}
                  aria-label={t("sources.channel.list.testNamed", { name: c.name })}
                  onClick={() => actions.onTest(c)}
                >
                  {testing?.has(c.id) ? t("sources.channel.form.testing") : t("sources.channel.form.test")}
                </Button>
              )}
              <Button
                variant="ghost"
                size="sm"
                aria-label={t("sources.channel.list.editNamed", { name: c.name })}
                onClick={() => actions.onEdit(c)}
              >
                {t("sources.channel.list.edit")}
              </Button>
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <Button variant="ghost" size="icon-sm" aria-label={t("sources.channel.list.more", { name: c.name })}>
                    <Ellipsis />
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end">
                  <DropdownMenuItem
                    variant="destructive"
                    disabled={!canDelete}
                    onSelect={() => canDelete && actions.onDelete(c)}
                  >
                    <Trash2 />
                    <span className="flex min-w-0 flex-col">
                      {t("sources.channel.list.deleteItem")}
                      {!canDelete && (
                        <span className="text-xs font-normal text-muted-foreground">
                          {t("sources.channel.list.deleteBlocked", { items: usedBy.join("、") })}
                        </span>
                      )}
                    </span>
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
            </span>
          </div>
        );
      })}
    </div>
  );
}
