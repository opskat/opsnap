import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { FormField } from "@/components/form/FormField";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { ApiError } from "@/lib/api";
import { ErrorCode } from "@/lib/auth";
import { currentRedirectURL, saveOidcConfig, type OidcConfig } from "@/lib/oidc";

const DEFAULT_SCOPES = "openid profile email";

/** 配置 OIDC 提供方；修改 Issuer 或 Client ID 且已绑定时，先确认清除绑定再保存 */
export function OidcConfigDialog({
  open,
  config,
  onOpenChange,
  onSaved,
}: {
  open: boolean;
  config: OidcConfig;
  onOpenChange: (open: boolean) => void;
  onSaved: (config: OidcConfig) => void;
}) {
  const { t } = useTranslation();
  const initial = () => ({
    display_name: config.display_name,
    issuer: config.issuer,
    client_id: config.client_id,
    client_secret: "",
    scopes: (config.scopes ?? []).join(" ") || DEFAULT_SCOPES,
  });
  const [values, setValues] = useState(initial);
  const [error, setError] = useState<string>();
  const [confirming, setConfirming] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const redirectURL = currentRedirectURL();

  const close = () => {
    setValues(initial());
    setError(undefined);
    setConfirming(false);
    onOpenChange(false);
  };

  const set = (field: keyof typeof values) => (e: { target: { value: string } }) =>
    setValues((v) => ({ ...v, [field]: e.target.value }));

  const save = async (confirmReset: boolean) => {
    setSubmitting(true);
    setError(undefined);
    try {
      const saved = await saveOidcConfig({
        display_name: values.display_name.trim(),
        issuer: values.issuer.trim(),
        client_id: values.client_id.trim(),
        client_secret: values.client_secret,
        scopes: values.scopes.split(/\s+/).filter(Boolean),
        redirect_url: redirectURL,
        confirm_reset: confirmReset,
      });
      setConfirming(false);
      onSaved(saved);
      close();
    } catch (err) {
      if (err instanceof ApiError && err.code === ErrorCode.OIDCResetConfirmRequired) {
        setConfirming(true);
      } else {
        setConfirming(false);
        setError(err instanceof Error ? err.message : String(err));
      }
    } finally {
      setSubmitting(false);
    }
  };

  const submit = (e: FormEvent) => {
    e.preventDefault();
    const missing =
      !values.display_name.trim() ||
      !values.issuer.trim() ||
      !values.client_id.trim() ||
      (!config.has_secret && !values.client_secret);
    if (missing) {
      setError(t("oidc.required"));
      return;
    }
    void save(false);
  };

  return (
    <>
      <Dialog open={open && !confirming} onOpenChange={(next) => !next && close()}>
        <DialogContent className="gap-0 bg-card p-0 sm:max-w-lg">
          <form onSubmit={submit} noValidate>
            <DialogHeader className="gap-1.5 border-b px-5 py-4 text-left">
              <DialogTitle>{t("oidc.configure")}</DialogTitle>
              <DialogDescription>{t("oidc.configureHint")}</DialogDescription>
            </DialogHeader>
            <div className="flex flex-col gap-4 p-5">
              {error && (
                <p role="alert" className="rounded-md bg-destructive-soft px-3 py-2.5 text-sm text-destructive">
                  {error}
                </p>
              )}
              <FormField label={t("oidc.displayName")} value={values.display_name} onChange={set("display_name")} />
              <FormField label="Issuer" value={values.issuer} onChange={set("issuer")} mono />
              <FormField label="Client ID" value={values.client_id} onChange={set("client_id")} mono />
              <FormField
                label="Client Secret"
                type="password"
                value={values.client_secret}
                onChange={set("client_secret")}
                placeholder={config.has_secret ? t("oidc.secretKeep") : undefined}
                autoComplete="off"
                mono
              />
              <FormField label="Scopes" value={values.scopes} onChange={set("scopes")} mono />
              <div className="flex flex-col gap-1.5">
                <span className="text-sm text-muted-foreground">{t("oidc.redirectURL")}</span>
                <code className="rounded-md border bg-background px-3 py-2 font-mono text-xs break-all">
                  {redirectURL}
                </code>
                <span className="text-xs text-faint-foreground">{t("oidc.redirectHint")}</span>
              </div>
            </div>
            <DialogFooter className="border-t bg-sidebar px-5 py-3.5">
              <Button type="button" variant="outline" onClick={close}>
                {t("common.cancel")}
              </Button>
              <Button type="submit" disabled={submitting}>
                {submitting ? t("common.submitting") : t("common.save")}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      <Dialog open={open && confirming} onOpenChange={(next) => !next && setConfirming(false)}>
        <DialogContent className="gap-0 bg-card p-0 sm:max-w-md">
          <DialogHeader className="gap-1.5 border-b px-5 py-4 text-left">
            <DialogTitle>{t("oidc.resetTitle")}</DialogTitle>
            <DialogDescription>
              {t("oidc.resetHint")}
              {!config.password_login && ` ${t("oidc.reenableNotice")}`}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter className="mt-4 border-t bg-sidebar px-5 py-3.5">
            <Button variant="outline" onClick={() => setConfirming(false)}>
              {t("common.cancel")}
            </Button>
            <Button variant="destructive" disabled={submitting} onClick={() => void save(true)}>
              {t("oidc.resetConfirm")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
