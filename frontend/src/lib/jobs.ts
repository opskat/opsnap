import { request } from "@/lib/api";
import type { DataSourceItem, DataSourceKind, ProbeItem } from "@/lib/sources";

// ============ 接口类型（与 internal/api/job 一致） ============

export type JobType = "backup" | "sync";
/** instance：整个实例；databases：指定数据库 */
export type JobScope = "instance" | "databases";
export type JobMethod = "full";
export type Compression = "none" | "gzip" | "zstd";
export type ScheduleKind = "hourly" | "daily" | "weekly" | "cron";

/** 一并备份的内容：MySQL 看 routines/triggers/events/users，PostgreSQL 看 globals */
export interface JobOptions {
  routines: boolean;
  triggers: boolean;
  events: boolean;
  users: boolean;
  globals: boolean;
}

export interface JobSchedule {
  kind: ScheduleKind;
  minute: number;
  hour: number;
  /** 0 为周日 */
  weekdays: number[];
  cron: string;
  timezone: string;
}

export interface JobRetention {
  days: number;
  weeks: number;
  months: number;
}

export interface JobFailure {
  retries: number;
  /** 分钟 */
  retry_interval: number;
  /** 分钟 */
  timeout: number;
}

export interface JobItem {
  id: number;
  name: string;
  type: JobType;
  datasource_id: number;
  datasource_name: string;
  datasource_kind: DataSourceKind;
  storage_id: number;
  storage_name: string;
  prefix: string;
  /** 仓库内位置：<存储名>:/<路径前缀> */
  location: string;
  scope: JobScope;
  databases: string[];
  method: JobMethod;
  options: JobOptions;
  exclude_tables: string[];
  compression: Compression;
  schedule: JobSchedule;
  retention: JobRetention;
  failure: JobFailure;
  /** false 表示已暂停 */
  enabled: boolean;
  /** Unix 秒，暂停时为 0 */
  next_run_at: number;
  created_at: number;
  updated_at: number;
  /** 最近一次运行（按触发顺序），没有时为 null；后端总是返回，设为可选避免影响既有测试夹具 */
  last_run?: Run | null;
  /** 最近一次读取仓库得到的本任务快照数；后端总是返回，设为可选避免影响既有测试夹具 */
  snapshot_count?: number;
}

/** 数据源实时读取的一个数据库；size 为字节 */
export interface DatabaseInfo {
  name: string;
  size: number;
}

export type RunStatus = "queued" | "running" | "success" | "failed" | "canceled" | "skipped";
export type RunTrigger = "schedule" | "manual" | "catchup" | "retry";
export type FailedStep = "" | "prepare" | "connect" | "export" | "verify" | "retention";

/** 一次运行（docs/specs/2026-09-27-backup-jobs.md「运行记录」） */
export interface Run {
  id: number;
  job_id: number;
  status: RunStatus;
  trigger: RunTrigger;
  retry_attempt: number;
  retry_total: number;
  /** 计划与补跑对应的计划时间（秒），其余为 0 */
  scheduled_at: number;
  created_at: number;
  /** 尚未开始或结束时为 0 */
  started_at: number;
  finished_at: number;
  /** 耗时（毫秒）；运行中为已运行的时间 */
  duration_ms: number;
  /** 导出工具输出的字节数；运行中为实时的已导出量 */
  exported_bytes: number;
  /** 去重、压缩后新增写入仓库的字节数（仅成功） */
  uploaded_bytes: number;
  /** 仅成功 */
  snapshot_id: string;
  /** 仅失败 */
  failed_step: FailedStep;
  /** 失败原因（仅失败）；跳过与被 OpsNap 重启取消时为原因 */
  reason: string;
}

/** 已在运行或排队中：立即执行不可用，列表与详情页需要自动刷新 */
export function isRunActive(run?: Run | null): boolean {
  return run?.status === "running" || run?.status === "queued";
}

export interface DeleteJobResult {
  snapshots_deleted: number;
  snapshots_failed: number;
  snapshots_message: string;
}

/** 一条执行日志（task 13 任务详情页展开的运行记录）；Omitted 大于 0 表示这一行是省略标记 */
export interface RunLogLine {
  /** 毫秒 */
  time: number;
  step: FailedStep;
  message: string;
  omitted?: number;
}

