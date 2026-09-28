import { ArrowRight, X } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link, useNavigate, useParams } from "react-router";

import { CancelWizardDialog } from "@/components/jobs/CancelWizardDialog";
import { errorMessage, type Loadable } from "@/components/jobs/loadable";
import { StepContent } from "@/components/jobs/StepContent";
import { StepDestination } from "@/components/jobs/StepDestination";
import { StepSource } from "@/components/jobs/StepSource";
import { WizardSteps } from "@/components/jobs/WizardSteps";
import { Button } from "@/components/ui/button";
import { ApiError } from "@/lib/api";
import {
  draftOf,
  dumpToolUnavailable,
  emptyDraft,
  excludeLines,
  getJob,
  invalidExcludes,
  listJobs,
  prefixConflict,
  selectDataSource,
  validPrefix,
  WIZARD_STEPS,
  type JobDraft,
  type JobItem,
  type WizardErrors,
} from "@/lib/jobs";
import { listDataSources, type DataSourceItem } from "@/lib/sources";
import { listStorages, type Storage } from "@/lib/storage";

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

  const sourceList = sources.status === "ready" ? sources.data : [];
  const source = sourceList.find((d) => d.id === draft.datasourceId);
  const storageList = storages.status === "ready" ? storages.data : [];

  const update = (patch: Partial<JobDraft>) => setDraft((d) => ({ ...d, ...patch }));
  const onSourceChange = useCallback(
    (item: DataSourceItem) =>
      setSources((s) => (s.status === "ready" ? { ...s, data: s.data.map((d) => (d.id === item.id ? item : d)) } : s)),
    []
  );

  const stepErrors = (i: number): WizardErrors => {
    const e: WizardErrors = {};
    if (i === 0) {
      if (!source) e.source = t("jobs.wizard.errors.sourceRequired");
      else if (source.status !== "ok") {
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
        const sep = i18n.language.toLowerCase().startsWith("zh") ? "、" : ", ";
        e.exclude = t(
          source.kind === "postgres" ? "jobs.wizard.errors.excludeInvalidPg" : "jobs.wizard.errors.excludeInvalidMysql",
          { lines: bad.join(sep) }
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
    return e;
  };

  const errors = attempted.has(step) ? stepErrors(step) : {};

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
            onSelect={(ds) => setDraft((d) => selectDataSource(d, ds))}
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
        {step > 2 && <p className="text-sm text-muted-foreground">{t("common.comingSoon")}</p>}
      </div>

      <footer className="sticky bottom-0 flex items-center justify-between gap-3 border-t bg-background px-8 py-4">
        <Button variant="ghost" onClick={cancel}>
          {t("common.cancel")}
        </Button>
        <div className="flex items-center gap-2">
          <Button variant="outline" disabled={step === 0} onClick={() => setStep((s) => Math.max(s - 1, 0))}>
            {t("jobs.wizard.back")}
          </Button>
          <Button disabled={step === WIZARD_STEPS.length - 1} onClick={goNext}>
            {t("jobs.wizard.next")}
            <ArrowRight />
          </Button>
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
