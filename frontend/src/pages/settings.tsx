import * as React from "react";
import { useSearchParams } from "react-router";
import { AlertTriangle, Check, Copy, Plus } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
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
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { CodeBlock, Field, FormError, Mono, PageTitle, Row, Sheet, Skeleton, useCopy } from "@/components/bits";
import { ChangePasswordDialog } from "@/components/change-password-dialog";
import { RotateMasterKeyDialog } from "@/components/rotate-master-key-dialog";
import {
  api,
  errorMessage,
  type APIKey,
  type APIKeyExpiry,
  type CreatedAPIKey,
  type Settings,
  type SettingsUpdate,
} from "@/lib/api";
import { fmtAgo, fmtDate } from "@/lib/format";
import { useLoad } from "@/lib/use-load";
import { cn } from "@/lib/utils";

const TABS = [
  { id: "general", label: "通用" },
  { id: "api", label: "API 密钥" },
] as const;
type TabId = (typeof TABS)[number]["id"];

export function SettingsPage() {
  const [params, setParams] = useSearchParams();
  const tab: TabId = params.get("tab") === "api" ? "api" : "general";
  const select = (id: TabId) => setParams(id === "general" ? {} : { tab: id }, { replace: true });

  return (
    <>
      <PageTitle title="设置" />
      <div className="mb-8 flex gap-6 border-b border-line" role="tablist" aria-label="设置分类">
        {TABS.map((t) => (
          <button
            key={t.id}
            id={`settings-tab-${t.id}`}
            role="tab"
            type="button"
            aria-selected={tab === t.id}
            aria-controls={`settings-panel-${t.id}`}
            tabIndex={tab === t.id ? 0 : -1}
            onClick={() => select(t.id)}
            onKeyDown={(e) => {
              if (e.key === "ArrowRight" || e.key === "ArrowLeft") {
                const next = TABS[(TABS.findIndex((x) => x.id === t.id) + 1) % TABS.length].id;
                select(next);
                document.getElementById(`settings-tab-${next}`)?.focus();
              }
            }}
            className={cn(
              "-mb-px border-b-2 border-transparent pb-2.5 text-[14px] text-ink-2 hover:text-ink",
              tab === t.id && "border-ink font-medium text-ink"
            )}
          >
            {t.label}
          </button>
        ))}
      </div>
      <div role="tabpanel" id={`settings-panel-${tab}`} aria-labelledby={`settings-tab-${tab}`}>
        {tab === "general" ? <General /> : <ApiKeys />}
      </div>
    </>
  );
}

// ---- 通用 ----

/** 与后端 setting_entity 的取值范围一致 */
const MAX_CONCURRENT_RUNS = 64;
const MAX_RETENTION_DAYS = 3650;

interface GeneralForm {
  maxConcurrent: string;
  retentionDays: string;
  timezone: string;
  clearMirror: boolean;
}
type GeneralErrors = Partial<Record<"maxConcurrent" | "retentionDays" | "timezone", string>>;

function toForm(s: Settings): GeneralForm {
  return {
    maxConcurrent: String(s.max_concurrent_runs),
    retentionDays: String(s.retention_days),
    timezone: s.timezone,
    clearMirror: s.clear_mirror_after_backup,
  };
}

function intIn(v: string, min: number, max: number): number | null {
  if (!/^\d+$/.test(v.trim())) return null;
  const n = Number(v.trim());
  return n >= min && n <= max ? n : null;
}

/** 校验通用设置；全部合法时返回提交体 */
export function validateGeneral(f: GeneralForm): { body?: SettingsUpdate; errors: GeneralErrors } {
  const errors: GeneralErrors = {};
  const max = intIn(f.maxConcurrent, 1, MAX_CONCURRENT_RUNS);
  const days = intIn(f.retentionDays, 1, MAX_RETENTION_DAYS);
  if (max === null) errors.maxConcurrent = `同时运行的任务数须在 1 到 ${MAX_CONCURRENT_RUNS} 之间`;
  if (days === null) errors.retentionDays = `运行记录保留天数须在 1 到 ${MAX_RETENTION_DAYS} 之间`;
  if (!f.timezone.trim()) errors.timezone = "填写时区，例如 Asia/Shanghai";
  if (max === null || days === null || errors.timezone) return { errors };
  return {
    errors,
    body: {
      max_concurrent_runs: max,
      retention_days: days,
      timezone: f.timezone.trim(),
      clear_mirror_after_backup: f.clearMirror,
    },
  };
}

