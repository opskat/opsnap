// 运行记录的纯函数：状态映射、14 天运行带、按天分组、速度与耗时的文字。不依赖界面。
import type { RunItem, RunStatusValue } from "./api";
import { fmtDate } from "./format";
import type { RunStatus } from "@/components/bits";

export type { RunItem } from "./api";

/** 运行状态在界面上的写法：圆点颜色和文字。跳过没有专属颜色，用「未运行」的灰点加「已跳过」。 */
export function runStatusView(s: RunStatusValue): { status: RunStatus; text: string } {
  if (s === "skipped") return { status: "none", text: "已跳过" };
  const text = { running: "运行中", success: "成功", warning: "有警告", failed: "失败" }[s];
  return { status: s, text };
}

/** 同一天有多次运行时取最需要注意的结果 */
const SEVERITY: Record<RunStatus, number> = { none: 0, success: 1, warning: 2, failed: 3, running: 4 };

const DAY_MS = 86400_000;
const dayStart = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();

/** 运行带覆盖的天数 */
export const STRIP_DAYS = 14;

/** 某个任务最近 14 天的运行带：每天一格，最后一格是今天；跳过的运行不算结果 */
export function dayStrip(
  runs: Pick<RunItem, "job_id" | "started_at" | "status">[],
  jobId: number,
  now: Date = new Date()
): RunStatus[] {
  const strip: RunStatus[] = Array.from({ length: STRIP_DAYS }, () => "none");
  const today = dayStart(now);
  for (const r of runs) {
    if (r.job_id !== jobId || r.status === "skipped") continue;
    const ago = Math.round((today - dayStart(new Date(r.started_at * 1000))) / DAY_MS);
    if (ago < 0 || ago >= STRIP_DAYS) continue;
    const i = STRIP_DAYS - 1 - ago;
    if (SEVERITY[r.status] > SEVERITY[strip[i]]) strip[i] = r.status;
  }
  return strip;
}

/** 一次采样：已处理的字节数和采样时刻（毫秒） */
export interface Sample {
  bytes: number;
  at: number;
}

/** 两次采样之间的速度（字节/秒）；第一次采样、时间没有前进或数据量减少时没有速度 */
export function speedBetween(prev: Sample | undefined, bytes: number, at: number): number | null {
  if (!prev || at <= prev.at || bytes < prev.bytes) return null;
  return ((bytes - prev.bytes) * 1000) / (at - prev.at);
}

/** 耗时：「19 秒」「1 分 48 秒」「1 小时 02 分」 */
export function fmtDurationMs(ms: number): string {
  const sec = Math.max(1, Math.round(ms / 1000));
  if (sec < 60) return `${sec} 秒`;
  const pad = (n: number) => String(n).padStart(2, "0");
  if (sec < 3600) return `${Math.floor(sec / 60)} 分 ${pad(sec % 60)} 秒`;
  return `${Math.floor(sec / 3600)} 小时 ${pad(Math.floor((sec % 3600) / 60))} 分`;
}

export function fmtClock(unix: number): string {
  const d = new Date(unix * 1000);
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}

/** 按开始日期分组，保持原有顺序；今天、昨天写名字，更早写日期 */
export function groupByDay(runs: RunItem[], now: Date = new Date()): { label: string; rows: RunItem[] }[] {
  const today = dayStart(now);
  const groups: { key: number; label: string; rows: RunItem[] }[] = [];
  for (const r of runs) {
    const key = dayStart(new Date(r.started_at * 1000));
    let g = groups[groups.length - 1];
    if (!g || g.key !== key) {
      const ago = Math.round((today - key) / DAY_MS);
      g = { key, label: ago === 0 ? "今天" : ago === 1 ? "昨天" : fmtDate(r.started_at, now), rows: [] };
      groups.push(g);
    }
    g.rows.push(r);
  }
  return groups;
}

/** 「今天 16:14:02」「10月10日 04:00:00」 */
export function fmtStart(unix: number, now: Date = new Date()): string {
  const d = new Date(unix * 1000);
  const pad = (n: number) => String(n).padStart(2, "0");
  const same = d.toDateString() === now.toDateString();
  return `${same ? "今天" : fmtDate(unix, now)} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

/** 日志行的时间，Unix 毫秒 → 「16:14:02」 */
export function fmtLogTime(ms: number): string {
  const d = new Date(ms);
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}
