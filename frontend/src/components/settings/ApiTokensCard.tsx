import { Plus } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { formatDate, relativeTime } from "@/lib/format";
import { listTokens, revokeToken, type ApiToken } from "@/lib/tokens";
import { cn } from "@/lib/utils";

import { CreateTokenDialog } from "./CreateTokenDialog";
import { SettingsCard } from "./SettingsCard";

type State = { status: "loading" } | { status: "error"; message: string } | { status: "ready"; items: ApiToken[] };

const columns = "grid grid-cols-[minmax(0,1fr)_9rem_8rem_8rem_8rem_4.5rem] items-center gap-3 px-4.5";

export function ApiTokensCard() {
  const { t } = useTranslation();
  const [state, setState] = useState<State>({ status: "loading" });
  const [attempt, setAttempt] = useState(0);
  const [creating, setCreating] = useState(false);
  const [revoking, setRevoking] = useState<ApiToken>();
  const [revokeError, setRevokeError] = useState<string>();

  useEffect(() => {
    let cancelled = false;
    listTokens()
      .then((r) => !cancelled && setState({ status: "ready", items: r.items }))
      .catch(
        (err: unknown) =>
          !cancelled && setState({ status: "error", message: err instanceof Error ? err.message : String(err) })
      );
    return () => {
      cancelled = true;
    };
  }, [attempt]);

  const reload = () => setAttempt((n) => n + 1);

  const confirmRevoke = async () => {
    if (!revoking) return;
    try {
      await revokeToken(revoking.id);
      setRevoking(undefined);
      reload();
    } catch (err) {
      setRevokeError(err instanceof Error ? err.message : String(err));
    }
  };

  return (
    <SettingsCard
      title={t("tokens.title")}
      description={t("tokens.hint")}
      actions={
        <Button onClick={() => setCreating(true)}>
          <Plus />
          {t("tokens.createButton")}
        </Button>
      }
    >
      <div aria-live="polite">
        {state.status === "loading" && (
          <p className="border-t px-4.5 py-3 text-sm text-muted-foreground">{t("common.loading")}</p>
        )}
        {state.status === "error" && (
          <div className="flex items-center justify-between gap-3 border-t bg-destructive-soft px-4.5 py-2.5">
            <p className="text-sm text-destructive">{t("tokens.loadFailed", { message: state.message })}</p>
            <Button variant="outline" size="sm" onClick={reload}>
              {t("common.retry")}
            </Button>
          </div>
        )}
        {state.status === "ready" && state.items.length === 0 && (
          <p className="border-t px-4.5 py-6 text-center text-sm text-muted-foreground">{t("tokens.empty")}</p>
        )}
        {state.status === "ready" && state.items.length > 0 && (
          <div role="table" aria-label={t("tokens.title")}>
            <div role="row" className={cn(columns, "border-t bg-accent py-2.25 text-xs text-muted-foreground")}>
              <span role="columnheader">{t("tokens.name")}</span>
              <span role="columnheader">{t("tokens.token")}</span>
              <span role="columnheader">{t("tokens.createdAt")}</span>
              <span role="columnheader">{t("tokens.lastUsed")}</span>
              <span role="columnheader">{t("tokens.expiresAt")}</span>
              <span role="columnheader" className="sr-only">
                {t("tokens.actions")}
              </span>
            </div>
            {state.items.map((item) => {
              const revoked = item.status === "revoked";
              return (
                <div
                  key={item.id}
                  role="row"
                  className={cn(columns, "border-t py-2.75 text-sm", revoked && "text-faint-foreground")}
                >
                  <span role="cell" className="flex min-w-0 items-center gap-2">
                    <span className="truncate font-medium">{item.name}</span>
                    {item.status === "expired" && (
                      <span className="rounded-sm bg-warning-soft px-1.75 py-0.5 text-2xs font-medium text-warning">
                        {t("tokens.expired")}
                      </span>
                    )}
                    {revoked && (
                      <span className="rounded-sm bg-accent px-1.75 py-0.5 text-2xs font-medium text-muted-foreground">
                        {t("tokens.revoked")}
                      </span>
                    )}
                  </span>
                  <span role="cell" className="font-mono text-xs">
                    {item.prefix}…
                  </span>
                  <span role="cell" className="font-mono text-xs text-muted-foreground">
                    {formatDate(item.created_at)}
                  </span>
                  <span role="cell" className="font-mono text-xs text-muted-foreground">
                    {item.last_used_at ? relativeTime(t, item.last_used_at) : t("tokens.neverUsed")}
                  </span>
                  <span
                    role="cell"
                    className={cn(
                      "font-mono text-xs",
                      item.status === "expired" ? "text-warning" : "text-muted-foreground"
                    )}
                  >
                    {item.expires_at ? formatDate(item.expires_at) : t("tokens.never")}
                  </span>
                  <span role="cell" className="text-right">
                    {!revoked && (
                      <Button
                        variant="ghost"
                        size="sm"
                        className="text-destructive"
                        aria-label={t("tokens.revokeNamed", { name: item.name })}
                        onClick={() => {
                          setRevokeError(undefined);
                          setRevoking(item);
                        }}
                      >
                        {t("tokens.revoke")}
                      </Button>
                    )}
                  </span>
                </div>
              );
            })}
          </div>
        )}
      </div>

      <CreateTokenDialog open={creating} onOpenChange={setCreating} onCreated={reload} />

      <Dialog open={revoking !== undefined} onOpenChange={(open) => !open && setRevoking(undefined)}>
        <DialogContent className="gap-0 bg-card p-0 sm:max-w-md">
          <DialogHeader className="gap-1.5 border-b px-5 py-4 text-left">
            <DialogTitle>{t("tokens.revokeTitle")}</DialogTitle>
            <DialogDescription>{t("tokens.revokeHint", { name: revoking?.name ?? "" })}</DialogDescription>
          </DialogHeader>
          {revokeError && (
            <p role="alert" className="mx-5 mt-4 rounded-md bg-destructive-soft px-3 py-2.5 text-sm text-destructive">
              {revokeError}
            </p>
          )}
          <DialogFooter className="mt-4 border-t bg-sidebar px-5 py-3.5">
            <Button variant="outline" onClick={() => setRevoking(undefined)}>
              {t("common.cancel")}
            </Button>
            <Button variant="destructive" onClick={() => void confirmRevoke()}>
              {t("tokens.revoke")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </SettingsCard>
  );
}
