// 设置 → 单点登录：提供方配置、绑定账号、密码登录开关
import * as React from "react";
import { useSearchParams } from "react-router";
import { AlertTriangle, Check, Lock } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { CodeBlock, Field, FormError, Mono, Row, Sheet, Skeleton } from "@/components/bits";
import { LoadError } from "@/pages/settings";
import { SECRET_MASKED, api, errorMessage, type SSOSettings, type SSOUpdate } from "@/lib/api";
import { useLoad } from "@/lib/use-load";
import { fmtDate } from "@/lib/format";
import { redirect } from "@/lib/redirect";

/** 与后端 sso_entity 一致 */
const MAX_BUTTON_LABEL = 32;
const CALLBACK_PATH = "/api/v1/auth/oidc/callback";

interface ProviderForm {
  issuer: string;
  clientId: string;
  clientSecret: string;
  buttonLabel: string;
  panelUrl: string;
}
type ProviderErrors = Partial<Record<keyof ProviderForm, string>>;

function toForm(s: SSOSettings): ProviderForm {
  return {
    issuer: s.issuer,
    clientId: s.client_id,
    clientSecret: "",
    buttonLabel: s.button_label,
    // 未配置时预填浏览器里的地址，通常就是面板对外地址
    panelUrl: s.panel_url || window.location.origin,
  };
}

function isHttpURL(v: string): boolean {
  try {
    const u = new URL(v);
    return (u.protocol === "http:" || u.protocol === "https:") && u.host !== "";
  } catch {
    return false;
  }
}

/** 密码登录关闭（且没有被环境变量强制打开）时，绑定不能失效：Issuer、Client ID、解除绑定、停用都锁定 */
function isLocked(s: SSOSettings): boolean {
  return !s.password_login && !s.password_login_forced;
}

const LOCKED_HINT = "密码登录关闭期间不能修改，避免绑定失效后无法登录";

function LockedHint() {
  return (
    <span className="inline-flex items-center gap-1">
      <Lock className="size-3" aria-hidden />
      {LOCKED_HINT}
    </span>
  );
}

/** 回调地址：面板对外地址去掉结尾的 / 再加固定路径 */
export function callbackURL(panelUrl: string): string {
  return panelUrl.trim().replace(/\/+$/, "") + CALLBACK_PATH;
}

/** 校验提供方配置；全部合法时返回提交体。secretSaved 表示已保存过 Client Secret，可以留空 */
export function validateProvider(f: ProviderForm, secretSaved: boolean): { body?: SSOUpdate; errors: ProviderErrors } {
  const errors: ProviderErrors = {};
  const label = f.buttonLabel.trim();
  if (!f.issuer.trim()) errors.issuer = "填写 Issuer 地址，例如 https://git.example.com/";
  else if (!isHttpURL(f.issuer.trim())) errors.issuer = "Issuer 地址须以 http:// 或 https:// 开头";
  if (!f.clientId.trim()) errors.clientId = "填写 Client ID";
  if (!f.clientSecret && !secretSaved) errors.clientSecret = "填写 Client Secret";
  if (!label || [...label].length > MAX_BUTTON_LABEL)
    errors.buttonLabel = `按钮文字不能为空，最多 ${MAX_BUTTON_LABEL} 个字符`;
  if (!isHttpURL(f.panelUrl.trim()))
    errors.panelUrl = "填写浏览器打开 opsnap 用的完整地址，例如 https://opsnap.example.com";
  if (Object.keys(errors).length) return { errors };
  return {
    errors,
    body: {
      issuer: f.issuer.trim(),
      client_id: f.clientId.trim(),
      client_secret: f.clientSecret,
      button_label: label,
      panel_url: f.panelUrl.trim(),
    },
  };
}

