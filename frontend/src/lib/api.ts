// opsnap HTTP API 客户端。
// 后端是 cago：成功返回 {code:0,msg,data}，失败返回 HTTP 状态码 + {code,msg}。
// 会话放在 HttpOnly Cookie opsnap_session 里，前端不接触令牌。

export const API_BASE = "/api/v1";

/** 读取时代替已保存凭据的占位；原样提交表示保留已保存的值，与后端 secret.Masked 一致 */
export const SECRET_MASKED = "已设置";

/** 接口返回的错误。message 是服务端给出的中文说明，可以直接展示。 */
export class ApiError extends Error {
  /** HTTP 状态码；0 表示没有连上服务 */
  readonly status: number;
  /** cago 错误码（internal/pkg/code），未知时为 -1 */
  readonly code: number;

  constructor(status: number, code: number, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
  }
}

/** 后端错误码，与 internal/pkg/code 保持一致 */
export const ErrorCode = {
  Unauthorized: 11000,
  AdminNotInitialized: 11001,
  AdminAlreadyInitialized: 11002,
  LoginFailed: 11003,
  LoginLocked: 11004,
  RepoExistingPasswordRequired: 11312,
  RepoExistingPasswordWrong: 11313,
  RepoBusy: 11314,
  JobFieldInvalid: 11401,
  RunAlreadyRunning: 11501,
  RunNotRunning: 11502,
  RunStopNotConfirmed: 11503,
  SnapshotNotFound: 11600,
  SnapshotPathNotFound: 11601,
  SnapshotRepoLocked: 11602,
  SnapshotReadFailed: 11603,
  SnapshotNotDatabase: 11604,
} as const;

/** 读取错误里可展示的说明 */
export function errorMessage(err: unknown): string {
  if (err instanceof Error && err.message) return err.message;
  return "操作没有完成，请稍后重试";
}

/** 登录页地址，带上当前位置以便登录后返回 */
export function loginPath(current: string): string {
  if (current.startsWith("/login")) return current;
  if (current === "/" || current === "") return "/login";
  return `/login?next=${encodeURIComponent(current)}`;
}

let onUnauthorized: (() => void) | null = null;

/**
 * 设置收到 401 时的处理。面板在路由内注册为跳转登录页；
 * 传 null 恢复默认行为（整页跳转到登录页）。
 */
export function setUnauthorizedHandler(fn: (() => void) | null) {
  onUnauthorized = fn;
}

function handleUnauthorized() {
  if (onUnauthorized) {
    onUnauthorized();
    return;
  }
  const here = window.location.pathname + window.location.search;
  if (!here.startsWith("/login") && !here.startsWith("/setup")) {
    window.location.assign(loginPath(here));
  }
}

type Method = "GET" | "POST" | "PUT" | "DELETE";

/** 读取接口的时限；超时后中止请求，出错状态写明是哪个接口超时 */
export const GET_TIMEOUT_MS = 60_000;

/** 发送请求并解开 cago 的响应包装 */
export async function request<T>(method: Method, path: string, body?: unknown): Promise<T> {
  const init: RequestInit = { method, credentials: "same-origin", headers: { Accept: "application/json" } };
  if (body !== undefined) {
    init.headers = { ...init.headers, "Content-Type": "application/json" };
    init.body = JSON.stringify(body);
  }
  // 只给读取加时限：保存、测试连接、维护等操作可能本来就要很久
  const endpoint = `${method} ${API_BASE}${path.split("?")[0]}`;
  const ctrl = method === "GET" ? new AbortController() : null;
  let timedOut = false;
  const timer = ctrl
    ? window.setTimeout(() => {
        timedOut = true;
        ctrl.abort();
      }, GET_TIMEOUT_MS)
    : undefined;
  if (ctrl) init.signal = ctrl.signal;

  let res: Response;
  try {
    res = await fetch(API_BASE + path, init);
  } catch {
    if (timedOut) {
      throw new ApiError(0, -1, `${endpoint} 超时（${GET_TIMEOUT_MS / 1000} 秒内没有响应），请稍后重试`);
    }
    throw new ApiError(0, -1, "无法连接 opsnap 服务，请检查网络后重试");
  } finally {
    window.clearTimeout(timer);
  }

  let payload: { code?: unknown; msg?: unknown; data?: unknown } | null;
  try {
    payload = await res.json();
  } catch {
    payload = null;
  }

  if (res.status === 401) {
    handleUnauthorized();
  }
  if (!payload || typeof payload !== "object" || typeof payload.code !== "number") {
    throw new ApiError(res.status, -1, `opsnap 服务返回 ${res.status}，请稍后重试`);
  }
  if (!res.ok || payload.code !== 0) {
    const msg = typeof payload.msg === "string" && payload.msg ? payload.msg : `请求失败（${res.status}）`;
    throw new ApiError(res.status, payload.code, msg);
  }
  return payload.data as T;
}

