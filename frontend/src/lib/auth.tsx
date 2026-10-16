import * as React from "react";
import { Navigate, useLocation, useNavigate } from "react-router";
import { ApiError, api, errorMessage, loginPath, setUnauthorizedHandler, type Me } from "./api";

interface AuthContextValue {
  /** 当前身份；undefined 表示还没确认，null 表示未登录 */
  me: Me | null | undefined;
  setMe: (me: Me | null) => void;
}

const AuthContext = React.createContext<AuthContextValue | null>(null);

/**
 * 保存登录态，并把 API 客户端的 401 处理接到路由上：
 * 任何接口返回 401 时清空登录态并跳到登录页，登录后回到原页面。
 */
export function AuthProvider({ children }: { children: React.ReactNode }) {
  const [me, setMe] = React.useState<Me | null | undefined>(undefined);
  const navigate = useNavigate();
  const location = useLocation();
  const here = React.useRef("");
  React.useLayoutEffect(() => {
    here.current = location.pathname + location.search;
  });

  React.useEffect(() => {
    setUnauthorizedHandler(() => {
      setMe(null);
      const cur = here.current;
      if (!cur.startsWith("/login") && !cur.startsWith("/setup")) {
        navigate(loginPath(cur), { replace: true });
      }
    });
    return () => setUnauthorizedHandler(null);
  }, [navigate]);

  const value = React.useMemo(() => ({ me, setMe }), [me]);
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthContextValue {
  const ctx = React.useContext(AuthContext);
  if (!ctx) throw new Error("useAuth 必须在 AuthProvider 内使用");
  return ctx;
}

/** 登录后才能看的页面：先确认是否已创建管理员，再确认会话 */
export function RequireAuth({ children }: { children: React.ReactNode }) {
  const { me, setMe } = useAuth();
  const [needSetup, setNeedSetup] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [attempt, setAttempt] = React.useState(0);

  React.useEffect(() => {
    if (me) return;
    let cancelled = false;
    (async () => {
      try {
        const st = await api.auth.status();
        if (cancelled) return;
        if (!st.initialized) {
          setNeedSetup(true);
          return;
        }
        const who = await api.auth.me();
        if (!cancelled) setMe(who);
      } catch (err) {
        // 401 已由 AuthProvider 跳转登录页
        if (!cancelled && !(err instanceof ApiError && err.status === 401)) {
          setError(errorMessage(err));
        }
      }
    })();
    return () => {
      cancelled = true;
    };
    // me 变成 null（401）时不需要重新请求，登录页会重新设置
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [attempt]);

  if (needSetup) return <Navigate to="/setup" replace />;
  if (me) return <>{children}</>;
  if (error) {
    return (
      <div className="grid min-h-svh place-items-center px-6">
        <div className="max-w-sm text-center" role="alert">
          <p className="font-medium">读取登录状态失败</p>
          <p className="mt-1 text-[13px] text-ink-2">{error}</p>
          <button
            type="button"
            className="mt-4 rounded-md border border-line-strong bg-surface px-3 py-1.5 text-[13px] hover:bg-accent"
            onClick={() => {
              setError(null);
              setAttempt((n) => n + 1);
            }}
          >
            重试
          </button>
        </div>
      </div>
    );
  }
  return <div className="min-h-svh" aria-busy="true" />;
}
