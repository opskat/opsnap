// 时间显示：面板里统一用「10月2日」「今天 08:00」「12 分钟前」这类短格式

function pad(n: number) {
  return String(n).padStart(2, "0");
}

function sameDay(a: Date, b: Date) {
  return a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate();
}

/** 日期：今年的写「10月2日」，往年的写「2025年10月2日」 */
export function fmtDate(unix: number, now: Date = new Date()): string {
  const d = new Date(unix * 1000);
  const md = `${d.getMonth() + 1}月${d.getDate()}日`;
  return d.getFullYear() === now.getFullYear() ? md : `${d.getFullYear()}年${md}`;
}

/** 过去的时间：一小时内写「N 分钟前」，今天写「今天 08:00」，更早写日期 */
export function fmtAgo(unix: number, now: Date = new Date()): string {
  const d = new Date(unix * 1000);
  const sec = Math.floor((now.getTime() - d.getTime()) / 1000);
  if (sec < 60) return "刚刚";
  if (sec < 3600) return `${Math.floor(sec / 60)} 分钟前`;
  if (sameDay(d, now)) return `今天 ${pad(d.getHours())}:${pad(d.getMinutes())}`;
  return fmtDate(unix, now);
}

/** 字节数：「1.4 TB」「820 MB」，不到 1 KB 写「0 B」这类整数 */
export function fmtBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  const units = ["KB", "MB", "GB", "TB", "PB"];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v >= 100 ? Math.round(v) : v.toFixed(1)} ${units[i]}`;
}

/** 将来的时间：「今天 03:00」「明天 03:00」，更远写「10月20日 03:00」 */
export function fmtNext(unix: number, now: Date = new Date()): string {
  const d = new Date(unix * 1000);
  const day0 = (x: Date) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
  const diff = Math.round((day0(d) - day0(now)) / 86400_000);
  const hm = `${pad(d.getHours())}:${pad(d.getMinutes())}`;
  if (diff === 0) return `今天 ${hm}`;
  if (diff === 1) return `明天 ${hm}`;
  return `${fmtDate(unix, now)} ${hm}`;
}