// ---- 认证 internal/api/auth ----

export interface AuthStatus {
  initialized: boolean;
  /** 已配置并已绑定账号，可以用单点登录 */
  sso_enabled?: boolean;
  /** 登录按钮文字，例如 Gitea */
  sso_label?: string;
  /** 是否允许用户名密码登录（已考虑环境变量 OPSNAP_PASSWORD_LOGIN） */
  password_login?: boolean;
}
export interface Credentials {
  username: string;
  password: string;
}
export interface Me {
  kind: "admin" | "api_key";
  username?: string;
  key_id?: number;
  key_name?: string;
}
export interface ChangePasswordBody {
  old_password: string;
  new_password: string;
}

// ---- API 密钥 internal/api/apikey ----

export type APIKeyExpiry = "30d" | "90d" | "never";
export interface APIKey {
  id: number;
  name: string;
  /** 密钥前几位，用于识别 */
  prefix: string;
  /** Unix 秒，0 表示从未使用 */
  last_used_at: number;
  created_at: number;
  /** Unix 秒，0 表示永不过期 */
  expires_at: number;
}
export interface CreatedAPIKey extends APIKey {
  /** 完整密钥，仅此一次返回 */
  key: string;
}

// ---- 设置 internal/api/setting ----

export interface Settings {
  admin_username: string;
  max_concurrent_runs: number;
  retention_days: number;
  timezone: string;
  clear_mirror_after_backup: boolean;
  /** rotated_at：最近一次轮换主密钥的时间，没有轮换过为 0 */
  master_key: { configured: boolean; algorithm: string; rotated_at: number };
  /** 文件镜像目录：本机唯一存放明文的地方 */
  mirror_dir: string;
}
export type SettingsUpdate = Pick<
  Settings,
  "max_concurrent_runs" | "retention_days" | "timezone" | "clear_mirror_after_backup"
>;

// ---- 单点登录 internal/api/sso ----

export interface SSOSettings {
  /** 是否已保存提供方配置 */
  configured: boolean;
  issuer: string;
  client_id: string;
  /** 已保存时为 SECRET_MASKED，否则为空；不返回内容 */
  client_secret: string;
  button_label: string;
  panel_url: string;
  /** 要填到提供方 OAuth2 应用里的重定向 URI */
  callback_url: string;
  /** 绑定的提供方账号；只认 issuer + sub，name 只用于展示 */
  binding: SSOBinding | null;
  /** 设置里保存的「允许用户名密码登录」 */
  password_login: boolean;
  /** 环境变量 OPSNAP_PASSWORD_LOGIN 正在强制打开密码登录 */
  password_login_forced: boolean;
}
export interface SSOBinding {
  issuer: string;
  sub: string;
  name: string;
  /** Unix 秒 */
  bound_at: number;
}
/** client_secret 为空表示保留已保存的值 */
export type SSOUpdate = Pick<SSOSettings, "issuer" | "client_id" | "client_secret" | "button_label" | "panel_url">;

// ---- 通知 internal/api/notify ----

export interface NotifyConfig {
  url: string;
  headers: Record<string, string>;
  on_failed: boolean;
  on_recovered: boolean;
  on_stale: boolean;
  on_success: boolean;
  stale_hours: number;
  /** 面板地址，消息里的 link 以它开头；保存时自动填入浏览器里的地址 */
  panel_url?: string;
}
export interface NotifyTestResult {
  status_code: number;
}

