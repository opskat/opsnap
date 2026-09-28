import { useEffect, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { FormField } from "@/components/form/FormField";
import {
  excludeLines,
  formatBytes,
  formatScheduleTime,
  listDatabases,
  type DatabaseInfo,
  type JobDraft,
  type WizardErrors,
} from "@/lib/jobs";
import type { DataSourceItem } from "@/lib/sources";
import type { Storage } from "@/lib/storage";

import { Choice, SectionTitle } from "./Choice";
import { errorMessage, type Loadable } from "./loadable";
import { scheduleDescription } from "./schedule";
import type { SchedulePreview } from "./StepSchedule";

/** 第 5 步：确认（名称、四组摘要、创建后的选择、源数据量估算） */
export function StepConfirm({
  draft,
  editing,
  source,
  storage,
  preview,
  errors,
  submitError,
  onChangeName,
  onChangeRunNow,
  onGoStep,
}: {
  draft: JobDraft;
  editing: boolean;
  source?: DataSourceItem;
  storage?: Storage;
  preview: Loadable<SchedulePreview>;
  errors: Pick<WizardErrors, "name">;
  submitError?: string;
  onChangeName: (name: string) => void;
  onChangeRunNow: (runNow: boolean) => void;
  onGoStep: (step: number) => void;
}) {
  const { t, i18n } = useTranslation();
  const [dbs, setDbs] = useState<Loadable<DatabaseInfo[]>>({ status: "loading" });

  useEffect(() => {
    // 编辑时不显示“首次全量”的估算与“创建后”的选择
    if (!source || editing) return;
    let cancelled = false;
    listDatabases(source.id)
      .then((r) => !cancelled && setDbs({ status: "ready", data: r.databases }))
      .catch((err: unknown) => !cancelled && setDbs({ status: "error", message: errorMessage(err) }));
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps -- 按数据源 ID 重新拉取即可，source 对象每次刷新都会换身份
  }, [source?.id, editing]);

  const sizeText = () => {
    if (dbs.status !== "ready") return undefined;
    const chosen = draft.scope === "databases" ? dbs.data.filter((d) => draft.databases.includes(d.name)) : dbs.data;
    const total = chosen.reduce((sum, d) => sum + d.size, 0);
    return t("jobs.wizard.confirm.sizeEstimate", { size: formatBytes(total) });
  };

  const excludeCount = excludeLines(draft.excludeText).length;

  const scheduleSummary = () => scheduleDescription(t, i18n.language, draft.schedule);

  return (
    <div className="flex flex-col gap-6">
      {submitError && (
        <p role="alert" className="rounded-md bg-destructive-soft px-4 py-3 text-sm text-destructive">
          {submitError}
        </p>
      )}

      <FormField
        label={t("jobs.wizard.confirm.name")}
        value={draft.name}
        maxLength={64}
        error={errors.name}
        onChange={(e) => onChangeName(e.target.value)}
      />

      <div className="flex flex-col gap-3">
        <SummarySection title={t("jobs.wizard.steps.source")} onEdit={() => onGoStep(0)}>
          <p className="text-sm">
            {t(`jobs.wizard.type.${draft.type}`)} · {source?.name}
          </p>
          {source && <p className="text-xs text-muted-foreground">{t(`sources.dataSource.kind.${source.kind}`)}</p>}
        </SummarySection>

        <SummarySection title={t("jobs.wizard.steps.content")} onEdit={() => onGoStep(1)}>
          <p className="text-sm">
            {draft.scope === "instance"
              ? t("jobs.wizard.confirm.scopeInstance")
              : t("jobs.wizard.confirm.scopeDatabases", { count: draft.databases.length })}
          </p>
          {excludeCount > 0 && (
            <p className="text-xs text-muted-foreground">
              {t("jobs.wizard.confirm.excludeCount", { count: excludeCount })}
            </p>
          )}
        </SummarySection>

        <SummarySection title={t("jobs.wizard.steps.destination")} onEdit={() => onGoStep(2)}>
          <p className="font-mono text-sm">{storage ? `${storage.name}:/${draft.prefix}` : draft.prefix}</p>
          <p className="text-xs text-muted-foreground">
            {draft.compression === "none" ? t("jobs.wizard.destination.compressionNone") : draft.compression}
          </p>
        </SummarySection>

        <SummarySection title={t("jobs.wizard.steps.schedule")} onEdit={() => onGoStep(3)}>
          <p className="text-sm">
            <span>{scheduleSummary()}</span> · <span className="font-mono">{draft.schedule.timezone}</span>
          </p>
          <p className="text-xs text-muted-foreground">
            {t("jobs.wizard.confirm.retentionSummary", {
              days: draft.retention.days,
              weeks: draft.retention.weeks,
              months: draft.retention.months,
            })}
          </p>
          <p className="text-xs text-muted-foreground">
            {t("jobs.wizard.confirm.failureSummary", {
              retries: draft.failure.retries,
              interval: draft.failure.retry_interval,
              timeout: draft.failure.timeout,
            })}
          </p>
        </SummarySection>
      </div>

      {!editing && (
        <section className="flex flex-col gap-2.5">
          <SectionTitle title={t("jobs.wizard.confirm.createAfterTitle")} />
          <div
            role="radiogroup"
            aria-label={t("jobs.wizard.confirm.createAfterTitle")}
            className="flex flex-col divide-y rounded-lg border bg-card"
          >
            <Choice checked={draft.runNow} onSelect={() => onChangeRunNow(true)} className="px-4 py-3">
              <span className="text-sm">{t("jobs.wizard.confirm.runNow")}</span>
            </Choice>
            <Choice checked={!draft.runNow} onSelect={() => onChangeRunNow(false)} className="px-4 py-3">
              <span className="flex flex-col gap-0.5">
                <span className="text-sm">{t("jobs.wizard.confirm.runLater")}</span>
                {preview.status === "ready" && preview.data.nextRuns[0] && (
                  <span className="text-xs text-muted-foreground">
                    {t("jobs.wizard.confirm.runLaterNext", { time: formatScheduleTime(preview.data.nextRuns[0]) })}
                  </span>
                )}
              </span>
            </Choice>
          </div>
          <p className="text-xs text-muted-foreground">
            <span className="font-mono">
              {dbs.status === "loading"
                ? t("jobs.wizard.confirm.sizeLoading")
                : (sizeText() ?? t("jobs.wizard.confirm.sizeUnavailable"))}
            </span>{" "}
            <span>{t("jobs.wizard.confirm.sizeNote")}</span>
          </p>
        </section>
      )}
    </div>
  );
}

function SummarySection({ title, onEdit, children }: { title: string; onEdit: () => void; children: ReactNode }) {
  const { t } = useTranslation();
  return (
    <section className="flex flex-col gap-2 rounded-lg border bg-card px-4 py-3.5">
      <div className="flex items-center justify-between gap-3">
        <h3 className="text-sm font-semibold">{title}</h3>
        <button
          type="button"
          aria-label={t("jobs.wizard.confirm.editSection", { section: title })}
          className="text-xs text-primary hover:underline"
          onClick={onEdit}
        >
          {t("jobs.wizard.confirm.edit")}
        </button>
      </div>
      <div className="flex flex-col gap-1">{children}</div>
    </section>
  );
}
