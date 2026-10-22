// 任务表单的数据模型：从接口数据生成表单、校验并生成提交体、摘要文字。纯函数，不依赖界面。
import {
  ApiError,
  ErrorCode,
  SECRET_MASKED,
  type JobCompression,
  type JobInput,
  type JobItem,
  type JobSchedule,
  type JobSourceType,
  type ScheduleKind,
} from "./api";
import { LONG_TERM, type RetentionRule } from "./retention";
import { parseAddr } from "./servers";

/** 表单上的一种数据源 */
export interface SourceOption {
  value: JobSourceType;
  label: string;
  /** 用哪个 logo */
  tile: "mysql" | "postgres" | "redis" | "mongo" | "files";
  port: number;
}

export const SOURCES: SourceOption[] = [
  { value: "mysql", label: "MySQL", tile: "mysql", port: 3306 },
  { value: "postgres", label: "PostgreSQL", tile: "postgres", port: 5432 },
  { value: "redis", label: "Redis", tile: "redis", port: 6379 },
  { value: "mongodb", label: "MongoDB", tile: "mongo", port: 27017 },
  { value: "files", label: "文件", tile: "files", port: 0 },
];

export function sourceOf(t: JobSourceType): SourceOption {
  return SOURCES.find((s) => s.value === t) ?? SOURCES[0];
}

export const isFiles = (t: JobSourceType) => t === "files";
/** 有库列表可勾选（Redis 整个实例导出） */
export const hasDatabaseList = (t: JobSourceType) => t !== "files" && t !== "redis";

export const WEEKDAYS = ["周日", "周一", "周二", "周三", "周四", "周五", "周六"] as const;

export const SCHEDULE_LABEL: Record<ScheduleKind, string> = {
  hourly: "每小时",
  "6h": "每 6 小时",
  daily: "每天",
  weekly: "每周",
  cron: "cron",
};

export const DEFAULT_TIMEOUT_SEC = 7200;
export const COMPRESSION_LABEL: Record<JobCompression, string> = { auto: "自动", max: "最大", off: "关闭" };

export interface JobForm {
  name: string;
  source_type: JobSourceType;
  conn_mode: "ssh" | "direct";
  server_id: number;
  /** 数据库地址，host:port */
  db_addr: string;
  db_user: string;
  db_password: string;
  /** 已保存过密码：留空表示保留 */
  db_password_saved: boolean;
  databases: string[];
  all_databases: boolean;
  auth_db: string;
  paths: string[];
  excludes: string[];
  repo_id: number;
  schedule: JobSchedule;
  retention: RetentionRule;
  timeout_sec: number;
  /** 限速，MB/s，空表示不限 */
  bandwidth_mb: string;
  compression: JobCompression;
  /** 附加参数，空格分隔 */
  extra_args: string;
  paused: boolean;
}

export function emptyJobForm(repoId = 0): JobForm {
  return {
    name: "",
    source_type: "mysql",
    conn_mode: "ssh",
    server_id: 0,
    db_addr: "",
    db_user: "",
    db_password: "",
    db_password_saved: false,
    databases: [],
    all_databases: false,
    auth_db: "admin",
    paths: [""],
    excludes: [],
    repo_id: repoId,
    schedule: { kind: "daily", time: "03:00", weekday: 0, cron: "" },
    retention: { preset: "long", ...LONG_TERM },
    timeout_sec: DEFAULT_TIMEOUT_SEC,
    bandwidth_mb: "",
    compression: "auto",
    extra_args: "",
    paused: false,
  };
}

function hostPort(host: string, port: number) {
  if (!host) return "";
  return `${host.includes(":") ? `[${host}]` : host}:${port}`;
}

function mbOf(kbps: number): string {
  return kbps > 0 ? String(Math.round((kbps / 1024) * 100) / 100) : "";
}

export function formFromItem(j: JobItem): JobForm {
  const isDb = !isFiles(j.source_type);
  return {
    name: j.name,
    source_type: j.source_type,
    conn_mode: j.conn_mode || "ssh",
    server_id: j.server_id,
    db_addr: isDb ? hostPort(j.db_host, j.db_port) : "",
    db_user: j.db_user,
    db_password: "",
    db_password_saved: j.db_password === SECRET_MASKED,
    databases: j.databases ?? [],
    all_databases: j.all_databases,
    auth_db: j.auth_db || "admin",
    paths: j.paths && j.paths.length > 0 ? j.paths : [""],
    excludes: j.excludes ?? [],
    repo_id: j.repo_id,
    schedule: j.schedule,
    retention: j.retention,
    timeout_sec: j.advanced.timeout_sec || DEFAULT_TIMEOUT_SEC,
    bandwidth_mb: mbOf(j.advanced.bandwidth_kbps),
    compression: j.advanced.compression || "auto",
    extra_args: (j.advanced.extra_args ?? []).join(" "),
    paused: j.paused,
  };
}