// ---- 服务器与代理链 internal/api/server ----

/** 直接填写私有凭据时的认证方式，与密钥类型一一对应（credential_id > 0 时由密钥的类型决定） */
export type ServerAuthType = CredentialKind;
export type HopType = "ssh" | "socks5" | "http";
export type ServerStatus = "unknown" | "ok" | "failed";

/** 提交的一跳：SSH 跳板只填 server_id，代理填地址，用户名和密码可选 */
export interface HopInput {
  type: HopType;
  server_id: number;
  host: string;
  port: number;
  username: string;
  password: string;
}
export interface HopView {
  type: HopType;
  server_id?: number;
  server_name?: string;
  host: string;
  port: number;
  username: string;
  password: string;
}

/**
 * 提交的服务器配置；添加、修改、测试都带这一组字段。
 * 登录凭据有两种来源：credential_id > 0 时引用一条共享密钥，下面四个凭据字段不使用；
 * credential_id 为 0 时按 auth_type 直接填写，占位「已设置」表示保留这台服务器已有的私有凭据。
 */
export interface ServerInput {
  name: string;
  host: string;
  port: number;
  username: string;
  /** > 0：使用这条共享密钥；0：直接填写 */
  credential_id: number;
  /** 仅直接填写时使用；引用共享密钥时留空 */
  auth_type: ServerAuthType | "";
  password: string;
  private_key: string;
  passphrase: string;
  /** 直接填写时把它存成这个名称的共享密钥；空串或省略表示保持私有 */
  share_as?: string;
  hops: HopInput[];
  host_key_fingerprint: string;
}

/** 服务器用的登录凭据；只告诉调用方有没有内容与名称，永不返回密文或明文 */
export interface ServerCredential {
  id: number;
  kind: CredentialKind;
  /** 共享密钥的名称；私有凭据（直接填写的那份）为空串 */
  name: string;
  /** 是共享密钥（能在密钥页看到、被多台服务器选用） */
  shared: boolean;
  username: string;
  note: string;
  has_secret: boolean;
  has_passphrase: boolean;
}

export interface ServerItem {
  id: number;
  name: string;
  host: string;
  port: number;
  username: string;
  credential: ServerCredential | null;
  hops: HopView[] | null;
  host_key_fingerprint: string;
  status: ServerStatus;
  latency_ms: number;
  /** 从何时起连不上，Unix 秒，0 表示正常 */
  fail_since: number;
  last_error: string;
  last_check_at: number;
  job_count: number;
  created_at?: number;
  updated_at?: number;
}
export type HopFailReason =
  "timeout" | "auth_failed" | "fingerprint_mismatch" | "fingerprint_unconfirmed" | "unreachable" | "canceled";
/** 一跳的测试结果；index 从 1 开始，目标是最后一跳 */
export interface HopResult {
  index: number;
  type: string;
  name: string;
  addr: string;
  ok: boolean;
  latency_ms: number;
  reason: HopFailReason | "";
  reason_label: string;
  message: string;
  fingerprint: string;
}
export interface ServerTestResult {
  ok: boolean;
  hops: HopResult[] | null;
  /** 目标主机实际出示的 SHA256 指纹 */
  fingerprint: string;
}

// ---- 密钥库 internal/api/credential ----

/** 密钥类型，与服务器的认证方式一一对应 */
export type CredentialKind = "password" | "ssh_key";

export const CREDENTIAL_KIND_LABEL: Record<CredentialKind, string> = { password: "密码", ssh_key: "私钥" };

/** 使用某条密钥的服务器 */
export interface CredentialServerRef {
  id: number;
  name: string;
  /** 地址与用户是明文，用来在抽屉里写「用户@地址」 */
  host: string;
  port: number;
  username: string;
}

/** 一条共享密钥；只告诉调用方有没有内容，永不返回密文或明文 */
export interface CredentialItem {
  id: number;
  name: string;
  kind: CredentialKind;
  /** 默认用户：选了这条密钥时，服务器上为空的「用户」自动填它 */
  username: string;
  note: string;
  /** 有没有密码或私钥 */
  has_secret: boolean;
  /** 私钥有没有口令 */
  has_passphrase: boolean;
  /** 使用这条密钥的服务器；没有时是空数组 */
  servers: CredentialServerRef[] | null;
  created_at: number;
  updated_at: number;
}

