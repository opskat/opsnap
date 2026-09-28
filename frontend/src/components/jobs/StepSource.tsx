import { Archive, Database, RefreshCw, Server } from "lucide-react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";

import { Button } from "@/components/ui/button";
import type { JobDraft } from "@/lib/jobs";
import { dataSourceVersionLabel, probeSummaryOf, type DataSourceItem } from "@/lib/sources";

import { Choice, FieldError, LoadError, SectionTitle, SoonBadge, StatusBadge } from "./Choice";
import type { Loadable } from "./loadable";

/** 探测摘要文案，格式与数据源列表一致 */
function useProbeSummaryText() {
  const { t } = useTranslation();
  return (item: DataSourceItem) => {
    const s = probeSummaryOf(item.probe);
    if (s.kind === "probing") return t("sources.dataSource.list.probing");
    if (s.kind === "unprobeable") return t("sources.dataSource.list.unprobeable");
    if (s.kind === "ok") return t("sources.dataSource.list.probeAllOk", { count: s.ok });
    const parts: string[] = [];
    if (s.ok > 0) parts.push(t("sources.dataSource.list.probeOk", { count: s.ok }));
    if (s.warn > 0) parts.push(t("sources.dataSource.list.probeWarn", { count: s.warn }));
    if (s.fail > 0) parts.push(t("sources.dataSource.list.probeFail", { count: s.fail }));
    return parts.join(" · ");
  };
}

/** 第 1 步：任务类型与数据源 */
export function StepSource({
  draft,
  sources,
  locked,
  error,
  onSelect,
  onRetry,
}: {
  draft: JobDraft;
  sources: Loadable<DataSourceItem[]>;
  /** 编辑任务时数据源不能修改 */
  locked: boolean;
  error?: string;
  onSelect: (ds: DataSourceItem) => void;
  onRetry: () => void;
}) {
  const { t } = useTranslation();
  const summary = useProbeSummaryText();

  const list = () => {
    if (sources.status === "loading") return <p className="text-sm text-muted-foreground">{t("common.loading")}</p>;
    if (sources.status === "error") {
      return <LoadError message={t("jobs.wizard.source.loadFailed", { message: sources.message })} onRetry={onRetry} />;
    }
    const items = sources.data;
    if (!items.some((d) => d.kind !== "server_file")) {
      return (
        <div className="flex flex-col items-center gap-2 rounded-lg border bg-card px-6 py-10 text-center">
          <p className="text-md font-medium">{t("jobs.wizard.source.empty")}</p>
          <p className="text-sm text-muted-foreground">{t("jobs.wizard.source.emptyHint")}</p>
          <Button asChild variant="outline" className="mt-2">
            <Link to="/sources">{t("jobs.wizard.source.create")}</Link>
          </Button>
        </div>
      );
    }
    return (
      <div
        role="radiogroup"
        aria-label={t("jobs.wizard.source.title")}
        aria-invalid={error ? true : undefined}
        className="divide-y overflow-hidden rounded-lg border bg-card"
      >
        {items.map((d) => {
          const soon = d.kind === "server_file";
          const checked = d.id === draft.datasourceId;
          return (
            <Choice
              key={d.id}
              checked={checked}
              disabled={soon || (locked && !checked)}
              onSelect={() => onSelect(d)}
              className="px-3.5 py-3"
            >
              <span className="flex items-center gap-3">
                <span className="flex size-8 shrink-0 items-center justify-center rounded-md border bg-background text-muted-foreground [&_svg]:size-4">
                  {d.kind === "server_file" ? <Server /> : <Database />}
                </span>
                <span className="flex min-w-0 flex-1 flex-col">
                  <span className="truncate text-sm font-medium">{d.name}</span>
                  <span className="truncate text-xs text-muted-foreground">
                    {t(`sources.dataSource.kind.${d.kind}`)} · <span className="font-mono">{d.address}</span>
                  </span>
                </span>
                <span className="flex shrink-0 flex-col items-end gap-1">
                  <span className="flex items-center gap-2">
                    {soon ? (
                      <SoonBadge />
                    ) : (
                      <>
                        <span className="text-xs text-muted-foreground">{dataSourceVersionLabel(d)}</span>
                        <StatusBadge
                          tone={d.status === "ok" ? "ok" : d.status === "host_key_changed" ? "warn" : "fail"}
                        >
                          {t(`sources.dataSource.status.${d.status}`)}
                        </StatusBadge>
                      </>
                    )}
                  </span>
                  {!soon && d.status === "ok" && <span className="text-xs text-muted-foreground">{summary(d)}</span>}
                </span>
              </span>
              {!soon && d.status !== "ok" && (
                <span className="mt-1.5 block pl-11 text-xs text-destructive">
                  {d.status_message || t(`sources.dataSource.status.${d.status}`)}
                </span>
              )}
            </Choice>
          );
        })}
      </div>
    );
  };

  return (
    <div className="flex flex-col gap-6">
      <section className="flex flex-col gap-2.5">
        <SectionTitle title={t("jobs.wizard.type.title")} />
        <div role="radiogroup" aria-label={t("jobs.wizard.type.title")} className="grid grid-cols-2 gap-3">
          <TypeCard
            icon={<Archive />}
            title={t("jobs.wizard.type.backup")}
            hint={t("jobs.wizard.type.backupHint")}
            checked={draft.type === "backup"}
          />
          <TypeCard
            icon={<RefreshCw />}
            title={t("jobs.wizard.type.sync")}
            hint={t("jobs.wizard.type.syncHint")}
            checked={false}
            disabled
          />
        </div>
      </section>
      <section className="flex flex-col gap-2.5">
        <SectionTitle
          title={t("jobs.wizard.source.title")}
          hint={locked ? t("jobs.wizard.destination.lockedHint") : t("jobs.wizard.source.hint")}
        />
        {list()}
        <FieldError>{error}</FieldError>
      </section>
    </div>
  );
}

function TypeCard({
  icon,
  title,
  hint,
  checked,
  disabled,
}: {
  icon: ReactNode;
  title: string;
  hint: string;
  checked: boolean;
  disabled?: boolean;
}) {
  return (
    <Choice
      checked={checked}
      disabled={disabled}
      onSelect={() => {}}
      className={checked ? "rounded-lg border border-primary p-3.5" : "rounded-lg border bg-card p-3.5"}
    >
      <span className="flex items-start gap-3">
        <span className="flex size-8 shrink-0 items-center justify-center rounded-md border bg-background text-muted-foreground [&_svg]:size-4">
          {icon}
        </span>
        <span className="flex min-w-0 flex-col gap-0.5">
          <span className="flex items-center gap-2 text-sm font-semibold">
            {title}
            {disabled && <SoonBadge />}
          </span>
          <span className="text-xs text-muted-foreground">{hint}</span>
        </span>
      </span>
    </Choice>
  );
}
