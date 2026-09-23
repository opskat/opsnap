import type { ReactNode } from "react";

export function PageHeader({ title, subtitle }: { title: ReactNode; subtitle?: ReactNode }) {
  return (
    <header className="flex flex-col gap-1 border-b px-8 py-5">
      <h1 className="text-2xl font-bold">{title}</h1>
      {subtitle && <p className="text-[13px] text-muted-foreground">{subtitle}</p>}
    </header>
  );
}
