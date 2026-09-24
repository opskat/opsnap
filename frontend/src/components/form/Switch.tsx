import type { ComponentProps } from "react";

import { cn } from "@/lib/utils";

/** 开关：role="switch"，aria-label 或外层 label 提供名称 */
export function Switch({
  checked,
  onCheckedChange,
  className,
  ...props
}: Omit<ComponentProps<"button">, "onClick" | "role"> & {
  checked: boolean;
  onCheckedChange: (checked: boolean) => void;
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      onClick={() => onCheckedChange(!checked)}
      className={cn(
        "flex h-4.5 w-8 shrink-0 items-center rounded-full p-0.5 transition-colors disabled:opacity-50",
        checked ? "justify-end bg-primary" : "justify-start bg-track",
        className
      )}
      {...props}
    >
      <span className="size-3.5 rounded-full bg-primary-foreground" />
    </button>
  );
}
