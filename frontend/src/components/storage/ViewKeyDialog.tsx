import { CircleAlert, Info, ShieldCheck } from "lucide-react";
import { useEffect, useEffectEvent, useRef, useState } from "react";
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
import { ErrorCode, getAuthStatus, type AuthStatus } from "@/lib/auth";
import { kopiaConnectCommand, locationOf, reauthURL, revealKey, type Storage } from "@/lib/storage";
import { useResetOnOpen, useRetained } from "@/lib/useRetained";

import { KeyActions } from "./KeyActions";
import { useDownloadKeyFile } from "./useDownloadKeyFile";

/** view：查看密钥；download：验证后直接下载密钥文件 */
export type RevealIntent = "view" | "download";

export interface RevealRequest {
  storage: Storage;
  intent: RevealIntent;
  /** OIDC 再次验证后回到页面：不再询问，直接用本会话的授权查看 */
  afterReauth?: boolean;
}

/**
 * 查看或下载仓库密钥：每次都要再次验证身份。密码登录开启时输入登录密码；
 * 关闭时跳转 IdP 重新验证，回来后自动继续。关闭对话框即丢弃密钥。
 */
export function ViewKeyDialog({ request, onClose }: { request?: RevealRequest; onClose: () => void }) {
  const { t } = useTranslation();
  const [auth, setAuth] = useState<AuthStatus>();
  const [password, setPassword] = useState("");
  const [passwordError, setPasswordError] = useState<string>();
  const [error, setError] = useState<string>();
  const [submitting, setSubmitting] = useState(false);
  const [revealed, setRevealed] = useState<{ key: string; fingerprint: string }>();
  // 已处理的请求（存储、意图与方式）；列表刷新会换掉请求对象，但不能因此再查看一次
  const started = useRef<string>(undefined);
  // 每次关闭加一：关闭前发出的请求迟到时丢弃结果，不显示在下一次打开的对话框中
  const session = useRef(0);
  const storage = useRetained(request?.storage);
  const downloadKeyFile = useDownloadKeyFile();

  const onRevealed = (req: RevealRequest, res: { key: string; fingerprint: string }) => {
    setRevealed(res);
    if (req.intent === "download") {
      downloadKeyFile({
        name: req.storage.name,
        location: locationOf(req.storage),
        key: res.key,
        fingerprint: res.fingerprint,
      });
    }
  };

  const onFailed = (err: unknown) => {
    if (err instanceof ApiError && err.code === ErrorCode.ReauthPasswordWrong) setPasswordError(err.message);
    else setError(err instanceof Error ? err.message : String(err));
  };

  const verify = async () => {
    if (!request) return;
    const current = session.current;
    setSubmitting(true);
    setError(undefined);
    setPasswordError(undefined);
    try {
      const res = await revealKey(request.storage.id, password);
      if (session.current === current) onRevealed(request, res);
    } catch (err) {
      if (session.current === current) onFailed(err);
    } finally {
      if (session.current === current) setSubmitting(false);
    }
  };

  // 打开时只执行一次：开发模式下 effect 会重复触发，而 OIDC 再次验证的授权只能用一次
  const onOpen = useEffectEvent((req: RevealRequest) => {
    const key = `${req.storage.id}:${req.intent}:${req.afterReauth ? "reauth" : "ask"}`;
    if (started.current === key) return;
    started.current = key;
    const current = session.current;
    const fresh = () => session.current === current;
    if (req.afterReauth) {
      revealKey(req.storage.id)
        .then((res) => fresh() && onRevealed(req, res))
        .catch((err: unknown) => fresh() && onFailed(err));
      return;
    }
    getAuthStatus()
      .then((s) => fresh() && setAuth(s))
      .catch((err: unknown) => fresh() && onFailed(err));
  });

  useEffect(() => {
    if (request) onOpen(request);
  }, [request]);

  const reset = () => {
    setAuth(undefined);
    setPassword("");
    setPasswordError(undefined);
    setError(undefined);
    setRevealed(undefined);
    setSubmitting(false);
  };

  // 每次打开都从验证身份开始；关闭时退场期间内容不变，退场结束（onCloseAutoFocus）即丢弃密钥与密码
  useResetOnOpen(request !== undefined, reset);

  const close = () => {
    started.current = undefined;
    session.current += 1;
    onClose();
  };

  const reauth = () => {
    if (!request) return;
    window.location.assign(reauthURL(`/storage?reveal=${request.storage.id}&intent=${request.intent}`));
  };

  const name = storage?.name ?? "";
  return (
    <Dialog open={request !== undefined} onOpenChange={(next) => !next && close()}>
      <DialogContent
        className="gap-0 bg-card p-0 sm:max-w-lg"
        onCloseAutoFocus={() => {
          if (request === undefined) reset();
        }}
      >
        <DialogHeader className="gap-1.5 border-b px-5 py-4 text-left">
          <DialogTitle>
            {revealed ? t("storage.reveal.shownTitle", { name }) : t("storage.reveal.title", { name })}
          </DialogTitle>
          <DialogDescription>{revealed ? t("storage.reveal.shownHint") : t("storage.reveal.hint")}</DialogDescription>
        </DialogHeader>
        {revealed && storage ? (
          <>
            <div className="flex flex-col gap-4 p-5">
              <div className="flex flex-col gap-3 rounded-md border bg-background p-3.5">
                <code aria-label={t("storage.key.label")} className="font-mono text-md break-all">
                  {revealed.key}
                </code>
                <div className="flex flex-wrap gap-2">
                  <KeyActions
                    name={storage.name}
                    location={locationOf(storage)}
                    keyText={revealed.key}
                    fingerprint={revealed.fingerprint}
                  />
                </div>
                <p className="font-mono text-xs text-muted-foreground">
                  {t("storage.key.fingerprintLine", {
                    fingerprint: revealed.fingerprint,
                    encryption: storage.encryption,
                  })}
                </p>
              </div>
              <div className="flex items-start gap-2 rounded-md bg-running-soft px-3 py-2.5 text-sm text-running">
                <Info className="mt-0.5 size-4 shrink-0" />
                <div className="flex min-w-0 flex-col gap-1.5">
                  <span>{t("storage.reveal.cli")}</span>
                  <code className="font-mono text-xs break-all">{kopiaConnectCommand(locationOf(storage))}</code>
                </div>
              </div>
            </div>
            <DialogFooter className="border-t bg-sidebar px-5 py-3.5">
              <Button onClick={close}>{t("storage.unlock.finish")}</Button>
            </DialogFooter>
          </>
        ) : (
          <form
            onSubmit={(e) => {
              e.preventDefault();
              if (auth?.password_login) void verify();
            }}
          >
            <div className="flex flex-col gap-4 p-5">
              {auth?.password_login && (
                <FormField
                  label={t("storage.reveal.password")}
                  type="password"
                  value={password}
                  error={passwordError}
                  autoComplete="current-password"
                  autoFocus
                  onChange={(e) => setPassword(e.target.value)}
                />
              )}
              {auth && !auth.password_login && (
                <p className="text-sm text-muted-foreground">
                  {t("storage.reveal.oidcHint", { provider: auth.oidc_login?.display_name ?? "OIDC" })}
                </p>
              )}
              {!auth && !error && <p className="text-sm text-muted-foreground">{t("common.loading")}</p>}
              {error && (
                <p
                  role="alert"
                  className="flex items-start gap-2 rounded-md bg-destructive-soft px-3 py-2.5 text-sm text-destructive"
                >
                  <CircleAlert className="mt-0.5 size-4 shrink-0" />
                  {error}
                </p>
              )}
            </div>
            <DialogFooter className="border-t bg-sidebar px-5 py-3.5">
              <Button type="button" variant="outline" onClick={close}>
                {t("common.cancel")}
              </Button>
              {auth?.password_login && (
                <Button type="submit" disabled={!password || submitting}>
                  <ShieldCheck />
                  {submitting ? t("common.submitting") : t("storage.reveal.verify")}
                </Button>
              )}
              {auth && !auth.password_login && (
                <Button type="button" disabled={!auth.oidc_login} onClick={reauth}>
                  <ShieldCheck />
                  {t("storage.reveal.reauth", { provider: auth.oidc_login?.display_name ?? "OIDC" })}
                </Button>
              )}
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}
