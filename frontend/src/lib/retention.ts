// 保留规则：句式、快照数估算和时间轴点位。
// 估算只在前端做，按 restic forget 的语义（keep-within、keep-daily/weekly/monthly）计算；
// 预设与自定义数字如何换成 forget 参数以后端 internal/pkg/retention 的 ForgetArgs 为准。

export type RetentionPreset = "7d" | "30d" | "long" | "custom";

export interface RetentionRule {
  preset: RetentionPreset;
  /** 以下三个数字只在 custom 时生效 */
  days: number;
  weeks: number;
  months: number;
}

export const RETENTION_LABEL: Record<RetentionPreset, string> = {
  "7d": "7 天",
  "30d": "30 天",
  long: "长期",
  custom: "自定义",
};

/** 「长期」预设的数字，与后端 PresetLongTerm 一致 */
export const LONG_TERM = { days: 7, weeks: 4, months: 6 } as const;

/** 自定义规则中每个数字的上限，与后端 MaxCount 一致 */
export const MAX_COUNT = 1000;

/** 按天、周、月计数保留的数字；7 天、30 天预设按时间窗口保留，返回 null */
export function retentionCounts(r: RetentionRule): { days: number; weeks: number; months: number } | null {
  switch (r.preset) {
    case "long":
      return { ...LONG_TERM };
    case "custom":
      return { days: r.days, weeks: r.weeks, months: r.months };
    default:
      return null;
  }
}

/** 自定义规则里的一个数字是否会被后端接受：0 到 MAX_COUNT 的整数 */
function validCount(n: number): boolean {
  return Number.isInteger(n) && n >= 0 && n <= MAX_COUNT;
}

const DAY_MS = 24 * 3600_000;

/** UTC 日期的 ISO 周：返回 年*100+周。调用方传入的 Date 用 UTC 字段表示「墙上时间」 */
function isoWeekKey(t: number): number {
  const d = new Date(t);
  const day = d.getUTCDay() || 7; // 周一为 1，周日为 7
  const thursday = Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), d.getUTCDate() + 4 - day);
  const y = new Date(thursday).getUTCFullYear();
  const week = Math.floor((thursday - Date.UTC(y, 0, 1)) / DAY_MS / 7) + 1;
  return y * 100 + week;
}

const dayKey = (t: number) => {
  const d = new Date(t);
  return d.getUTCFullYear() * 10000 + (d.getUTCMonth() + 1) * 100 + d.getUTCDate();
};
const monthKey = (t: number) => {
  const d = new Date(t);
  return d.getUTCFullYear() * 100 + d.getUTCMonth() + 1;
};

/**
 * 估算按固定间隔 intervalMs 持续备份、历史足够长时，在 now 这一刻 forget 之后保留的快照数。
 * 假设快照时间为 now、now-interval、now-2·interval……，按 restic 的规则逐个判断：
 * keep-within 保留比最新快照早不到窗口期的快照；keep-daily/weekly/monthly 从新到旧，
 * 在每个尚未计数的日/ISO 周/月里保留最新的一个，直到用完次数。
 * now 用 UTC 字段表示墙上时间（见 wallClock）。规则或间隔无效（后端会拒绝的规则）返回 null。
 */
export function estimateSnapshots(r: RetentionRule, intervalMs: number, now: Date): number | null {
  if (!(intervalMs > 0)) return null;
  const nowMs = now.getTime();
  if (r.preset === "7d" || r.preset === "30d") {
    const n = r.preset === "7d" ? 7 : 30;
    const from = new Date(nowMs);
    from.setUTCDate(from.getUTCDate() - n);
    const window = nowMs - from.getTime();
    return Math.ceil(window / intervalMs);
  }
  // 未知预设、负数、小数、超过上限的数字后端都会拒绝，不给估算
  const c = retentionCounts(r);
  if (!c || ![c.days, c.weeks, c.months].every(validCount)) return null;
  if (c.days + c.weeks + c.months <= 0) return null;

  const buckets = [
    { left: c.days, last: -1, key: dayKey },
    { left: c.weeks, last: -1, key: isoWeekKey },
    { left: c.months, last: -1, key: monthKey },
  ];
  let kept = 0;
  for (let k = 0; ;) {
    const t = nowMs - k * intervalMs;
    let active = false;
    let keep = false;
    for (const b of buckets) {
      if (b.left <= 0) continue;
      active = true;
      const key = b.key(t);
      if (key !== b.last) {
        b.last = key;
        b.left--;
        keep = true;
      }
    }
    if (!active) return kept;
    if (keep) kept++;
    // 当天更早的快照不可能是任何一个日/周/月里最新的那个，直接跳到前一天的最新一份
    const d = new Date(t);
    const dayStart = Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), d.getUTCDate());
    const next = Math.floor((nowMs - dayStart) / intervalMs) + 1;
    k = next <= k ? k + 1 : next;
  }
}

/** 把真实时刻换成「UTC 字段 = 本地墙上时间」的 Date，供 estimateSnapshots 使用 */
export function wallClock(now: Date): Date {
  return new Date(now.getTime() - now.getTimezoneOffset() * 60_000);
}

/** 最早保留到多久以前，写成「6 个月」「8 周」「14 天」 */
export function oldestText(r: RetentionRule): string {
  const c = retentionCounts(r);
  if (!c) return `${r.preset === "7d" ? 7 : 30} 天`;
  if (c.months) return `${c.months} 个月`;
  if (c.weeks) return `${c.weeks} 周`;
  return `${c.days} 天`;
}

/** 摘要里的保留一行 */
export function retentionSentence(r: RetentionRule, estimate?: number | null): string {
  if (r.preset === "7d") return "最近 7 天的全部备份";
  if (r.preset === "30d") return "最近 30 天的全部备份";
  if (estimate == null) return r.preset === "long" ? "长期" : "自定义规则";
  return `约 ${estimate} 个快照，最早 ${oldestText(r)}前`;
}

export type MarkKind = "day" | "week" | "month";
export interface TimelineMark {
  /** 0 最早，100 现在 */
  x: number;
  kind: MarkKind;
}

/** 时间轴示意：平方根刻度，越近越放大。7 天、30 天按时间窗口保留，窗口里每天一个点，均匀排列 */
export function timelineMarks(r: RetentionRule): TimelineMark[] {
  const c = retentionCounts(r);
  if (!c) {
    const n = r.preset === "7d" ? 7 : 30;
    return Array.from({ length: n }, (_, i) => ({ x: 100 - (i / n) * 100, kind: "day" as const }));
  }
  const span = Math.max(c.months * 30.5, c.weeks * 7, c.days, 1);
  const pos = (d: number) => 100 - Math.sqrt(d / span) * 100;
  const marks: TimelineMark[] = [];
  for (let i = 0; i < c.days; i++) marks.push({ x: pos(i), kind: "day" });
  for (let i = 1; i <= c.weeks; i++) if (i * 7 >= c.days) marks.push({ x: pos(i * 7), kind: "week" });
  for (let i = 1; i <= c.months; i++) if (i * 30.5 >= c.weeks * 7) marks.push({ x: pos(i * 30.5), kind: "month" });
  return marks;
}
