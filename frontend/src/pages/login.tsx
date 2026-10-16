import * as React from "react";
import { useNavigate, useSearchParams } from "react-router";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Field, FormError } from "@/components/bits";
import { Mark } from "@/components/shell";
import { ApiError, ErrorCode, api, errorMessage } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { redirect } from "@/lib/redirect";
import { LogIn, Loader2 } from "lucide-react";

/** 登录后要回到的地址；只接受站内路径，防止跳到外站 */
export function safeNext(next: string | null): string {
  if (!next || !next.startsWith("/") || next.startsWith("//") || next.startsWith("/\\")) return "/";
  if (next.startsWith("/login") || next.startsWith("/setup")) return "/";
  return next;
}

/** 认证页左侧的深色插画区，登录和首次设置共用 */
export function AuthAside() {
  return (
    <div className="relative hidden overflow-hidden bg-ink text-paper lg:flex lg:flex-col lg:justify-between lg:p-12">
      <div className="flex items-center gap-2.5">
        <Mark className="[&_rect:first-child]:fill-paper/10" />
        <span className="text-[16px] font-semibold">opsnap</span>
      </div>
      <Strata />
      <p className="max-w-sm text-[15px] leading-relaxed text-paper/70">
        线上的数据库和文件，每天一层，安静地沉到你自己的 NAS 里。
      </p>
    </div>
  );
}

export function LoginPage() {
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const { setMe } = useAuth();
  const [username, setUsername] = React.useState("admin");
  const [password, setPassword] = React.useState("");
  const [error, setError] = React.useState<string | null>(null);
  const [busy, setBusy] = React.useState(false);
  // 单点登录：按钮文字（未启用时为 null）、跳转中、失败原因
  const [ssoLabel, setSsoLabel] = React.useState<string | null>(null);
  const [ssoBusy, setSsoBusy] = React.useState(false);
  const [ssoError, setSsoError] = React.useState<string | null>(null);
  // 密码登录关闭时只剩单点登录按钮；读不到状态时按允许处理
  const [passwordLogin, setPasswordLogin] = React.useState(true);
  const ssoErrorId = params.get("sso_error");

  React.useEffect(() => {
    let cancelled = false;
    // 读不到状态时按未启用处理，密码登录照常可用
    api.auth
      .status()
      .then((st) => {
        if (cancelled) return;
        if (st.sso_enabled) setSsoLabel(st.sso_label || "单点登录");
        setPasswordLogin(st.password_login !== false);
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, []);

  React.useEffect(() => {
    if (!ssoErrorId) return;
    let cancelled = false;
    // 失败原因由服务端整理后保存，地址里只带一个 id，不把任意文本放进页面
    api.auth
      .oidcResult(ssoErrorId)
      .then((r) => {
        if (!cancelled && r.message) setSsoError(r.message);
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [ssoErrorId]);

  async function startSso() {
    setSsoBusy(true);
    setSsoError(null);
    try {
      const { url } = await api.auth.oidcLogin(safeNext(params.get("next")));
      redirect.to(url);
    } catch (err) {
      setSsoError(errorMessage(err));
      setSsoBusy(false);
    }
  }

  // 只有在确实能用单点登录时才隐藏密码表单，避免两种方式都看不到
  const showPassword = passwordLogin || !ssoLabel;

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!username || !password) {
      setError("填写用户名和密码");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const res = await api.auth.login({ username, password });
      setMe({ kind: "admin", username: res.username });
      navigate(safeNext(params.get("next")), { replace: true });
    } catch (err) {
      if (err instanceof ApiError && err.code === ErrorCode.AdminNotInitialized) {
        navigate("/setup", { replace: true });
        return;
      }
      // 错误次数和锁定时间由服务端写在说明里，例如「还剩 3 次机会」
      setError(errorMessage(err));
      setPassword("");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="grid min-h-svh lg:grid-cols-[1.1fr_1fr]">
      <AuthAside />
      <div className="flex items-center justify-center px-6 py-16">
        <form className="w-full max-w-[340px]" onSubmit={submit} noValidate>
          <div className="mb-10 lg:hidden">
            <Mark />
          </div>
          <h1 className="text-[26px] font-semibold tracking-[-0.01em]">登录 opsnap</h1>
          <p className="mt-1 text-[13.5px] text-ink-2">{window.location.host}</p>
          {showPassword && (
            <div className="mt-8 grid gap-4">
              <Field label="用户名">
                <Input
                  value={username}
                  onChange={(e) => setUsername(e.target.value)}
                  autoComplete="username"
                  className="h-10 bg-surface"
                />
              </Field>
              <Field label="密码">
                <Input
                  type="password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  autoComplete="current-password"
                  autoFocus
                  aria-invalid={error ? true : undefined}
                  aria-describedby={error ? "login-err" : undefined}
                  className="h-10 bg-surface"
                />
              </Field>
              <FormError id="login-err">{error}</FormError>
              <Button type="submit" className="h-10 w-full" disabled={busy || ssoBusy}>
                登录
              </Button>
            </div>
          )}
          {(ssoLabel || ssoError) && (
            <div className={showPassword ? "mt-4 grid gap-4" : "mt-8 grid gap-4"}>
              {ssoLabel && showPassword && (
                <div className="flex items-center gap-3 text-xs text-ink-3" aria-hidden>
                  <span className="h-px flex-1 bg-line" />或<span className="h-px flex-1 bg-line" />
                </div>
              )}
              {/* 原因里可能有提供方给的不能折行的长串（被拒账号的显示名回退为 sub、错误码），在任意处折行，不撑出登录框 */}
              <FormError className="wrap-anywhere">{ssoError}</FormError>
              {ssoLabel && (
                <Button
                  type="button"
                  variant={showPassword ? "outline" : "default"}
                  className="h-10 w-full"
                  disabled={ssoBusy}
                  onClick={startSso}
                >
                  {ssoBusy ? <Loader2 className="animate-spin" /> : <LogIn />}
                  {ssoBusy ? `正在跳转到 ${ssoLabel}…` : `用 ${ssoLabel} 登录`}
                </Button>
              )}
              {ssoLabel && !showPassword && (
                <p className="text-xs text-ink-3">密码登录已关闭。{ssoLabel} 不可用时，见部署说明里的恢复方法。</p>
              )}
            </div>
          )}
        </form>
      </div>
    </div>
  );
}

/** 登录页插画：一层层的快照「地层」，最上面一层是主题色 */
function Strata() {
  const layers = Array.from({ length: 14 }, (_, i) => i);
  return (
    <svg viewBox="0 0 520 300" className="w-full max-w-[560px]" aria-hidden>
      {layers.map((i) => {
        const y = 40 + i * 17;
        const amp = 10 + ((i * 7) % 9);
        const ph = i * 0.7;
        const pts: string[] = [];
        for (let x = 0; x <= 520; x += 20) pts.push(`${x} ${(y + Math.sin(x / 70 + ph) * amp * 0.4).toFixed(1)}`);
        return (
          <path
            key={i}
            d={`M${pts.join(" L")}`}
            fill="none"
            stroke={i === 0 ? "var(--brand)" : "currentColor"}
            strokeWidth={i === 0 ? 3 : 1.2}
            opacity={i === 0 ? 1 : 0.55 - i * 0.03}
          />
        );
      })}
    </svg>
  );
}
