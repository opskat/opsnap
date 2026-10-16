import { cn } from "@/lib/utils";
import type { RunStatus } from "@/components/bits";

const FILL: Record<RunStatus, string> = {
  success: "bg-ok-fill",
  warning: "bg-warn-fill",
  failed: "bg-fail-fill",
  running: "bg-brand",
  none: "bg-idle",
};
const LABEL: Record<RunStatus, string> = {
  success: "成功",
  warning: "有警告",
  failed: "失败",
  running: "运行中",
  none: "没有运行",
};

/** 最近 14 天的运行带：每天一格，最后一格是今天 */
export function RunStrip({ days, className }: { days: RunStatus[]; className?: string }) {
  const total = days.length;
  return (
    <div className={cn("flex gap-[3px]", className)} role="img" aria-label="最近 14 天运行结果">
      {days.map((s, i) => (
        <span
          key={i}
          title={`${total - 1 - i === 0 ? "今天" : `${total - 1 - i} 天前`}：${LABEL[s]}`}
          className={cn("h-3.5 w-[7px] rounded-[2px]", FILL[s])}
        />
      ))}
    </div>
  );
}
