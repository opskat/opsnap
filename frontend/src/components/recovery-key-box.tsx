import { Check, Copy, Download, Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { FormError, Mono, useCopy } from "@/components/bits";
import { downloadText } from "@/lib/download";
import type { useRecoveryKey } from "@/lib/use-recovery-key";

/** 展示一把恢复密钥（只此一次），提供复制和下载，并要求确认「已存进密码管理器」 */
export function RecoveryKeyBox({
  rk,
  note,
  className,
}: {
  rk: ReturnType<typeof useRecoveryKey>;
  note: string;
  className?: string;
}) {
  const [copied, copy] = useCopy();
  if (rk.error) {
    return (
      <div className={className}>
        <FormError>{rk.error}</FormError>
        <Button size="sm" variant="outline" className="mt-2" onClick={rk.retry}>
          重新生成
        </Button>
      </div>
    );
  }
  if (rk.value === null) {
    return (
      <div className={className} aria-busy="true">
        <span className="flex items-center gap-2 text-[13px] text-ink-3">
          <Loader2 className="size-4 animate-spin" aria-hidden />
          正在生成恢复密钥
        </span>
      </div>
    );
  }
  const value = rk.value;
  return (
    <div className={className}>
      <div className="flex items-center gap-2 rounded-md bg-sunken px-3 py-2">
        <Mono className="flex-1 text-[13px] break-all select-text">{value}</Mono>
        <Button variant="ghost" size="icon-xs" aria-label="复制恢复密钥" onClick={() => void copy(value)}>
          {copied ? <Check /> : <Copy />}
        </Button>
        <Button
          variant="ghost"
          size="icon-xs"
          aria-label="下载恢复密钥"
          onClick={() => downloadText("opsnap-recovery-key.txt", `${value}\n`)}
        >
          <Download />
        </Button>
      </div>
      <p className="mt-2 text-xs text-ink-2">{note}</p>
      <label className="mt-3 flex items-center gap-2 text-[13px] font-medium">
        <Checkbox checked={rk.acked} onCheckedChange={(v) => rk.setAcked(v === true)} />
        已存进密码管理器
      </label>
    </div>
  );
}
