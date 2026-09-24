import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Navigate, Outlet, useLocation, useNavigate } from "react-router";

import { Button } from "@/components/ui/button";
import { ApiError, onUnauthorized } from "@/lib/api";
import { getAuthStatus, getMe, logout as apiLogout, type Me } from "@/lib/auth";
import { SessionContext } from "@/lib/session";

type State =
  | { status: "loading" }
  | { status: "error"; message: string }
  | { status: "setup" }
  | { status: "login" }
  | { status: "ready"; user: Me };

/** 控制台的登录门禁：未初始化去首次设置页，未登录去登录页（带上原本要访问的地址） */
export function AuthGate() {
  const { t } = useTranslation();
  const location = useLocation();
  const navigate = useNavigate();
  const [state, setState] = useState<State>({ status: "loading" });
  const [attempt, setAttempt] = useState(0);
  const next = location.pathname + location.search;

  const toLogin = useCallback(
    () => navigate(`/login?next=${encodeURIComponent(next)}`, { replace: true }),
    [navigate, next]
  );

  useEffect(() => onUnauthorized(() => void toLogin()), [toLogin]);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      const status = await getAuthStatus();
      if (!status.initialized) return { status: "setup" } as const;
      try {
        return { status: "ready", user: await getMe() } as const;
      } catch (err) {
        if (err instanceof ApiError && err.status === 401) return { status: "login" } as const;
        throw err;
      }
    })()
      .then((s) => !cancelled && setState(s))
      .catch(
        (err: unknown) =>
          !cancelled && setState({ status: "error", message: err instanceof Error ? err.message : String(err) })
      );
    return () => {
      cancelled = true;
    };
    // 只在进入控制台和重试时检查；站内跳转不重复请求
  }, [attempt]);

  if (state.status === "setup") return <Navigate to="/setup" replace />;
  if (state.status === "login") return <Navigate to={`/login?next=${encodeURIComponent(next)}`} replace />;
  if (state.status === "loading") {
    return <p className="p-8 text-sm text-muted-foreground">{t("common.loading")}</p>;
  }
  if (state.status === "error") {
    return (
      <div className="flex items-center gap-3 p-8">
        <p className="text-sm text-destructive">{t("auth.statusFailed", { message: state.message })}</p>
        <Button
          variant="outline"
          size="sm"
          onClick={() => {
            setState({ status: "loading" });
            setAttempt((n) => n + 1);
          }}
        >
          {t("common.retry")}
        </Button>
      </div>
    );
  }

  const logout = async () => {
    try {
      await apiLogout();
    } catch {
      // 会话已失效或网络失败都不影响回到登录页
    }
    await navigate("/login", { replace: true });
  };

  return (
    <SessionContext value={{ user: state.user, logout }}>
      <Outlet />
    </SessionContext>
  );
}
