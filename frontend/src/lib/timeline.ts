// 运行时间线的点位计算：纯函数，不依赖界面。
// 横轴是真实时间：左端是窗口第一天 00:00，右端是「现在」之后的一小段计划；位置用占整条轨道宽度的百分比表示。
import type { OverviewTimeline, TimelineJob, TimelineRun } from "./api";
import { fmtDate } from "./format";

const DAY = 86400;
const WEEK = ["周日", "周一", "周二", "周三", "周四", "周五", "周六"];

/** 时间 → 横向位置的比例尺；时间都是 Unix 秒 */
export interface Scale {
  range: 7 | 30;
  from: number;
  to: number;
  now: number;
  /** 时间 → 百分比（0 在最左，100 在最右） */
  x: (sec: number) => number;
  /** 「现在」线的位置 */
  nowX: number;
}

/** 由服务端给出的窗口和「现在」建立比例尺 */
export function makeScale(tl: Pick<OverviewTimeline, "window" | "from" | "to">, now: number): Scale {
  const span = tl.to - tl.from;
  const x = (sec: number) => ((sec - tl.from) / span) * 100;
  return { range: tl.window === "30d" ? 30 : 7, from: tl.from, to: tl.to, now, x, nowX: x(now) };
}

/** 轴上的一天 */
export interface AxisDay {
  /** 格子起点：服务端设置时区里当天的 00:00，毫秒 */
  t: number;
  label: string;
  weekend: boolean;
  /** 7 天每天标注，30 天只标注周一；靠近「现在」线和右端的标签隐藏，免得压住「现在」标记和「下次运行」列 */
  showLabel: boolean;
  /** 格子的左边界和宽度，百分比 */
  x: number;
  width: number;
}

/**
 * 日期轴的格子。scale.from 是服务端设置时区里窗口第一天的 00:00，从它起每 86400 秒一格，
 * 格子与服务端的日界对齐（浏览器在别的时区时也一样）。浏览器所在时区的夏令时切换按偏移差修正，
 * 浏览器与设置同一时区时每格正好是一个日历日。标签和周末取格子中间那一刻在浏览器里的日期，
 * 两边时差在 12 小时以内时就是服务端的那一天。
 */
export function axisDays(scale: Scale): AxisDay[] {
  const fromOffset = new Date(scale.from * 1000).getTimezoneOffset();
  // 隐藏阈值（占轨道宽度的百分比，按约 800px 宽估算）：
  // 现在线左边的标签要放得下自己和「现在」标记的半宽，右边只要让开标记；右端要放得下自己
  const gapBefore = scale.range === 7 ? 14 : 9;
  const gapAfter = scale.range === 7 ? 6 : 5;
  const gapEnd = scale.range === 7 ? 9 : 4;
  const days: AxisDay[] = [];
  for (let i = 0; ; i++) {
    const step = scale.from + i * DAY;
    const sec = step + (new Date(step * 1000).getTimezoneOffset() - fromOffset) * 60;
    if (sec >= scale.to) break;
    const x = scale.x(sec);
    const d = new Date((sec + DAY / 2) * 1000);
    const dow = d.getDay();
    days.push({
      t: sec * 1000,
      label:
        scale.range === 7 ? `${d.getMonth() + 1}/${d.getDate()} ${WEEK[dow]}` : `${d.getMonth() + 1}/${d.getDate()}`,
      weekend: dow === 0 || dow === 6,
      showLabel:
        (scale.range === 7 || dow === 1) &&
        (x < scale.nowX ? scale.nowX - x > gapBefore : x - scale.nowX > gapAfter) &&
        100 - x > gapEnd,
      x,
      width: Math.min((DAY / (scale.to - scale.from)) * 100, 100 - x),
    });
  }
  return days;
}

/** 已经发生的运行：实心点，颜色由结果决定 */
export interface PastDot {
  id: number;
  x: number;
  at: number;
  status: TimelineRun["status"];
}
/** 计划中的运行：空心点 */
export interface PlannedDot {
  x: number;
  at: number;
}

/** 一条轨道上的点；窗口以外的点不画 */
export function jobDots(job: TimelineJob, scale: Scale): { past: PastDot[]; planned: PlannedDot[] } {
  return {
    past: job.runs
      .filter((r) => r.started_at >= scale.from && r.started_at <= scale.to)
      .map((r) => ({ id: r.id, x: scale.x(r.started_at), at: r.started_at, status: r.status })),
    planned: job.planned.filter((at) => at >= scale.from && at <= scale.to).map((at) => ({ x: scale.x(at), at })),
  };
}

const pad = (n: number) => String(n).padStart(2, "0");

/** 悬停时的时间：「今天 16:00」「昨天 03:00」「明天 03:00」，更远写「10月10日 04:05」 */
export function whenLabel(sec: number, nowSec: number): string {
  const t = new Date(sec * 1000);
  const now = new Date(nowSec * 1000);
  const day0 = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
  const diff = Math.round((day0(t) - day0(now)) / (DAY * 1000));
  const day = diff === 0 ? "今天" : diff === -1 ? "昨天" : diff === 1 ? "明天" : fmtDate(sec, now);
  return `${day} ${pad(t.getHours())}:${pad(t.getMinutes())}`;
}
