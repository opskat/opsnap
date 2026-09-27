import {
  CircleAlert,
  CircleCheck,
  Clock,
  Database,
  Ellipsis,
  Eye,
  Loader2,
  Server,
  ShieldAlert,
  Trash2,
  TriangleAlert,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";

import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { relativeTime } from "@/lib/format";
import {
  dataSourceChainNames,
  dataSourceVersionLabel,
  probeSummaryOf,
  type DataSourceItem,
  type DataSourceStatus,
} from "@/lib/sources";
import { cn } from "@/lib/utils";

const columns =
  "grid grid-cols-[minmax(0,1.3fr)_minmax(0,1.6fr)_minmax(0,1.1fr)_minmax(0,1.1fr)_auto] items-center gap-4 px-4.5";

const statusStyle: Record<DataSourceStatus, string> = {
  ok: "bg-success-soft text-success",
  unreachable: "bg-destructive-soft text-destructive",
  host_key_changed: "bg-warning-soft text-warning",
};

const probeStyle: Record<string, string> = {
  probing: "text-muted-foreground",
  unprobeable: "text-muted-foreground",
  ok: "text-success",
  warn: "text-warning",
  fail: "text-destructive",
};

function kindIcon(kind: DataSourceItem["kind"]) {
  return kind === "server_file" ? <Server /> : <Database />;
}

function addressLine(t: (key: string, opts?: Record<string, unknown>) => string, item: DataSourceItem) {
  const version = dataSourceVersionLabel(item);
  const hops = dataSourceChainNames(item.chain);
  const chain =
    hops.length > 0
      ? t("sources.dataSource.list.via", { chain: hops.join(" → ") })
      : t("sources.dataSource.list.direct");
  return [version, chain].filter(Boolean).join(" · ");
}

function ProbeCell({ item }: { item: DataSourceItem }) {
  const { t } = useTranslation();
  const summary = probeSummaryOf(item.probe);
  if (summary.kind === "probing") {
    return (
      <span className={cn("flex items-center gap-1.5 text-xs", probeStyle.probing)}>
        <Loader2 className="size-3.5 shrink-0 animate-spin" />
        {t("sources.dataSource.list.probing")}
      </span>
    );
  }
  if (summary.kind === "unprobeable") {
    return (
      <span className={cn("flex items-center gap-1.5 text-xs", probeStyle.unprobeable)}>
        <Clock className="size-3.5 shrink-0" />
        {t("sources.dataSource.list.unprobeable")}
      </span>
    );
  }
  const icon =
    summary.kind === "fail" ? (
      <CircleAlert className="size-3.5 shrink-0" />
    ) : summary.kind === "warn" ? (
      <TriangleAlert className="size-3.5 shrink-0" />
    ) : (
      <CircleCheck className="size-3.5 shrink-0" />
    );
  const parts: string[] = [];
  if (summary.kind === "ok") {
    parts.push(t("sources.dataSource.list.probeAllOk", { count: summary.ok }));
  } else {
    if (summary.ok > 0) parts.push(t("sources.dataSource.list.probeOk", { count: summary.ok }));
    if (summary.warn > 0) parts.push(t("sources.dataSource.list.probeWarn", { count: summary.warn }));
    if (summary.fail > 0) parts.push(t("sources.dataSource.list.probeFail", { count: summary.fail }));
  }
  return (
    <span className={cn("flex items-center gap-1.5 text-xs", probeStyle[summary.kind])}>
      {icon}
      {parts.join(" · ")}
    </span>
  );
}

export interface DataSourceRowActions {
  onTest: (d: DataSourceItem) => void;
  onEdit: (d: DataSourceItem) => void;
  onReconfirm: (d: DataSourceItem) => void;
  onDelete: (d: DataSourceItem) => void;
}

export function DataSourceTable({
  items,
  testing,
  actions,
}: {
  items: DataSourceItem[];
  /** 正在测试连接的数据源 ID */
  testing?: ReadonlySet<number>;
  actions: DataSourceRowActions;
}) {
  const { t } = useTranslation();
  return (
    <div role="table" aria-label={t("sources.tabs.dataSources")} className="overflow-hidden rounded-lg border bg-card">
      <div role="row" className={cn(columns, "bg-accent py-2.25 text-xs text-muted-foreground")}>
        <span role="columnheader">{t("sources.dataSource.list.name")}</span>
        <span role="columnheader">{t("sources.dataSource.list.address")}</span>
        <span role="columnheader">{t("sources.dataSource.list.probe")}</span>
        <span role="columnheader">{t("sources.dataSource.list.status")}</span>
        <span role="columnheader" className="text-right">
          {t("sources.dataSource.list.actions")}
        </span>
      </div>
      {items.map((d) => (
        <div key={d.id} role="row" className={cn(columns, "border-t py-3 text-sm")}>
          <span role="cell" className="flex min-w-0 items-center gap-3">
            <span className="flex size-8 shrink-0 items-center justify-center rounded-md border bg-background text-muted-foreground [&_svg]:size-4">
              {kindIcon(d.kind)}
            </span>
            <span className="flex min-w-0 flex-col">
              <Link to={`/sources/${d.id}`} className="truncate font-medium hover:underline">
                {d.name}
              </Link>
              <span className="text-xs text-muted-foreground">{t(`sources.dataSource.kind.${d.kind}`)}</span>
            </span>
          </span>
          <span role="cell" className="flex min-w-0 flex-col font-mono text-xs">
            <span className="truncate">{d.address}</span>
            <span className="truncate font-sans text-muted-foreground">{addressLine(t, d)}</span>
          </span>
          <span role="cell">
            <ProbeCell item={d} />
          </span>
          <span role="cell" className="flex min-w-0 flex-col items-start gap-1">
            <span className={cn("rounded-sm px-1.75 py-0.5 text-2xs font-medium", statusStyle[d.status])}>
              {t(`sources.dataSource.status.${d.status}`)}
            </span>
            <span className="max-w-full truncate text-xs text-muted-foreground" title={d.status_message || undefined}>
              {d.status === "ok"
                ? t("sources.dataSource.list.checked", { time: relativeTime(t, d.checked_at) })
                : d.status_message}
            </span>
          </span>
          <span role="cell" className="flex items-center justify-end gap-1">
            {d.status === "host_key_changed" && (
              <Button
                variant="ghost"
                size="sm"
                className="text-warning"
                aria-label={t("sources.dataSource.list.reconfirmNamed", { name: d.name })}
                onClick={() => actions.onReconfirm(d)}
              >
                <ShieldAlert />
                {t("sources.dataSource.list.reconfirm")}
              </Button>
            )}
            <Button
              variant="ghost"
              size="sm"
              disabled={testing?.has(d.id)}
              aria-label={t("sources.dataSource.list.testNamed", { name: d.name })}
              onClick={() => actions.onTest(d)}
            >
              {testing?.has(d.id) ? t("sources.dataSource.form.testing") : t("sources.dataSource.list.test")}
            </Button>
            <Button
              variant="ghost"
              size="sm"
              aria-label={t("sources.dataSource.list.editNamed", { name: d.name })}
              onClick={() => actions.onEdit(d)}
            >
              {t("sources.dataSource.list.edit")}
            </Button>
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="ghost" size="icon-sm" aria-label={t("sources.dataSource.list.more", { name: d.name })}>
                  <Ellipsis />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuItem asChild>
                  <Link to={`/sources/${d.id}`}>
                    <Eye />
                    {t("sources.dataSource.list.viewDetail")}
                  </Link>
                </DropdownMenuItem>
                <DropdownMenuItem variant="destructive" onSelect={() => actions.onDelete(d)}>
                  <Trash2 />
                  {t("sources.dataSource.list.deleteItem")}
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </span>
        </div>
      ))}
    </div>
  );
}
