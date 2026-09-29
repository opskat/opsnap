import { ArrowRight, X } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link, useNavigate, useParams } from "react-router";

import { CancelWizardDialog } from "@/components/jobs/CancelWizardDialog";
import { errorMessage, type Loadable } from "@/components/jobs/loadable";
import { StepConfirm } from "@/components/jobs/StepConfirm";
import { StepContent } from "@/components/jobs/StepContent";
import { StepDestination } from "@/components/jobs/StepDestination";
import { StepSchedule, type SchedulePreview } from "@/components/jobs/StepSchedule";
import { StepSource } from "@/components/jobs/StepSource";
import { WizardSteps } from "@/components/jobs/WizardSteps";
import { Button } from "@/components/ui/button";
import { ApiError } from "@/lib/api";
import { ErrorCode } from "@/lib/auth";
import { joinNames } from "@/lib/format";
import {
  createJob,
  draftOf,
  dumpToolUnavailable,
  emptyDraft,
  excludeLines,
  getJob,
  invalidExcludes,
  listJobs,
  prefixConflict,
  schedulePreview,
  selectDataSource,
  updateJob,
  validPrefix,
  WIZARD_STEPS,
  type JobDraft,
  type JobItem,
  type WizardErrors,
} from "@/lib/jobs";
import { listDataSources, type DataSourceItem } from "@/lib/sources";
import { listStorages, type Storage } from "@/lib/storage";

/** 服务端字段级错误码 → 向导字段；决定校验失败时跳回哪一步、哪个字段 */
const FIELD_OF_CODE: Partial<Record<number, keyof WizardErrors>> = {
  [ErrorCode.JobNameInvalid]: "name",
  [ErrorCode.JobNameDuplicate]: "name",
  [ErrorCode.JobDatabasesRequired]: "databases",
  [ErrorCode.JobExcludeInvalid]: "exclude",
  [ErrorCode.JobPrefixInvalid]: "prefix",
  [ErrorCode.JobPrefixConflict]: "prefix",
  [ErrorCode.JobScheduleInvalid]: "schedule",
  [ErrorCode.JobTimezoneInvalid]: "timezone",
  [ErrorCode.JobRetentionDaysInvalid]: "retentionDays",
  [ErrorCode.JobRetentionWeeksInvalid]: "retentionWeeks",
  [ErrorCode.JobRetentionMonthsInvalid]: "retentionMonths",
  [ErrorCode.JobRetriesInvalid]: "retries",
  [ErrorCode.JobRetryIntervalInvalid]: "retryInterval",
  [ErrorCode.JobTimeoutInvalid]: "timeout",
};

/** 向导字段 → 所在步骤 */
const STEP_OF_FIELD: Record<keyof WizardErrors, number> = {
  source: 0,
  method: 1,
  databases: 1,
  exclude: 1,
  storage: 2,
  prefix: 2,
  schedule: 3,
  timezone: 3,
  retentionDays: 3,
  retentionWeeks: 3,
  retentionMonths: 3,
  retries: 3,
  retryInterval: 3,
  timeout: 3,
  name: 4,
};

/** 修改某个字段所属的草稿键时，清掉该字段上一次的服务端错误 */
const ERROR_FIELDS_OF_PATCH_KEY: Partial<Record<keyof JobDraft, (keyof WizardErrors)[]>> = {
  datasourceId: ["source"],
  storageId: ["storage"],
  prefix: ["prefix"],
  databases: ["databases"],
  excludeText: ["exclude"],
  schedule: ["schedule", "timezone"],
  retention: ["retentionDays", "retentionWeeks", "retentionMonths"],
  failure: ["retries", "retryInterval", "timeout"],
  name: ["name"],
};

/** 计划、保留策略的本地取值范围校验；Cron 语法与频率能否触发交给服务端预览 */
function localScheduleErrors(
  draft: JobDraft,
  t: (key: string) => string
): Pick<
  WizardErrors,
  "schedule" | "retentionDays" | "retentionWeeks" | "retentionMonths" | "retries" | "retryInterval" | "timeout"