/** 最近 30 次已结束的运行（不含等待中、运行中）中成功与失败的次数 */
export interface RunRecent {
  runs: number;
  success: number;
  failed: number;
  /** 成功数 /（成功数 + 失败数），0–1；两者都为 0 时为 0 */
  success_rate: number;
}

/** 任务详情页的统计（docs/specs/2026-09-27-backup-jobs.md「任务详情」） */
export interface JobStats {
  snapshot_count: number;
  /** 没有快照时为 0 */
  earliest_snapshot_at: number;
  /** 本任务现存快照引用的数据块去重、压缩后在仓库中的大小 */
  packed_bytes: number;
  /** 现存快照的导出总量 */
  export_bytes: number;
  /** 节省比例 1 - packed_bytes/export_bytes（0–1） */
  savings: number;
  /** 无法读取仓库时的提示（按请求语言），此时以上快照字段为 0 */
  storage_error: string;
  last_success: Run | null;
  recent: RunRecent;
}

/** 每页条数（docs/specs/2026-09-27-backup-jobs.md「任务详情」「运行记录」） */
export const RUNS_PAGE_SIZE = 20;

/** 运行记录，按开始时间倒序 */
export function listRuns(jobId: number, page = 1) {
  return request<{ items: Run[]; total: number }>(`/jobs/${jobId}/runs?page=${page}`);
}

/** 一次运行的执行日志：每行带时间和步骤名，最多 1000 行，超出时保留首尾并注明省略行数 */
export function getRunLog(jobId: number, runId: number) {
  return request<{ lines: RunLogLine[] }>(`/jobs/${jobId}/runs/${runId}/log`);
}

/** 任务详情的统计：快照数、仓库占用、最近一次成功、最近 30 次成功率 */
export function getJobStats(jobId: number) {
  return request<JobStats>(`/jobs/${jobId}/stats`);
}

export function listJobs() {
  return request<{ items: JobItem[] }>("/jobs");
}

export function getJob(id: number) {
  return request<{ item: JobItem }>(`/jobs/${id}`);
}

/** 暂停：计划不再触发，已在运行或排队的运行不受影响 */
export function pauseJob(id: number) {
  return request<{ item: JobItem }>(`/jobs/${id}/pause`, { method: "POST" });
}

/** 启用：从下一次计划时间开始，暂停期间的计划不算错过 */
export function enableJob(id: number) {
  return request<{ item: JobItem }>(`/jobs/${id}/enable`, { method: "POST" });
}

/** 删除任务；正在运行或排队时后端拒绝。deleteSnapshots 为 true 时同时删除仓库中本任务的快照 */
export function deleteJob(id: number, deleteSnapshots: boolean) {
  const qs = deleteSnapshots ? "?delete_snapshots=true" : "";
  return request<DeleteJobResult>(`/jobs/${id}${qs}`, { method: "DELETE" });
}

/** 立即执行一次；任务已在运行或排队时后端拒绝。暂停的任务也可以立即执行 */
export function runJobNow(id: number) {
  return request<{ run: Run }>(`/jobs/${id}/run`, { method: "POST" });
}

/** 取消运行中或排队中的运行 */
export function cancelRun(jobId: number, runId: number) {
  return request<{ run: Run }>(`/jobs/${jobId}/runs/${runId}/cancel`, { method: "POST" });
}

/** 从数据源实时读取数据库列表（MySQL 不含系统库；PostgreSQL 为允许连接的非模板库） */
export function listDatabases(dataSourceId: number) {
  return request<{ databases: DatabaseInfo[] }>(`/datasources/${dataSourceId}/databases`);
}

/** 第 4 步的预览：按所选时区计算的接下来三次执行时间（RFC3339），以及按当前计划最多保留的快照份数 */
export function schedulePreview(body: { schedule: JobSchedule; retention: JobRetention }) {
  return request<{ next_runs: string[]; max_snapshots: number }>("/jobs/schedule-preview", {
    method: "POST",
    body: JSON.stringify(body),
  });
}

/** 新建 / 编辑任务提交给后端的字段；数据源、存储、路径前缀在编辑时不发送真实值（发 0 / 空串表示不变） */
function jobPayload(draft: JobDraft, editing: boolean) {
  return {
    name: draft.name.trim(),
    type: draft.type,
    datasource_id: editing ? 0 : draft.datasourceId,
    storage_id: editing ? 0 : draft.storageId,
    prefix: editing ? "" : draft.prefix,
    scope: draft.scope,
    databases: draft.databases,
    method: draft.method,
    options: draft.options,
    exclude_tables: excludeLines(draft.excludeText),
    compression: draft.compression,
    schedule: draft.schedule,
    retention: draft.retention,
    failure: draft.failure,
  };
}

