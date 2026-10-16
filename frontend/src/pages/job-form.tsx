import * as React from "react";
import { ChevronLeft } from "lucide-react";
import { Link, Navigate, Route, Routes, useNavigate, useParams } from "react-router";
import { toast } from "sonner";
import { FormError, PageTitle, Skeleton } from "@/components/bits";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { api, errorMessage, type JobItem, type RepoItem, type ServerItem } from "@/lib/api";
import {
  KNOWN_FIELDS,
  buildInput,
  emptyJobForm,
  errorKeysOf,
  fieldErrorsOf,
  formFromItem,
  formSignature,
  scheduleIntervalMs,
  type FieldErrors,
  type JobForm,
} from "@/lib/jobs";
import { estimateSnapshots, wallClock } from "@/lib/retention";
import { useLoad } from "@/lib/use-load";
import { useNextRuns } from "@/lib/use-next-runs";
import { AdvancedCard, ConnectionCard, RepoCard, ScheduleCard, SourceCard } from "./job-sections";
import { JobSummary, type TestState } from "./job-summary";
import { JobsPage } from "./jobs";
import { LoadError } from "./settings";

/** /jobs/* 下由本任务负责的路由；任务列表（/jobs）由 F5 实现，这里先保持占位 */
export function JobsRoutes() {
  return (
    <Routes>
      <Route path="new" element={<JobFormPage />} />
      <Route path=":id/edit" element={<JobFormPage />} />
      <Route index element={<JobsPage />} />
      <Route path="*" element={<Navigate to="/jobs" replace />} />
    </Routes>
  );
}

interface Loaded {
  servers: ServerItem[];
  repos: RepoItem[];
  job: JobItem | null;
}

/** 新建（/jobs/new）和编辑（/jobs/:id/edit）备份任务 */
export function JobFormPage() {
  const params = useParams();
  const jobId = params.id ? Number(params.id) : 0;
  const load = React.useCallback(async (): Promise<Loaded> => {
    const [servers, repos, job] = await Promise.all([
      api.servers.list(),
      api.repos.list(),
      jobId ? api.jobs.get(jobId) : Promise.resolve(null),
    ]);
    return { servers: servers.list ?? [], repos: repos.list ?? [], job };
  }, [jobId]);
  const { data, error, reload } = useLoad(load);

  if (error) return <LoadError what="任务配置" message={error} onRetry={reload} />;
  if (!data) {
    return (
      <div className="grid gap-4" aria-busy>
        <Skeleton className="h-8 w-48" />
        <Skeleton className="h-40" />
        <Skeleton className="h-64" />
      </div>
    );
  }
  return <JobFormBody key={jobId} jobId={jobId} data={data} />;
}

