import { ChevronRight } from "lucide-react";
import { useEffect, useId, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { Segmented } from "@/components/form/Segmented";
import { Checkbox } from "@/components/ui/checkbox";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import {
  dumpToolItem,
  dumpToolUnavailable,
  formatBytes,
  listDatabases,
  type DatabaseInfo,
  type JobDraft,
  type JobOptions,
  type JobScope,
  type WizardErrors,
} from "@/lib/jobs";
import { pickProbeText, type DataSourceItem } from "@/lib/sources";
import { cn } from "@/lib/utils";

import { Choice, FieldError, LoadError, SectionTitle, SoonBadge } from "./Choice";
import { errorMessage, type Loadable } from "./loadable";
import { FixBlock, ProbePanel } from "./ProbePanel";

/** 第 2 步：内容与方式（MySQL / PostgreSQL 两种） */
export function StepContent({
  draft,
  source,
  errors,
  onChange,
  onSourceChange,
}: {
  draft: JobDraft;
  source: DataSourceItem;
  errors: Pick<WizardErrors, "method" | "databases" | "exclude">;
  onChange: (patch: Partial<JobDraft>) => void;
  onSourceChange: (item: DataSourceItem) => void;
}) {
  const { t } = useTranslation();
  const isPg = source.kind === "postgres";

  return (
    <div className="flex flex-col gap-6">
      <ProbePanel item={source} onChange={onSourceChange} />
      <ScopeSection key={source.id} draft={draft} sourceId={source.id} error={errors.databases} onChange={onChange} />
      <MethodSection source={source} error={errors.method} />
      <section className="flex flex-col gap-2.5">
        <SectionTitle title={t(isPg ? "jobs.wizard.content.pgOptions" : "jobs.wizard.content.mysqlOptions")} />
        <div className="flex flex-col gap-3 rounded-lg border bg-card px-4 py-3.5 text-sm">
          <div className="flex gap-4">
            <span className="w-20 shrink-0 text-muted-foreground">{t("jobs.wizard.content.tool")}</span>
            <span>{t(isPg ? "jobs.wizard.content.pgTool" : "jobs.wizard.content.mysqlTool")}</span>
          </div>
          <div className="flex gap-4">
            <span className="w-20 shrink-0 text-muted-foreground">{t("jobs.wizard.content.alsoBackup")}</span>
            <span className="flex flex-wrap gap-x-5 gap-y-2">
              {(isPg ? PG_OPTIONS : MYSQL_OPTIONS).map((key) => (
                <OptionBox
                  key={key}
                  label={t(`jobs.wizard.content.options.${key}`)}
                  checked={draft.options[key]}
                  onChange={(v) => onChange({ options: { ...draft.options, [key]: v } })}
                />
              ))}
            </span>
          </div>
          <Advanced draft={draft} isPg={isPg} error={errors.exclude} onChange={onChange} />
        </div>
      </section>
    </div>
  );
}

const MYSQL_OPTIONS: (keyof JobOptions)[] = ["routines", "triggers", "events", "users"];
const PG_OPTIONS: (keyof JobOptions)[] = ["globals"];

function OptionBox({
  label,
  checked,
  disabled,
  onChange,
  children,
}: {
  label: string;
  checked: boolean;
  disabled?: boolean;
  onChange: (checked: boolean) => void;
  children?: ReactNode;
}) {
  return (
    <label className={cn("flex items-center gap-2", disabled ? "cursor-not-allowed" : "cursor-pointer")}>
      <Checkbox
        aria-label={label}
        checked={checked}
        disabled={disabled}
        onCheckedChange={(v) => onChange(v === true)}
      />
      {children ?? label}
    </label>
  );
}

/** 备份范围：整个实例 / 指定数据库；数据库列表在进入这一步时从数据源实时读取 */
function ScopeSection({
  draft,
  sourceId,
  error,
  onChange,
}: {
  draft: JobDraft;
  sourceId: number;
  error?: string;
  onChange: (patch: Partial<JobDraft>) => void;
}) {
  const { t } = useTranslation();
  const [dbs, setDbs] = useState<Loadable<DatabaseInfo[]>>({ status: "loading" });
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    let cancelled = false;
    listDatabases(sourceId)
      .then((r) => !cancelled && setDbs({ status: "ready", data: r.databases }))
      .catch((err: unknown) => !cancelled && setDbs({ status: "error", message: errorMessage(err) }));
    return () => {
      cancelled = true;
    };
  }, [sourceId, attempt]);

  const specified = draft.scope === "databases";
  const loaded = dbs.status === "ready" ? dbs.data : [];
  // 编辑时已选的库可能已不在数据源中：仍列出，便于取消勾选
  const rows: (DatabaseInfo & { missing?: boolean })[] = [
    ...loaded,
    ...(specified && dbs.status === "ready"
      ? draft.databases
          .filter((n) => !loaded.some((d) => d.name === n))
          .map((name) => ({ name, size: 0, missing: true }))
      : []),
  ];
  const chosen = specified ? loaded.filter((d) => draft.databases.includes(d.name)) : loaded;
  const total = chosen.reduce((sum, d) => sum + d.size, 0);
  const summary =
    dbs.status !== "ready"
      ? undefined
      : specified
        ? t("jobs.wizard.content.selectedSummary", { count: draft.databases.length, size: formatBytes(total) })
        : t("jobs.wizard.content.instanceSummary", { count: loaded.length, size: formatBytes(total) });

  const toggle = (name: string, on: boolean) =>
    onChange({ databases: on ? [...draft.databases, name] : draft.databases.filter((n) => n !== name) });

  return (
    <section className="flex flex-col gap-2.5">
      <SectionTitle
        title={t("jobs.wizard.content.scope")}
        aside={summary && <span className="font-mono text-xs text-muted-foreground">{summary}</span>}
      />
      <div className="flex flex-col gap-3 rounded-lg border bg-card px-4 py-3.5">
        <Segmented<JobScope>
          label={t("jobs.wizard.content.scope")}
          value={draft.scope}
          onChange={(scope) => onChange({ scope })}
          options={[
            { value: "instance", label: t("jobs.wizard.content.instance") },
            { value: "databases", label: t("jobs.wizard.content.databases") },
          ]}
        />
        <p className="text-xs text-muted-foreground">
          {t(specified ? "jobs.wizard.content.databasesHint" : "jobs.wizard.content.instanceHint")}
        </p>
        {dbs.status === "loading" && <p className="text-sm text-muted-foreground">{t("common.loading")}</p>}
        {dbs.status === "error" && (
          <LoadError
            message={t("jobs.wizard.content.databasesFailed", { message: dbs.message })}
            onRetry={() => {
              setDbs({ status: "loading" });
              setAttempt((n) => n + 1);
            }}
          />
        )}
        {dbs.status === "ready" && rows.length === 0 && (
          <p className="text-sm text-muted-foreground">{t("jobs.wizard.content.noDatabases")}</p>
        )}
        {rows.length > 0 && (
          <div className="grid grid-cols-1 gap-x-6 gap-y-2 sm:grid-cols-2">
            {rows.map((d) => (
              <OptionBox
                key={d.name}
                label={d.name}
                checked={specified ? draft.databases.includes(d.name) : true}
                disabled={!specified}
                onChange={(on) => toggle(d.name, on)}
              >
                <span className="flex min-w-0 flex-1 items-center justify-between gap-3 text-sm">
                  <span className="truncate font-mono">{d.name}</span>
                  <span
                    className={cn(
                      "shrink-0 text-xs",
                      d.missing ? "text-destructive" : "font-mono text-muted-foreground"
                    )}
                  >
                    {d.missing ? t("jobs.wizard.content.databaseMissing") : formatBytes(d.size)}
                  </span>
                </span>
              </OptionBox>
            ))}
          </div>
        )}
        <FieldError>{error}</FieldError>
      </div>
    </section>
  );
}

