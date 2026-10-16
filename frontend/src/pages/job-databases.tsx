import * as React from "react";
import { RefreshCw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Switch } from "@/components/ui/switch";
import { Mono } from "@/components/bits";
import { api, errorMessage, type JobDatabasesResult } from "@/lib/api";
import { fmtBytes } from "@/lib/format";
import { buildInput, connReady, connSignature, type JobForm } from "@/lib/jobs";
import { cn } from "@/lib/utils";

interface LoadState {
  /** 这份结果对应的连接信息；连接信息一变就作废 */
  sig: string;
  status: "idle" | "loading" | "done";
  result: JobDatabasesResult | null;
  error: string | null;
}

/**
 * 库的勾选清单。连接信息填全后稍等片刻自动读取库列表和大小，
 * 系统库显示但不可选；读取失败时在原地写明原因并提供重试。
 */
export function DatabasePicker({
  form,
  jobId,
  onChange,
  error,
}: {
  form: JobForm;
  /** 编辑中的任务 id，新建为 0 */
  jobId: number;
  onChange: (patch: Partial<Pick<JobForm, "databases" | "all_databases">>) => void;
  error: string | null;
}) {
  const sig = connSignature(form);
  const ready = connReady(form);
  const [state, setState] = React.useState<LoadState>({ sig, status: "idle", result: null, error: null });
  if (state.sig !== sig) setState({ sig, status: "idle", result: null, error: null });

  const load = React.useEffectEvent(async (forSig: string) => {
    const patch = (p: Partial<LoadState>) => setState((s) => (s.sig === forSig ? { ...s, ...p } : s));
    patch({ status: "loading", error: null });
    try {
      const result = await api.jobs.databases(jobId, buildInput(form).input);
      patch({ status: "done", result });
    } catch (err) {
      patch({ status: "done", error: errorMessage(err) });
    }
  });

  React.useEffect(() => {
    if (!ready || state.status !== "idle") return;
    const timer = setTimeout(() => void load(sig), 350);
    return () => clearTimeout(timer);
  }, [ready, sig, state.status]);

  const labelId = React.useId();
  const msgId = `${labelId}-msg`;
  const list = state.result?.ok ? (state.result.list ?? []) : [];
  const failure = state.error ?? (state.result && !state.result.ok ? failureText(state.result) : null);
  const picked = form.databases.length;

  const toggle = (name: string, on: boolean) =>
    onChange({ databases: on ? [...form.databases, name] : form.databases.filter((d) => d !== name) });

  return (
    <div role="group" aria-labelledby={labelId} aria-describedby={error ? msgId : undefined} className="mt-6">
      <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
        <span className="text-[13px] font-medium">
          <span id={labelId}>库</span>
          <span className="ml-2 font-normal text-ink-3">已选 {form.all_databases ? "全部" : picked}</span>
        </span>
        <div className="flex items-center gap-2">
          <label className="flex items-center gap-2 text-[13px] text-ink-2">
            <Switch checked={form.all_databases} onCheckedChange={(v) => onChange({ all_databases: v })} />
            全部，含以后新建
          </label>
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            aria-label="重新读取库列表"
            title="重新读取"
            disabled={!ready || state.status === "loading"}
            onClick={() => void load(sig)}
          >
            <RefreshCw className={cn(state.status === "loading" && "animate-spin")} />
          </Button>
        </div>
      </div>

      {failure ? (
        <div
          role="alert"
          className="flex flex-wrap items-center gap-3 rounded-lg border border-fail/30 bg-fail-wash px-3 py-2.5"
        >
          <p className="min-w-0 flex-1 text-[13px] text-fail">{failure}</p>
          <Button type="button" size="sm" variant="outline" onClick={() => void load(sig)}>
            重试
          </Button>
        </div>
      ) : state.status === "loading" ? (
        <p
          className="rounded-lg border border-line-strong bg-surface px-3 py-3 text-[13px] text-ink-3"
          aria-live="polite"
        >
          正在读取库列表…
        </p>
      ) : !state.result ? (
        <p className="rounded-lg border border-dashed border-line-strong px-3 py-3 text-[13px] text-ink-3">
          填好连接信息后列出库
        </p>
      ) : (
        <ul
          className={cn(
            "divide-y divide-line rounded-lg border border-line-strong bg-surface",
            form.all_databases && "opacity-50"
          )}
        >
          {list.map((d) => (
            <li key={d.name}>
              <label className={cn("flex h-10 items-center gap-3 px-3", d.selectable && "cursor-pointer")}>
                <Checkbox
                  aria-label={d.name}
                  checked={form.all_databases ? d.selectable : d.selectable && form.databases.includes(d.name)}
                  disabled={form.all_databases || !d.selectable}
                  onCheckedChange={(v) => toggle(d.name, v === true)}
                />
                <Mono className={cn("flex-1 text-[13px]", !d.selectable && "text-ink-3")}>{d.name}</Mono>
                <span className="text-xs text-ink-3 num">
                  {d.system ? "系统库" : d.size_bytes >= 0 ? fmtBytes(d.size_bytes) : "大小未知"}
                </span>
              </label>
            </li>
          ))}
          {list.length === 0 && <li className="px-3 py-3 text-[13px] text-ink-3">没有可备份的库</li>}
        </ul>
      )}
      {error && (
        <span id={msgId} className="mt-1.5 block text-xs text-fail">
          {error}
        </span>
      )}
    </div>
  );
}

function failureText(r: JobDatabasesResult): string {
  const label = r.reason_label || "读取库列表失败";
  return r.message ? `${label}：${r.message}` : label;
}
