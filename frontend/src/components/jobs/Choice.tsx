import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

/** 单选项（卡片或列表行）：role="radio"，禁用时仍显示，用于“后续版本支持”或状态异常的项 */
export function Choice({
  checked,
  disabled,
  onSelect,
  className,
  children,
}: {
  checked: boolean;
  disabled?: boolean;
  onSelect: () => void;
  className?: string;
  children: ReactNode;
}) {
  return (
    <button
      type="button"
      role="radio"
      aria-checked={checked}
      disabled={disabled}
      onClick={onSelect}
      className={cn(
        "flex w-full items-start gap-3 text-left transition-colors outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50",
        checked ? "bg-accent" : !disabled && "hover:bg-accent/60",
        disabled && "cursor-not-allowed",
        className
      )}
    >
      <span className="min-w-0 flex-1">{children}</span>
      <RadioDot checked={checked} disabled={disabled} />
    </button>
  );
}

function RadioDot({ checked, disabled }: { checked: boolean; disabled?: boolean }) {
  return (
    <span
      aria-hidden
      className={cn(
        "mt-0.5 flex size-4 shrink-0 items-center justify-center rounded-full border",
        checked ? "border-primary bg-primary" : "border-input bg-background",
        disabled && "opacity-50"
      )}
    >
      {checked && <span className="size-1.5 rounded-full bg-primary-foreground" />}
    </span>
  );
}

/** “后续版本支持”标记 */
export function SoonBadge() {
  const { t } = useTranslation();
  return (
    <span className="rounded-sm bg-pending-soft px-1.75 py-0.5 text-2xs font-medium text-pending">
      {t("jobs.wizard.soon")}
    </span>
  );
}

const badgeStyle: Record<string, string> = {
  ok: "bg-success-soft text-success",
  warn: "bg-warning-soft text-warning",
  fail: "bg-destructive-soft text-destructive",
};

/** 状态徽标：ok / warn / fail 三档配色，文字由调用方给出 */
export function StatusBadge({ tone, children }: { tone: "ok" | "warn" | "fail"; children: ReactNode }) {
  return (
    <span className={cn("shrink-0 rounded-sm px-1.75 py-0.5 text-2xs font-medium", badgeStyle[tone])}>{children}</span>
  );
}

/** 区块标题：标题 + 可选说明，右侧可放附加信息 */
export function SectionTitle({ title, hint, aside }: { title: ReactNode; hint?: ReactNode; aside?: ReactNode }) {
  return (
    <div className="flex items-end justify-between gap-3">
      <div className="flex flex-col gap-0.5">
        <h2 className="text-base font-semibold">{title}</h2>
        {hint && <p className="text-xs text-muted-foreground">{hint}</p>}
      </div>
      {aside}
    </div>
  );
}

/** 字段旁的错误提示 */
export function FieldError({ id, children }: { id?: string; children?: ReactNode }) {
  if (!children) return null;
  return (
    <p id={id} className="text-xs text-destructive">
      {children}
    </p>
  );
}

/** 区域内的加载失败：原因 + 重试 */
export function LoadError({ message, onRetry }: { message: string; onRetry: () => void }) {
  const { t } = useTranslation();
  return (
    <div className="flex items-center justify-between gap-3 rounded-md bg-destructive-soft px-4 py-3">
      <p className="text-sm text-destructive">{message}</p>
      <Button variant="outline" size="sm" onClick={onRetry}>
        {t("common.retry")}
      </Button>
    </div>
  );
}