/** 提交的一条密钥；编辑时密码、私钥、口令留空表示保留已保存的值 */
export interface CredentialInput {
  name: string;
  kind: CredentialKind;
  password: string;
  private_key: string;
  passphrase: string;
  username: string;
  note: string;
}

// ---- 存储位置 internal/api/repo ----

export type RepoType = "local" | "sftp" | "s3" | "rest" | "webdav";
export type RepoStatus = "unknown" | "ok" | "not_mounted" | "error";

/** 地址参数，按类型取用；凭据读取时只返回「已设置」，原样提交表示保留 */
export interface RepoParams {
  path: string;
  host: string;
  port: number;
  username: string;
  password: string;
  private_key: string;
  host_key: string;
  server_id: number;
  endpoint: string;
  bucket: string;
  prefix: string;
  region: string;
  access_key_id: string;
  secret_access_key: string;
  path_style: boolean;
  url: string;
  vendor: string;
}
export interface RepoItem {
  id: number;
  name: string;
  type: RepoType;
  params: RepoParams;
  address: string;
  encryption: string;
  status: RepoStatus;
  status_message: string;
  status_at: number;
  /** 实际占用，字节 */
  stored_size: number;
  /** 原始数据量，字节 */
  raw_size: number;
  /** 原始数据量 / 实际占用，没有数据时为 0 */
  dedup_ratio: number;
  snapshot_count: number;
  /** NAS 剩余空间，字节；拿不到时为 null */
  free_space: number | null;
  key_count: number;
  /** 统计更新时间，0 表示还没有统计 */
  stats_at: number;
  no_recovery_key: boolean;
  last_check_at: number;
  last_check_ok: boolean;
  last_check_message: string;
  last_prune_at: number;
  last_prune_ok: boolean;
  last_prune_message: string;
  /** 正在进行的维护：check | prune，空表示没有 */
  maintenance: "" | "check" | "prune";
  job_count: number;
  created_at: number;
  updated_at: number;
}
export interface RepoCreateInput {
  name: string;
  type: RepoType;
  params: Partial<RepoParams>;
  /** 已是 restic 仓库时它现有的密码；只用于接入，不保存 */
  existing_password?: string;
  recovery_key: boolean;
  recovery_key_value: string;
  recovery_key_saved: boolean;
}
export interface RepoCreated extends RepoItem {
  adopted: boolean;
  /** 仓库已保存但恢复密钥没有加入成功的原因 */
  recovery_key_error: string;
}
export interface KeyStatus {
  present: boolean;
  id: string;
  created_at: number;
}
export interface RepoKeys {
  opsnap_key: KeyStatus;
  recovery_key: KeyStatus;
  no_recovery_key: boolean;
}
export interface SFTPHostKey {
  fingerprint: string;
  host_key: string;
}

// ---- 备份任务 internal/api/job ----

export type JobSourceType = "mysql" | "mariadb" | "postgres" | "redis" | "mongodb" | "files";
export type JobConnMode = "ssh" | "direct";
export type ScheduleKind = "hourly" | "6h" | "daily" | "weekly" | "cron";
export type JobCompression = "auto" | "max" | "off";

