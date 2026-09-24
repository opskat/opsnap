import { Aperture } from "lucide-react";
import type { ReactNode } from "react";

import { PreferenceToggles } from "./PreferenceToggles";

/** 登录与首次设置页的外框：右上角主题与语言切换，居中卡片 */
export function AuthLayout({
  title,
  subtitle,
  children,
  footer,
}: {
  title: ReactNode;
  subtitle: ReactNode;
  children: ReactNode;
  footer?: ReactNode;
}) {
  return (
    <div className="flex min-h-screen flex-col bg-background">
      <div className="flex justify-end px-7 py-5">
        <PreferenceToggles />
      </div>
      <main className="flex flex-1 items-center justify-center px-4 pb-20">
        <div className="flex w-full max-w-sm flex-col gap-5">
          <div className="flex items-center gap-2.5">
            <span className="flex size-8 items-center justify-center rounded-md bg-brand text-brand-foreground">
              <Aperture className="size-4.5" />
            </span>
            <span className="text-lg font-bold">OpsNap</span>
          </div>
          <div className="flex flex-col gap-1.5">
            <h1 className="text-2xl font-bold">{title}</h1>
            <p className="text-sm text-muted-foreground">{subtitle}</p>
          </div>
          <div className="flex flex-col gap-4 rounded-md border bg-card p-5">{children}</div>
          {footer && <p className="text-xs text-faint-foreground">{footer}</p>}
        </div>
      </main>
    </div>
  );
}
