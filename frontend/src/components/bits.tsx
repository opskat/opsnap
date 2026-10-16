// 面板通用的小组件：页面标题、分区、面板、表单字段、状态点、数据源图标等。
// 外观只由 styles.css 的 token 决定。
import * as React from "react";
import { Icon, type IconifyIcon } from "@iconify/react";
import mysqlIcon from "@iconify-icons/logos/mysql-icon";
import mariadbIcon from "@iconify-icons/logos/mariadb-icon";
import postgresqlIcon from "@iconify-icons/logos/postgresql";
import redisIcon from "@iconify-icons/logos/redis";
import mongodbIcon from "@iconify-icons/logos/mongodb-icon";
import { Check, Copy, FolderClosed } from "lucide-react";
import { DialogContent } from "@/components/ui/dialog";
import { cn } from "@/lib/utils";

/** 运行状态 */
export type RunStatus = "success" | "warning" | "failed" | "running" | "none";
/** 数据源类型 */
export type SourceType = "mysql" | "mariadb" | "postgres" | "redis" | "mongo" | "files";

export const STATUS_TEXT: Record<RunStatus, string> = {
  success: "成功",
  warning: "有警告",
  failed: "失败",
  running: "运行中",
  none: "未运行",
};

const DOT: Record<RunStatus, string> = {
  success: "bg-ok-fill",
  warning: "bg-warn-fill",
  failed: "bg-fail-fill",
  running: "bg-brand",
  none: "bg-idle",
};

/** 状态圆点；只作装饰，旁边必须配文字 */
export function Dot({ status, className }: { status: RunStatus; className?: string }) {
  return (
    <span className={cn("relative inline-flex size-2 shrink-0 rounded-full", DOT[status], className)} aria-hidden>
      {status === "running" && (
        <span className="absolute inset-0 rounded-full bg-brand [animation:dot-ring_1.6s_ease-out_infinite]" />
      )}
    </span>
  );
}

/** 状态：圆点 + 文字，从不只靠颜色区分 */
export function Status({
  status,
  className,
  children,
}: {
  status: RunStatus;
  className?: string;
  children?: React.ReactNode;
}) {
  const tone = {
    success: "text-ok",
    warning: "text-warn",
    failed: "text-fail",
    running: "text-brand-ink",
    none: "text-ink-3",
  }[status];
  return (
    <span className={cn("inline-flex items-center gap-1.5 text-[13px] font-medium", tone, className)}>
      <Dot status={status} />
      {children ?? STATUS_TEXT[status]}
    </span>
  );
}

const LOGO: Record<Exclude<SourceType, "files">, IconifyIcon> = {
  mysql: mysqlIcon,
  mariadb: mariadbIcon,
  postgres: postgresqlIcon,
  redis: redisIcon,
  mongo: mongodbIcon,
};

/** 数据源图标：官方 logo 放在统一的白底方块里，文件用文件夹 */
export function TypeTile({
  type,
  size = "md",
  className,
}: {
  type: SourceType;
  size?: "sm" | "md" | "lg";
  className?: string;
}) {
  const px = { sm: 14, md: 18, lg: 24 }[size];
  return (
    <span
      className={cn(
        "inline-flex shrink-0 items-center justify-center rounded-md border border-line bg-white dark:bg-[#f4f4f2]",
        size === "sm" && "size-6",
        size === "md" && "size-8",
        size === "lg" && "size-11",
        className
      )}
      aria-hidden
    >
      {type === "files" ? (
        <FolderClosed
          style={{ width: px, height: px }}
          className="fill-amber-400/80 text-amber-600"
          strokeWidth={1.5}
        />
      ) : (
        <Icon icon={LOGO[type]} width={px} height={px} />
      )}
    </span>
  );
}