export interface JobSchedule {
  kind: ScheduleKind;
  /** HH:MM，daily 与 weekly 使用 */
  time: string;
  /** 0 表示周日，weekly 使用 */
  weekday: number;
  cron: string;
}
export interface JobRetention {
  preset: "7d" | "30d" | "long" | "custom";
  days: number;
  weeks: number;
  months: number;
}
export interface JobAdvanced {
  /** 秒，0 表示默认 2 小时 */
  timeout_sec: number;
  /** KiB/s，0 表示不限速 */
  bandwidth_kbps: number;
  compression: JobCompression | "";
  extra_args: string[] | null;
}
/** 提交的任务配置；新建、修改、测试、读库都带这一组字段 */
export interface JobInput {
  name: string;
  source_type: JobSourceType;
  server_id: number;
  conn_mode: JobConnMode;
  db_host: string;
  db_port: number;
  db_user: string;
  /** 空表示没有；SECRET_MASKED 表示保留已保存的密码 */
  db_password: string;
  databases: string[];
  all_databases: boolean;
  auth_db: string;
  paths: string[];
  excludes: string[];
  repo_id: number;
  schedule: JobSchedule;
  retention: JobRetention;
  advanced: JobAdvanced;
  paused: boolean;
}
export interface JobItem extends Omit<JobInput, "databases" | "paths" | "excludes"> {
  id: number;
  server_name: string;
  repo_name: string;
  databases: string[] | null;
  paths: string[] | null;
  excludes: string[] | null;
  /** 下次运行，Unix 秒；已暂停为 0 */
  next_run_at: number;
  attention: boolean;
  created_at: number;
  updated_at: number;
  /** 以下三项只在列表里返回：最近一次运行（不含跳过），可能早于 14 天 */
  last_run?: JobRunBrief | null;
  /** 最近 14 天的运行（不含跳过），按开始时间升序 */
  recent_runs?: JobRunBrief[] | null;
  /** 正在运行（含排队等待名额） */
  running?: boolean;
}
/** 任务列表里的一次运行，时间是 Unix 秒 */
export interface JobRunBrief {
  id: number;
  status: RunStatusValue;
  started_at: number;
  bytes_added: number;
}
export interface JobDatabase {
  name: string;
  /** 未知为 -1 */
  size_bytes: number;
  system: boolean;
  selectable: boolean;
}
/** 读取失败不是接口错误：ok 为 false，reason_label 与 message 原地显示 */
export interface JobDatabasesResult {
  ok: boolean;
  list: JobDatabase[] | null;
  reason: string;
  reason_label: string;
  message: string;
}
export type JobCheckKey = "ssh" | "db_reach" | "db_auth" | "db_privs" | "repo";
export interface JobCheck {
  key: JobCheckKey;
  label: string;
  ok: boolean;
  /** 前面的检查失败，这一项没有执行 */
  skipped: boolean;
  reason: string;
  /** 可复制的修复命令，例如 GRANT 语句 */
  fix: string;
}
export interface JobTestResult {
  ok: boolean;
  checks: JobCheck[] | null;
}
export interface JobNextRuns {
  timezone: string;
  /** Unix 秒 */
  times: number[] | null;
}

// ---- 运行记录 internal/api/run ----

export type RunStatusValue = "running" | "success" | "warning" | "failed" | "skipped";
export type StepStatus = "pending" | "running" | "success" | "warning" | "failed" | "skipped";
export interface RunStep {
  key: "preflight" | "export" | "forget" | "notify";
  label: string;
  status: StepStatus;
  message: string;
  /** Unix 毫秒，0 表示未开始或未结束 */
  started_at: number;
  finished_at: number;
}
/** 一次运行；时间是 Unix 秒，耗时是毫秒 */
export interface RunItem {
  id: number;
  job_id: number;
  job_name: string;
  /** schedule | manual | api_key:<密钥名称> */
  trigger: string;
  trigger_label: string;
  status: RunStatusValue;
  reason: string;
  notify_failed: boolean;
  started_at: number;
  finished_at: number;
  duration_ms: number;
  bytes_processed: number;
  bytes_added: number;
  snapshot_ids: string[] | null;
  steps: RunStep[] | null;
}
export interface RunList {
  list: RunItem[] | null;
  total: number;
}
export type LogLevel = "info" | "warn" | "error";
export interface LogLine {
  /** Unix 毫秒 */
  t: number;
  level: LogLevel;
  text: string;
}
export interface RunLog {
  running: boolean;
  lines: LogLine[] | null;
}

export type JobListFilter = "all" | "attention" | "paused";

// ---- 快照 internal/api/snapshot ----

