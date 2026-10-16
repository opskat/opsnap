import { describe, expect, it } from "vitest";
import { attentionView, overviewTone, statusSentence } from "./overview";
import type { AttentionItem, OverviewSummary } from "./api";

const sum = (over: Partial<OverviewSummary>): OverviewSummary => ({
  jobs: 6,
  attention: 0,
  paused: 0,
  ok: 6,
  servers: 2,
  repos: 1,
  ...over,
});

describe("状态句", () => {
  it("有任务需要处理：先说数量，再说其余", () => {
    expect(statusSentence(sum({ attention: 3, ok: 3 }))).toEqual({
      lead: "3 个任务需要你处理，",
      rest: "其余 3 个运行正常。",
    });
  });
  it("全部都需要处理时不写「其余」", () => {
    expect(statusSentence(sum({ jobs: 2, attention: 2, ok: 0 }))).toEqual({ lead: "2 个任务需要你处理。", rest: "" });
  });
  it("全部正常也用一句话说明", () => {
    expect(statusSentence(sum({}))).toEqual({ lead: "全部 6 个任务运行正常。", rest: "" });
  });
  it("有暂停的任务：写出暂停数", () => {
    expect(statusSentence(sum({ attention: 1, ok: 4, paused: 1 }))).toEqual({
      lead: "1 个任务需要你处理，",
      rest: "其余 4 个运行正常，另有 1 个已暂停。",
    });
    expect(statusSentence(sum({ ok: 5, paused: 1 }))).toEqual({
      lead: "5 个任务运行正常，",
      rest: "另有 1 个已暂停。",
    });
    expect(statusSentence(sum({ jobs: 2, ok: 0, paused: 2 }))).toEqual({ lead: "2 个任务都已暂停。", rest: "" });
  });
  it("状态点：有失败为红，只有警告或太久没有成功为黄，否则为绿", () => {
    const item = (kind: AttentionItem["kind"]) => ({ kind }) as AttentionItem;
    expect(overviewTone([item("warning"), item("failed")])).toBe("failed");
    expect(overviewTone([item("stale"), item("warning")])).toBe("warning");
    expect(overviewTone([])).toBe("success");
  });
});

const NOW = new Date(2026, 9, 15, 16, 0);
const t = (d: number, h = 4) => Math.floor(new Date(2026, 9, d, h, 0).getTime() / 1000);

function item(over: Partial<AttentionItem>): AttentionItem {
  return {
    job_id: 7,
    job_name: "文件服务",
    source_type: "files",
    kind: "failed",
    reason: "连接超时",
    since: t(13),
    last_success_at: t(12, 2),
    run_id: 1291,
    repo_id: 3,
    repo_not_mounted: false,
    ...over,
  };
}

describe("需要处理的操作", () => {
  it("失败：重新运行 + 查看日志，写明原因、起始时间和最近可用快照", () => {
    const v = attentionView(item({}), NOW);
    expect(v.title).toBe("备份失败");
    expect(v.since).toBe("10月13日 起");
    expect(v.body).toBe("连接超时 最近的可用快照是 10月12日。");
    expect(v.actions).toEqual([
      { key: "log", label: "查看日志", to: "/runs/1291" },
      { key: "rerun", label: "重新运行" },
    ]);
  });
  it("存储位置没有挂载：给出检查存储位置，不再给查看日志", () => {
    const v = attentionView(item({ reason: "没有挂载", repo_not_mounted: true }), NOW);
    expect(v.actions).toEqual([
      { key: "repo", label: "检查存储位置", to: "/repos" },
      { key: "rerun", label: "重新运行" },
    ]);
  });
  it("警告：只有查看日志，快照可用所以没有重新运行", () => {
    const v = attentionView(item({ kind: "warning", reason: "跳过了 3 个文件", since: t(15) }), NOW);
    expect(v.title).toBe("备份有警告");
    expect(v.since).toBe("今天 04:00 起");
    expect(v.actions.map((a) => a.key)).toEqual(["log"]);
  });
  it("太久没有成功：说明上次成功时间；从未成功过也要说清", () => {
    const stale = attentionView(item({ kind: "stale", reason: "太久没有成功运行" }), NOW);
    expect(stale.title).toBe("太久没有成功");
    expect(stale.body).toContain("上次成功是 10月12日");
    expect(stale.actions.map((a) => a.key)).toEqual(["log", "rerun"]);
    const never = attentionView(item({ kind: "stale", last_success_at: 0, run_id: 0 }), NOW);
    expect(never.body).toContain("还没有成功运行过");
    expect(never.actions.map((a) => a.key)).toEqual(["rerun"]);
  });
});
