import * as React from "react";
import { Check, Plus, Send, Trash2, X } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Field, FormError, Mono, PageTitle, Sheet, Skeleton } from "@/components/bits";
import { SECRET_MASKED, api, errorMessage, type NotifyConfig } from "@/lib/api";
import { useLoad } from "@/lib/use-load";
import { cn } from "@/lib/utils";
import { LoadError } from "./settings";

/** 与后端 notify_entity 的上限一致 */
const MAX_STALE_HOURS = 8760;

type EventKey = "on_failed" | "on_recovered" | "on_stale" | "on_success";

const EVENTS: { key: EventKey; title: string; body: string }[] = [
  { key: "on_failed", title: "备份失败", body: "任意一次运行失败或超时。" },
  { key: "on_recovered", title: "恢复正常", body: "失败后的第一次成功。" },
  { key: "on_stale", title: "太久没有成功", body: "能发现小主机宕机、计划卡住这类静默故障。" },
  { key: "on_success", title: "每次成功", body: "消息较多。" },
];

interface HeaderRow {
  id: number;
  name: string;
  value: string;
  /** 已保存过的请求头的原名；值留空且名字没改时回传占位，保留原值 */
  savedName?: string;
}

interface NotifyForm {
  url: string;
  headers: HeaderRow[];
  events: Record<EventKey, boolean>;
  staleHours: string;
}

interface FormErrors {
  staleHours?: string;
  headers?: Record<number, string>;
}

let rowSeq = 0;

function toForm(c: NotifyConfig): NotifyForm {
  return {
    url: c.url,
    headers: Object.keys(c.headers ?? {})
      .sort()
      .map((name) => ({ id: ++rowSeq, name, value: "", savedName: name })),
    events: { on_failed: c.on_failed, on_recovered: c.on_recovered, on_stale: c.on_stale, on_success: c.on_success },
    staleHours: String(c.stale_hours),
  };
}

/** 校验并生成提交体；未改动的已保存请求头回传「已设置」 */
export function buildNotifyBody(f: NotifyForm): { body?: NotifyConfig; errors: FormErrors } {
  const errors: FormErrors = {};
  const headerErrors: Record<number, string> = {};
  const headers: Record<string, string> = {};
  for (const h of f.headers) {
    const name = h.name.trim();
    if (!name && !h.value) continue; // 空行忽略
    if (!name || /[\s:]/.test(name)) {
      headerErrors[h.id] = "请求头名不能为空，也不能含空格或冒号";
    } else if (name in headers) {
      headerErrors[h.id] = `请求头 ${name} 重复`;
    } else if (h.value) {
      headers[name] = h.value;
    } else if (h.savedName === name) {
      headers[name] = SECRET_MASKED;
    } else {
      headerErrors[h.id] = `填写 ${name} 的值`;
    }
  }
  if (Object.keys(headerErrors).length) errors.headers = headerErrors;

  const hours = /^\d+$/.test(f.staleHours.trim()) ? Number(f.staleHours.trim()) : NaN;
  if (!(hours >= 1 && hours <= MAX_STALE_HOURS)) errors.staleHours = `阈值须在 1 到 ${MAX_STALE_HOURS} 小时之间`;

  if (errors.headers || errors.staleHours) return { errors };
  return {
    errors,
    // 面板地址取浏览器里的地址，消息里的链接据此拼成能直接打开的完整地址
    body: { url: f.url.trim(), headers, ...f.events, stale_hours: hours, panel_url: window.location.origin },
  };
}

/** 比较两份表单是否等价（用于判断有没有未保存的修改） */
function sameForm(a: NotifyForm, b: NotifyForm): boolean {
  const norm = (f: NotifyForm) =>
    JSON.stringify({
      url: f.url.trim(),
      headers: f.headers
        .filter((h) => h.name.trim() || h.value)
        .map((h) => [h.name.trim(), h.value, h.savedName ?? null]),
      events: f.events,
      staleHours: f.staleHours.trim(),
    });
  return norm(a) === norm(b);
}

type TestResult = { tone: "ok" | "fail"; text: string };