export function SsoSettings() {
  const { data, error, loading, reload, setData } = useLoad(api.sso.get);
  const [params] = useSearchParams();
  const bindErrorId = params.get("sso_error");
  const [bindError, setBindError] = React.useState<string | null>(null);

  React.useEffect(() => {
    if (!bindErrorId) return;
    let cancelled = false;
    // 绑定失败从提供方跳回来时，地址里只带失败原因的 id
    api.auth
      .oidcResult(bindErrorId)
      .then((r) => {
        if (!cancelled && r.message) setBindError(r.message);
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [bindErrorId]);

  if (error) return <LoadError what="单点登录设置" message={error} onRetry={reload} />;
  if (loading || !data) return <SsoSkeleton />;
  return (
    <div className="flex max-w-3xl flex-col gap-10">
      {bindError && (
        <Sheet className="flex items-start gap-3 border-fail/40 bg-fail-wash px-5 py-4" role="alert">
          <AlertTriangle className="mt-0.5 size-4 shrink-0 text-fail" aria-hidden />
          <p className="flex-1 text-[13.5px] text-fail">绑定失败：{bindError}</p>
        </Sheet>
      )}
      <Provider data={data} onSaved={setData} />
      <Binding data={data} onChanged={setData} />
      <PasswordLogin data={data} onChanged={setData} />
    </div>
  );
}

function Provider({ data, onSaved }: { data: SSOSettings; onSaved: (s: SSOSettings) => void }) {
  const [form, setForm] = React.useState<ProviderForm>(() => toForm(data));
  const [errors, setErrors] = React.useState<ProviderErrors>({});
  const [submitError, setSubmitError] = React.useState<string | null>(null);
  const [saving, setSaving] = React.useState(false);
  const [disabling, setDisabling] = React.useState(false);
  // 已绑定时修改 Issuer 或 Client ID 会解除绑定，先确认
  const [confirmBody, setConfirmBody] = React.useState<SSOUpdate | null>(null);

  // 保存或停用后服务端返回新数据：重置表单
  const [prevData, setPrevData] = React.useState(data);
  if (data !== prevData) {
    setPrevData(data);
    setForm(toForm(data));
  }

  const secretSaved = data.client_secret === SECRET_MASKED;
  const locked = isLocked(data);
  const set = (patch: Partial<ProviderForm>) => setForm((f) => ({ ...f, ...patch }));
  const label = form.buttonLabel.trim() || "…";

  async function save(e: React.FormEvent) {
    e.preventDefault();
    const { body, errors } = validateProvider(form, secretSaved);
    setErrors(errors);
    setSubmitError(null);
    if (!body) return;
    if (data.binding && (body.issuer !== data.issuer || body.client_id !== data.client_id)) {
      setConfirmBody(body);
      return;
    }
    await submit(body);
  }

  async function submit(body: SSOUpdate) {
    setSaving(true);
    try {
      onSaved(await api.sso.update(body));
      toast.success("已保存，提供方检查通过");
    } catch (err) {
      setSubmitError(errorMessage(err));
    } finally {
      setSaving(false);
    }
  }

  async function disable() {
    try {
      onSaved(await api.sso.disable());
      toast.success("单点登录已停用");
    } catch (err) {
      toast.error(`停用失败：${errorMessage(err)}`);
    }
  }

  return (
    <form onSubmit={save} noValidate>
      <Row title="提供方" note="任何支持 OIDC 的服务，例如 Gitea、Authentik、Keycloak。">
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <Field
            label="Issuer 地址"
            className="sm:col-span-2"
            error={errors.issuer}
            hint={
              locked ? (
                <LockedHint />
              ) : (
                <>
                  Gitea 填它的 ROOT_URL；opsnap 会读取 <Mono>/.well-known/openid-configuration</Mono>
                </>
              )
            }
          >
            <Input
              disabled={locked}
              value={form.issuer}
              onChange={(e) => set({ issuer: e.target.value })}
              placeholder="https://git.example.com/"
              autoComplete="off"
            />
          </Field>
          <Field
            label="Client ID"
            className="sm:col-span-2"
            error={errors.clientId}
            hint={locked ? <LockedHint /> : undefined}
          >
            <Input
              disabled={locked}
              value={form.clientId}
              onChange={(e) => set({ clientId: e.target.value })}
              className="font-mono text-[12.5px]"
              autoComplete="off"
            />
          </Field>
          <Field
            label="Client Secret"
            error={errors.clientSecret}
            hint={secretSaved ? "已设置；留空表示不修改" : undefined}
          >
            <Input
              type="password"
              value={form.clientSecret}
              onChange={(e) => set({ clientSecret: e.target.value })}
              placeholder={secretSaved ? SECRET_MASKED : ""}
              autoComplete="new-password"
            />
          </Field>
          <Field label="按钮文字" error={errors.buttonLabel} hint={`登录页显示「用 ${label} 登录」`}>
            <Input
              value={form.buttonLabel}
              onChange={(e) => set({ buttonLabel: e.target.value })}
              maxLength={MAX_BUTTON_LABEL}
            />
          </Field>
          <Field
            label="面板对外地址"
            className="sm:col-span-2"
            error={errors.panelUrl}
            hint="浏览器打开 opsnap 用的地址，回调地址由它拼成"
          >
            <Input
              value={form.panelUrl}
              onChange={(e) => set({ panelUrl: e.target.value })}
              placeholder="https://opsnap.example.com"
              autoComplete="off"
            />
          </Field>
          <div className="sm:col-span-2">
            <CodeBlock
              title="回调地址：填到提供方 OAuth2 应用的重定向 URI（Gitea 在「设置 → 应用」里创建）"
              text={callbackURL(form.panelUrl)}
            />
          </div>
          <FormError className="sm:col-span-2">{submitError}</FormError>
          <div className="flex flex-wrap items-center gap-3 sm:col-span-2">
            <Button type="submit" disabled={saving}>
              保存并检查
            </Button>
            {data.configured && (
              <Button
                type="button"
                variant="ghost"
                className="text-fail hover:text-fail"
                disabled={locked}
                onClick={() => setDisabling(true)}
              >
                停用单点登录
              </Button>
            )}
            {data.configured && locked && <p className="text-xs text-ink-3">密码登录关闭期间不能停用单点登录。</p>}
          </div>
        </div>
      </Row>

      <AlertDialog open={confirmBody !== null} onOpenChange={(v) => !v && setConfirmBody(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>保存后会解除当前绑定</AlertDialogTitle>
            <AlertDialogDescription>
              修改了 Issuer 或 Client ID，原来绑定的 {data.binding?.name} 不再有效，保存后需要重新绑定。
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                if (confirmBody) void submit(confirmBody);
              }}
            >
              保存并解除绑定
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <AlertDialog open={disabling} onOpenChange={setDisabling}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>停用单点登录？</AlertDialogTitle>
            <AlertDialogDescription>
              会清空提供方配置并解除绑定，之后登录页不再显示「用 {data.button_label} 登录」。
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction
              className="bg-destructive text-white hover:bg-destructive/90"
              onClick={() => void disable()}
            >
              停用
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </form>
  );
}

function Binding({ data, onChanged }: { data: SSOSettings; onChanged: (s: SSOSettings) => void }) {
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [unbinding, setUnbinding] = React.useState(false);
  const label = data.button_label;

  async function bind() {
    setBusy(true);
    setError(null);
    try {
      const { url } = await api.sso.bind();
      redirect.to(url);
    } catch (err) {
      setError(errorMessage(err));
      setBusy(false);
    }
  }

  async function unbind() {
    try {
      onChanged(await api.sso.unbind());
      toast.success("已解除绑定");
    } catch (err) {
      toast.error(`解除绑定失败：${errorMessage(err)}`);
    }
  }

  const b = data.binding;
  return (
    <Row title="绑定账号" note="只有绑定的这个账号能用单点登录进入 opsnap。">
      {b ? (
        <div className="grid gap-1.5">
          <div className="flex flex-wrap items-center gap-3">
            {/* 提供方不给 preferred_username 和 name 时名字就是 sub，同样可能是不能折行的长串 */}
            <p className="flex flex-1 items-center gap-1.5 text-[13.5px] wrap-anywhere">
              <Check className="size-4 text-ok" aria-hidden />
              已绑定 <span className="font-medium">{b.name}</span>
            </p>
            <Button
              size="sm"
              variant="ghost"
              className="text-fail hover:text-fail"
              disabled={isLocked(data)}
              onClick={() => setUnbinding(true)}
            >
              解除绑定
            </Button>
          </div>
          {/* sub 可能是不能折行的长串（如 Authentik 默认的 64 位哈希），窄屏下在任意处折行，不撑出屏幕 */}
          <p className="text-xs text-ink-3 num wrap-anywhere">
            sub {b.sub} · {b.issuer} · 绑定于 {fmtDate(b.bound_at)}
          </p>
          {isLocked(data) && <p className="text-xs text-ink-3">密码登录关闭期间不能解除绑定。</p>}
        </div>
      ) : (
        <div className="grid gap-2">
          <div className="flex flex-wrap items-center gap-3">
            <p className="flex-1 text-[13.5px] text-ink-2">
              {data.configured ? `还没有绑定。点击后跳转到 ${label} 授权，完成后回到这里。` : "先保存提供方配置。"}
            </p>
            <Button size="sm" disabled={!data.configured || busy} onClick={bind}>
              绑定 {label} 账号
            </Button>
          </div>
          <FormError>{error}</FormError>
        </div>
      )}

      <AlertDialog open={unbinding} onOpenChange={setUnbinding}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>解除绑定 {b?.name}？</AlertDialogTitle>
            <AlertDialogDescription>
              解除后不能再用 {label} 登录，直到重新绑定。当前会话不受影响。
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction
              className="bg-destructive text-white hover:bg-destructive/90"
              onClick={() => void unbind()}
            >
              解除绑定
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Row>
  );
}

function PasswordLogin({ data, onChanged }: { data: SSOSettings; onChanged: (s: SSOSettings) => void }) {
  const [confirmOff, setConfirmOff] = React.useState(false);
  const [busy, setBusy] = React.useState(false);
  const label = data.button_label;
  const bound = data.configured && data.binding !== null;
  const forced = data.password_login_forced;

  async function set(enabled: boolean) {
    setBusy(true);
    try {
      onChanged(await api.sso.setPasswordLogin(enabled));
      toast.success(enabled ? "密码登录已打开" : "密码登录已关闭");
    } catch (err) {
      toast.error(`保存失败：${errorMessage(err)}`);
    } finally {
      setBusy(false);
    }
  }

  return (
    <Row title="密码登录">
      <div className="grid gap-3">
        <div className="flex items-start justify-between gap-4">
          <span>
            <span className="block text-[13px] font-medium">允许用户名密码登录</span>
            <span className="block text-xs text-ink-3">
              {bound ? `关闭后登录页只显示「用 ${label} 登录」` : "绑定账号后才能关闭"}
            </span>
          </span>
          <Switch
            checked={forced || data.password_login}
            disabled={busy || forced || (!bound && data.password_login)}
            onCheckedChange={(v) => (v ? void set(true) : setConfirmOff(true))}
            aria-label="允许用户名密码登录"
          />
        </div>
        {forced ? (
          <p className="flex items-start gap-1.5 text-xs text-warn">
            <AlertTriangle className="mt-px size-3.5 shrink-0" aria-hidden />
            <span>
              环境变量 <Mono className="text-[11.5px]">OPSNAP_PASSWORD_LOGIN=1</Mono> 正在强制打开密码登录
              {data.password_login ? "。" : "（设置里保存的是「关闭」）。恢复访问后，去掉这个变量并重启容器。"}
            </span>
          </p>
        ) : (
          bound && (
            <p className="text-xs text-ink-3">
              {label} 不可用时，用 <Mono className="text-[11.5px]">OPSNAP_PASSWORD_LOGIN=1</Mono>{" "}
              重启容器可以临时打开密码登录。
            </p>
          )
        )}
      </div>

      <AlertDialog open={confirmOff} onOpenChange={setConfirmOff}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>关闭密码登录？</AlertDialogTitle>
            <AlertDialogDescription>
              之后只能用 {label} 账号 {data.binding?.name} 登录。{label} 不可用时，用环境变量 OPSNAP_PASSWORD_LOGIN=1
              重启容器可以临时打开密码登录。API 密钥不受影响。
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction onClick={() => void set(false)}>关闭密码登录</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Row>
  );
}

function SsoSkeleton() {
  return (
    <div className="flex max-w-3xl flex-col gap-6" aria-busy="true" aria-label="正在读取单点登录设置">
      <Skeleton className="h-8 w-1/2" />
      <Skeleton className="h-40" />
      <Skeleton className="h-10 w-2/3" />
    </div>
  );
}
