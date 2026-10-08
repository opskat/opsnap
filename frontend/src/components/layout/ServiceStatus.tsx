import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

import { getHealth, type Health } from "@/lib/system";
import { cn } from "@/lib/utils";

type State = { status: "loading" } | { status: "error" } | { status: "ready"; health: Health };

/** 侧栏底部：服务状态与版本、提交短号（设计稿「服务运行中 / 版本 · 自托管」） */
export function ServiceStatus() {
  const { t } = useTranslation();
  const [state, setState] = useState<State>({ status: "loading" });

  useEffect(() => {
    let cancelled = false;
    getHealth()
      .then((health) => !cancelled && setState({ status: "ready", health }))
      .catch(() => !cancelled && setState({ status: "error" }));
    return () => {
      cancelled = true;
    };
  }, []);

  if (state.status === "loading") return null;
  const healthy = state.status === "ready" && state.health.database === "ok";
  const label = healthy
    ? t("service.running")
    : state.status === "ready"
      ? t("service.databaseError")
      : t("service.unreachable");

  return (
    <div className="flex flex-col gap-1 rounded-md border px-3 py-2.5" aria-live="polite">
      <p className="flex items-center gap-2 text-xs font-medium text-sidebar-foreground">
        <span className={cn("size-1.5 shrink-0 rounded-full", healthy ? "bg-success" : "bg-destructive")} />
        {label}
      </p>
      {state.status === "ready" && (
        <p className="font-mono text-2xs break-words text-faint-foreground" data-testid="service-version">
          {[state.health.version, state.health.commit, t("service.selfHosted")].filter(Boolean).join(" · ")}
        </p>
      )}
    </div>
  );
}