/** 备份方式：本版本只有“仅全量”；导出工具不可用时它也不可选，并显示原因与修复方法 */
function MethodSection({ source, error }: { source: DataSourceItem; error?: string }) {
  const { t, i18n } = useTranslation();
  const isPg = source.kind === "postgres";
  const unavailable = dumpToolUnavailable(source);
  const tool = dumpToolItem(source);
  return (
    <section className="flex flex-col gap-2.5">
      <SectionTitle title={t("jobs.wizard.content.method")} />
      <div role="radiogroup" aria-label={t("jobs.wizard.content.method")} className="flex flex-col gap-2.5">
        <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
          <Choice
            checked={!unavailable}
            disabled={unavailable}
            onSelect={() => {}}
            className={cn("rounded-lg border p-3.5", unavailable ? "bg-card" : "border-primary")}
          >
            <span className="flex flex-col gap-0.5">
              <span className="text-sm font-semibold">{t("jobs.wizard.content.full")}</span>
              <span className="text-xs text-muted-foreground">
                {t(isPg ? "jobs.wizard.content.fullHintPg" : "jobs.wizard.content.fullHintMysql")}
              </span>
            </span>
          </Choice>
          <Choice checked={false} disabled onSelect={() => {}} className="rounded-lg border bg-card p-3.5">
            <span className="flex flex-col gap-0.5">
              <span className="flex items-center gap-2 text-sm font-semibold">
                {t(isPg ? "jobs.wizard.content.incrementalPg" : "jobs.wizard.content.incrementalMysql")}
                <SoonBadge />
              </span>
              <span className="text-xs text-muted-foreground">
                {t(isPg ? "jobs.wizard.content.incrementalPgHint" : "jobs.wizard.content.incrementalMysqlHint")}
              </span>
            </span>
          </Choice>
        </div>
        {unavailable && tool && (
          <div className="flex flex-col gap-2 rounded-lg border border-destructive bg-destructive-soft px-3.5 py-3">
            <p className="text-sm text-destructive">
              {t("jobs.wizard.content.fullUnavailable", { reason: pickProbeText(tool.detail, i18n.language) })}
            </p>
            <FixBlock fix={tool.fix} />
          </div>
        )}
      </div>
      <FieldError>{error}</FieldError>
    </section>
  );
}