/** 被保留的原因，对应保留规则的 最近、每天、每周、每月 */
export type SnapshotReason = "last" | "daily" | "weekly" | "monthly";
export interface SnapshotItem {
  /** 完整快照 ID */
  id: string;
  short_id: string;
  /** Unix 秒 */
  time: number;
  /** 文件任务是镜像目录，数据库任务是导出文件 */
  paths: string[];
  run_id: number;
  /** 这次新增的数据量；未知为 -1 */
  bytes_added: number;
  reasons: SnapshotReason[];
}
export interface SnapshotList {
  job_name: string;
  source_type: JobSourceType;
  snapshots: SnapshotItem[];
}
export interface SnapshotEntry {
  name: string;
  /** 快照内的绝对路径 */
  path: string;
  type: "dir" | "file" | "symlink" | "other";
  size: number;
  /** Unix 秒 */
  mtime: number;
}
export interface SnapshotExport {
  /** 快照内路径，下载时作为 path 参数 */
  path: string;
  size: number;
  /** 下载时需要带 gzip=true */
  gzip: boolean;
  /** 下载得到的文件名 */
  filename: string;
  /** 可复制的恢复命令示例 */
  restore_command: string;
}

function snapshotQuery(paths: string[], gzip: boolean): string {
  const params = new URLSearchParams();
  for (const p of paths) params.append("path", p);
  if (gzip) params.set("gzip", "true");
  return params.toString();
}

// ---- 概览 internal/api/overview ----

export type OverviewWindow = "7d" | "30d";
export type AttentionKind = "failed" | "warning" | "stale";
export interface OverviewSummary {
  jobs: number;
  attention: number;
  /** 已暂停且不需要处理的任务 */
  paused: number;
  ok: number;
  servers: number;
  repos: number;
}
export interface OverviewTotals {
  raw_bytes: number;
  stored_bytes: number;
  snapshots: number;
}
/** 时间都是 Unix 秒，0 表示没有 */
export interface AttentionItem {
  job_id: number;
  job_name: string;
  source_type: JobSourceType;
  kind: AttentionKind;
  reason: string;
  since: number;
  last_success_at: number;
  /** 最近一次有结果的运行，查看日志用；没有运行过为 0 */
  run_id: number;
  repo_id: number;
  repo_not_mounted: boolean;
}
export interface TimelineRun {
  id: number;
  started_at: number;
  status: Exclude<RunStatusValue, "skipped">;
}
export interface TimelineJob {
  job_id: number;
  name: string;
  source_type: JobSourceType;
  paused: boolean;
  schedule: JobSchedule;
  /** 最近一次有结果的运行；没有运行过为空 */
  last_status: Exclude<RunStatusValue, "skipped" | "running"> | "";
  /** 下次运行；已暂停为 0 */
  next_run_at: number;
  runs: TimelineRun[];
  /** 现在之后的计划运行 */
  planned: number[];
}
export interface OverviewTimeline {
  window: OverviewWindow;
  /** 窗口第一天 00:00 */
  from: number;
  /** 现在加上向后显示的小时数 */
  to: number;
  ahead_hours: number;
  jobs: TimelineJob[];
}
export interface OverviewRepo {
  id: number;
  name: string;
  type: RepoType;
  status: RepoStatus;
  stored_bytes: number;
  raw_bytes: number;
  /** 去重压缩比，没有数据时为 0 */
  ratio: number;
  snapshots: number;
  jobs: number;
  /** 所在卷剩余字节，未知为 -1 */
  free_space: number;
}
export interface Overview {
  now: number;
  summary: OverviewSummary;
  totals: OverviewTotals;
  attention: AttentionItem[];
  timeline: OverviewTimeline;
  repos: OverviewRepo[];
}

