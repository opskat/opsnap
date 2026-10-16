// 快照页的辅助函数。纯函数，不依赖界面。
import type { SnapshotEntry, SnapshotItem, SnapshotReason } from "./api";

export const REASON_LABEL: Record<SnapshotReason, string> = {
  last: "最近",
  daily: "每天",
  weekly: "每周",
  monthly: "每月",
};

/** 新增量相对最大值的百分比（0-100）。未知（负数）和 0 不画条；有数据时至少露出 2% */
export function addedPercent(added: number, max: number): number {
  if (added <= 0 || max <= 0) return 0;
  return Math.min(100, Math.max(2, Math.round((added / max) * 100)));
}

function pad(n: number) {
  return String(n).padStart(2, "0");
}

/** 快照时间：「今天 03:05」「昨天 23:00」，更早写「10月2日 03:00」 */
export function fmtSnapshotTime(unix: number, now: Date = new Date()): string {
  const d = new Date(unix * 1000);
  const day0 = (x: Date) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
  const diff = Math.round((day0(now) - day0(d)) / 86400_000);
  const hm = `${pad(d.getHours())}:${pad(d.getMinutes())}`;
  if (diff === 0) return `今天 ${hm}`;
  if (diff === 1) return `昨天 ${hm}`;
  const md = `${d.getMonth() + 1}月${d.getDate()}日`;
  return `${d.getFullYear() === now.getFullYear() ? md : `${d.getFullYear()}年${md}`} ${hm}`;
}

/** 文件树的起点：快照只记录了一个目录时从它开始，否则从根开始 */
export function rootPath(snap: Pick<SnapshotItem, "paths">): string {
  return snap.paths.length === 1 ? snap.paths[0] : "/";
}

/** 去掉被其他所选目录包含的项：打包时目录已经带上了它们 */
export function withoutDescendants(picked: SnapshotEntry[]): SnapshotEntry[] {
  return picked.filter((e) => !picked.some((o) => o !== e && o.type === "dir" && e.path.startsWith(`${o.path}/`)));
}

/** 所选项汇总：全是文件时给出总大小，含目录时大小未知 */
export function pickedSummary(picked: SnapshotEntry[]): { count: number; bytes: number | null } {
  const top = withoutDescendants(picked);
  const bytes = top.every((e) => e.type === "file") ? top.reduce((n, e) => n + e.size, 0) : null;
  return { count: top.length, bytes };
}

/** 文件名里第一个点开始的后缀，例如 shop.sql.gz 的「.sql.gz」 */
export function fileSuffix(filename: string): string {
  const i = filename.indexOf(".");
  return i < 0 ? "" : filename.slice(i);
}