/** 字段名 → 原因（一个字段可有多条） */
export type FieldErrors = Record<string, string[]>;

/** 解析 11401 的 msg：每行 [json.path] 原因 */
export function parseFieldErrors(msg: string): FieldErrors {
  const out: FieldErrors = {};
  for (const line of msg.split("\n")) {
    const m = /^\[([\w.]+)\] (.*)$/.exec(line.trim());
    if (m) (out[m[1]] ??= []).push(m[2]);
  }
  return out;
}

/** 错误是字段校验失败（400 + 11401）时返回逐字段的原因，否则 null */
export function fieldErrorsOf(err: unknown): FieldErrors | null {
  if (!(err instanceof ApiError) || err.status !== 400 || err.code !== ErrorCode.JobFieldInvalid) return null;
  const fields = parseFieldErrors(err.message);
  return Object.keys(fields).length > 0 ? fields : null;
}

/** 页面上有对应位置的字段；其余的错误显示在表单顶部 */
export const KNOWN_FIELDS = [
  "name",
  "server_id",
  "db_host",
  "db_port",
  "db_user",
  "db_password",
  "databases",
  "auth_db",
  "paths",
  "excludes",
  "repo_id",
  "retention",
  "schedule.kind",
  "schedule.time",
  "schedule.weekday",
  "schedule.cron",
  "advanced.timeout_sec",
  "advanced.bandwidth_kbps",
  "advanced.compression",
  "advanced.extra_args",
] as const;

/** 改动某个表单字段时，哪些错误字段随之作废 */
export function errorKeysOf(formKey: keyof JobForm): string[] {
  switch (formKey) {
    case "db_addr":
      return ["db_host", "db_port"];
    case "timeout_sec":
      return ["advanced.timeout_sec"];
    case "bandwidth_mb":
      return ["advanced.bandwidth_kbps"];
    case "compression":
      return ["advanced.compression"];
    case "extra_args":
      return ["advanced.extra_args"];
    case "conn_mode":
      return ["conn_mode", "server_id", "db_host", "db_port"];
    default:
      return [formKey];
  }
}

/** 一个字段的错误合成一句 */
export function firstError(errs: FieldErrors, ...keys: string[]): string | null {
  const all = keys.flatMap((k) => errs[k] ?? []);
  return all.length > 0 ? all.join("；") : null;
}

/** 生成提交体。input 总是可用于读库和测试（地址写不对时端口为 0）；errors 是前端就能判断的错误 */
export function buildInput(f: JobForm): { input: JobInput; errors: FieldErrors } {
  const errors: FieldErrors = {};
  const files = isFiles(f.source_type);
  let host = "";
  let port = 0;
  if (!files) {
    const a = parseAddr(f.db_addr);
    if (a) ({ host, port } = a);
    else errors.db_host = ["地址写成 主机:端口，例如 172.17.0.2:3306"];
  }
  const password = f.db_password !== "" ? f.db_password : f.db_password_saved ? SECRET_MASKED : "";
  const bw = Number(f.bandwidth_mb);
  const input: JobInput = {
    name: f.name.trim(),
    source_type: f.source_type,
    server_id: files || f.conn_mode === "ssh" ? f.server_id : 0,
    conn_mode: files ? "ssh" : f.conn_mode,
    db_host: host,
    db_port: port,
    db_user: files ? "" : f.db_user.trim(),
    db_password: files ? "" : password,
    databases: files || f.all_databases || !hasDatabaseList(f.source_type) ? [] : f.databases,
    all_databases: !files && hasDatabaseList(f.source_type) && f.all_databases,
    auth_db: f.source_type === "mongodb" ? f.auth_db.trim() : "",
    paths: files ? f.paths.map((p) => p.trim()).filter(Boolean) : [],
    excludes: files ? f.excludes : [],
    repo_id: f.repo_id,
    schedule: f.schedule,
    retention: f.retention,
    advanced: {
      timeout_sec: f.timeout_sec,
      bandwidth_kbps: Number.isFinite(bw) && bw > 0 ? Math.round(bw * 1024) : 0,
      compression: f.compression,
      extra_args: f.extra_args.split(/\s+/).filter(Boolean),
    },
    paused: f.paused,
  };
  if (f.bandwidth_mb.trim() !== "" && !(Number.isFinite(bw) && bw >= 0)) {
    errors["advanced.bandwidth_kbps"] = ["限速写成数字，单位 MB/s，留空表示不限"];
  }
  return { input, errors };
}