> {
  const e: WizardErrors = {};
  const { kind, cron, weekdays } = draft.schedule;
  if (kind === "cron" && cron.trim() === "") e.schedule = t("jobs.wizard.errors.cronRequired");
  else if (kind === "weekly" && weekdays.length === 0) e.schedule = t("jobs.wizard.errors.weekdaysRequired");
  const { days, weeks, months } = draft.retention;
  // 都是整数（后端按整数解析，小数会让整个请求失败而不是指出字段）
  const outside = (v: number, min: number, max: number) => !Number.isInteger(v) || v < min || v > max;
  if (outside(days, 1, 365)) e.retentionDays = t("jobs.wizard.errors.retentionDaysInvalid");
  if (outside(weeks, 0, 520)) e.retentionWeeks = t("jobs.wizard.errors.retentionWeeksInvalid");
  if (outside(months, 0, 120)) e.retentionMonths = t("jobs.wizard.errors.retentionMonthsInvalid");
  const { retries, retry_interval: retryInterval, timeout } = draft.failure;
  if (outside(retries, 0, 5)) e.retries = t("jobs.wizard.errors.retriesInvalid");
  if (outside(retryInterval, 1, 120)) e.retryInterval = t("jobs.wizard.errors.retryIntervalInvalid");
  if (outside(timeout, 10, 2880)) e.timeout = t("jobs.wizard.errors.timeoutInvalid");
  return e;
}

type EditState =
  { status: "new" } | { status: "loading" } | { status: "not_found" } | { status: "error"; message: string };

