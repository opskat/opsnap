import { Check, CircleAlert, CircleCheck, Copy, TriangleAlert } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";

import { Button } from "@/components/ui/button";
import { pickProbeText, type ProbeItem, type ProbeText } from "@/lib/sources";
import { cn } from "@/lib/utils";

const tierStyle: Record<string, string> = {
  ok: "text-success",
  warn: "text-warning",
  fail: "text-destructive",
};

/** 探测项结论的图标：通过、提醒、不可用 */
export function TierIcon({ tier }: { tier: ProbeItem["tier"] }) {
  const cls = cn("size-4 shrink-0", tierStyle[tier]);
  if (tier === "fail") return <CircleAlert className={cls} />;
  if (tier === "warn") return <TriangleAlert className={cls} />;
  return <CircleCheck className={cls} />;
}

/** 探测项的修复方法，可复制；没有对应语言的文本时不显示。数据源详情页与新建任务向导共用 */
export function FixBlock({ fix }: { fix: ProbeText }) {
  const { t, i18n } = useTranslation();
  const [copied, setCopied] = useState(false);
  const text = pickProbeText(fix, i18n.language);
  if (!text) return null;
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    } catch {
      // 剪贴板不可用时静默失败
    }
  };
  return (
    <div className="flex items-center justify-between gap-3 rounded-md bg-warning-soft px-3 py-2 text-xs text-warning">
      <code className="break-all whitespace-pre-wrap">{text}</code>
      <Button type="button" variant="outline" size="xs" className="shrink-0" onClick={() => void copy()}>
        {copied ? <Check /> : <Copy />}
        {copied ? t("common.copied") : t("common.copy")}
      </Button>
    </div>
  );
}
