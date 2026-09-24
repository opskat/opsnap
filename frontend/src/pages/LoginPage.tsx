import { useEffect, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { Navigate, useNavigate, useSearchParams } from "react-router";

import { FormField } from "@/components/form/FormField";
import { AuthLayout } from "@/components/layout/AuthLayout";
import { KeyRound } from "lucide-react";

import { Button } from "@/components/ui/button";
import { getAuthStatus, login, type AuthStatus } from "@/lib/auth";
import { oidcLoginURL } from "@/lib/oidc";
import { useOidcErrorMessage } from "@/lib/useOidcError";

/** 只接受站内路径，防止登录后被带去其他站点；规则与后端 oidc_svc.safeNext 一致。
 * 含控制字符的一律拒绝：浏览器解析地址时会删掉制表符与换行，"/\t/evil.com" 会变成 "//evil.com" */
function safeNext(next: string | null) {
  if (!next || !next.startsWith("/") || next.startsWith("//") || next.startsWith("/\\")) return "/";
  // eslint-disable-next-line no-control-regex -- 这里就是要匹配控制字符
  return /[\u0000-\u001f\u007f]/.test(next) ? "/" : next;
}

export function LoginPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const [status, setStatus] = useState<AuthStatus>();
  const oidcError = useOidcErrorMessage(params);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string>();
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    let cancelled = false;
    getAuthStatus()
      .then((s) => !cancelled && setStatus(s))
      // 状态读取失败时仍显示登录表单；提交时会得到明确的错误
      .catch(() => !cancelled && setStatus({ initialized: true, password_login: true, oidc_login: null }));
    return () => {
      cancelled = true;
    };
  }, []);

  if (status?.initialized === false) return <Navigate to="/setup" replace />;
  const next = safeNext(params.get("next"));
  const shownError = error ?? oidcError;
  // 状态未返回前按开启处理，避免表单闪烁消失
  const passwordLogin = status?.password_login ?? true;

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setSubmitting(true);
    setError(undefined);
    try {
      await login({ username, password });
      await navigate(next, { replace: true });
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <AuthLayout title={t("login.title")} subtitle={t("login.subtitle")} footer={t("login.forgot")}>
      <form className="flex flex-col gap-4" onSubmit={(e) => void submit(e)}>
        {shownError && (
          <p role="alert" className="rounded-md bg-destructive-soft px-3 py-2.5 text-sm text-destructive">
            {shownError}
          </p>
        )}
        {passwordLogin && (
          <>
            <FormField
              label={t("auth.username")}
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              autoComplete="username"
              required
              mono
            />
            <FormField
              label={t("auth.password")}
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              autoComplete="current-password"
              required
              mono
            />
            <Button type="submit" disabled={submitting}>
              {submitting ? t("common.submitting") : t("login.submit")}
            </Button>
          </>
        )}
        {status?.oidc_login && (
          <>
            {passwordLogin && (
              <div className="flex items-center gap-2.5 text-xs text-faint-foreground">
                <span className="h-px flex-1 bg-border" />
                {t("login.or")}
                <span className="h-px flex-1 bg-border" />
              </div>
            )}
            <Button variant="outline" asChild>
              <a href={oidcLoginURL(next)}>
                <KeyRound />
                {t("login.withOidc", { name: status.oidc_login.display_name })}
              </a>
            </Button>
          </>
        )}
      </form>
    </AuthLayout>
  );
}
