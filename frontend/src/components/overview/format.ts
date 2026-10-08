import type { TFunction } from "i18next";

const pad = (n: number) => String(n).padStart(2, "0");

const sameDay = (a: Date, b: Date) =>
  a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate();

/** 距今天相差的日历天数（本地时区）：今天 0，明天 1 */
function calendarDaysFromToday(d: Date, now: Date) {
  const start = (x: Date) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
  return Math.round((start(d) - start(now)) / 86400000);
}

/** 当天只显示“时:分”，不是今天时带上“月-日” */
function clock(d: Date, now: Date) {
  const hm = `${pad(d.getHours())}:${pad(d.getMinutes())}`;
  return sameDay(d, now) ? hm : `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${hm}`;
}

/** 最近运行的时间列：1 分钟内“刚刚”，当天“时:分”，更早“月-日 时:分” */
export function runTimeLabel(t: TFunction, unix: number, now = new Date()) {
  if (now.getTime() / 1000 - unix < 60) return t("time.justNow");
  return clock(new Date(unix * 1000), now);
}

/** 下一次运行的主数值：按浏览器时区的“时:分”，不是今天时带上日期 */
export function nextRunTime(unix: number, now = new Date()) {
  return clock(new Date(unix * 1000), now);
}

/** 下一次运行的相对时间：一小时内按分钟，今天稍后按小时，明天，更晚按天 */
export function nextRunRelative(t: TFunction, unix: number, now = new Date()) {
  const minutes = Math.ceil((unix * 1000 - now.getTime()) / 60000);
  if (minutes < 60) return t("overview.stats.inMinutes", { count: Math.max(1, minutes) });
  const days = calendarDaysFromToday(new Date(unix * 1000), now);
  if (days <= 0) return t("overview.stats.inHours", { count: Math.floor(minutes / 60) });
  if (days === 1) return t("overview.stats.tomorrow");
  return t("overview.stats.inDays", { count: days });
}

/** YYYY-MM-DD → MM/DD（柱状图横轴） */
export function shortDate(date: string) {
  return date.slice(5).replace("-", "/");
}

/** 0–1 的比例 → 整数百分比 */
export function percent(ratio: number) {
  return Math.round(ratio * 100);
}
