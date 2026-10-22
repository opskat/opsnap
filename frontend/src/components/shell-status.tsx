// 面板外壳的实时数据：「备份任务」旁需要处理的任务数，以及侧边栏「正在备份」卡片。
// 每 5 秒轮询一次；换页和页面里的操作（暂停、删除、运行）可以立即触发一次刷新。
import * as React from "react";
import { useLocation } from "react-router";
import { Shell, type NavCounts, type RunningInfo } from "@/components/shell";
import { api, type JobItem, type RunList } from "@/lib/api";
import { fmtBytes } from "@/lib/format";
import { nextSpeedSample, type SpeedSample } from "@/lib/runs";

const POLL_MS = 5000;

const RefreshContext = React.createContext<() => void>(() => {});

/** 侧边栏轮询到的正在运行的任务 ID；还没轮询到时为 null */
export const RunningJobsContext = React.createContext<ReadonlySet<number> | null>(null);

/** 正在运行的任务（每 5 秒更新）；页面据此及时禁用「立即运行」 */
export function useRunningJobs() {
  return React.useContext(RunningJobsContext);
}

/** 页面里的操作改变了任务或运行状态后调用，侧边栏立即刷新 */
export function useShellRefresh() {
  return React.useContext(RefreshContext);
}

interface Status {
  attention: number;
  running: RunningInfo | null;
  runningJobs: ReadonlySet<number> | null;
}

export function ShellStatus({ children }: { children: React.ReactNode }) {
  const [status, setStatus] = React.useState<Status>({ attention: 0, running: null, runningJobs: null });
  const [tick, setTick] = React.useState(0);
  const refresh = React.useCallback(() => setTick((n) => n + 1), []);

  // 换页后刷新一次：在渲染期间对比
  const { pathname } = useLocation();
  const [prevPath, setPrevPath] = React.useState(pathname);
  if (pathname !== prevPath) {
    setPrevPath(pathname);
    refresh();
  }

  // 上一次轮询的结果与采样；只在轮询里读写，不放进 setState 的更新函数（严格模式下会被调用两次）
  const lastRef = React.useRef<Status>(status);
  const sampleRef = React.useRef<(SpeedSample & { runId: number }) | null>(null);

  React.useEffect(() => {
    let cancelled = false;
    // 上一次还没返回时跳过这一轮：接口慢于 5 秒时请求不叠加，旧响应也不会覆盖新结果
    let inFlight = false;
    async function poll() {
      if (inFlight) return;
      inFlight = true;
      // 任何一个接口读不到就保留上一次的结果，不打扰当前页面
      const [jobs, runs] = await Promise.allSettled([
        api.jobs.list("attention"),
        api.runs.list({ status: "running", size: 100 }),
      ]);
      inFlight = false;
      if (cancelled) return;
      const next = nextStatus(lastRef.current, jobs, runs, sampleRef);
      lastRef.current = next;
      setStatus(next);
    }
    void poll();
    const timer = window.setInterval(() => void poll(), POLL_MS);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [tick]);

  const counts: NavCounts = status.attention ? { "/jobs": status.attention } : {};
  return (
    <RefreshContext.Provider value={refresh}>
      <RunningJobsContext.Provider value={status.runningJobs}>
        <Shell counts={counts} running={status.running}>
          {children}
        </Shell>
      </RunningJobsContext.Provider>
    </RefreshContext.Provider>
  );
}

/** 由一次轮询的结果算出新的状态；读不到的部分沿用上一次，并更新处理速度的采样 */
function nextStatus(
  prev: Status,
  jobs: PromiseSettledResult<{ list: JobItem[] | null }>,
  runs: PromiseSettledResult<RunList>,
  sampleRef: React.RefObject<(SpeedSample & { runId: number }) | null>
): Status {
  const attention = jobs.status === "fulfilled" ? (jobs.value.list ?? []).length : prev.attention;
  if (runs.status !== "fulfilled") return { attention, running: prev.running, runningJobs: prev.runningJobs };
  const list = runs.value.list ?? [];
  const ids = list.map((r) => r.job_id);
  // 集合内容没变时沿用原对象，页面不必因此刷新
  const runningJobs =
    prev.runningJobs && prev.runningJobs.size === new Set(ids).size && ids.every((id) => prev.runningJobs?.has(id))
      ? prev.runningJobs
      : new Set(ids);
  const first = list[0];
  if (!first) {
    sampleRef.current = null;
    return { attention, running: null, runningJobs };
  }
  const now = Date.now();
  const last = sampleRef.current;
  const sample = nextSpeedSample(last && last.runId === first.id ? last : undefined, first.bytes_processed, now);
  sampleRef.current = { ...sample, runId: first.id };
  const parts = [`已处理 ${fmtBytes(first.bytes_processed)}`];
  if (sample.speed !== null) parts.push(`${fmtBytes(sample.speed)}/s`);
  let progress = parts.join("，");
  if (list.length > 1) progress += `，另有 ${list.length - 1} 个在运行`;
  return { attention, running: { runId: first.id, jobName: first.job_name, progress }, runningJobs };
}
