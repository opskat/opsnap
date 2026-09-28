import { useId, type ChangeEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { Segmented } from "@/components/form/Segmented";
import { FormField } from "@/components/form/FormField";
import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import { formatScheduleTime, timezoneList, type JobDraft, type ScheduleKind, type WizardErrors } from "@/lib/jobs";
import { cn } from "@/lib/utils";

import { FieldError, SectionTitle } from "./Choice";
import type { Loadable } from "./loadable";

const WEEKDAYS = [0, 1, 2, 3, 4, 5, 6];
const pad = (n: number) => String(n).padStart(2, "0");

export interface SchedulePreview {
  nextRuns: string[];
  maxSnapshots: number;
}

/** 第 4 步：计划与保留（频率、时区、接下来三次、保留策略、失败处理） */
export function StepSchedule({
  draft,
  errors,
  preview,
  onChange,
}: {
  draft: JobDraft;
  errors: Pick<
    WizardErrors,
    | "schedule"
    | "timezone"
    | "retentionDays"
    | "retentionWeeks"
    | "retentionMonths"
    | "retries"
    | "retryInterval"
    | "timeout"
  >;
  preview: Loadable<SchedulePreview>;
  onChange: (patch: Partial<JobDraft>) => void;
}) {
  const { t } = useTranslation();
  const schedule = draft.schedule;

  const setSchedule = (patch: Partial<JobDraft["schedule"]>) => onChange({ schedule: { ...schedule, ...patch } });
  const setRetention = (patch: Partial<JobDraft["retention"]>) =>
    onChange({ retention: { ...draft.retention, ...patch } });
  const setFailure = (patch: Partial<JobDraft["failure"]>) => onChange({ failure: { ...draft.failure, ...patch } });

  const time = `${pad(schedule.hour)}:${pad(schedule.minute)}`;
  const onTimeChange = (e: ChangeEvent<HTMLInputElement>) => {
    const [h, m] = e.target.value.split(":").map(Number);
    if (Number.isInteger(h) && Number.isInteger(m)) setSchedule({ hour: h, minute: m });
  };

  const toggleWeekday = (d: number, on: boolean) =>
    setSchedule({
      weekdays: on ? [...schedule.weekdays, d].sort((a, b) => a - b) : schedule.weekdays.filter((w) => w !== d),
    });

  // 空串或无法解析（例如只输入了一个负号）时按 0 处理，交给取值范围校验提示，避免把 NaN 存进草稿
  const num = (v: string) => {
    const n = Number(v);
    return v === "" || Number.isNaN(n) ? 0 : n;
  };

  return (
    <div className="flex flex-col gap-6">
      <section className="flex flex-col gap-2.5">
        <SectionTitle title={t("jobs.wizard.schedule.frequency")} />
        <div className="flex flex-col gap-3 rounded-lg border bg-card px-4 py-3.5">
          <Segmented<ScheduleKind>
            label={t("jobs.wizard.schedule.frequency")}
            value={schedule.kind}
            onChange={(kind) => setSchedule({ kind })}
            options={[
              { value: "hourly", label: t("jobs.wizard.schedule.hourly") },
              { value: "daily", label: t("jobs.wizard.schedule.daily") },
              { value: "weekly", label: t("jobs.wizard.schedule.weekly") },
              { value: "cron", label: t("jobs.wizard.schedule.cron") },
            ]}
          />

          {schedule.kind === "hourly" && (
            <SelectField
              label={t("jobs.wizard.schedule.minuteOfHour")}
              value={String(schedule.minute)}
              mono
              onChange={(e) => setSchedule({ minute: num(e.target.value) })}
            >
              {Array.from({ length: 60 }, (_, i) => (
                <option key={i} value={i}>
                  {pad(i)}
                </option>
              ))}
            </SelectField>
          )}

          {(schedule.kind === "daily" || schedule.kind === "weekly") && (
            <FormField
              type="time"
              label={t("jobs.wizard.schedule.timeOfDay")}
              mono
              className="w-32"
              value={time}
              onChange={onTimeChange}
            />
          )}

          {schedule.kind === "weekly" && (
            <div className="flex flex-col gap-1.5">
              <Label className="font-normal text-muted-foreground">{t("jobs.wizard.schedule.weekdaysLabel")}</Label>
              <div className="flex flex-wrap gap-4">
                {WEEKDAYS.map((d) => (
                  <label key={d} className="flex cursor-pointer items-center gap-1.5 text-sm">
                    <Checkbox
                      aria-label={t(`jobs.wizard.schedule.weekday.${d}`)}
                      checked={schedule.weekdays.includes(d)}
                      onCheckedChange={(v) => toggleWeekday(d, v === true)}
                    />
                    {t(`jobs.wizard.schedule.weekday.${d}`)}
                  </label>
                ))}
              </div>
              <FieldError>{errors.schedule}</FieldError>
            </div>
          )}

          {schedule.kind === "cron" && (
            <FormField
              label={t("jobs.wizard.schedule.cronLabel")}
              mono
              placeholder="*/30 * * * *"
              value={schedule.cron}
              error={errors.schedule}
              hint={errors.schedule ? undefined : t("jobs.wizard.schedule.cronHint")}
              onChange={(e) => setSchedule({ cron: e.target.value })}
            />
          )}

          <SelectField
            label={t("jobs.wizard.schedule.timezone")}
            value={schedule.timezone}
            error={errors.timezone}
            onChange={(e) => setSchedule({ timezone: e.target.value })}
          >
            {timezoneList().map((tz) => (
              <option key={tz} value={tz}>
                {tz}
              </option>
            ))}
          </SelectField>

          <div className="flex flex-col gap-1 border-t pt-3">
            <span className="text-sm font-medium">{t("jobs.wizard.schedule.nextRuns")}</span>
            {preview.status === "loading" && (
              <span className="text-xs text-muted-foreground">{t("jobs.wizard.schedule.nextRunsLoading")}</span>
            )}
            {preview.status === "ready" && (
              <ul className="flex flex-col gap-0.5 font-mono text-xs text-muted-foreground">
                {preview.data.nextRuns.map((iso) => (
                  <li key={iso}>{formatScheduleTime(iso)}</li>
                ))}
              </ul>
            )}
          </div>
        </div>
      </section>

      <section className="flex flex-col gap-2.5">
        <SectionTitle title={t("jobs.wizard.schedule.retentionTitle")} />
        <div className="flex flex-col gap-3 rounded-lg border bg-card px-4 py-3.5">
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
            <FormField
              type="number"
              min={1}
              max={365}
              mono
              label={t("jobs.wizard.schedule.retentionDays")}
              value={draft.retention.days}
              error={errors.retentionDays}
              onChange={(e) => setRetention({ days: num(e.target.value) })}
            />
            <FormField
              type="number"
              min={0}
              max={520}
              mono
              label={t("jobs.wizard.schedule.retentionWeeks")}
              value={draft.retention.weeks}
              error={errors.retentionWeeks}
              onChange={(e) => setRetention({ weeks: num(e.target.value) })}
            />
            <FormField
              type="number"
              min={0}
              max={120}
              mono
              label={t("jobs.wizard.schedule.retentionMonths")}
              value={draft.retention.months}
              error={errors.retentionMonths}
              onChange={(e) => setRetention({ months: num(e.target.value) })}
            />
          </div>
          <p className="text-xs text-muted-foreground">
            {preview.status === "ready"
              ? t("jobs.wizard.schedule.maxSnapshots", { count: preview.data.maxSnapshots })
              : t("jobs.wizard.schedule.nextRunsLoading")}
          </p>
          <p className="text-xs text-muted-foreground">{t("jobs.wizard.schedule.alwaysKeepLatest")}</p>
        </div>
      </section>

      <section className="flex flex-col gap-2.5">
        <SectionTitle title={t("jobs.wizard.schedule.failureTitle")} />
        <div className="grid grid-cols-1 gap-3 rounded-lg border bg-card px-4 py-3.5 sm:grid-cols-3">
          <FormField
            type="number"
            min={0}
            max={5}
            mono
            label={t("jobs.wizard.schedule.retries")}
            value={draft.failure.retries}
            error={errors.retries}
            onChange={(e) => setFailure({ retries: num(e.target.value) })}
          />
          <FormField
            type="number"
            min={1}
            max={120}
            mono
            label={t("jobs.wizard.schedule.retryInterval")}
            value={draft.failure.retry_interval}
            error={errors.retryInterval}
            onChange={(e) => setFailure({ retry_interval: num(e.target.value) })}
          />
          <FormField
            type="number"
            min={10}
            max={2880}
            mono
            label={t("jobs.wizard.schedule.timeout")}
            value={draft.failure.timeout}
            error={errors.timeout}
            onChange={(e) => setFailure({ timeout: num(e.target.value) })}
          />
        </div>
      </section>
    </div>
  );
}

function SelectField({
  label,
  value,
  error,
  mono,
  onChange,
  children,
}: {
  label: string;
  value: string;
  error?: string;
  mono?: boolean;
  onChange: (e: ChangeEvent<HTMLSelectElement>) => void;
  children: ReactNode;
}) {
  const id = useId();
  return (
    <div className="flex flex-col gap-1.5">
      <Label htmlFor={id} className="font-normal text-muted-foreground">
        {label}
      </Label>
      <select
        id={id}
        value={value}
        aria-invalid={error ? true : undefined}
        onChange={onChange}
        className={cn(
          "h-9 w-full rounded-md border border-input bg-background px-3 text-sm shadow-xs outline-none",
          "focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 aria-invalid:border-destructive",
          mono && "font-mono"
        )}
      >
        {children}
      </select>
      <FieldError>{error}</FieldError>
    </div>
  );
}
