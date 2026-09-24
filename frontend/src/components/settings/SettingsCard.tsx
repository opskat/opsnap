import { useId, type ReactNode } from "react";

/** 设置页的卡片：标题、说明、右上角操作，下方为内容 */
export function SettingsCard({
  title,
  description,
  actions,
  children,
}: {
  title: ReactNode;
  description: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
}) {
  const id = useId();
  return (
    <section aria-labelledby={id} className="overflow-hidden rounded-md border bg-card">
      <div className="flex items-center justify-between gap-4 px-4.5 py-3.5">
        <div className="flex min-w-0 flex-col gap-1">
          <h2 id={id} className="text-md font-semibold">
            {title}
          </h2>
          <p className="text-sm text-muted-foreground">{description}</p>
        </div>
        {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
      </div>
      {children}
    </section>
  );
}

/** 卡片中的一行键值 */
export function SettingsRow({ label, children }: { label: ReactNode; children: ReactNode }) {
  return (
    <div className="flex items-center gap-2.5 border-t px-4.5 py-2.75">
      <span className="w-40 shrink-0 text-sm text-muted-foreground">{label}</span>
      <span className="min-w-0 text-sm">{children}</span>
    </div>
  );
}
