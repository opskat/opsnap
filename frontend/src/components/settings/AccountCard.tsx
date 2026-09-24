import { KeyRound } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

import { Button } from "@/components/ui/button";
import { getMe, type Me } from "@/lib/auth";
import { describeUserAgent, formatDateTime } from "@/lib/format";

import { ChangePasswordDialog } from "./ChangePasswordDialog";
import { SettingsCard, SettingsRow } from "./SettingsCard";

type State = { status: "loading" } | { status: "error"; message: string } | { status: "ready"; me: Me };

export function AccountCard() {
  const { t } = useTranslation();
  const [state, setState] = useState<State>({ status: "loading" });
  const [attempt, setAttempt] = useState(0);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [changed, setChanged] = useState(false);

  useEffect(() => {
    let cancelled = false;
    getMe()
      .then((me) => !cancelled && setState({ status: "ready", me }))
      .catch(
        (err: unknown) =>
          !cancelled && setState({ status: "error", message: err instanceof Error ? err.message : String(err) })
      );
    return () => {
      cancelled = true;
    };
  }, [attempt]);

  const reload = () => setAttempt((n) => n + 1);

  return (
    <SettingsCard
      title={t("settings.account")}
      description={t("settings.accountHint")}
      actions={
        <Button variant="outline" onClick={() => setDialogOpen(true)}>
          <KeyRound />
          {t("settings.changePassword")}
        </Button>
      }
    >
      <div aria-live="polite">
        {changed && (
          <p role="status" className="border-t bg-success-soft px-4.5 py-2.5 text-sm text-success">
            {t("settings.passwordChanged")}
          </p>
        )}
        {state.status === "loading" && (
          <p className="border-t px-4.5 py-3 text-sm text-muted-foreground">{t("common.loading")}</p>
        )}
        {state.status === "error" && (
          <div className="flex items-center justify-between gap-3 border-t bg-destructive-soft px-4.5 py-2.5">
            <p className="text-sm text-destructive">{t("settings.loadFailed", { message: state.message })}</p>
            <Button variant="outline" size="sm" onClick={reload}>
              {t("common.retry")}
            </Button>
          </div>
        )}
        {state.status === "ready" && (
          <>
            <SettingsRow label={t("auth.username")}>
              <span className="font-mono">{state.me.username}</span>
            </SettingsRow>
            <SettingsRow label={t("settings.passwordUpdatedAt")}>
              <span className="font-mono">{formatDateTime(state.me.password_updated_at)}</span>
            </SettingsRow>
            {state.me.session && (
              <SettingsRow label={t("settings.currentSession")}>
                {t("settings.sessionSummary", {
                  browser: describeUserAgent(state.me.session.user_agent),
                  ip: state.me.session.ip,
                  expires: formatDateTime(state.me.session.expires_at),
                })}
              </SettingsRow>
            )}
          </>
        )}
      </div>
      <ChangePasswordDialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        onChanged={() => {
          setChanged(true);
          reload();
        }}
      />
    </SettingsCard>
  );
}
