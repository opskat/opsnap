// 设置 → 单点登录：提供方配置
import * as React from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
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
import { CodeBlock, Field, FormError, Mono, Row, Skeleton } from "@/components/bits";
import { LoadError } from "@/pages/settings";
import { SECRET_MASKED, api, errorMessage, type SSOSettings, type SSOUpdate } from "@/lib/api";
import { useLoad } from "@/lib/use-load";

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
  if (error) return <LoadError what="单点登录设置" message={error} onRetry={reload} />;
  if (loading || !data) return <SsoSkeleton />;
  return (
    <div className="flex max-w-3xl flex-col gap-10">
      <Provider data={data} onSaved={setData} />
    </div>
  );
}

function Provider({ data, onSaved }: { data: SSOSettings; onSaved: (s: SSOSettings) => void }) {
  const [form, setForm] = React.useState<ProviderForm>(() => toForm(data));
  const [errors, setErrors] = React.useState<ProviderErrors>({});
  const [submitError, setSubmitError] = React.useState<string | null>(null);
  const [saving, setSaving] = React.useState(false);
  const [disabling, setDisabling] = React.useState(false);

  // 保存或停用后服务端返回新数据：重置表单
  const [prevData, setPrevData] = React.useState(data);
  if (data !== prevData) {
    setPrevData(data);
    setForm(toForm(data));
  }

  const secretSaved = data.client_secret === SECRET_MASKED;
  const set = (patch: Partial<ProviderForm>) => setForm((f) => ({ ...f, ...patch }));
  const label = form.buttonLabel.trim() || "…";

  async function save(e: React.FormEvent) {
    e.preventDefault();
    const { body, errors } = validateProvider(form, secretSaved);
    setErrors(errors);
    setSubmitError(null);
    if (!body) return;
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
        <div className="grid gap-4 sm:grid-cols-2">
          <Field
            label="Issuer 地址"
            className="sm:col-span-2"
            error={errors.issuer}
            hint={
              <>
                Gitea 填它的 ROOT_URL；opsnap 会读取 <Mono>/.well-known/openid-configuration</Mono>
              </>
            }
          >
            <Input
              value={form.issuer}
              onChange={(e) => set({ issuer: e.target.value })}
              placeholder="https://git.example.com/"
              autoComplete="off"
            />
          </Field>
          <Field label="Client ID" className="sm:col-span-2" error={errors.clientId}>
            <Input
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
                onClick={() => setDisabling(true)}
              >
                停用单点登录
              </Button>
            )}
          </div>
        </div>
      </Row>

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

function SsoSkeleton() {
  return (
    <div className="flex max-w-3xl flex-col gap-6" aria-busy="true" aria-label="正在读取单点登录设置">
      <Skeleton className="h-8 w-1/2" />
      <Skeleton className="h-40" />
      <Skeleton className="h-10 w-2/3" />
    </div>
  );
}
