const pad = (n: number) => String(n).padStart(2, "0");

/** Unix 秒 → 本地时间 YYYY-MM-DD HH:mm（界面上的时间统一用等宽字体显示） */
export function formatDateTime(unix: number) {
  const d = new Date(unix * 1000);
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

/** Unix 秒 → 本地日期 YYYY-MM-DD */
export function formatDate(unix: number) {
  return formatDateTime(unix).slice(0, 10);
}

/** 从 User-Agent 中取出浏览器名称；识别不了时返回第一个产品标识 */
export function describeUserAgent(ua: string) {
  const rules: [RegExp, string][] = [
    [/Edg\//, "Edge"],
    [/OPR\//, "Opera"],
    [/Firefox\//, "Firefox"],
    [/Chrome\//, "Chrome"],
    [/Safari\//, "Safari"],
  ];
  for (const [re, name] of rules) if (re.test(ua)) return name;
  return ua.split(/[\s/]/)[0] || "—";
}
