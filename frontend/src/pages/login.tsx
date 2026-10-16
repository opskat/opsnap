import * as React from "react";
import { useNavigate, useSearchParams } from "react-router";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Field, FormError } from "@/components/bits";
import { Mark } from "@/components/shell";
import { ApiError, ErrorCode, api, errorMessage } from "@/lib/api";
import { useAuth } from "@/lib/auth";

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
            <Button type="submit" className="h-10 w-full" disabled={busy}>
              登录
            </Button>
          </div>
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
