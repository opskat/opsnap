import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

import { PageHeader } from "@/components/layout/PageHeader";
import { Button } from "@/components/ui/button";
import { getHealth, type Health } from "@/lib/system";
import { cn } from "@/lib/utils";

type State = { status: "loading" } | { status: "error"; message: string } | { status: "ready"; health: Health };

export function OverviewPage() {
  const { t } = useTranslation();
  const [state, setState] = useState<State>({ status: "loading" });
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    let cancelled = false;
    getHealth()
      .then((health) => !cancelled && setState({ status: "ready", health }))
      .catch(
        (err: unknown) =>
          !cancelled && setState({ status: "error", message: err instanceof Error ? err.message : String(err) })
      );
    return () => {
      cancelled = true;
    };
  }, [attempt]);

  const retry = () => {
    setState({ status: "loading" });
    setAttempt((n) => n + 1);
  };

  return (
    <>
      <PageHeader title={t("nav.overview")} subtitle={t("overview.subtitle")} />
      <section className="px-8 py-6">
        <div className="max-w-md rounded-md border bg-card p-4" aria-live="polite">
          <h2 className="mb-3 text-md font-semibold">{t("overview.systemStatus")}</h2>
          {state.status === "loading" && <p className="text-sm text-muted-foreground">{t("common.loading")}</p>}
          {state.status === "error" && (
            <div className="flex items-center justify-between gap-3 rounded-md bg-destructive-soft px-3 py-2">
              <p className="text-sm text-destructive">{t("overview.loadFailed", { message: state.message })}</p>
              <Button variant="outline" size="sm" onClick={retry}>
                {t("common.retry")}
              </Button>
            </div>
          )}
          {state.status === "ready" && (
            <dl className="grid grid-cols-[auto_1fr] gap-x-6 gap-y-2 text-sm">
              <dt className="text-muted-foreground">{t("overview.version")}</dt>
              <dd className="font-mono" data-testid="health-version">
                {state.health.version}
              </dd>
              <dt className="text-muted-foreground">{t("overview.database")}</dt>
              <dd data-testid="health-database">
                <span
                  className={cn(
                    "inline-flex items-center gap-1.5 rounded-md px-2 py-0.5 text-xs font-medium",
                    state.health.database === "ok"
                      ? "bg-success-soft text-success"
                      : "bg-destructive-soft text-destructive"
                  )}
                >
                  <span
                    className={cn(
                      "size-1.5 rounded-full",
                      state.health.database === "ok" ? "bg-success" : "bg-destructive"
                    )}
                  />
                  {state.health.database === "ok" ? t("overview.databaseOk") : t("overview.databaseError")}
                </span>
              </dd>
            </dl>
          )}
        </div>
      </section>
    </>
  );
}
