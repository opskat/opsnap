// 运行时间线：每个任务一条轨道，左边是已经发生的运行（实心点，颜色表示结果），
// 右边是计划中的运行（空心点），主题色竖线是「现在」。点位计算见 lib/timeline.ts。
import { Link } from "react-router";
import { TypeTile, type RunStatus } from "@/components/bits";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import type { OverviewTimeline } from "@/lib/api";
import { scheduleText, sourceOf } from "@/lib/jobs";
import { runStatusView } from "@/lib/runs";
import { axisDays, jobDots, makeScale, whenLabel } from "@/lib/timeline";
import { cn } from "@/lib/utils";

const FILL: Record<RunStatus, string> = {
  success: "bg-ok-fill",
  warning: "bg-warn-fill",
  failed: "bg-fail-fill",
  running: "bg-brand",
  none: "bg-idle",
};

const COLS = "grid-cols-[220px_1fr_104px]";

export function Timeline({ data, now }: { data: OverviewTimeline; now: number }) {
  const scale = makeScale(data, now);
  const days = axisDays(scale);
  const dot = scale.range === 7 ? "size-[9px]" : "size-[6px]";
  const bigDot = scale.range === 7 ? "size-[11px]" : "size-[8px]";

  return (
    <div className="relative">
      {/* 轴 */}
      <div className={cn("grid border-b border-line", COLS)}>
        <div />
        <div className="relative h-8">
          {days.map(
            (d) =>
              d.showLabel && (
                <span
                  key={d.t}
                  className="absolute bottom-2 pl-1.5 text-[11.5px] whitespace-nowrap text-ink-3 num"
                  style={{ left: `${d.x}%` }}
                >
                  {d.label}
                </span>
              )
          )}
        </div>
        <div className="flex items-end pb-2 pl-4 text-[11.5px] text-ink-3">下次运行</div>
      </div>

      <div className="relative">
        {/* 背景：日分隔、周末底色、计划区斜纹、现在线 */}
        <div className="pointer-events-none absolute inset-y-0 right-[104px] left-[220px]" aria-hidden>
          {days.map((d) => (
            <div
              key={d.t}
              className={cn("absolute inset-y-0 border-l border-line", d.weekend && "bg-sunken/50")}
              style={{ left: `${d.x}%`, width: `${d.width}%` }}
            />
          ))}
          <div
            className="absolute inset-y-0 right-0 bg-[repeating-linear-gradient(135deg,transparent_0_6px,color-mix(in_oklab,var(--line)_70%,transparent)_6px_7px)]"
            style={{ left: `${scale.nowX}%` }}
          />
          <div
            data-testid="now-line"
            className="absolute inset-y-0 w-[2px] -translate-x-1/2 bg-brand"
            style={{ left: `${scale.nowX}%` }}
          />
          <div
            className="absolute -top-[30px] -translate-x-1/2 rounded-full bg-brand px-2 py-0.5 text-[11px] font-medium whitespace-nowrap text-primary-foreground num"
            style={{ left: `${scale.nowX}%` }}
          >
            {whenLabel(now, now)}
          </div>
        </div>

        <ul>
          {data.jobs.map((j, li) => {
            const { past, planned } = jobDots(j, scale);
            const [nextDay, nextTime] = j.next_run_at ? whenLabel(j.next_run_at, now).split(" ") : ["", ""];
            return (
              <li
                key={j.job_id}
                className={cn("group grid border-b border-line last:border-b-0 hover:bg-sunken/40", COLS)}
              >
                <Link to={`/jobs/${j.job_id}/edit`} className="flex min-w-0 items-center gap-3 py-3 pr-3">
                  <TypeTile type={sourceOf(j.source_type).tile} />
                  <span className="min-w-0">
                    <span
                      className={cn(
                        "block truncate font-medium group-hover:underline",
                        j.last_status === "failed" && "text-fail"
                      )}
                    >
                      {j.name}
                    </span>
                    <span className="block truncate text-xs text-ink-3">
                      {j.paused ? "已暂停" : scheduleText(j.schedule)}
                    </span>
                  </span>
                </Link>
                <div
                  className="relative [animation:lane-in_700ms_cubic-bezier(.2,.7,.2,1)_both]"
                  style={{ animationDelay: `${li * 45}ms` }}
                >
                  <div className="absolute top-1/2 h-px bg-line-strong" style={{ left: 0, width: `${scale.nowX}%` }} />
                  {past.map((r) => {
                    const view = runStatusView(r.status);
                    const text = `${whenLabel(r.at, now)}，${view.text}`;
                    return (
                      <Tooltip key={r.id}>
                        <TooltipTrigger asChild>
                          <Link
                            to={`/runs/${r.id}`}
                            aria-label={text}
                            data-kind="past"
                            data-status={r.status}
                            className={cn(
                              "absolute top-1/2 -translate-x-1/2 -translate-y-1/2 rounded-full ring-2 ring-surface transition-transform hover:scale-150",
                              r.status === "failed" ? bigDot : dot,
                              FILL[view.status]
                            )}
                            style={{ left: `${r.x}%` }}
                          >
                            {r.status === "running" && (
                              <span className="absolute inset-0 rounded-full bg-brand [animation:dot-ring_1.6s_ease-out_infinite]" />
                            )}
                          </Link>
                        </TooltipTrigger>
                        <TooltipContent side="top">{text}</TooltipContent>
                      </Tooltip>
                    );
                  })}
                  {planned.map((p) => {
                    const text = `计划 ${whenLabel(p.at, now)}`;
                    return (
                      <Tooltip key={p.at}>
                        <TooltipTrigger asChild>
                          <span
                            role="img"
                            tabIndex={0}
                            aria-label={text}
                            data-kind="planned"
                            className={cn(
                              "absolute top-1/2 -translate-x-1/2 -translate-y-1/2 rounded-full border-[1.5px] border-ink-3 bg-surface",
                              dot
                            )}
                            style={{ left: `${p.x}%` }}
                          />
                        </TooltipTrigger>
                        <TooltipContent side="top">{text}</TooltipContent>
                      </Tooltip>
                    );
                  })}
                </div>
                <div className="flex flex-col justify-center pl-4 text-[13px] num">
                  {j.next_run_at ? (
                    <>
                      <span className="font-medium">{nextTime}</span>
                      <span className="text-xs text-ink-3">{nextDay}</span>
                    </>
                  ) : (
                    <span className="text-ink-3">—</span>
                  )}
                </div>
              </li>
            );
          })}
        </ul>
      </div>
    </div>
  );
}