/** 新建并启用任务（向导第 5 步“创建任务”） */
export function createJob(draft: JobDraft) {
  return request<{ item: JobItem }>("/jobs", {
    method: "POST",
    body: JSON.stringify({ ...jobPayload(draft, false), run_now: draft.runNow }),
  });
}

/** 编辑任务：数据源、存储、路径前缀创建后不能修改，不在请求中发送 */
export function updateJob(id: number, draft: JobDraft) {
  return request<{ item: JobItem }>(`/jobs/${id}`, {
    method: "PUT",
    body: JSON.stringify(jobPayload(draft, true)),
  });
}

// ============ 向导状态 ============

export const WIZARD_STEPS = ["source", "content", "destination", "schedule", "confirm"] as const;
export type WizardStep = (typeof WIZARD_STEPS)[number];

/** 向导中填写的内容；返回修改时保留 */
export interface JobDraft {
  type: JobType;
  datasourceId: number;
  scope: JobScope;
  databases: string[];
  method: JobMethod;
  options: JobOptions;
  /** 排除表原文，每行一个 */
  excludeText: string;
  storageId: number;
  prefix: string;
  /** 用户改过前缀后，换数据源不再覆盖为默认前缀 */
  prefixEdited: boolean;
  compression: Compression;
  schedule: JobSchedule;
  retention: JobRetention;
  failure: JobFailure;
  /** 任务名称，第 5 步展示与编辑 */
  name: string;
  /** 用户改过名称后，换数据源不再覆盖为默认名称 */
  nameEdited: boolean;
  /** 第 5 步“创建后”：true 为立即执行一次，false 为等下一次计划 */
  runNow: boolean;
}

/** 新建时的默认计划：时区取浏览器时区（不支持时退回 UTC） */
function defaultTimezone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  } catch {
    return "UTC";
  }
}

export const defaultSchedule = (): JobSchedule => ({
  kind: "daily",
  minute: 0,
  hour: 2,
  weekdays: [],
  cron: "",
  timezone: defaultTimezone(),
});

export const defaultRetention = (): JobRetention => ({ days: 7, weeks: 4, months: 6 });

export const defaultFailure = (): JobFailure => ({ retries: 2, retry_interval: 5, timeout: 120 });

export const defaultOptions = (): JobOptions => ({
  routines: true,
  triggers: true,
  events: true,
  users: false,
  globals: true,
});

export const emptyDraft = (): JobDraft => ({
  type: "backup",
  datasourceId: 0,
  scope: "instance",
  databases: [],
  method: "full",
  options: defaultOptions(),
  excludeText: "",
  storageId: 0,
  prefix: "",
  prefixEdited: false,
  compression: "zstd",
  schedule: defaultSchedule(),
  retention: defaultRetention(),
  failure: defaultFailure(),
  name: "",
  nameEdited: false,
  runNow: true,
});

/** 编辑已有任务时的初始内容 */
export function draftOf(job: JobItem): JobDraft {
  return {
    type: job.type,
    datasourceId: job.datasource_id,
    scope: job.scope,
    databases: [...job.databases],
    method: job.method,
    options: { ...job.options },
    excludeText: job.exclude_tables.join("\n"),
    storageId: job.storage_id,
    prefix: job.prefix,
    prefixEdited: true,
    compression: job.compression,
    schedule: { ...job.schedule, weekdays: [...job.schedule.weekdays] },
    retention: { ...job.retention },
    failure: { ...job.failure },
    name: job.name,
    nameEdited: true,
    runNow: true,
  };
}

/** 选中（或换）数据源：换了数据源时第 2 步的内容按新数据源重置；前缀未被用户改过时跟随数据源 */
export function selectDataSource(draft: JobDraft, ds: DataSourceItem): JobDraft {
  if (draft.datasourceId === ds.id) return draft;
  return {
    ...draft,
    datasourceId: ds.id,
    scope: "instance",
    databases: [],
    options: defaultOptions(),
    excludeText: "",
    prefix: draft.prefixEdited ? draft.prefix : defaultPrefix(ds.kind, ds.name),
  };
}

