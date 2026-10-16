import { describe, expect, it } from "vitest";
import { axisDays, jobDots, makeScale, whenLabel } from "./timeline";
import type { OverviewTimeline, TimelineJob } from "./api";

const sec = (d: Date) => Math.floor(d.getTime() / 1000);
// 2026-10-15 16:00（本地时间），与后端概览一致：窗口从 range-1 天前的 00:00 开始，向后看 30 / 72 小时
const NOW = sec(new Date(2026, 9, 15, 16, 0));
const HOUR = 3600;

function tl(window: "7d" | "30d"): Pick<OverviewTimeline, "window" | "from" | "to"> {
  const days = window === "7d" ? 7 : 30;
  const ahead = window === "7d" ? 30 : 72;
  return { window, from: sec(new Date(2026, 9, 15 - (days - 1), 0, 0)), to: NOW + ahead * HOUR };
}

function job(over: Partial<TimelineJob> = {}): TimelineJob {
  return {
    job_id: 1,
    name: "商城数据库",
    source_type: "mysql",
    paused: false,
    schedule: { kind: "daily", time: "03:00", weekday: 0, cron: "" },
    last_status: "success",
    next_run_at: NOW + 11 * HOUR,
    runs: [],
    planned: [],
    ...over,
  };
}

describe("时间线点位", () => {
  it("时间按真实比例换算成横向百分比", () => {
    const s = makeScale(tl("7d"), NOW);
    const total = tl("7d").to - tl("7d").from;
    expect(s.x(tl("7d").from)).toBe(0);
    expect(s.x(tl("7d").to)).toBe(100);
    expect(s.x(tl("7d").from + total / 4)).toBeCloseTo(25, 6);
    // 间隔相同的两个时刻，距离相同
    expect(s.x(NOW) - s.x(NOW - 6 * HOUR)).toBeCloseTo(s.x(NOW - 12 * HOUR) - s.x(NOW - 18 * HOUR), 9);
  });

  it("「现在」线的位置按服务器时间算，在过去窗口和未来之间", () => {
    const s = makeScale(tl("7d"), NOW);
    // 6 天 16 小时 / (6 天 16 小时 + 30 小时)
    expect(s.nowX).toBeCloseTo((160 / 190) * 100, 6);
    expect(s.x(NOW)).toBe(s.nowX);
  });

  it("切到 30 天后比例尺变小，「现在」线靠得更右", () => {
    const a = makeScale(tl("7d"), NOW);
    const b = makeScale(tl("30d"), NOW);
    expect(b.nowX).toBeCloseTo(((29 * 24 + 16) / (29 * 24 + 16 + 72)) * 100, 6);
    expect(b.nowX).toBeGreaterThan(a.nowX);
    // 同样 6 小时在 30 天里占的宽度比 7 天里窄
    expect(b.x(NOW) - b.x(NOW - 6 * HOUR)).toBeLessThan(a.x(NOW) - a.x(NOW - 6 * HOUR));
    expect(b.range).toBe(30);
    expect(a.range).toBe(7);
  });

  it("过去的运行是实心点并带结果，计划是空心点，二者按时间落在现在线两侧", () => {
    const s = makeScale(tl("7d"), NOW);
    const { past, planned } = jobDots(
      job({
        runs: [
          { id: 1, started_at: NOW - 30 * HOUR, status: "failed" },
          { id: 2, started_at: NOW - 13 * HOUR, status: "success" },
        ],
        planned: [NOW + 11 * HOUR],
      }),
      s
    );
    expect(past.map((d) => [d.id, d.status])).toEqual([
      [1, "failed"],
      [2, "success"],
    ]);
    expect(past[0].x).toBeLessThan(past[1].x);
    expect(past.every((d) => d.x < s.nowX)).toBe(true);
    expect(planned).toHaveLength(1);
    expect(planned[0].x).toBeGreaterThan(s.nowX);
    expect(planned[0].x).toBeCloseTo(s.x(NOW + 11 * HOUR), 9);
    expect(planned[0].at).toBe(NOW + 11 * HOUR);
  });

  it("窗口以外的点不画", () => {
    const s = makeScale(tl("7d"), NOW);
    const { past, planned } = jobDots(
      job({
        runs: [
          { id: 1, started_at: tl("7d").from - 1, status: "success" },
          { id: 2, started_at: NOW - HOUR, status: "success" },
        ],
        planned: [NOW + 31 * HOUR, NOW + 2 * HOUR],
      }),
      s
    );
    expect(past.map((d) => d.id)).toEqual([2]);
    expect(planned.map((d) => d.at)).toEqual([NOW + 2 * HOUR]);
  });

  it("日期轴：7 天每天一个标签；靠近「现在」线的标签隐藏，避免和「现在」标记重叠", () => {
    const s = makeScale(tl("7d"), NOW);
    const days = axisDays(s);
    expect(days[0].label).toBe("10/9 周五");
    expect(days.filter((d) => d.showLabel).map((d) => d.label)).toEqual([
      "10/9 周五",
      "10/10 周六",
      "10/11 周日",
      "10/12 周一",
      "10/13 周二",
      "10/14 周三",
    ]);
    // 今天 00:00 离现在线只有 16 小时，约 8.4%，小于 9% 的阈值，被隐藏；10/15 之后的日期标签也在阈值内或之外
    const today = days.find((d) => d.label === "10/15 周四");
    expect(today?.showLabel).toBe(false);
    expect(days.find((d) => d.label === "10/16 周五")?.showLabel).toBe(false);
  });

  it("日期轴：贴着右端放不下的标签也隐藏，不压住「下次运行」列", () => {
    const late = sec(new Date(2026, 9, 15, 22, 0));
    const t = { window: "7d" as const, from: tl("7d").from, to: late + 30 * HOUR };
    const days = axisDays(makeScale(t, late));
    // 窗口到 10/17 04:00，10/17 的 00:00 在最右边 4 小时处，约 2%
    const last = days[days.length - 1];
    expect(last.label).toBe("10/17 周六");
    expect(last.x).toBeGreaterThan(95);
    expect(last.showLabel).toBe(false);
    // 现在线左边的标签要给「现在」标记和自己的宽度留位置：10/15 00:00 离现在线约 12%，也隐藏；10/14 够远，保留
    expect(days.find((d) => d.label === "10/15 周四")?.showLabel).toBe(false);
    expect(days.find((d) => d.label === "10/14 周三")?.showLabel).toBe(true);
  });

  it("日期轴：30 天只在周一标注，周末有底色", () => {
    const s = makeScale(tl("30d"), NOW);
    const days = axisDays(s);
    const labelled = days.filter((d) => d.showLabel);
    expect(labelled.length).toBeGreaterThan(0);
    expect(labelled.every((d) => new Date(d.t).getDay() === 1)).toBe(true);
    expect(days[0].label).toBe("9/16");
    expect(days.filter((d) => d.weekend).every((d) => [0, 6].includes(new Date(d.t).getDay()))).toBe(true);
  });

  it("日期轴按服务端给的窗口起点逐天划格：设置时区与浏览器不同也和服务端的日界对齐", () => {
    // 设置时区比浏览器早 5 小时：服务端的 10/9 00:00 是浏览器的 10/8 19:00
    const from = sec(new Date(2026, 9, 9, 0, 0)) - 5 * HOUR;
    const s = makeScale({ window: "7d", from, to: NOW + 30 * HOUR }, NOW);
    const days = axisDays(s);
    expect(days[0].x).toBe(0);
    expect(days[0].t).toBe(from * 1000);
    const step = ((24 * HOUR) / (s.to - s.from)) * 100;
    days.forEach((d, i) => expect(d.x).toBeCloseTo(i * step, 6));
    // 标签取格子中间那一刻的日期，仍是服务端的那一天
    expect(days.slice(0, 3).map((d) => d.label)).toEqual(["10/9 周五", "10/10 周六", "10/11 周日"]);
    expect(days[1].weekend).toBe(true);
  });

  it("日期轴的格子宽度加起来不超过 100%", () => {
    const days = axisDays(makeScale(tl("7d"), NOW));
    for (const d of days) expect(d.x + d.width).toBeLessThanOrEqual(100 + 1e-6);
  });

  it("悬停文字：今天、昨天、明天写名字，更早写日期", () => {
    expect(whenLabel(NOW, NOW)).toBe("今天 16:00");
    expect(whenLabel(NOW - 24 * HOUR, NOW)).toBe("昨天 16:00");
    expect(whenLabel(NOW + 11 * HOUR, NOW)).toBe("明天 03:00");
    expect(whenLabel(sec(new Date(2026, 9, 10, 4, 5)), NOW)).toBe("10月10日 04:05");
  });
});
