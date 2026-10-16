import { describe, expect, it } from "vitest";
import {
  MAX_COUNT,
  estimateSnapshots,
  retentionCounts,
  retentionSentence,
  timelineMarks,
  type RetentionRule,
} from "./retention";

// 按 restic forget 的语义手算的期望值；规则到 forget 参数的映射见 internal/pkg/retention 的 ForgetArgs。
// 2026-10-15 是周四；ISO 周从周一开始。
const NOW = new Date(Date.UTC(2026, 9, 15, 2, 0, 0));
const HOUR = 3600_000;
const DAY = 24 * HOUR;
const WEEK = 7 * DAY;

const rule = (preset: RetentionRule["preset"], days = 0, weeks = 0, months = 0): RetentionRule => ({
  preset,
  days,
  weeks,
  months,
});

describe("保留估算：持续按固定间隔备份时 forget 之后剩下的快照数", () => {
  const cases: [string, RetentionRule, number, number][] = [
    ["7 天 × 每小时", rule("7d"), HOUR, 168],
    ["7 天 × 每 6 小时", rule("7d"), 6 * HOUR, 28],
    ["7 天 × 每天", rule("7d"), DAY, 7],
    ["7 天 × 每周", rule("7d"), WEEK, 1],
    ["30 天 × 每天", rule("30d"), DAY, 30],
    ["30 天 × 每周", rule("30d"), WEEK, 5],
    ["长期 × 每天（不是 7+4+6）", rule("long"), DAY, 14],
    ["长期 × 每小时", rule("long"), HOUR, 14],
    ["长期 × 每周", rule("long"), WEEK, 11],
    ["自定义 14 天、8 周 × 每天", rule("custom", 14, 8), DAY, 19],
    ["自定义只留 3 个月 × 每天", rule("custom", 0, 0, 3), DAY, 3],
  ];
  it.each(cases)("%s", (_name, r, interval, want) => {
    expect(estimateSnapshots(r, interval, NOW)).toBe(want);
  });

  it("间隔无效或自定义全为 0 时返回 null", () => {
    expect(estimateSnapshots(rule("7d"), 0, NOW)).toBeNull();
    expect(estimateSnapshots(rule("7d"), -HOUR, NOW)).toBeNull();
    expect(estimateSnapshots(rule("custom"), HOUR, NOW)).toBeNull();
  });

  it("后端会拒绝的规则不估算，返回 null", () => {
    expect(estimateSnapshots(rule("custom", -1, 4), DAY, NOW)).toBeNull();
    expect(estimateSnapshots(rule("custom", 7, 1.5), DAY, NOW)).toBeNull();
    expect(estimateSnapshots(rule("custom", 7, 0, MAX_COUNT + 1), DAY, NOW)).toBeNull();
    expect(estimateSnapshots(rule("custom", Number.NaN), DAY, NOW)).toBeNull();
    expect(estimateSnapshots(rule("90d" as RetentionRule["preset"]), DAY, NOW)).toBeNull();
    // 上限本身是允许的
    expect(estimateSnapshots(rule("custom", MAX_COUNT), DAY, NOW)).toBe(MAX_COUNT);
  });
});

describe("retentionCounts", () => {
  it("长期固定为 7 天、4 周、6 个月，忽略自定义数字", () => {
    expect(retentionCounts(rule("long", 1, 1, 1))).toEqual({ days: 7, weeks: 4, months: 6 });
    expect(retentionCounts(rule("custom", 3, 8, 12))).toEqual({ days: 3, weeks: 8, months: 12 });
  });
});

describe("retentionSentence", () => {
  it("预设写成保留期内的每一次备份", () => {
    expect(retentionSentence(rule("7d"))).toBe("最近 7 天的全部备份");
    expect(retentionSentence(rule("30d"))).toBe("最近 30 天的全部备份");
  });
  it("长期和自定义写成快照数估算", () => {
    expect(retentionSentence(rule("long"), 14)).toBe("约 14 个快照，最早 6 个月前");
    expect(retentionSentence(rule("custom", 14, 8), 19)).toBe("约 19 个快照，最早 8 周前");
    expect(retentionSentence(rule("custom", 14), 14)).toBe("约 14 个快照，最早 14 天前");
  });
});

describe("timelineMarks", () => {
  it("每个粒度一组点位，位置在 0-100 之间，越近越靠右", () => {
    const marks = timelineMarks(rule("long"));
    expect(marks.filter((m) => m.kind === "day")).toHaveLength(7);
    expect(marks.filter((m) => m.kind === "week").length).toBeGreaterThan(0);
    expect(marks.filter((m) => m.kind === "month")).toHaveLength(6);
    for (const m of marks) {
      expect(m.x).toBeGreaterThanOrEqual(0);
      expect(m.x).toBeLessThanOrEqual(100);
    }
    const today = marks.find((m) => m.kind === "day")!;
    expect(today.x).toBe(100);
    const oldest = Math.min(...marks.map((m) => m.x));
    expect(oldest).toBe(0);
  });
  it("只留每月时没有日和周的点", () => {
    const marks = timelineMarks(rule("custom", 0, 0, 3));
    expect(marks.map((m) => m.kind)).toEqual(["month", "month", "month"]);
  });

  it("7 天、30 天按时间窗口保留：窗口里每天一个点，同样配时间轴", () => {
    for (const [preset, n] of [
      ["7d", 7],
      ["30d", 30],
    ] as const) {
      const marks = timelineMarks(rule(preset));
      expect(marks).toHaveLength(n);
      expect(marks.every((m) => m.kind === "day")).toBe(true);
      expect(Math.max(...marks.map((m) => m.x))).toBe(100);
      expect(Math.min(...marks.map((m) => m.x))).toBeGreaterThanOrEqual(0);
    }
  });
});