function timezones(): string[] {
  try {
    return Intl.supportedValuesOf("timeZone");
  } catch {
    return [];
  }
}

function General() {
  const { data, error, loading, reload, setData } = useLoad(api.settings.get);
  const [form, setForm] = React.useState<GeneralForm | null>(null);
  const [errors, setErrors] = React.useState<GeneralErrors>({});
  const [submitError, setSubmitError] = React.useState<string | null>(null);
  const [saving, setSaving] = React.useState(false);
  const [changePw, setChangePw] = React.useState(false);
  const [rotating, setRotating] = React.useState(false);
  // 轮换后的时间单独记下，不重新读取设置，以免冲掉没保存的修改
  const [rotatedAt, setRotatedAt] = React.useState<number | null>(null);
  const zones = React.useMemo(() => timezones(), []);

  // 读到（或重新读到）数据时重置表单：在渲染期间对比上一次的 data
  const [prevData, setPrevData] = React.useState(data);
  if (data !== prevData) {
    setPrevData(data);
    if (data) setForm(toForm(data));
  }

  if (error) return <LoadError what="设置" message={error} onRetry={reload} />;
  if (loading || !data || !form) return <GeneralSkeleton />;

  const set = (patch: Partial<GeneralForm>) => setForm((f) => (f ? { ...f, ...patch } : f));

  async function save(e: React.FormEvent) {
    e.preventDefault();
    if (!form) return;
    const { body, errors } = validateGeneral(form);
    setErrors(errors);
    setSubmitError(null);
    if (!body) return;
    setSaving(true);
    try {
      setData(await api.settings.update(body));
      toast.success("设置已保存");
    } catch (err) {
      setSubmitError(errorMessage(err));
    } finally {
      setSaving(false);
    }
  }

  return (
    <form className="flex max-w-3xl flex-col gap-10" onSubmit={save} noValidate>
      <Row title="管理员">
        <div className="flex flex-wrap items-center gap-3">
          <p className="flex-1 text-[13.5px]">
            用户名 <span className="font-medium">{data.admin_username}</span>
          </p>
          <Button type="button" size="sm" variant="outline" onClick={() => setChangePw(true)}>
            修改密码
          </Button>
        </div>
      </Row>
      <Row title="运行">
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="同时运行的任务" error={errors.maxConcurrent}>
            <Input
              inputMode="numeric"
              value={form.maxConcurrent}
              onChange={(e) => set({ maxConcurrent: e.target.value })}
              className="w-20 text-center num"
            />
          </Field>
          <Field label="运行记录保留" htmlFor="retention-days" error={errors.retentionDays}>
            <div className="flex items-center gap-2">
              <Input
                id="retention-days"
                inputMode="numeric"
                aria-label="运行记录保留天数"
                aria-invalid={errors.retentionDays ? true : undefined}
                aria-describedby={errors.retentionDays ? "retention-days-msg" : undefined}
                value={form.retentionDays}
                onChange={(e) => set({ retentionDays: e.target.value })}
                className="w-20 text-center num"
              />
              <span className="text-[13px] text-ink-2">天</span>
            </div>
          </Field>
          <Field label="时区" error={errors.timezone} hint="IANA 时区名，定时计划按它计算">
            <Input
              list="tz-options"
              value={form.timezone}
              onChange={(e) => set({ timezone: e.target.value })}
              autoComplete="off"
            />
          </Field>
          <datalist id="tz-options">
            {zones.map((z) => (
              <option key={z} value={z} />
            ))}
          </datalist>
          <div className="flex items-start justify-between gap-4 sm:col-span-2">
            <span>
              <span className="block text-[13px] font-medium">备份后清空文件镜像</span>
              <span className="block text-xs text-ink-3">本机不留明文副本，但每次都要全量传输</span>
            </span>
            <Switch
              checked={form.clearMirror}
              onCheckedChange={(v) => set({ clearMirror: v })}
              aria-label="备份后清空文件镜像"
            />
          </div>
          <p className="flex items-start gap-1.5 text-xs text-warn sm:col-span-2">
            <AlertTriangle className="mt-px size-3.5 shrink-0" aria-hidden />
            <span>
              文件镜像目录 <Mono className="text-[11.5px]">{data.mirror_dir}</Mono> 存放明文
              {form.clearMirror ? "，每次备份后清空。" : "，备份后保留以便增量同步。"}
            </span>
          </p>
        </div>
      </Row>
      <Row title="主密钥">
        <div className="flex flex-wrap items-center gap-3">
          <p className="flex-1 text-[13.5px]">
            加密保存的密码、私钥和 opsnap 密钥，来自 <Mono>OPSNAP_SECRET_KEY</Mono>
          </p>
          {data.master_key.configured ? (
            <span className="flex items-center gap-1.5 text-[13px] text-ok">
              <Check className="size-3.5" aria-hidden />
              已加载
              {data.master_key.algorithm && <span className="text-ink-3">· {data.master_key.algorithm}</span>}
            </span>
          ) : (
            <span className="flex items-center gap-1.5 text-[13px] text-fail">
              <AlertTriangle className="size-3.5" aria-hidden />
              未加载
            </span>
          )}
          <Button
            type="button"
            size="sm"
            variant="outline"
            disabled={!data.master_key.configured}
            onClick={() => setRotating(true)}
          >
            轮换主密钥
          </Button>
        </div>
        {(rotatedAt ?? data.master_key.rotated_at) > 0 && (
          <p className="mt-1.5 text-xs text-ink-3 num">上次轮换 {fmtDate(rotatedAt ?? data.master_key.rotated_at)}</p>
        )}
      </Row>
      <div className="grid gap-3 sm:pl-[220px]">
        <FormError>{submitError}</FormError>
        <div>
          <Button type="submit" disabled={saving}>
            保存设置
          </Button>
        </div>
      </div>
      <ChangePasswordDialog open={changePw} onOpenChange={setChangePw} />
      <RotateMasterKeyDialog
        open={rotating}
        onOpenChange={setRotating}
        onRotated={() => setRotatedAt(Math.floor(Date.now() / 1000))}
      />
    </form>
  );
}

