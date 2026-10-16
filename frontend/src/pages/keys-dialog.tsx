import * as React from "react";
import { AlertTriangle, Download, KeyRound, Loader2, ShieldCheck } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { FormError, Skeleton } from "@/components/bits";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { RecoveryKeyBox } from "@/components/recovery-key-box";
import { api, errorMessage, type KeyStatus, type RepoItem, type RepoKeys } from "@/lib/api";
import { downloadText } from "@/lib/download";
import { fmtDate } from "@/lib/format";
import { useLoad } from "@/lib/use-load";
import { useRecoveryKey } from "@/lib/use-recovery-key";

/** 下载恢复包：仓库地址、恢复步骤和 restic 命令，不含任何密钥 */
export async function downloadRecoveryBundle(repoId: number) {
  const b = await api.repos.recoveryBundle(repoId);
  downloadText(b.filename, b.content);
}

/** 一个存储位置的两把密钥：查看、更换 opsnap 密钥、生成或移除恢复密钥、下载恢复包 */
export function KeysDialog({
  repo,
  onClose,
  onChanged,
}: {
  repo: RepoItem;
  onClose: () => void;
  onChanged: () => void;
}) {
  const load = React.useCallback(() => api.repos.keys(repo.id), [repo.id]);
  const { data: keys, error, setData } = useLoad<RepoKeys>(load);
  const [adding, setAdding] = React.useState(false);
  const [busy, setBusy] = React.useState<"" | "rotate" | "add" | "bundle">("");
  const [actionError, setActionError] = React.useState<string | null>(null);
  const [removing, setRemoving] = React.useState(false);
  const rk = useRecoveryKey(adding);

  async function act(kind: "rotate" | "add" | "bundle", fn: () => Promise<void>) {
    setBusy(kind);
    setActionError(null);
    try {
      await fn();
    } catch (err) {
      setActionError(errorMessage(err));
    } finally {
      setBusy("");
    }
  }

  function applyKeys(next: RepoKeys) {
    setData(next);
    onChanged();
  }

  return (
    <Dialog open onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="max-h-[92svh] overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{repo.name} 的密钥</DialogTitle>
          <DialogDescription>备份以 AES-256 加密，任意一把密钥都能解开。</DialogDescription>
        </DialogHeader>
        {error && !keys ? (
          <FormError>读取密钥失败：{error}</FormError>
        ) : !keys ? (
          <Skeleton className="h-28" />
        ) : (
          <ul className="divide-y divide-line rounded-lg border border-line-strong">
            <li className="flex items-center gap-3 px-4 py-3">
              <ShieldCheck className="size-4 shrink-0 text-ok" aria-hidden />
              <div className="min-w-0 flex-1">
                <div className="text-[13.5px] font-medium">opsnap 密钥</div>
                <div className="text-xs text-ink-3">opsnap 加密保管，日常备份用{created(keys.opsnap_key)}</div>
              </div>
              <Button
                size="sm"
                variant="ghost"
                disabled={busy !== ""}
                onClick={() =>
                  void act("rotate", async () => {
                    applyKeys(await api.repos.rotateKey(repo.id));
                    toast.success("opsnap 密钥已更换");
                  })
                }
              >
                {busy === "rotate" && <Loader2 className="animate-spin" />}
                更换
              </Button>
            </li>
            {keys.recovery_key.present ? (
              <li className="flex items-center gap-3 px-4 py-3">
                <KeyRound className="size-4 shrink-0 text-ink-2" aria-hidden />
                <div className="min-w-0 flex-1">
                  <div className="text-[13.5px] font-medium">恢复密钥</div>
                  <div className="text-xs text-ink-3">由你保管，opsnap 不保存{created(keys.recovery_key)}</div>
                </div>
                <Button
                  size="sm"
                  variant="ghost"
                  className="text-fail hover:text-fail"
                  disabled={busy !== ""}
                  onClick={() => setRemoving(true)}
                >
                  移除
                </Button>
              </li>
            ) : !adding ? (
              <li className="flex items-center gap-3 px-4 py-3">
                <AlertTriangle className="size-4 shrink-0 text-warn" aria-hidden />
                <div className="min-w-0 flex-1">
                  <div className="text-[13.5px] font-medium text-warn">没有恢复密钥</div>
                  <div className="text-xs text-ink-3">opsnap 所在机器坏了将无法恢复</div>
                </div>
                <Button size="sm" onClick={() => setAdding(true)}>
                  生成恢复密钥
                </Button>
              </li>
            ) : (
              <li className="px-4 py-3">
                <div className="text-[13.5px] font-medium">新的恢复密钥</div>
                <RecoveryKeyBox className="mt-2" rk={rk} note="只显示这一次。关闭后 opsnap 无法再找回。" />
              </li>
            )}
          </ul>
        )}
        <FormError>{actionError}</FormError>
        <DialogFooter className="sm:justify-between">
          <Button
            variant="ghost"
            disabled={busy !== ""}
            onClick={() => void act("bundle", () => downloadRecoveryBundle(repo.id))}
          >
            <Download />
            下载恢复包
          </Button>
          {adding ? (
            <div className="flex gap-2">
              <Button variant="ghost" disabled={busy !== ""} onClick={() => setAdding(false)}>
                取消
              </Button>
              <Button
                disabled={!rk.ready || busy !== ""}
                onClick={() =>
                  void act("add", async () => {
                    applyKeys(await api.repos.addRecoveryKey(repo.id, rk.value ?? ""));
                    setAdding(false);
                    toast.success("恢复密钥已添加");
                  })
                }
              >
                {busy === "add" && <Loader2 className="animate-spin" />}
                添加密钥
              </Button>
            </div>
          ) : (
            <Button variant="outline" onClick={onClose}>
              完成
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
      <ConfirmDialog
        open={removing}
        title="移除恢复密钥？"
        description="移除后，opsnap 所在机器损坏时将无法用恢复密钥恢复这个存储位置。之后可以重新生成一把。"
        confirmLabel="确认移除"
        onClose={() => setRemoving(false)}
        onConfirm={async () => {
          applyKeys(await api.repos.removeRecoveryKey(repo.id));
          setRemoving(false);
          toast.success("恢复密钥已移除");
        }}
      />
    </Dialog>
  );
}

function created(k: KeyStatus) {
  return k.created_at ? `，${fmtDate(k.created_at)}创建` : "";
}