export function PageTitle({
  title,
  lead,
  actions,
  back,
}: {
  title: React.ReactNode;
  lead?: React.ReactNode;
  actions?: React.ReactNode;
  back?: React.ReactNode;
}) {
  return (
    <div className="mb-8">
      {back}
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div className="min-w-0">
          <h1 className="text-[26px] leading-tight font-semibold tracking-[-0.01em]">{title}</h1>
          {lead && <p className="mt-2 max-w-[68ch] text-ink-2">{lead}</p>}
        </div>
        {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
      </div>
    </div>
  );
}

export function Section({
  title,
  aside,
  children,
  className,
}: {
  title: React.ReactNode;
  aside?: React.ReactNode;
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <section className={cn("mt-12 first:mt-0", className)}>
      <div className="mb-3 flex items-baseline justify-between gap-4">
        <h2 className="text-[15px] font-semibold">{title}</h2>
        {aside}
      </div>
      {children}
    </section>
  );
}

/** 白色面板 */
export function Sheet({ className, ...props }: React.ComponentProps<"div">) {
  return <div className={cn("rounded-xl border border-line bg-surface", className)} {...props} />;
}

export function Skeleton({ className }: { className?: string }) {
  return <div className={cn("animate-pulse rounded-md bg-sunken", className)} aria-hidden />;
}

/** 等宽字体，只用于路径、地址、快照 ID、命令和日志 */
export function Mono({ className, ...props }: React.ComponentProps<"span">) {
  return <span className={cn("font-mono text-[12.5px]", className)} {...props} />;
}

type ControlProps = { id?: string; "aria-describedby"?: string; "aria-invalid"?: boolean | "true" | "false" };

/**
 * 表单字段：标题、控件、下方的提示或错误。
 * children 是单个控件时自动关联 id、aria-describedby 和 aria-invalid；
 * 控件包在其他元素里时，传 htmlFor 并给控件设置同样的 id。
 */
export function Field({
  label,
  hint,
  error,
  htmlFor,
  children,
  className,
}: {
  label: string;
  hint?: React.ReactNode;
  error?: string | null;
  htmlFor?: string;
  children: React.ReactNode;
  className?: string;
}) {
  const auto = React.useId();
  const single = !htmlFor && React.isValidElement<ControlProps>(children) ? children : null;
  const id = htmlFor ?? single?.props.id ?? auto;
  const msgId = `${id}-msg`;
  const hasMsg = Boolean(error || hint);
  let control = children;
  if (single) {
    const p = single.props;
    control = React.cloneElement(single, {
      id,
      "aria-describedby": p["aria-describedby"] ?? (hasMsg ? msgId : undefined),
      "aria-invalid": p["aria-invalid"] ?? (error ? true : undefined),
    });
  }
  return (
    <div className={cn("grid content-start gap-1.5", className)}>
      <label htmlFor={id} className="text-[13px] font-medium text-ink">
        {label}
      </label>
      {control}
      {error ? (
        <span id={msgId} className="text-xs text-fail">
          {error}
        </span>
      ) : hint ? (
        <span id={msgId} className="text-xs text-ink-3">
          {hint}
        </span>
      ) : null}
    </div>
  );
}

/** 表单级错误，读屏器会立即读出 */
export function FormError({
  children,
  className,
  id,
}: {
  children?: React.ReactNode;
  className?: string;
  id?: string;
}) {
  if (!children) return null;
  return (
    <p id={id} role="alert" className={cn("text-[13px] text-fail", className)}>
      {children}
    </p>
  );
}

/**
 * 设置类页面的一行：左边标题，右边内容。
 * 列宽用 minmax(0,1fr)：内容里有不能折行的长文本（如回调地址）时由它自己滚动，不把整列撑出屏幕。
 */
export function Row({ title, note, children }: { title: string; note?: React.ReactNode; children: React.ReactNode }) {
  return (
    <section className="grid grid-cols-1 gap-4 border-t border-line pt-8 first:border-t-0 first:pt-0 sm:grid-cols-[180px_minmax(0,1fr)] sm:gap-10">
      <div>
        <h2 className="text-[15px] font-semibold">{title}</h2>
        {note && <p className="mt-1 text-[13px] text-ink-2">{note}</p>}
      </div>
      <div>{children}</div>
    </section>
  );
}

/** 单选卡片 */
export function Choice({
  checked,
  onClick,
  disabled,
  children,
  className,
}: {
  checked: boolean;
  onClick: () => void;
  disabled?: boolean;
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <button
      type="button"
      role="radio"
      aria-checked={checked}
      disabled={disabled}
      onClick={onClick}
      className={cn(
        "relative rounded-lg border border-line-strong bg-surface text-left transition-[border-color,box-shadow] hover:border-ink-3 disabled:cursor-not-allowed disabled:opacity-55 disabled:hover:border-line-strong",
        checked && "border-brand shadow-[0_0_0_1px_var(--brand)]",
        className
      )}
    >
      {children}
    </button>
  );
}

/** 分段选择器 */
export function Segmented<T extends string>({
  value,
  onChange,
  options,
  label,
  className,
}: {
  value: T;
  onChange: (v: T) => void;
  options: { value: T; label: React.ReactNode; disabled?: boolean }[];
  label: string;
  className?: string;
}) {
  return (
    <div className={cn("inline-flex rounded-lg bg-sunken p-0.5", className)} role="radiogroup" aria-label={label}>
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          role="radio"
          aria-checked={value === o.value}
          disabled={o.disabled}
          onClick={() => onChange(o.value)}
          className={cn(
            "flex items-center gap-1.5 rounded-md px-3 py-1 text-[13px] whitespace-nowrap text-ink-2 hover:text-ink disabled:opacity-40",
            value === o.value && "bg-surface font-medium text-ink shadow-xs"
          )}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

/** 深色代码块，带复制按钮 */
export function CodeBlock({ title, text }: { title: string; text: string }) {
  const [copied, copy] = useCopy();
  return (
    <div className="overflow-hidden rounded-xl bg-log-bg">
      <div className="flex items-center justify-between border-b border-white/10 px-4 py-2 text-xs text-white/55">
        {title}
        <button type="button" onClick={() => copy(text)} className="flex items-center gap-1 rounded hover:text-white">
          {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
          {copied ? "已复制" : "复制"}
        </button>
      </div>
      <pre className="overflow-x-auto p-4 font-mono text-[12.5px] leading-relaxed text-log-ink select-text">{text}</pre>
    </div>
  );
}

/** 复制到剪贴板；返回 [刚复制过, 复制函数] */
export function useCopy(): [boolean, (text: string) => Promise<void>] {
  const [copied, setCopied] = React.useState(false);
  const timer = React.useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  React.useEffect(() => () => clearTimeout(timer.current), []);
  const copy = React.useCallback(async (text: string) => {
    try {
      await navigator.clipboard.writeText(text);
    } catch {
      return; // 非安全上下文（http 访问）没有剪贴板权限，用户可以手动选中复制
    }
    setCopied(true);
    clearTimeout(timer.current);
    timer.current = setTimeout(() => setCopied(false), 1600);
  }, []);
  return [copied, copy];
}

/** 从右侧滑出的编辑面板，基于 Dialog */
export function DrawerContent({ className, children, ...props }: React.ComponentProps<typeof DialogContent>) {
  return (
    <DialogContent
      className={cn(
        "top-0 right-0 left-auto flex h-svh max-h-svh w-full max-w-[600px] translate-x-0 translate-y-0 flex-col gap-0 rounded-none border-y-0 border-r-0 bg-paper p-0 sm:max-w-[600px] data-[state=open]:slide-in-from-right data-[state=open]:zoom-in-100 data-[state=closed]:slide-out-to-right data-[state=closed]:zoom-out-100",
        className
      )}
      {...props}
    >
      {children}
    </DialogContent>
  );
}