function JobFormBody({ jobId, data }: { jobId: number; data: Loaded }) {
  const navigate = useNavigate();
  const { servers, repos, job } = data;
  const [form, setForm] = React.useState<JobForm>(() =>
    job ? formFromItem(job) : emptyJobForm(repos.find((r) => r.status !== "not_mounted")?.id ?? 0)
  );
  const [errs, setErrs] = React.useState<FieldErrors>({});
  const [formError, setFormError] = React.useState<string | null>(null);
  const [test, setTest] = React.useState<TestState>({ status: "idle" });
  const [saving, setSaving] = React.useState(false);
  /** 新建时第一次保存之后拿到的 id；之后的保存是修改 */
  const [savedId, setSavedId] = React.useState(0);
  const [confirm, setConfirm] = React.useState<null | { run: boolean }>(null);
  const [now] = React.useState(() => new Date());

  const id = jobId || savedId;
  const sig = formSignature(form);
  const passed = test.status === "done" && test.sig === sig && test.result.ok;
  const stale = test.status === "done" && test.sig !== sig;

  const next = useNextRuns(form.schedule);
  const interval = scheduleIntervalMs(form.schedule, next.times ?? []);
  const estimate = estimateSnapshots(form.retention, interval, wallClock(now));

  /** 改动表单：同时清掉这些字段已有的错误 */
  function set(patch: Partial<JobForm>) {
    setForm((f) => ({ ...f, ...patch }));
    const keys = Object.keys(patch).flatMap((k) => errorKeysOf(k as keyof JobForm));
    if (keys.length === 0) return;
    setErrs((e) => {
      const rest = Object.fromEntries(
        Object.entries(e).filter(([k]) => !keys.some((c) => k === c || k.startsWith(`${c}.`)))
      );
      return Object.keys(rest).length === Object.keys(e).length ? e : rest;
    });
  }

  /** 把 11401 的逐字段错误放到字段下；返回是否处理了 */
  function showFieldErrors(err: unknown): boolean {
    const fields = fieldErrorsOf(err);
    if (!fields) return false;
    setErrs(fields);
    const loose = Object.entries(fields).filter(([k]) => !(KNOWN_FIELDS as readonly string[]).includes(k));
    setFormError(
      loose.length > 0
        ? loose.map(([k, v]) => `${k}：${v.join("；")}`).join("\n")
        : `有 ${Object.keys(fields).length} 处需要修改，已标在对应字段下`
    );
    return true;
  }

  async function runTest() {
    const { input, errors } = buildInput(form);
    setErrs(errors);
    setFormError(null);
    setTest({ status: "running" });
    try {
      const result = await api.jobs.test(id, input);
      setTest({ status: "done", sig, result });
    } catch (err) {
      setTest({ status: "idle" });
      if (!showFieldErrors(err)) setTest({ status: "error", message: errorMessage(err) });
    }
  }

  async function save(run: boolean) {
    const { input, errors } = buildInput(form);
    if (Object.keys(errors).length > 0) {
      setErrs(errors);
      setFormError(`有 ${Object.keys(errors).length} 处需要修改，已标在对应字段下`);
      return;
    }
    setSaving(true);
    setFormError(null);
    try {
      const item = id ? await api.jobs.update(id, input) : await api.jobs.create(input);
      setSavedId(item.id);
      setErrs({});
      if (run) {
        const { run_id } = await api.jobs.run(item.id);
        navigate(`/runs/${run_id}`);
      } else {
        toast.success("任务已保存");
        navigate("/jobs");
      }
    } catch (err) {
      if (!showFieldErrors(err)) setFormError(errorMessage(err));
    } finally {
      setSaving(false);
    }
  }

  /** 点保存：连接测试没通过（或没测过）时先确认一次 */
  function submit(run: boolean) {
    const { errors } = buildInput(form);
    if (Object.keys(errors).length > 0 || passed) {
      void save(run);
      return;
    }
    setConfirm({ run });
  }

  const repo = repos.find((r) => r.id === form.repo_id);
  const server = servers.find((s) => s.id === form.server_id);
  const failedTest = test.status === "done" && !stale && !test.result.ok;

  return (
    <>
      <PageTitle
        back={
          <Link to="/jobs" className="mb-4 inline-flex items-center gap-1 text-[13px] text-ink-2 hover:text-ink">
            <ChevronLeft className="size-4" aria-hidden />
            备份任务
          </Link>
        }
        title={job ? "编辑备份任务" : "新建备份任务"}
      />

      <div className="grid gap-8 xl:grid-cols-[1fr_340px]">
        <form
          className="flex min-w-0 flex-col gap-4"
          noValidate
          onSubmit={(e) => {
            e.preventDefault();
            submit(false);
          }}
        >
          <FormError className="whitespace-pre-line">{formError}</FormError>
          <SourceCard form={form} set={set} errs={errs} />
          <ConnectionCard form={form} set={set} errs={errs} servers={servers} jobId={id} />
          <RepoCard form={form} set={set} errs={errs} repos={repos} />
          <ScheduleCard form={form} set={set} errs={errs} next={next} estimate={estimate} now={now} />
          <AdvancedCard form={form} set={set} errs={errs} />
        </form>

        <JobSummary
          form={form}
          serverName={server?.name ?? ""}
          repoName={repo?.name ?? ""}
          estimate={estimate}
          test={test}
          stale={stale}
          busy={saving}
          onTest={() => void runTest()}
          onSave={submit}
          onCancel={() => navigate("/jobs")}
        />
      </div>

      <ConfirmDialog
        open={confirm !== null}
        title={failedTest ? "连接测试没有通过" : "还没有测试连接"}
        description={
          failedTest
            ? "仍然保存的话，运行时可能连不上或导出失败。"
            : "没有测试过当前的配置，保存后到运行时才会发现连不上。"
        }
        confirmLabel={confirm?.run ? "仍然保存并运行" : "仍然保存"}
        destructive={false}
        onConfirm={async () => {
          const run = confirm?.run ?? false;
          setConfirm(null);
          await save(run);
        }}
        onClose={() => setConfirm(null)}
      />
    </>
  );
}
