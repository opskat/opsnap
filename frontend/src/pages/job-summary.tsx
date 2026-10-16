import * as React from "react";
import { Check, CircleDashed, Copy, Loader2, Lock, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { FormError, Mono, Sheet, useCopy } from "@/components/bits";
import type { JobCheck, JobTestResult } from "@/lib/api";
import { scheduleText, sourceSummary, type JobForm } from "@/lib/jobs";
import { retentionSentence } from "@/lib/retention";
import { cn } from "@/lib/utils";

/** 连接测试的状态；结果只对生成它的那份配置（sig）有效 */
export type TestState =
  | { status: "idle" }
  | { status: "running" }
  | { status: "error"; message: string }
  | { status: "done"; sig: string; result: JobTestResult };

function SummaryRow({ k, children }: { k: string; children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-[40px_1fr] gap-3 px-5 py-3">
      <dt className="text-ink-3">{k}</dt>
      <dd className="min-w-0">{children}</dd>
    </div>
  );
}

/** 右侧固定的摘要：来源 / 存到 / 加密 / 计划 / 保留，随表单实时更新；下面是连接测试和保存按钮 */
export function JobSummary({
  form,
  serverName,
  repoName,
  estimate,
  test,
  stale,
  busy,
  onTest,
  onSave,
  onCancel,
}: {
  form: JobForm;
  serverName: string;
  repoName: string;
  estimate: number | null;
  test: TestState;
  /** 测试之后配置又改过 */
  stale: boolean;
  busy: boolean;
  onTest: () => void;
  onSave: (run: boolean) => void;
  onCancel: () => void;
}) {
  const src = sourceSummary(form, serverName);
  return (
    <aside aria-label="摘要" className="xl:sticky xl:top-8 xl:self-start">
      <Sheet className="overflow-hidden">
        <dl className="divide-y divide-line text-[13px]">
          <SummaryRow k="来源">
            {src.main}
            <Mono className="block truncate text-ink-3">{src.detail}</Mono>
          </SummaryRow>
          <SummaryRow k="存到">{repoName || <span className="text-ink-3">未选择</span>}</SummaryRow>
          <SummaryRow k="加密">
            <span className="flex items-center gap-1.5">
              <Lock className="size-3.5 text-ok" aria-hidden />
              AES-256，传输前加密
            </span>
          </SummaryRow>
          <SummaryRow k="计划">{scheduleText(form.schedule)}</SummaryRow>
          <SummaryRow k="保留">{retentionSentence(form.retention, estimate)}</SummaryRow>
        </dl>

        <div className="border-t border-line p-5">
          <TestResults test={test} stale={stale} />
          <Button variant="outline" className="w-full" onClick={onTest} disabled={test.status === "running"}>
            {test.status === "running" ? "测试中…" : test.status === "idle" ? "测试连接" : "重新测试"}
          </Button>
        </div>

        <div className="flex gap-2 border-t border-line bg-sunken/50 p-4">
          <Button variant="ghost" onClick={onCancel}>
            取消
          </Button>
          <Button variant="outline" className="ml-auto" disabled={busy} onClick={() => onSave(false)}>
            保存
          </Button>
          <Button disabled={busy} onClick={() => onSave(true)}>
            保存并运行
          </Button>
        </div>
      </Sheet>
    </aside>
  );
}

function TestResults({ test, stale }: { test: TestState; stale: boolean }) {
  if (test.status === "idle") return null;
  if (test.status === "running") {
    return (
      <p className="mb-4 flex items-center gap-2 text-[13px] text-ink-2" aria-live="polite">
        <Loader2 className="size-4 animate-spin text-brand" aria-hidden />
        正在逐项检查…
      </p>
    );
  }
  if (test.status === "error") return <FormError className="mb-4">{test.message}</FormError>;

  const checks = test.result.checks ?? [];
  const bad = checks.filter((c) => !c.ok).length;
  return (
    <>
      <div className="mb-3 flex items-center justify-between">
        <span className="text-[13px] font-medium">连接测试</span>
        <span className={cn("text-xs font-medium", test.result.ok ? "text-ok" : "text-fail")}>
          {test.result.ok ? "全部通过" : `${bad} 项未通过`}
        </span>
      </div>
      <ul className="mb-4 flex flex-col gap-2 text-[13px]" aria-live="polite">
        {checks.map((c) => (
          <CheckRow key={c.key} c={c} />
        ))}
      </ul>
      {stale && <p className="mb-4 text-xs text-ink-3">配置改过之后还没有重新测试，这份结果可能已经过期。</p>}
    </>
  );
}

function CheckRow({ c }: { c: JobCheck }) {
  const [copied, copy] = useCopy();
  const state = c.ok ? "ok" : c.skipped ? "skipped" : "fail";
  return (
    <li className="flex gap-2">
      <span className="mt-0.5 shrink-0">
        {state === "ok" && <Check className="size-4 text-ok" aria-label="通过" />}
        {state === "fail" && <X className="size-4 text-fail" aria-label="未通过" />}
        {state === "skipped" && <CircleDashed className="size-4 text-ink-3" aria-label="已跳过" />}
      </span>
      <span className={cn("min-w-0", state === "skipped" && "text-ink-3")}>
        {c.label}
        {c.reason && <span className={cn("block", state === "fail" ? "text-fail" : "text-xs")}>{c.reason}</span>}
        {c.fix && (
          <span className="mt-1 flex items-start gap-1 rounded bg-sunken px-2 py-1">
            <code className="min-w-0 flex-1 font-mono text-[11.5px] break-all text-ink select-text">{c.fix}</code>
            <button
              type="button"
              aria-label="复制修复命令"
              title={copied ? "已复制" : "复制"}
              onClick={() => void copy(c.fix)}
              className="mt-0.5 shrink-0 rounded text-ink-3 hover:text-ink"
            >
              {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
            </button>
          </span>
        )}
      </span>
    </li>
  );
}
