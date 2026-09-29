import { useTranslation } from "react-i18next";
import { Link } from "react-router";

import { formatBytes } from "@/lib/jobs";
import type { OverviewStorage } from "@/lib/overview";

import { percent } from "./format";
import { Panel, Placeholder } from "./Panel";

function StorageRow({ storage }: { storage: OverviewStorage }) {
  const { t } = useTranslation();
  const disk = storage.kind === "local" ? storage.disk : null;
  const used = disk && disk.total_bytes > 0 ? percent(disk.used_bytes / disk.total_bytes) : 0;

  return (
    <li className="flex flex-col gap-2">
      <div className="flex items-baseline justify-between gap-3">
        <p className="min-w-0 truncate text-sm font-medium">
          {storage.name} · {t(`storage.kind.${storage.kind}`)}
        </p>
        <p className="shrink-0 font-mono text-xs text-muted-foreground">
          {storage.readable
            ? t("overview.storage.usage", { size: formatBytes(storage.packed_bytes), count: storage.snapshots })
            : "—"}
        </p>
      </div>
      {disk && (
        <div
          role="progressbar"
          aria-label={t("overview.storage.diskUsed")}
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={used}
          className="h-1.5 overflow-hidden rounded-full bg-track"
        >
          <div className="h-full rounded-full bg-brand" style={{ width: `${used}%` }} />
        </div>
      )}
      <p className="font-mono text-xs break-words text-muted-foreground">
        {storage.kind === "local" ? storage.path : storage.location}
        {disk &&
          ` · ${t("overview.storage.disk", {
            used: formatBytes(disk.used_bytes),
            total: formatBytes(disk.total_bytes),
            free: formatBytes(disk.free_bytes),
          })}`}
      </p>
      {!storage.readable && <p className="text-xs text-destructive">{storage.reason}</p>}
    </li>
  );
}

/** 存储目标（spec「概览」→「存储目标」）；data 为空表示加载中 */
export function StorageTargets({ data }: { data?: OverviewStorage[] }) {
  const { t } = useTranslation();
  return (
    <Panel title={t("overview.storage.title")} loading={!data}>
      <div className="px-5 pb-5">
        {!data && (
          <div className="flex flex-col gap-3">
            <Placeholder className="h-4 w-full" />
            <Placeholder className="h-1.5 w-full" />
            <Placeholder className="h-4 w-2/3" />
          </div>
        )}
        {data && data.length === 0 && (
          <div className="flex flex-col items-start gap-1.5 text-sm">
            <p className="text-muted-foreground">{t("overview.storage.empty")}</p>
            <Link to="/storage" className="text-brand-text hover:underline">
              {t("overview.storage.add")}
            </Link>
          </div>
        )}
        {data && data.length > 0 && (
          <ul className="flex flex-col gap-4">
            {data.map((s) => (
              <StorageRow key={s.id} storage={s} />
            ))}
          </ul>
        )}
      </div>
    </Panel>
  );
}