/** 高级选项（默认折叠）：只有“排除表”，每行一个 */
function Advanced({
  draft,
  isPg,
  error,
  onChange,
}: {
  draft: JobDraft;
  isPg: boolean;
  error?: string;
  onChange: (patch: Partial<JobDraft>) => void;
}) {
  const { t } = useTranslation();
  const id = useId();
  const [open, setOpen] = useState(draft.excludeText !== "");
  return (
    <Collapsible open={open || !!error} onOpenChange={setOpen} className="flex flex-col gap-2.5 border-t pt-3">
      <CollapsibleTrigger className="flex w-fit items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground">
        <ChevronRight className={cn("size-4 transition-transform", (open || !!error) && "rotate-90")} />
        {t("jobs.wizard.content.advanced")}
        <span className="text-xs text-faint-foreground">{t("jobs.wizard.content.exclude")}</span>
      </CollapsibleTrigger>
      <CollapsibleContent className="flex flex-col gap-1.5">
        <label htmlFor={id} className="text-sm text-muted-foreground">
          {t("jobs.wizard.content.exclude")}
        </label>
        <textarea
          id={id}
          rows={4}
          value={draft.excludeText}
          placeholder={isPg ? "analytics.public.events_raw" : "orders.audit_log"}
          aria-invalid={error ? true : undefined}
          aria-describedby={`${id}-hint`}
          className={cn(
            "w-full rounded-md border border-input bg-background px-3 py-2 font-mono text-xs shadow-xs outline-none",
            "focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 aria-invalid:border-destructive"
          )}
          onChange={(e) => onChange({ excludeText: e.target.value })}
        />
        {error ? (
          <FieldError id={`${id}-hint`}>{error}</FieldError>
        ) : (
          <p id={`${id}-hint`} className="text-xs text-faint-foreground">
            {t(isPg ? "jobs.wizard.content.excludeHintPg" : "jobs.wizard.content.excludeHintMysql")}
          </p>
        )}
      </CollapsibleContent>
    </Collapsible>
  );
}
