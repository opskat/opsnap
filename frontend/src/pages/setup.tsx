import * as React from "react";
import { useNavigate } from "react-router";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Field, FormError } from "@/components/bits";
import { Mark } from "@/components/shell";
import { MIN_PASSWORD_LEN, checkNewPassword } from "@/components/change-password-dialog";
import { ApiError, ErrorCode, api, errorMessage } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { AuthAside } from "./login";

/** 首次运行：还没有管理员时创建一个，创建后直接登录 */
export function SetupPage() {
  const navigate = useNavigate();
  const { setMe } = useAuth();
  const [ready, setReady] = React.useState(false);
  const [username, setUsername] = React.useState("admin");
  const [password, setPassword] = React.useState("");
  const [confirm, setConfirm] = React.useState("");
  const [error, setError] = React.useState<string | null>(null);
  const [busy, setBusy] = React.useState(false);

  React.useEffect(() => {
    let cancelled = false;
    api.auth
      .status()
      .then((st) => {
        if (cancelled) return;
        if (st.initialized) navigate("/login", { replace: true });
        else setReady(true);
      })
      .catch((err) => {
        if (!cancelled) {
          setError(errorMessage(err));
          setReady(true);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [navigate]);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!username.trim()) {
      setError("填写用户名");
      return;
    }
    const invalid = checkNewPassword(password, confirm, "密码");
    if (invalid) {
      setError(invalid);
      return;
    }
    setBusy(true);
    setError(null);
    const creds = { username: username.trim(), password };
    try {
      await api.auth.setup(creds);
      const res = await api.auth.login(creds);
      setMe({ kind: "admin", username: res.username });
      navigate("/", { replace: true });
    } catch (err) {
      if (err instanceof ApiError && err.code === ErrorCode.AdminAlreadyInitialized) {
        navigate("/login", { replace: true });
        return;
      }
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="grid min-h-svh lg:grid-cols-[1.1fr_1fr]">
      <AuthAside />
      <div className="flex items-center justify-center px-6 py-16">
        <form className="w-full max-w-[340px]" onSubmit={submit} noValidate aria-busy={!ready}>
          <div className="mb-10 lg:hidden">
            <Mark />
          </div>
          <h1 className="text-[26px] font-semibold tracking-[-0.01em]">创建管理员</h1>
          <p className="mt-1 text-[13.5px] text-ink-2">opsnap 还没有管理员，先创建一个。</p>
          <div className="mt-8 grid gap-4">
            <Field label="用户名">
              <Input
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                autoComplete="username"
                className="h-10 bg-surface"
              />
            </Field>
            <Field label="密码" hint={`至少 ${MIN_PASSWORD_LEN} 位`}>
              <Input
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                autoComplete="new-password"
                className="h-10 bg-surface"
              />
            </Field>
            <Field label="再输一次">
              <Input
                type="password"
                value={confirm}
                onChange={(e) => setConfirm(e.target.value)}
                autoComplete="new-password"
                className="h-10 bg-surface"
              />
            </Field>
            <FormError>{error}</FormError>
            <Button type="submit" className="h-10 w-full" disabled={busy || !ready}>
              创建管理员
            </Button>
          </div>
        </form>
      </div>
    </div>
  );
}
