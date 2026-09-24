import { Copy, Link2, Pencil, Unlink } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { useSearchParams } from "react-router";

import { Switch } from "@/components/form/Switch";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { OIDC_BIND_URL, getOidcConfig, setPasswordLogin, unbindOidc, type OidcConfig } from "@/lib/oidc";
import { useOidcErrorMessage } from "@/lib/useOidcError";

import { OidcConfigDialog } from "./OidcConfigDialog";
import { SettingsCard, SettingsRow } from "./SettingsCard";

type State = { status: "loading" } | { status: "error"; message: string } | { status: "ready"; config: OidcConfig };

function Tag({ tone, children }: { tone: "success" | "muted"; children: string }) {
  return (
    <span
      className={
        tone === "success"
          ? "rounded-sm bg-success-soft px-1.75 py-0.5 text-2xs font-medium text-success"
          : "rounded-sm bg-accent px-1.75 py-0.5 text-2xs font-medium text-muted-foreground"
      }
    >
      {children}
    </span>
  );
}

export function LoginMethodsCard() {
  const { t } = useTranslation();
  const [params] = useSearchParams();
  const oidcError = useOidcErrorMessage(params);
  const justBound = params.get("oidc") === "bound";
  const [state, setState] = useState<State>({ status: "loading" });
  const [attempt, setAttempt] = useState(0);
  // 保存后换 key 重新挂载配置弹窗，让下次编辑从刚保存的配置开始
  const [saved, setSaved] = useState(0);
  const [editing, setEditing] = useState(false);
  const [unbinding, setUnbinding] = useState(false);
  const [actionError, setActionError] = useState<string>();
  const [disabling, setDisabling] = useState(false);

  useEffect(() => {
    let cancelled = false;
    getOidcConfig()
      .then((config) => !cancelled && setState({ status: "ready", config }))
      .catch(
        (err: unknown) =>
          !cancelled && setState({ status: "error", message: err instanceof Error ? err.message : String(err) })
      );
    return () => {
      cancelled = true;
    };
  }, [attempt]);

  const reload = () => setAttempt((n) => n + 1);
  const config = state.status === "ready" ? state.config : undefined;

  const togglePasswordLogin = async (enabled: boolean) => {
    if (state.status !== "ready") return;
    try {
      const r = await setPasswordLogin(enabled);
      setState({ status: "ready", config: { ...state.config, password_login: r.password_login } });
      setActionError(undefined);
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err));
    } finally {
      setDisabling(false);
    }
  };

  const confirmUnbind = async () => {
    try {
      await unbindOidc();
      setUnbinding(false);
      reload();
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err));
      setUnbinding(false);
    }
  };

  return (
    <SettingsCard
      title={t("oidc.title")}
      description={t("oidc.hint")}
      actions={
        config && (
          <>
            {config.configured && (
              <Button variant="outline" onClick={() => setEditing(true)}>
                <Pencil />
                {t("oidc.edit")}
              </Button>
            )}
            {config.configured && !config.binding && (
              <Button asChild>
                <a href={OIDC_BIND_URL}>
                  <Link2 />
                  {t("oidc.bind")}
                </a>
              </Button>
            )}
            {config.binding && (
              <Button variant="outline" className="text-destructive" onClick={() => setUnbinding(true)}>
                <Unlink />
                {t("oidc.unbind")}
              </Button>
            )}
            {!config.configured && <Button onClick={() => setEditing(true)}>{t("oidc.configure")}</Button>}
          </>
        )
      }
    >
      <div aria-live="polite">
        {justBound && !oidcError && (
          <p role="status" className="border-t bg-success-soft px-4.5 py-2.5 text-sm text-success">
            {t("oidc.boundNotice")}
          </p>
        )}
        {(oidcError || actionError) && (
          <p role="alert" className="border-t bg-destructive-soft px-4.5 py-2.5 text-sm text-destructive">
            {oidcError ?? actionError}
          </p>
        )}
        {state.status === "loading" && (
          <p className="border-t px-4.5 py-3 text-sm text-muted-foreground">{t("common.loading")}</p>
        )}
        {state.status === "error" && (
          <div className="flex items-center justify-between gap-3 border-t bg-destructive-soft px-4.5 py-2.5">
            <p className="text-sm text-destructive">{t("oidc.loadFailed", { message: state.message })}</p>
            <Button variant="outline" size="sm" onClick={reload}>
              {t("common.retry")}
            </Button>
          </div>
        )}
        {config && !config.configured && (
          <SettingsRow label="OIDC">
            <Tag tone="muted">{t("oidc.notConfigured")}</Tag>
          </SettingsRow>
        )}
        {config?.configured && (
          <>
            <SettingsRow label="OIDC">
              <span className="flex items-center gap-2">
                {config.display_name}
                {config.binding ? (
                  <Tag tone="success">{t("oidc.bound")}</Tag>
                ) : (
                  <Tag tone="muted">{t("oidc.notBound")}</Tag>
                )}
              </span>
            </SettingsRow>
            <SettingsRow label="Issuer">
              <span className="font-mono">{config.issuer}</span>
            </SettingsRow>
            <SettingsRow label="Client ID">
              <span className="font-mono">{config.client_id}</span>
            </SettingsRow>
            <SettingsRow label="Client Secret">
              <span className="text-muted-foreground">{t("oidc.secretSaved")}</span>
            </SettingsRow>
            <SettingsRow label={t("oidc.redirectURL")}>
              <span className="flex items-center gap-2">
                <span className="font-mono break-all">{config.redirect_url}</span>
                <Button
                  variant="ghost"
                  size="icon-xs"
                  aria-label={t("common.copy")}
                  onClick={() => void navigator.clipboard?.writeText(config.redirect_url).catch(() => undefined)}
                >
                  <Copy />
                </Button>
              </span>
            </SettingsRow>
            {config.binding && (
              <SettingsRow label={t("oidc.identity")}>
                {/* 显示邮箱，没有时显示 preferred_username（后端合并为 display），都没有时只显示 sub */}
                {config.binding.display ? (
                  <span>{config.binding.display}</span>
                ) : (
                  <span className="font-mono">{config.binding.subject}</span>
                )}
              </SettingsRow>
            )}
          </>
        )}
        {config && (
          <SettingsRow label={t("oidc.passwordLogin")}>
            <span className="flex items-center gap-2.5">
              <Switch
                checked={config.password_login}
                aria-label={t("oidc.passwordLogin")}
                disabled={config.password_login && !config.can_disable_password_login}
                onCheckedChange={(on) => (on ? void togglePasswordLogin(true) : setDisabling(true))}
              />
              <span className="text-muted-foreground">
                {!config.password_login
                  ? t("oidc.passwordLoginOff")
                  : config.can_disable_password_login
                    ? t("oidc.passwordLoginOn")
                    : t("oidc.passwordLoginLocked")}
              </span>
            </span>
          </SettingsRow>
        )}
      </div>

      <Dialog open={disabling} onOpenChange={setDisabling}>
        <DialogContent className="gap-0 bg-card p-0 sm:max-w-md">
          <DialogHeader className="gap-1.5 border-b px-5 py-4 text-left">
            <DialogTitle>{t("oidc.disableTitle")}</DialogTitle>
            <DialogDescription>{t("oidc.disableHint")}</DialogDescription>
          </DialogHeader>
          <DialogFooter className="mt-4 border-t bg-sidebar px-5 py-3.5">
            <Button variant="outline" onClick={() => setDisabling(false)}>
              {t("common.cancel")}
            </Button>
            <Button variant="destructive" onClick={() => void togglePasswordLogin(false)}>
              {t("oidc.disableConfirm")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {config && (
        <OidcConfigDialog
          key={`${attempt}-${saved}`}
          open={editing}
          config={config}
          onOpenChange={setEditing}
          onSaved={(next) => {
            setState({ status: "ready", config: next });
            setSaved((n) => n + 1);
          }}
        />
      )}

      <Dialog open={unbinding} onOpenChange={setUnbinding}>
        <DialogContent className="gap-0 bg-card p-0 sm:max-w-md">
          <DialogHeader className="gap-1.5 border-b px-5 py-4 text-left">
            <DialogTitle>{t("oidc.unbindTitle")}</DialogTitle>
            <DialogDescription>
              {t("oidc.unbindHint")}
              {config && !config.password_login && ` ${t("oidc.reenableNotice")}`}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter className="mt-4 border-t bg-sidebar px-5 py-3.5">
            <Button variant="outline" onClick={() => setUnbinding(false)}>
              {t("common.cancel")}
            </Button>
            <Button variant="destructive" onClick={() => void confirmUnbind()}>
              {t("oidc.unbind")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </SettingsCard>
  );
}