export function NotifyPage() {
  const { data, error, loading, reload, setData } = useLoad(api.notify.get);
  const [saved, setSaved] = React.useState<NotifyForm | null>(null);
  const [form, setForm] = React.useState<NotifyForm | null>(null);
  const [errors, setErrors] = React.useState<FormErrors>({});
  const [submitError, setSubmitError] = React.useState<string | null>(null);
  const [saving, setSaving] = React.useState(false);
  const [testing, setTesting] = React.useState(false);
  const [result, setResult] = React.useState<TestResult | null>(null);

  // 读到（或重新读到）数据时重置表单：在渲染期间对比上一次的 data
  const [prevData, setPrevData] = React.useState(data);
  if (data !== prevData) {
    setPrevData(data);
    if (data) {
      const f = toForm(data);
      setSaved(f);
      setForm(f);
    }
  }

  const content = (() => {
    if (error) return <LoadError what="通知设置" message={error} onRetry={reload} />;
    if (loading || !form || !saved || !data) {
      return (
        <div className="grid gap-4" aria-busy="true" aria-label="正在读取通知设置">
          <Skeleton className="h-32" />
          <Skeleton className="h-56" />
        </div>
      );
    }
    const dirty = !sameForm(form, saved);
    const set = (patch: Partial<NotifyForm>) => setForm((f) => (f ? { ...f, ...patch } : f));
    const setRow = (id: number, patch: Partial<HeaderRow>) =>
      setForm((f) => (f ? { ...f, headers: f.headers.map((h) => (h.id === id ? { ...h, ...patch } : h)) } : f));

    async function save(e: React.FormEvent) {
      e.preventDefault();
      if (!form) return;
      const { body, errors } = buildNotifyBody(form);
      setErrors(errors);
      setSubmitError(null);
      if (!body) return;
      setSaving(true);
      try {
        setData(await api.notify.update(body));
        setResult(null);
        toast.success("通知设置已保存");
      } catch (err) {
        setSubmitError(errorMessage(err));
      } finally {
        setSaving(false);
      }
    }

    async function test() {
      setTesting(true);
      setResult(null);
      try {
        const { status_code: code } = await api.notify.test();
        setResult(
          code >= 200 && code < 300
            ? { tone: "ok", text: `已送达，对方返回 ${code}` }
            : { tone: "fail", text: `对方返回 ${code}，请检查 Webhook 地址和请求头` }
        );
      } catch (err) {
        setResult({ tone: "fail", text: errorMessage(err) });
      } finally {
        setTesting(false);
      }
    }

    const testBlocked = dirty ? "有未保存的修改，保存后再测试" : !data.url ? "先填写并保存 Webhook 地址" : null;

    return (
      <form className="flex flex-col gap-10" onSubmit={save} noValidate aria-label="通知设置">
        <section>
          <h2 className="mb-3 text-[15px] font-semibold">发到哪里</h2>
          <Sheet className="grid gap-5 p-5">
            <Field label="Webhook 地址" hint="以 JSON POST 发送；留空表示不发通知">
              <Input
                value={form.url}
                onChange={(e) => set({ url: e.target.value })}
                placeholder="https://hooks.example.com/…"
                className="font-mono"
                autoComplete="off"
                spellCheck={false}
              />
            </Field>
            <div className="grid gap-2">
              <div className="text-[13px] font-medium">请求头（可选）</div>
              {form.headers.map((h, i) => {
                const rowError = errors.headers?.[h.id];
                return (
                  <div key={h.id} className="grid gap-1">
                    <div className="flex items-center gap-2">
                      <Input
                        aria-label={`请求头 ${i + 1} 名称`}
                        value={h.name}
                        onChange={(e) => setRow(h.id, { name: e.target.value })}
                        placeholder="Authorization"
                        className="w-2/5 font-mono"
                        aria-invalid={rowError ? true : undefined}
                        autoComplete="off"
                        spellCheck={false}
                      />
                      <Input
                        aria-label={`请求头 ${i + 1} 值`}
                        value={h.value}
                        onChange={(e) => setRow(h.id, { value: e.target.value })}
                        placeholder={h.savedName ? `${SECRET_MASKED}，留空保持不变` : "值"}
                        className="flex-1 font-mono"
                        aria-invalid={rowError ? true : undefined}
                        autoComplete="off"
                        spellCheck={false}
                      />
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon-sm"
                        aria-label={`删除请求头 ${h.name.trim() || i + 1}`}
                        onClick={() => set({ headers: form.headers.filter((x) => x.id !== h.id) })}
                      >
                        <Trash2 />
                      </Button>
                    </div>
                    {rowError && <span className="text-xs text-fail">{rowError}</span>}
                  </div>
                );
              })}
              <div>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  className="-ml-2 text-ink-2"
                  onClick={() => set({ headers: [...form.headers, { id: ++rowSeq, name: "", value: "" }] })}
                >
                  <Plus />
                  添加请求头
                </Button>
              </div>
            </div>
            <div className="flex flex-wrap items-center gap-3 border-t border-line pt-4">
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={test}
                disabled={testing || testBlocked !== null}
              >
                <Send />
                发一条测试消息
              </Button>
              <span role="status" aria-live="polite" className="text-[13px]">
                {testBlocked ? (
                  <span className="text-ink-3">{testBlocked}</span>
                ) : testing ? (
                  <span className="text-ink-3">正在发送…</span>
                ) : result ? (
                  <span className={cn("flex items-center gap-1", result.tone === "ok" ? "text-ok" : "text-fail")}>
                    {result.tone === "ok" ? (
                      <Check className="size-4" aria-hidden />
                    ) : (
                      <X className="size-4" aria-hidden />
                    )}
                    {result.text}
                  </span>
                ) : null}
              </span>
            </div>
          </Sheet>
        </section>

        <section>
          <h2 className="mb-3 text-[15px] font-semibold">什么时候发</h2>
          <Sheet className="divide-y divide-line">
            {EVENTS.map((e) => (
              <div key={e.key} className="flex items-start gap-6 px-5 py-4">
                <div className="flex-1">
                  <div className="font-medium" id={`evt-${e.key}`}>
                    {e.title}
                  </div>
                  <p className="mt-0.5 max-w-[60ch] text-[13px] text-ink-2" id={`evt-${e.key}-desc`}>
                    {e.body}
                  </p>
                  {e.key === "on_stale" && (
                    <div className="mt-3 grid gap-1">
                      <div className="flex items-center gap-2 text-[13px]">
                        超过
                        <Input
                          aria-label="太久没有成功的阈值（小时）"
                          inputMode="numeric"
                          value={form.staleHours}
                          onChange={(ev) => set({ staleHours: ev.target.value })}
                          aria-invalid={errors.staleHours ? true : undefined}
                          className="h-8 w-20 text-center num"
                        />
                        小时
                      </div>
                      {errors.staleHours && <span className="text-xs text-fail">{errors.staleHours}</span>}
                    </div>
                  )}
                </div>
                <Switch
                  checked={form.events[e.key]}
                  onCheckedChange={(v) => set({ events: { ...form.events, [e.key]: v } })}
                  aria-label={e.title}
                  aria-describedby={`evt-${e.key}-desc`}
                />
              </div>
            ))}
          </Sheet>
        </section>

        <div className="grid gap-3">
          <FormError>{submitError}</FormError>
          <div className="flex items-center gap-3">
            <Button type="submit" disabled={saving}>
              保存
            </Button>
            {dirty && <span className="text-[13px] text-ink-3">有未保存的修改</span>}
          </div>
        </div>
      </form>
    );
  })();

  return (
    <>
      <PageTitle title="通知" lead="以 JSON 推送到你的 Webhook。" />
      <div className="grid gap-10 xl:grid-cols-[1fr_400px]">
        <div>{content}</div>
        <section>
          <h2 className="mb-3 text-[15px] font-semibold">消息长这样</h2>
          <pre className="overflow-x-auto rounded-xl bg-log-bg p-5 font-mono text-[12px] leading-[1.8] text-log-ink select-text">
            {SAMPLE}
          </pre>
          <p className="mt-3 text-xs text-ink-3">
            <Mono className="text-[11.5px]">type</Mono> 取值 failed、recovered、stale、success；测试消息为
            test，其余字段为空。时间为 UTC。
          </p>
        </section>
      </div>
    </>
  );
}

/** 消息示例，字段与后端 notify_svc 的 payload 一致 */
const SAMPLE = `{
  "type": "failed",
  "job_id": 6,
  "job_name": "埋点日志",
  "run_id": 1285,
  "started_at": "2026-10-14T18:00:00Z",
  "duration_seconds": 30,
  "error_summary": "mongodump 退出码 1：连接超时",
  "last_success_at": "2026-10-11T18:00:41Z",
  "link": "http://opsnap.lan:8080/runs/1285"
}`;