/** 向导各步骤显示在字段旁的错误 */
export interface WizardErrors {
  source?: string;
  method?: string;
  databases?: string;
  exclude?: string;
  storage?: string;
  prefix?: string;
  schedule?: string;
  timezone?: string;
  retentionDays?: string;
  retentionWeeks?: string;
  retentionMonths?: string;
  retries?: string;
  retryInterval?: string;
  timeout?: string;
  name?: string;
}

// ============ 校验 ============

export const MAX_PREFIX_LENGTH = 128;

/** 默认前缀 <数据源类型>/<数据源名称>，名称中的非法字符（含 /）替换为 -，结果仍满足前缀规则 */
export function defaultPrefix(kind: DataSourceKind, name: string): string {
  const safe = name
    .trim()
    .replace(/[^A-Za-z0-9._-]+/g, "-")
    .replace(/\.{2,}/g, ".")
    .replace(/^-+|-+$/g, "");
  return `${kind}/${safe || "db"}`.slice(0, MAX_PREFIX_LENGTH);
}

/** 路径前缀规则：只含字母、数字和 ._-/，1–128 个字符，不以 / 开头或结尾，不含 .. 或连续的 /（与后端 ValidPrefix 一致） */
export function validPrefix(p: string): boolean {
  return (
    p.length >= 1 &&
    p.length <= MAX_PREFIX_LENGTH &&
    /^[A-Za-z0-9._/-]+$/.test(p) &&
    !p.startsWith("/") &&
    !p.endsWith("/") &&
    !p.includes("..") &&
    !p.includes("//")
  );
}

/** 同一存储中与之冲突（相同或互为上下级）的其他任务 */
export function prefixConflict(
  jobs: JobItem[],
  storageId: number,
  prefix: string,
  selfId?: number
): JobItem | undefined {
  return jobs.find(
    (j) =>
      j.id !== selfId &&
      j.storage_id === storageId &&
      (j.prefix === prefix || prefix.startsWith(`${j.prefix}/`) || j.prefix.startsWith(`${prefix}/`))
  );
}

/** 排除表逐行去掉首尾空白、空行与重复项 */
export function excludeLines(text: string): string[] {
  const out: string[] = [];
  for (const line of text.split("\n")) {
    const s = line.trim();
    if (s && !out.includes(s)) out.push(s);
  }
  return out;
}

/** 格式不对的排除表：MySQL 为 库.表（2 段），PostgreSQL 为 库.模式.表（3 段），每段非空 */
export function invalidExcludes(kind: DataSourceKind, lines: string[]): string[] {
  const want = kind === "postgres" ? 3 : 2;
  return lines.filter((s) => {
    const parts = s.split(".");
    return parts.length !== want || parts.some((p) => !p.trim());
  });
}

/** 探测结果中主控端导出工具那一项：MySQL 为 mysqldump，PostgreSQL 为 pg_dump */
export function dumpToolItem(ds: DataSourceItem): ProbeItem | undefined {
  const key = ds.kind === "postgres" ? "postgres.pg_dump" : "mysql.mysqldump";
  return ds.probe?.state === "done" ? ds.probe.items?.find((i) => i.key === key) : undefined;
}

/** 探测判定导出工具不可用（找不到，或 pg_dump 大版本低于服务端）时，全量备份也不可选 */
export function dumpToolUnavailable(ds: DataSourceItem): boolean {
  return dumpToolItem(ds)?.tier === "fail";
}

/** 字节数的可读形式，保留一位小数 */
export function formatBytes(bytes: number): string {
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  let v = bytes;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return i === 0 ? `${v} B` : `${v.toFixed(1)} ${units[i]}`;
}

/** IANA 时区列表；环境不支持时只给出当前浏览器时区，避免下拉框为空 */
export function timezoneList(): string[] {
  try {
    return Intl.supportedValuesOf("timeZone").sort();
  } catch {
    return [defaultTimezone()];
  }
}

/**
 * 服务端返回的接下来一次执行时间（RFC3339，带所选时区的偏移）→ 该时区下的挂钟时间 `YYYY-MM-DD HH:mm`。
 * 直接截取字符串而不经过 Date，避免被浏览器本地时区覆盖。
 */
export function formatScheduleTime(iso: string): string {
  return iso.length >= 16 ? `${iso.slice(0, 10)} ${iso.slice(11, 16)}` : iso;
}