export const api = {
  auth: {
    status: () => request<AuthStatus>("GET", "/auth/status"),
    setup: (body: Credentials) => request<Record<string, never>>("POST", "/auth/setup", body),
    login: (body: Credentials) => request<{ username: string }>("POST", "/auth/login", body),
    logout: () => request<Record<string, never>>("POST", "/auth/logout"),
    me: () => request<Me>("GET", "/auth/me"),
    changePassword: (body: ChangePasswordBody) => request<Record<string, never>>("PUT", "/auth/password", body),
    /** 开始单点登录，返回提供方授权页地址 */
    oidcLogin: (next: string) => request<{ url: string }>("POST", "/auth/oidc/login", { next }),
    /** 单点登录失败的原因；id 来自回调跳转地址里的 sso_error */
    oidcResult: (id: string) => request<{ message: string }>("GET", `/auth/oidc/result?id=${encodeURIComponent(id)}`),
  },
  apikeys: {
    list: () => request<{ list: APIKey[] | null }>("GET", "/apikeys"),
    create: (body: { name: string; expires_in: APIKeyExpiry }) => request<CreatedAPIKey>("POST", "/apikeys", body),
    revoke: (id: number) => request<Record<string, never>>("DELETE", `/apikeys/${id}`),
  },
  sso: {
    get: () => request<SSOSettings>("GET", "/settings/sso"),
    update: (body: SSOUpdate) => request<SSOSettings>("PUT", "/settings/sso", body),
    disable: () => request<SSOSettings>("DELETE", "/settings/sso"),
    /** 开始绑定，返回提供方授权页地址 */
    bind: () => request<{ url: string }>("POST", "/settings/sso/bind"),
    unbind: () => request<SSOSettings>("DELETE", "/settings/sso/binding"),
    setPasswordLogin: (enabled: boolean) => request<SSOSettings>("PUT", "/settings/sso/password-login", { enabled }),
  },
  settings: {
    get: () => request<Settings>("GET", "/settings"),
    update: (body: SettingsUpdate) => request<Settings>("PUT", "/settings", body),
    rotateMasterKey: (newKey: string) =>
      request<{ rotated_at: number }>("POST", "/settings/master-key/rotate", { new_key: newKey }),
  },
  notify: {
    get: () => request<NotifyConfig>("GET", "/notify"),
    update: (body: NotifyConfig) => request<NotifyConfig>("PUT", "/notify", body),
    test: () => request<NotifyTestResult>("POST", "/notify/test"),
  },
  servers: {
    list: () => request<{ list: ServerItem[] | null }>("GET", "/servers"),
    create: (body: ServerInput) => request<ServerItem>("POST", "/servers", body),
    update: (id: number, body: ServerInput) => request<ServerItem>("PUT", `/servers/${id}`, body),
    remove: (id: number) => request<Record<string, never>>("DELETE", `/servers/${id}`),
    /** 测试一份未保存的配置；id 非 0 时凭据占位取该服务器已保存的值 */
    test: (id: number, body: ServerInput) => request<ServerTestResult>("POST", "/servers/test", { id, ...body }),
    testSaved: (id: number) => request<ServerTestResult>("POST", `/servers/${id}/test`),
  },
  credentials: {
    list: () => request<{ list: CredentialItem[] | null }>("GET", "/credentials"),
    get: (id: number) => request<CredentialItem>("GET", `/credentials/${id}`),
    create: (body: CredentialInput) => request<CredentialItem>("POST", "/credentials", body),
    update: (id: number, body: CredentialInput) => request<CredentialItem>("PUT", `/credentials/${id}`, body),
    remove: (id: number) => request<Record<string, never>>("DELETE", `/credentials/${id}`),
  },
  repos: {
    list: () => request<{ list: RepoItem[] | null }>("GET", "/repos"),
    create: (body: RepoCreateInput) => request<RepoCreated>("POST", "/repos", body),
    /** 生成一把恢复密钥的值供展示；不保存、不加入任何仓库 */
    newRecoveryKey: () => request<{ recovery_key_value: string }>("POST", "/repos/recovery-key"),
    sftpHostKey: (body: { host: string; port: number; server_id: number }) =>
      request<SFTPHostKey>("POST", "/repos/sftp-host-key", body),
    recheck: (id: number) => request<RepoItem>("POST", `/repos/${id}/recheck`),
    check: (id: number) => request<RepoItem>("POST", `/repos/${id}/check`),
    prune: (id: number) => request<RepoItem>("POST", `/repos/${id}/prune`),
    unlock: (id: number) => request<Record<string, never>>("POST", `/repos/${id}/unlock`, { confirm: true }),
    keys: (id: number) => request<RepoKeys>("GET", `/repos/${id}/keys`),
    rotateKey: (id: number) => request<RepoKeys>("POST", `/repos/${id}/keys/rotate`),
    addRecoveryKey: (id: number, value: string) =>
      request<RepoKeys>("POST", `/repos/${id}/keys/recovery`, { recovery_key_value: value, recovery_key_saved: true }),
    removeRecoveryKey: (id: number) => request<RepoKeys>("DELETE", `/repos/${id}/keys/recovery`),
    recoveryBundle: (id: number) =>
      request<{ filename: string; content: string }>("GET", `/repos/${id}/recovery-bundle`),
  },
  jobs: {
    list: (filter: JobListFilter = "all", q = "") => {
      const params = new URLSearchParams({ filter });
      if (q.trim()) params.set("q", q.trim());
      return request<{ list: JobItem[] | null }>("GET", `/jobs?${params}`);
    },
    duplicate: (id: number) => request<JobItem>("POST", `/jobs/${id}/duplicate`),
    pause: (id: number) => request<JobItem>("POST", `/jobs/${id}/pause`),
    resume: (id: number) => request<JobItem>("POST", `/jobs/${id}/resume`),
    /** 删除任务；已有快照保留 */
    remove: (id: number) => request<Record<string, never>>("DELETE", `/jobs/${id}`),
    get: (id: number) => request<JobItem>("GET", `/jobs/${id}`),
    create: (body: JobInput) => request<JobItem>("POST", "/jobs", body),
    update: (id: number, body: JobInput) => request<JobItem>("PUT", `/jobs/${id}`, body),
    /** 预览计划接下来几次的运行时间，按设置里的时区计算 */
    nextRuns: (schedule: JobSchedule, count = 5) =>
      request<JobNextRuns>("POST", "/jobs/next-runs", { schedule, count }),
    /** 读取库列表及大小；id 非 0 时密码占位取该任务已保存的值 */
    databases: (id: number, body: JobInput) => request<JobDatabasesResult>("POST", "/jobs/databases", { id, ...body }),
    /** 测试一份未保存的配置 */
    test: (id: number, body: JobInput) => request<JobTestResult>("POST", "/jobs/test", { id, ...body }),
    /** 立即运行一次 */
    run: (id: number) => request<{ run_id: number }>("POST", `/jobs/${id}/run`),
  },
  overview: {
    get: (window: OverviewWindow = "7d") => request<Overview>("GET", `/overview?window=${window}`),
  },
  runs: {
    list: (q: { job_id?: number; status?: string; page?: number; size?: number } = {}) => {
      const params = new URLSearchParams();
      for (const [k, v] of Object.entries(q)) if (v) params.set(k, String(v));
      const qs = params.toString();
      return request<RunList>("GET", `/runs${qs ? `?${qs}` : ""}`);
    },
    get: (id: number) => request<RunItem>("GET", `/runs/${id}`),
    /** 停止运行；后端要求 confirm 为 true */
    stop: (id: number) => request<Record<string, never>>("POST", `/runs/${id}/stop`, { confirm: true }),
    log: (id: number) => request<RunLog>("GET", `/runs/${id}/log`),
  },
  snapshots: {
    list: (jobId: number) => request<SnapshotList>("GET", `/jobs/${jobId}/snapshots`),
    /** 目录的直接子项，path 缺省为快照根 */
    files: (jobId: number, sid: string, path = "") =>
      request<{ path: string; entries: SnapshotEntry[] | null }>(
        "GET",
        `/jobs/${jobId}/snapshots/${sid}/files${path ? `?${new URLSearchParams({ path })}` : ""}`
      ),
    exports: (jobId: number, sid: string) =>
      request<{ files: SnapshotExport[] | null }>("GET", `/jobs/${jobId}/snapshots/${sid}/exports`),
    /** 下载预检：仓库被锁、NAS 不可访问、快照或路径不存在时抛出带原因的错误，不读取数据 */
    checkDownload: (jobId: number, sid: string, paths: string[], gzip = false) =>
      request<Record<string, never>>(
        "GET",
        `/jobs/${jobId}/snapshots/${sid}/download/check?${snapshotQuery(paths, gzip)}`
      ),
    /** 真正的下载地址（流式响应），交给浏览器直接下载，不经过 fetch */
    downloadUrl: (jobId: number, sid: string, paths: string[], gzip = false) =>
      `${API_BASE}/jobs/${jobId}/snapshots/${sid}/download?${snapshotQuery(paths, gzip)}`,
  },
};