function GeneralSkeleton() {
  return (
    <div className="flex max-w-3xl flex-col gap-6" aria-busy="true" aria-label="正在读取设置">
      <Skeleton className="h-8 w-1/2" />
      <Skeleton className="h-24" />
      <Skeleton className="h-10 w-2/3" />
    </div>
  );
}

/** 读取失败：写明哪个接口出错，并提供重试 */
export function LoadError({ what, message, onRetry }: { what: string; message: string; onRetry: () => void }) {
  return (
    <Sheet className="flex flex-wrap items-center gap-3 px-5 py-4" role="alert">
      <AlertTriangle className="size-4 text-fail" aria-hidden />
      <p className="flex-1 text-[13.5px]">
        读取{what}失败：{message}
      </p>
      <Button size="sm" variant="outline" onClick={onRetry}>
        重试
      </Button>
    </Sheet>
  );
}

// ---- API 密钥 ----

function expiresText(k: APIKey, now: number): { text: string; expired: boolean } {
  if (!k.expires_at) return { text: "永不", expired: false };
  if (k.expires_at <= now) return { text: "已过期", expired: true };
  return { text: fmtDate(k.expires_at), expired: false };
}

function ApiKeys() {
  const load = React.useCallback(async () => (await api.apikeys.list()).list ?? [], []);
  const { data: keys, error, loading, reload } = useLoad(load);
  const [creating, setCreating] = React.useState(false);
  const [revoking, setRevoking] = React.useState<APIKey | null>(null);
  const [now] = React.useState(() => Math.floor(Date.now() / 1000));
  const origin = window.location.origin;

  async function revoke(k: APIKey) {
    try {
      await api.apikeys.revoke(k.id);
      toast.success(`已撤销 ${k.name}`);
    } catch (err) {
      toast.error(`撤销失败：${errorMessage(err)}`);
    }
    reload();
  }

  return (
    <div className="flex flex-col gap-10">
      <section>
        <div className="mb-3 flex items-center justify-between">
          <h2 className="text-[15px] font-semibold">密钥</h2>
          <Button size="sm" onClick={() => setCreating(true)}>
            <Plus />
            新建密钥
          </Button>
        </div>
        {error ? (
          <LoadError what="API 密钥" message={error} onRetry={reload} />
        ) : loading && !keys ? (
          <Sheet className="grid gap-3 p-5" aria-busy="true" aria-label="正在读取 API 密钥">
            <Skeleton className="h-5 w-2/3" />
            <Skeleton className="h-5 w-1/2" />
          </Sheet>
        ) : !keys?.length ? (
          <Sheet className="px-5 py-8 text-center text-[13.5px] text-ink-2">
            还没有 API 密钥。给 AI 或脚本用时新建一把。
          </Sheet>
        ) : (
          <Sheet className="overflow-hidden">
            <div
              className="hidden grid-cols-[1.4fr_1fr_1fr_1fr_auto] gap-6 border-b border-line px-5 py-2.5 text-xs text-ink-3 md:grid"
              aria-hidden
            >
              <span>名称</span>
              <span>最近使用</span>
              <span>创建</span>
              <span>过期</span>
              <span className="w-14" />
            </div>
            <ul className="divide-y divide-line">
              {keys.map((k) => {
                const exp = expiresText(k, now);
                return (
                  <li
                    key={k.id}
                    className="grid items-center gap-x-6 gap-y-1 px-5 py-3.5 text-[13px] md:grid-cols-[1.4fr_1fr_1fr_1fr_auto]"
                  >
                    <span>
                      <span className="block font-medium">{k.name}</span>
                      <Mono className="text-ink-3">{k.prefix}…</Mono>
                    </span>
                    <Cell label="最近使用">{k.last_used_at ? fmtAgo(k.last_used_at) : "从未使用"}</Cell>
                    <Cell label="创建">{fmtDate(k.created_at)}</Cell>
                    <Cell label="过期" className={exp.expired ? "text-fail" : undefined}>
                      {exp.text}
                    </Cell>
                    <Button
                      size="sm"
                      variant="ghost"
                      className="w-14 justify-self-start text-fail hover:text-fail md:justify-self-auto"
                      aria-label={`撤销 ${k.name}`}
                      onClick={() => setRevoking(k)}
                    >
                      撤销
                    </Button>
                  </li>
                );
              })}
            </ul>
          </Sheet>
        )}
      </section>

      <section>
        <h2 className="mb-3 text-[15px] font-semibold">接入</h2>
        <div className="grid gap-4 lg:grid-cols-[1fr_320px]">
          <CodeBlock
            title="示例：确认密钥可用"
            text={`curl ${origin}/api/v1/auth/me \\\n  -H "Authorization: Bearer osk_…"`}
          />
          <Sheet className="p-4 text-[13px]">
            <div className="font-medium">OpenAPI 文档</div>
            <a
              href="/api/v1/openapi.json"
              target="_blank"
              rel="noreferrer"
              className="mt-1 block text-ink-2 hover:text-ink hover:underline"
            >
              <Mono>/api/v1/openapi.json</Mono>
            </a>
            <p className="mt-3 text-xs text-ink-3">
              面板用的也是这套 API。请求带上 Authorization: Bearer 密钥；密钥无效、已撤销或已过期时返回 401。
            </p>
          </Sheet>
        </div>
      </section>

      <NewKeyDialog open={creating} onOpenChange={setCreating} onCreated={reload} />
      <AlertDialog open={revoking !== null} onOpenChange={(v) => !v && setRevoking(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>撤销密钥「{revoking?.name}」？</AlertDialogTitle>
            <AlertDialogDescription>撤销立即生效，之后用这把密钥的请求都会返回 401。</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction
              className="bg-destructive text-white hover:bg-destructive/90"
              onClick={() => {
                if (revoking) void revoke(revoking);
              }}
            >
              撤销
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

/** 列表单元格：窄屏下改成卡片行时带上列名 */
function Cell({ label, children, className }: { label: string; children: React.ReactNode; className?: string }) {
  return (
    <span className={cn("text-ink-2 num", className)}>
      <span className="text-ink-3 md:hidden">{label} </span>
      <span>{children}</span>
    </span>
  );
}

function NewKeyDialog({
  open,
  onOpenChange,
  onCreated,
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  onCreated: () => void;
}) {
  const [name, setName] = React.useState("");
  const [expiry, setExpiry] = React.useState<APIKeyExpiry>("90d");
  const [nameError, setNameError] = React.useState<string | null>(null);
  const [error, setError] = React.useState<string | null>(null);
  const [busy, setBusy] = React.useState(false);
  // 完整密钥只在内存里保留到对话框关闭
  const [created, setCreated] = React.useState<CreatedAPIKey | null>(null);
  const [copied, copy] = useCopy();

  function close(v: boolean) {
    if (!v) {
      setName("");
      setExpiry("90d");
      setNameError(null);
      setError(null);
      setCreated(null);
    }
    onOpenChange(v);
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!name.trim()) {
      setNameError("填写名称，方便以后认出这把密钥");
      return;
    }
    setNameError(null);
    setError(null);
    setBusy(true);
    try {
      setCreated(await api.apikeys.create({ name: name.trim(), expires_in: expiry }));
      onCreated();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent
        className="sm:max-w-md"
        // 密钥显示期间不因误点遮罩或按 Esc 关闭，避免还没复制就丢失
        onInteractOutside={(e) => created && e.preventDefault()}
        onEscapeKeyDown={(e) => created && e.preventDefault()}
      >
        {created ? (
          <>
            <DialogHeader>
              <DialogTitle>密钥已创建</DialogTitle>
              <DialogDescription>只显示这一次，请现在复制。关闭后无法再查看。</DialogDescription>
            </DialogHeader>
            <div className="flex items-center gap-2 rounded-md bg-sunken px-3 py-2.5">
              <Mono className="flex-1 text-[13px] break-all select-all">{created.key}</Mono>
              <Button
                type="button"
                variant="ghost"
                size="icon-xs"
                aria-label="复制密钥"
                onClick={() => copy(created.key)}
              >
                {copied ? <Check /> : <Copy />}
              </Button>
            </div>
            <DialogFooter>
              <Button type="button" onClick={() => close(false)}>
                我已复制
              </Button>
            </DialogFooter>
          </>
        ) : (
          <form onSubmit={submit} className="grid gap-4" noValidate>
            <DialogHeader>
              <DialogTitle>新建 API 密钥</DialogTitle>
              <DialogDescription>
                这把密钥拥有与管理员相同的权限，能修改配置和下载快照明文；给每个 AI 或脚本单独一把，方便随时撤销。
              </DialogDescription>
            </DialogHeader>
            <Field label="名称" error={nameError}>
              <Input
                value={name}
                onChange={(e) => setName(e.target.value)}
                maxLength={64}
                placeholder="例如 claude-desktop"
                autoFocus
              />
            </Field>
            <Field label="过期" htmlFor="apikey-expiry">
              <Select value={expiry} onValueChange={(v) => setExpiry(v as APIKeyExpiry)}>
                <SelectTrigger id="apikey-expiry" className="w-40">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="30d">30 天</SelectItem>
                  <SelectItem value="90d">90 天</SelectItem>
                  <SelectItem value="never">永不</SelectItem>
                </SelectContent>
              </Select>
            </Field>
            <FormError>{error}</FormError>
            <DialogFooter>
              <Button type="button" variant="ghost" onClick={() => close(false)}>
                取消
              </Button>
              <Button type="submit" disabled={busy}>
                创建
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}
