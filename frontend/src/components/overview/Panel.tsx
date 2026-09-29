import { useId, type ReactNode } from "react";

import { cn } from "@/lib/utils";

/** 加载中的占位块：保持区块的位置与大致高度，不闪空白 */
export function Placeholder({ className }: { className?: string }) {
  return <span aria-hidden className={cn("block animate-pulse rounded-sm bg-muted", className)} />;
}

/** 概览里的一个区块：标题、可选的右侧操作与内容；加载中时 aria-busy */
export function Panel({
  title,
  actions,
  loading,
  className,
  children,
}: {
  title: string;
  actions?: ReactNode;
  loading: boolean;
  className?: string;
  children: ReactNode;
}) {
  const id = useId();
  return (
    <section
      aria-labelledby={id}
      aria-busy={loading}
      className={cn("flex min-w-0 flex-col rounded-lg border bg-card", className)}
    >
      <div className="flex min-h-14 items-center justify-between gap-3 px-5 py-3">
        <h2 id={id} className="text-md font-semibold">
          {title}
        </h2>
        {actions}
      </div>
      {children}
    </section>
  );
}
