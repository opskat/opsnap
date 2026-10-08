import { request } from "@/lib/api";
import type { JobType, Run } from "@/lib/jobs";

// ============ 接口类型（与 internal/api/overview 一致） ============

/** 受保护的数据源：被已启用且成功过的任务引用 */
export interface ProtectedSources {
  count: number;
  /** 按类型名排序，只含数量大于 0 的类型 */
  by_kind: { kind: string; count: number }[];
}

/** 最近 24 小时开始、已结束的运行；跳过与取消不计入 */
export interface SuccessRate24h {
  /** 成功与失败的次数之和 */
  runs: number;
  success: number;
  failed: number;
  /** 0–1；runs 为 0 时为 0 */
  success_rate: number;
}

export interface NextRun {
  /** 计划执行时间（秒） */
  at: number;
  job_id: number;
  job_name: string;
}

/** 一条最近运行及其任务；运行中的带实时的已导出量与已运行时间 */
export interface RecentRun extends Run {
  job_name: string;
  job_type: JobType;
  /** mysql / postgres；数据源已不存在时为空 */
  datasource_kind: string;
  /** 如 mysql://host:port */
  datasource_address: string;
}

export interface RecentRuns {
  /** 最近 20 条，按触发顺序倒序 */
  items: RecentRun[];
  /** 最近 20 条失败的运行 */
  failed: RecentRun[];
  /** 最近 24 小时的失败次数 */
  failed_24h: number;
}

/** 一天中开始的成功与失败次数 */
export interface DayRuns {
  /** YYYY-MM-DD（请求的时区） */
  date: string;
  success: number;
  failed: number;
}

export interface StorageUsage {
  packed_bytes: number;
  original_bytes: number;
  /** 0–1 */
  savings: number;
  /** 无法读取用量的存储数 */
  unreadable: number;
}

export interface Disk {
  used_bytes: number;
  total_bytes: number;
  free_bytes: number;
}

export interface OverviewStorage {
  id: number;
  name: string;
  kind: "local" | "s3";
  location: string;
  path: string;
  status: string;
  /** 为 false 时用量为 0，界面显示“—”与 reason */
  readable: boolean;
  /** 按请求语言的原因 */
  reason: string;
  packed_bytes: number;
  original_bytes: number;
  snapshots: number;
  usage_recorded_at: number;
  /** 本地目录所在磁盘；S3 或读取失败时为 null */
  disk: Disk | null;
}

export interface Overview {
  protected: ProtectedSources;
  success_24h: SuccessRate24h;
  next_run: NextRun | null;
  recent: RecentRuns;
  timezone: string;
  /** 最近 14 天（含今天），按日期正序，最后一项为今天 */
  daily: DayRuns[];
  counts: { datasources: number; storages: number; jobs: number };
  storage_usage: StorageUsage;
  storages: OverviewStorage[];
}

/** 浏览器的 IANA 时区（14 天运行按它分天） */
export function browserTimeZone(): string {
  return Intl.DateTimeFormat().resolvedOptions().timeZone;
}

/** 概览数据；tz 为浏览器时区 */
export function getOverview(tz: string) {
  return request<Overview>(`/overview?tz=${encodeURIComponent(tz)}`);
}
