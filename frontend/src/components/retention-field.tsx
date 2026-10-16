import { Input } from "@/components/ui/input";
import { LONG_TERM, MAX_COUNT, retentionCounts, timelineMarks, oldestText, type RetentionRule } from "@/lib/retention";
import { cn } from "@/lib/utils";

/** 保留规则：一句话写结果，三个数字（自定义时可改），再配一条时间轴示意 */
export function RetentionField({
  rule,
  estimate,
  onChange,
  describedBy,
}: {
  rule: RetentionRule;
  /** 稳定后保留的快照数；算不出来时为 null */
  estimate: number | null;
  onChange: (r: RetentionRule) => void;
  describedBy?: string;
}) {
  if (rule.preset === "7d" || rule.preset === "30d") {
    return (
      <div className="max-w-xl">
        <p className="text-[13px] text-ink-2">
          保留最近 {rule.preset === "7d" ? 7 : 30} 天内的每一次备份，更早的删除。
          {estimate != null && <span className="ml-1 text-ink-3">共约 {estimate} 个快照。</span>}
        </p>
        <Timeline rule={rule} />
      </div>
    );
  }
  const c = retentionCounts(rule) ?? LONG_TERM;
  const custom = rule.preset === "custom";
  const num = (key: "days" | "weeks" | "months", label: string) =>
    custom ? (
      <Input
        value={String(rule[key])}
        inputMode="numeric"
        aria-label={label}
        aria-describedby={describedBy}
        onChange={(e) => {
          const n = Number(e.target.value.replace(/\D/g, "")) || 0;
          onChange({ ...rule, [key]: Math.min(n, MAX_COUNT) });
        }}
        className="mx-0.5 inline-flex h-7 w-14 bg-surface px-1 text-center num"
      />
    ) : (
      <b className="font-semibold text-ink num">{c[key]}</b>
    );

  return (
    <div className="max-w-xl">
      <ul className="flex flex-col gap-2 text-[13px] text-ink-2" aria-label="保留规则">
        <li className="flex items-center gap-2">
          <span className="size-2 shrink-0 rounded-full bg-brand" aria-hidden />
          <span>最近 {num("days", "最近几天")} 天：每天留一份</span>
        </li>
        <li className="flex items-center gap-2">
          <span className="size-2 shrink-0 rounded-full bg-ink-2" aria-hidden />
          <span>再往前到 {num("weeks", "往前到几周")} 周：每周留一份</span>
        </li>
        <li className="flex items-center gap-2">
          <span className="size-2.5 shrink-0 rounded-full bg-ink" aria-hidden />
          <span>再往前到 {num("months", "往前到几个月")} 个月：每月留一份</span>
        </li>
      </ul>
      <Timeline rule={rule} />
      {estimate != null && <p className="mt-2 text-xs text-ink-3">共约 {estimate} 个快照，其余自动删除。</p>}
    </div>
  );
}

/** 时间轴示意：最左是最早保留到的时刻，最右是现在 */
function Timeline({ rule }: { rule: RetentionRule }) {
  const marks = timelineMarks(rule);
  return (
    <>
      <div data-testid="retention-timeline" className="relative mt-4 h-5" aria-hidden>
        <div className="absolute inset-x-0 top-1/2 h-px bg-line-strong" />
        {marks.map((m, i) => (
          <span
            key={i}
            className={cn(
              "absolute top-1/2 -translate-x-1/2 -translate-y-1/2 rounded-full ring-2 ring-surface",
              m.kind === "month" && "size-2.5 bg-ink",
              m.kind === "week" && "size-2 bg-ink-2",
              m.kind === "day" && "size-2 bg-brand"
            )}
            style={{ left: `${m.x}%` }}
          />
        ))}
      </div>
      <div className="mt-1 flex justify-between text-[11px] text-ink-3 num" aria-hidden>
        <span>{oldestText(rule)}前</span>
        <span>现在</span>
      </div>
    </>
  );
}
