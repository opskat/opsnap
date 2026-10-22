import { describe, expect, it } from "vitest";
import type { RunItem } from "./api";
import { dayStrip, fmtDurationMs, groupByDay, nextSpeedSample, speedBetween } from "./runs";

const NOW = new Date(2026, 9, 15, 16, 30); // 2026-10-15 16:30 本地时间
const at = (daysAgo: number, h = 4) => Math.floor(new Date(2026, 9, 15 - daysAgo, h, 0).getTime() / 1000);

function run(over: Partial<RunItem>): RunItem {
  return {
    id: 1,
    job_id: 1,
    job_name: "j",
    trigger: "schedule",
    trigger_label: "计划",
    status: "success",
    reason: "",
    notify_failed: false,
    started_at: at(0),
    finished_at: at(0),
    duration_ms: 1000,
    bytes_processed: 0,
    bytes_added: 0,
    snapshot_ids: [],
    steps: [],
    ...over,
  };
}

describe("dayStrip", () => {
  it("14 格，最后一格是今天；没有运行的天是 none，同一天取最差的结果", () => {
    const runs = [
      run({ id: 1, started_at: at(0), status: "success" }),
      run({ id: 2, started_at: at(0, 10), status: "failed" }),
      run({ id: 3, started_at: at(2), status: "warning" }),
      run({ id: 4, started_at: at(2, 9), status: "success" }),
      run({ id: 5, started_at: at(13), status: "success" }),
      run({ id: 6, started_at: at(14), status: "failed" }), // 窗口之外
      run({ id: 7, job_id: 2, started_at: at(1), status: "failed" }), // 别的任务
      run({ id: 8, started_at: at(3), status: "skipped" }), // 跳过不算结果
    ];
    const s = dayStrip(runs, 1, NOW);
    expect(s).toHaveLength(14);
    expect(s[13]).toBe("failed");
    expect(s[12]).toBe("none");
    expect(s[11]).toBe("warning");
    expect(s[10]).toBe("none");
    expect(s[0]).toBe("success");
  });

  it("运行中的一天显示为运行中", () => {
    const s = dayStrip([run({ status: "running" }), run({ id: 2, status: "failed" })], 1, NOW);
    expect(s[13]).toBe("running");
  });
});

describe("speedBetween", () => {
  it("两次采样之间的字节增量除以秒数；第一次采样、时间倒退或数据量减少时没有速度", () => {
    expect(speedBetween({ bytes: 1000, at: 0 }, 6000, 2000)).toBe(2500);
    expect(speedBetween(undefined, 6000, 2000)).toBeNull();
    expect(speedBetween({ bytes: 1000, at: 2000 }, 6000, 2000)).toBeNull();
    expect(speedBetween({ bytes: 9000, at: 0 }, 6000, 2000)).toBeNull();
  });
});

describe("nextSpeedSample", () => {
  it("按两次字节变化的实际间隔算速度，轮询到相同字节时沿用速度", () => {
    const first = nextSpeedSample(undefined, 1000, 0);
    expect(first.speed).toBeNull();
    const unchanged = nextSpeedSample(first, 1000, 3000);
    expect(unchanged).toEqual(first);
    const changed = nextSpeedSample(unchanged, 11_001_000, 10_000);
    expect(changed.speed).toBe(1_100_000);
    expect(nextSpeedSample(changed, 11_001_000, 13_000)).toEqual(changed);
    expect(nextSpeedSample(changed, 11_001_000, 26_000).speed).toBeNull();
  });

  it("字节倒退或时钟倒退时重置采样，不显示虚高速度", () => {
    const sample = nextSpeedSample(undefined, 10_000, 1000);
    expect(nextSpeedSample(sample, 1000, 2000).speed).toBeNull();
    expect(nextSpeedSample(sample, 20_000, 500).speed).toBeNull();
  });
});

describe("fmtDurationMs", () => {
  it.each([
    [800, "1 秒"],
    [19_000, "19 秒"],
    [108_000, "1 分 48 秒"],
    [362_000, "6 分 02 秒"],
    [3_720_000, "1 小时 02 分"],
  ])("%i ms → %s", (ms, text) => {
    expect(fmtDurationMs(ms)).toBe(text);
  });
});

describe("groupByDay", () => {
  it("按天分组，今天和昨天用名字，更早写日期，顺序不变", () => {
    const g = groupByDay([run({ id: 1 }), run({ id: 2, started_at: at(1) }), run({ id: 3, started_at: at(5) })], NOW);
    expect(g.map((x) => x.label)).toEqual(["今天", "昨天", "10月10日"]);
    expect(g[0].rows.map((r) => r.id)).toEqual([1]);
  });
});