/** 读取库列表所依赖的连接信息；变了就要重新读 */
export function connSignature(f: JobForm): string {
  return JSON.stringify([
    f.source_type,
    f.conn_mode,
    f.conn_mode === "ssh" ? f.server_id : 0,
    f.db_addr.trim(),
    f.db_user.trim(),
    f.db_password,
    f.db_password_saved,
    f.auth_db.trim(),
  ]);
}

/** 连接信息已经填全，可以读库列表 */
export function connReady(f: JobForm): boolean {
  if (!hasDatabaseList(f.source_type)) return false;
  if (f.conn_mode === "ssh" && f.server_id <= 0) return false;
  if (!parseAddr(f.db_addr)) return false;
  return f.db_user.trim() !== "";
}

/** 整份配置的签名；连接测试的结果只对生成它的那份配置有效 */
export function formSignature(f: JobForm): string {
  return JSON.stringify(f);
}

export function scheduleText(s: JobSchedule): string {
  switch (s.kind) {
    case "hourly":
      return "每小时";
    case "6h":
      return "每 6 小时";
    case "daily":
      return `每天 ${s.time}`;
    case "weekly":
      return `每${WEEKDAYS[s.weekday] ?? "周日"} ${s.time}`;
    case "cron":
      return `cron ${s.cron}`;
  }
}

const HOUR = 3600_000;

/** 计划的备份间隔，用于估算保留的快照数；cron 取预览里最小的相邻间隔 */
export function scheduleIntervalMs(s: JobSchedule, previewSec: number[] = []): number {
  switch (s.kind) {
    case "hourly":
      return HOUR;
    case "6h":
      return 6 * HOUR;
    case "weekly":
      return 7 * 24 * HOUR;
    case "cron": {
      let min = Infinity;
      for (let i = 1; i < previewSec.length; i++) min = Math.min(min, (previewSec[i] - previewSec[i - 1]) * 1000);
      if (Number.isFinite(min) && min > 0) return min;
      return 24 * HOUR;
    }
    default:
      return 24 * HOUR;
  }
}

/** 秒数写成「2 小时」「30 分钟」 */
export function fmtDuration(sec: number): string {
  if (sec % 3600 === 0) return `${sec / 3600} 小时`;
  if (sec % 60 === 0) return `${sec / 60} 分钟`;
  return `${sec} 秒`;
}

/** 高级折叠标题行显示的当前值 */
export function advancedSummary(f: JobForm): string {
  const parts = [
    `超时 ${fmtDuration(f.timeout_sec)}`,
    f.bandwidth_mb.trim() && Number(f.bandwidth_mb) > 0 ? `限速 ${f.bandwidth_mb} MB/s` : "不限速",
    `${COMPRESSION_LABEL[f.compression]}压缩`,
  ];
  const extra = f.extra_args.split(/\s+/).filter(Boolean);
  if (extra.length > 0) parts.push(`${extra.length} 个附加参数`);
  return parts.join("，");
}

/** 摘要里的来源两行：主行写从哪里取，副行（等宽）写取什么 */
export function sourceSummary(f: JobForm, serverName: string): { main: string; detail: string } {
  const server = serverName || "未选服务器";
  const addr = f.db_addr.trim() || "未填地址";
  if (isFiles(f.source_type)) {
    return {
      main: server,
      detail:
        f.paths
          .map((p) => p.trim())
          .filter(Boolean)
          .join("、") || "未填目录",
    };
  }
  const main = f.conn_mode === "ssh" ? `${server} 隧道 → ${addr}` : `直连 ${addr}`;
  let detail: string;
  if (!hasDatabaseList(f.source_type)) detail = "整个实例";
  else if (f.all_databases) detail = "全部库";
  else detail = f.databases.join("、") || "未选库";
  return { main, detail };
}

/** 列表副标题里的「备份什么」：文件写路径，数据库写库名 */
export function jobTarget(j: Pick<JobItem, "source_type" | "paths" | "databases" | "all_databases">): string {
  if (j.source_type === "files") return (j.paths ?? []).join("、");
  if (j.source_type === "redis") return "整个实例";
  if (j.all_databases) return "全部库";
  return (j.databases ?? []).join("、");
}

/** 列表副标题里的「从哪里」：经 SSH 的写服务器名，直连的写数据库地址 */
export function jobOrigin(
  j: Pick<JobItem, "server_name" | "conn_mode" | "db_host" | "db_port" | "source_type">
): string {
  if (j.source_type !== "files" && j.conn_mode === "direct") return `${j.db_host}:${j.db_port}`;
  return j.server_name;
}
