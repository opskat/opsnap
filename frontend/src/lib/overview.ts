// 概览页的文案与动作：状态句、需要处理的每一项怎么写、有哪些直接操作。纯函数，不依赖界面。
import type { AttentionItem, OverviewSummary } from "./api";
import { fmtAgo } from "./format";
import type { RunStatus } from "@/components/bits";

/** 顶部状态句，lead 用墨色、rest 用灰色 */
export function statusSentence(s: OverviewSummary): { lead: string; rest: string } {
  const paused = s.paused > 0 ? `另有 ${s.paused} 个已暂停` : "";
  if (s.attention > 0) {
    if (s.ok > 0) {
      return {
        lead: `${s.attention} 个任务需要你处理，`,
        rest: `其余 ${s.ok} 个运行正常${paused ? `，${paused}` : ""}。`,
      };
    }
    return { lead: `${s.attention} 个任务需要你处理${paused ? "，" : "。"}`, rest: paused ? `${paused}。` : "" };
  }
  if (s.ok > 0) {
    if (paused) return { lead: `${s.ok} 个任务运行正常，`, rest: `${paused}。` };
    return { lead: `全部 ${s.ok} 个任务运行正常。`, rest: "" };
  }
  return { lead: `${s.paused} 个任务都已暂停。`, rest: "" };
}

/** 状态句前面的圆点：有失败为红，只有警告或太久没有成功为黄，否则为绿 */
export function overviewTone(attention: Pick<AttentionItem, "kind">[]): RunStatus {
  if (attention.some((a) => a.kind === "failed")) return "failed";
  return attention.length > 0 ? "warning" : "success";
}

export type AttentionAction =
  | { key: "log"; label: string; to: string }
  | { key: "repo"; label: string; to: string }
  | { key: "rerun"; label: string };

export interface AttentionView {
  title: string;
  /** 「10月13日 起」 */
  since: string;
  body: string;
  actions: AttentionAction[];
}

const TITLE = { failed: "备份失败", warning: "备份有警告", stale: "太久没有成功" } as const;

/**
 * 一项「需要处理」怎么写、能做什么：
 * 存储位置没有挂载时给「检查存储位置」，否则有运行记录就给「查看日志」；
 * 失败和太久没有成功可以「重新运行」，警告的快照可用，不给。
 */
export function attentionView(it: AttentionItem, now: Date): AttentionView {
  const last = it.last_success_at ? fmtAgo(it.last_success_at, now) : "";
  let body: string;
  if (it.kind === "stale") {
    body = last ? `上次成功是 ${last}。` : "还没有成功运行过。";
  } else if (it.kind === "failed") {
    const why = it.reason || "运行失败";
    body = last ? `${why} 最近的可用快照是 ${last}。` : `${why} 还没有可用的快照。`;
  } else {
    body = it.reason || "运行时有警告，快照可正常使用。";
  }

  const actions: AttentionAction[] = [];
  if (it.repo_not_mounted) actions.push({ key: "repo", label: "检查存储位置", to: "/repos" });
  else if (it.run_id) actions.push({ key: "log", label: "查看日志", to: `/runs/${it.run_id}` });
  if (it.kind !== "warning") actions.push({ key: "rerun", label: "重新运行" });
  return { title: TITLE[it.kind], since: `${fmtAgo(it.since, now)} 起`, body, actions };
}