/** 新建 / 编辑任务向导：/jobs/new、/jobs/:id/edit */
export function JobWizardPage() {
  const { t, i18n } = useTranslation();
  const navigate = useNavigate();
  const params = useParams<{ id: string }>();
  const editId = params.id === undefined ? undefined : Number(params.id);
  const editing = editId !== undefined;

  const [step, setStep] = useState(0);
  const [draft, setDraft] = useState<JobDraft>(emptyDraft);
  const [initial, setInitial] = useState<JobDraft>(emptyDraft);
  const [editJob, setEditJob] = useState<JobItem>();
  const [edit, setEdit] = useState<EditState>(() =>
    editId === undefined
      ? { status: "new" }
      : Number.isInteger(editId) && editId > 0
        ? { status: "loading" }
        : { status: "not_found" }
  );
  /** 点过“下一步”但未通过的步骤：之后该步骤的错误随输入实时更新 */
  const [attempted, setAttempted] = useState<ReadonlySet<number>>(new Set());
  const [confirmCancel, setConfirmCancel] = useState(false);

  const [sources, setSources] = useState<Loadable<DataSourceItem[]>>({ status: "loading" });
  const [sourcesAttempt, setSourcesAttempt] = useState(0);
  const [storages, setStorages] = useState<Loadable<Storage[]>>({ status: "loading" });
  const [storagesAttempt, setStoragesAttempt] = useState(0);
  const [jobs, setJobs] = useState<JobItem[]>([]);

  /** 服务端返回的字段级错误（预览或提交）；编辑对应字段时清空 */
  const [fieldErrors, setFieldErrors] = useState<Partial<WizardErrors>>({});
  const [preview, setPreview] = useState<Loadable<SchedulePreview>>({ status: "loading" });
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string>();

  useEffect(() => {
    let cancelled = false;
    listDataSources()
      .then((r) => !cancelled && setSources({ status: "ready", data: r.items }))
      .catch((err: unknown) => !cancelled && setSources({ status: "error", message: errorMessage(err) }));
    return () => {
      cancelled = true;
    };
  }, [sourcesAttempt]);

  useEffect(() => {
    let cancelled = false;
    listStorages()
      .then((r) => !cancelled && setStorages({ status: "ready", data: r.items }))
      .catch((err: unknown) => !cancelled && setStorages({ status: "error", message: errorMessage(err) }));
    // 其他任务的前缀仅用于在这一步提前发现冲突；读取失败时由创建接口兜底校验
    listJobs()
      .then((r) => !cancelled && setJobs(r.items))
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [storagesAttempt]);

  useEffect(() => {
    if (edit.status !== "loading" || editId === undefined) return;
    let cancelled = false;
    getJob(editId)
      .then((r) => {
        if (cancelled) return;
        const d = draftOf(r.item);
        setEditJob(r.item);
        setDraft(d);
        setInitial(d);
        setEdit({ status: "new" });
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        if (err instanceof ApiError && err.status === 404) setEdit({ status: "not_found" });
        else setEdit({ status: "error", message: errorMessage(err) });
      });
    return () => {
      cancelled = true;
    };
  }, [edit.status, editId]);

  // 第 4、5 步都需要预览；用布尔值而不是 step 本身做依赖，这样在两步之间来回切换
  // 不会被当成“变化”，不会取消正在进行的请求或重新发起一次
  const reachedSchedule = step >= 3;

  // 第 4 步：本地取值范围合法时，防抖请求服务端预览（接下来三次执行时间与最多保留份数）
  useEffect(() => {
    if (!reachedSchedule) return;
    if (Object.keys(localScheduleErrors(draft, t)).length > 0) return;
    let cancelled = false;
    const handle = window.setTimeout(() => {
      schedulePreview({ schedule: draft.schedule, retention: draft.retention })
        .then((r) => {
          if (cancelled) return;
          setPreview({ status: "ready", data: { nextRuns: r.next_runs, maxSnapshots: r.max_snapshots } });
        })
        .catch((err: unknown) => {
          if (cancelled) return;
          // 能归到字段的原因显示在字段旁；其余原因显示在“接下来三次”处
          const field = err instanceof ApiError ? FIELD_OF_CODE[err.code] : undefined;
          setPreview({ status: "error", message: field ? "" : errorMessage(err) });
          if (field) setFieldErrors((f) => ({ ...f, [field]: errorMessage(err) }));
        });
    }, 300);
    return () => {
      cancelled = true;
      window.clearTimeout(handle);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps -- t 每次渲染都变，只按实际用到的字段防抖
  }, [reachedSchedule, draft.schedule, draft.retention]);

  // 计划在本地就不合法时不显示上一次计划的预览（字段旁已有原因）
  const shownPreview: Loadable<SchedulePreview> =
    Object.keys(localScheduleErrors(draft, t)).length > 0 ? { status: "error", message: "" } : preview;

  const sourceList = sources.status === "ready" ? sources.data : [];
  const source = sourceList.find((d) => d.id === draft.datasourceId);
  const storageList = storages.status === "ready" ? storages.data : [];

  const update = (patch: Partial<JobDraft>) => {
    setDraft((d) => ({ ...d, ...patch }));
    const fields = (Object.keys(patch) as (keyof JobDraft)[]).flatMap((k) => ERROR_FIELDS_OF_PATCH_KEY[k] ?? []);
    if (fields.length > 0) {
      setFieldErrors((f) => {
        if (fields.every((k) => f[k] === undefined)) return f;
        const next = { ...f };
        for (const k of fields) delete next[k];
        return next;
      });
    }
  };
  const onSourceChange = useCallback(
    (item: DataSourceItem) =>
      setSources((s) => (s.status === "ready" ? { ...s, data: s.data.map((d) => (d.id === item.id ? item : d)) } : s)),
    []
  );

  const stepErrors = (i: number): WizardErrors => {
    const e: WizardErrors = {};
    if (i === 0) {
      if (!source) e.source = t("jobs.wizard.errors.sourceRequired");
      // 编辑时数据源不能修改，也不重新检查它的状态（与后端一致）
      else if (!editing && source.status !== "ok") {
        e.source = t("jobs.wizard.errors.sourceNotReady", {
          reason: source.status_message || t(`sources.dataSource.status.${source.status}`),
        });
      }
    }
    if (i === 1 && source) {
      if (dumpToolUnavailable(source)) e.method = t("jobs.wizard.errors.toolUnavailable");
      if (draft.scope === "databases" && draft.databases.length === 0) {
        e.databases = t("jobs.wizard.errors.databasesRequired");
      }
      const bad = invalidExcludes(source.kind, excludeLines(draft.excludeText));
      if (bad.length > 0) {
        e.exclude = t(
          source.kind === "postgres" ? "jobs.wizard.errors.excludeInvalidPg" : "jobs.wizard.errors.excludeInvalidMysql",
          { lines: joinNames(bad, i18n.language) }
        );
      }
    }
    if (i === 2) {
      const storage = storageList.find((s) => s.id === draft.storageId);
      if (!storage) e.storage = t("jobs.wizard.errors.storageRequired");
      // 编辑时存储不能修改，也不重新检查它的状态
      else if (!editing && storage.status !== "ok") e.storage = t("jobs.wizard.errors.storageNotReady");
      if (!validPrefix(draft.prefix)) e.prefix = t("jobs.wizard.errors.prefixInvalid");
      else {
        const other = prefixConflict(jobs, draft.storageId, draft.prefix, editJob?.id);
        if (other) e.prefix = t("jobs.wizard.errors.prefixConflict", { name: other.name });
      }
    }
    if (i === 3) Object.assign(e, localScheduleErrors(draft, t));
    if (i === 4) {
      const len = draft.name.trim().length;
      if (len === 0 || len > 64) e.name = t("jobs.wizard.errors.nameInvalid");
    }
    // 服务端返回的字段错误（预览或上一次提交失败）：本地未发现问题时补上
    for (const k of Object.keys(fieldErrors) as (keyof WizardErrors)[]) {
      if (STEP_OF_FIELD[k] === i && e[k] === undefined) e[k] = fieldErrors[k];
    }
    return e;
  };

  // 第 4、5 步的错误来自实时校验（含防抖预览与上一次提交），不需要先点过“下一步”
  const errors = attempted.has(step) || step >= 3 ? stepErrors(step) : {};

  const goNext = () => {
    if (Object.keys(stepErrors(step)).length > 0) {
      setAttempted((s) => new Set(s).add(step));
      return;
    }
    setStep((s) => Math.min(s + 1, WIZARD_STEPS.length - 1));
  };

  const leave = () => navigate("/jobs");
  const cancel = () => {
    if (JSON.stringify(draft) !== JSON.stringify(initial)) setConfirmCancel(true);
    else leave();
  };

  const submit = async () => {
    if (submitting) return;
    setSubmitting(true);
    setSubmitError(undefined);
    try {
      if (editing && editId !== undefined) {
        const res = await updateJob(editId, draft);
        navigate(`/jobs/${res.item.id}`);
      } else {
        const res = await createJob(draft);
        navigate(`/jobs/${res.item.id}`);
      }
    } catch (err) {
      // 创建失败时停留在第 5 步并显示原因；能归到字段的，回到对应步骤时字段旁也有提示
      const field = err instanceof ApiError ? FIELD_OF_CODE[err.code] : undefined;
      const message = errorMessage(err);
      if (field) {
        setFieldErrors((f) => ({ ...f, [field]: message }));
        setAttempted((s) => new Set(s).add(STEP_OF_FIELD[field]));
      }
      // 第 5 步自己的字段（名称）已在字段旁显示，不在顶部重复
      if (!field || STEP_OF_FIELD[field] !== WIZARD_STEPS.length - 1) setSubmitError(message);
    } finally {
      setSubmitting(false);
    }
  };

  const title = t(editing ? "jobs.wizard.editTitle" : "jobs.wizard.title");

  if (edit.status !== "new") {
    return (
      <section className="flex flex-col items-center gap-3 px-8 py-16 text-center">
        {edit.status === "loading" && <p className="text-sm text-muted-foreground">{t("common.loading")}</p>}
        {edit.status === "not_found" && <p className="text-lg font-medium">{t("jobs.wizard.notFound")}</p>}
        {edit.status === "error" && (
          <p className="text-sm text-destructive">{t("jobs.wizard.loadFailed", { message: edit.message })}</p>
        )}
        {edit.status !== "loading" && (
          <Button asChild variant="outline">
            <Link to="/jobs">{t("jobs.wizard.backToList")}</Link>
          </Button>
        )}
      </section>
    );
  }

  const stepKey = WIZARD_STEPS[step];

  return (
    <div className="flex min-h-full flex-col">
      <header className="flex items-start justify-between gap-4 border-b px-8 py-5">
        <div className="flex flex-col gap-1">
          <h1 className="text-xl font-bold">{title}</h1>
          <p className="text-sm text-muted-foreground">
            {t("jobs.wizard.progress", { step: step + 1, total: WIZARD_STEPS.length })} ·{" "}
            {t(`jobs.wizard.subtitles.${stepKey}`)}
          </p>
        </div>
        <Button variant="outline" size="icon-sm" aria-label={t("jobs.wizard.close")} onClick={cancel}>
          <X />
        </Button>
      </header>

      <div className="mx-auto flex w-full max-w-220 flex-1 flex-col gap-6 px-8 py-5">
        <WizardSteps current={step} onBack={setStep} />
        {step === 0 && (
          <StepSource
            draft={draft}
            sources={sources}
            locked={editing}
            error={errors.source}
            onSelect={(ds) =>
              setDraft((d) => {
                const next = selectDataSource(d, ds);
                if (next === d || next.nameEdited) return next;
                return { ...next, name: t("jobs.wizard.confirm.defaultName", { name: ds.name }) };
              })
            }
            onRetry={() => {
              setSources({ status: "loading" });
              setSourcesAttempt((n) => n + 1);
            }}
          />
        )}
        {step === 1 && source && (
          <StepContent
            draft={draft}
            source={source}
            errors={errors}
            onChange={update}
            onSourceChange={onSourceChange}
          />
        )}
        {step === 2 && (
          <StepDestination
            draft={draft}
            storages={storages}
            locked={editing}
            errors={errors}
            onChange={update}
            onRetry={() => {
              setStorages({ status: "loading" });
              setStoragesAttempt((n) => n + 1);
            }}
          />
        )}
        {step === 3 && <StepSchedule draft={draft} errors={errors} preview={shownPreview} onChange={update} />}
        {step === 4 && (
          <StepConfirm
            draft={draft}
            editing={editing}
            source={source}
            storage={storageList.find((s) => s.id === draft.storageId)}
            preview={shownPreview}
            errors={errors}
            submitError={submitError}
            onChangeName={(name) => update({ name, nameEdited: true })}
            onChangeRunNow={(runNow) => update({ runNow })}
            onGoStep={setStep}
          />
        )}
      </div>

      <footer className="sticky bottom-0 flex items-center justify-between gap-3 border-t bg-background px-8 py-4">
        <Button variant="ghost" disabled={submitting} onClick={cancel}>
          {t("common.cancel")}
        </Button>
        <div className="flex items-center gap-2">
          <Button
            variant="outline"
            disabled={step === 0 || submitting}
            onClick={() => setStep((s) => Math.max(s - 1, 0))}
          >
            {t("jobs.wizard.back")}
          </Button>
          {step === WIZARD_STEPS.length - 1 ? (
            <Button disabled={submitting} onClick={() => void submit()}>
              {submitting ? t("common.submitting") : t(editing ? "common.save" : "jobs.wizard.confirm.create")}
            </Button>
          ) : (
            <Button onClick={goNext}>
              {t("jobs.wizard.next")}
              <ArrowRight />
            </Button>
          )}
        </div>
      </footer>

      <CancelWizardDialog
        open={confirmCancel}
        editing={editing}
        onKeep={() => setConfirmCancel(false)}
        onDiscard={leave}
      />
    </div>
  );
}
