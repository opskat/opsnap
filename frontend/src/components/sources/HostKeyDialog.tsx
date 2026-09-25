import { KeyRound, ShieldAlert, ShieldCheck, TriangleAlert } from "lucide-react";
import { useTranslation } from "react-i18next";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import type { HostKeyPrompt } from "@/lib/sources";

import { useRetained } from "./useRetained";

/** 「确认主机密钥」与「主机密钥已变化」共用的请求：resolve(true) 表示用户选择信任 */
export interface HostKeyRequest {
  prompt: HostKeyPrompt;
}

/**
 * 主机密钥弹窗：prompt.changed 为 false 时是首次连接的确认，为 true 时是密钥已变化的重新确认。
 * 提交中（busy）不能关闭；两种情形都不在这里发起网络请求，交给调用方处理确认与取消。
 */
export function HostKeyDialog({
  request,
  busy = false,
  error,
  chain,
  onCancel,
  onTrust,
}: {
  request?: HostKeyRequest;
  /** 经由的完整链路（如“OpsNap → office-socks (SOCKS5) → bastion.corp:22”），首次连接时显示 */
  chain?: string;
  busy?: boolean;
  error?: string;
  onCancel: () => void;
  onTrust: () => void;
}) {
  const { t } = useTranslation();
  const shown = useRetained(request);
  const prompt = shown?.prompt;
  const changed = prompt?.changed ?? false;

  return (
    <Dialog open={request !== undefined} onOpenChange={(next) => !next && !busy && onCancel()}>
      <DialogContent className="gap-0 bg-card p-0 sm:max-w-lg">
        <DialogHeader className="gap-1.5 border-b px-5 py-4 text-left">
          <DialogTitle className="flex items-center gap-2">
            {changed ? (
              <ShieldAlert className="size-5 text-warning" />
            ) : (
              <KeyRound className="size-5 text-muted-foreground" />
            )}
            {changed ? t("sources.hostKey.changed.title") : t("sources.hostKey.confirm.title")}
          </DialogTitle>
          <DialogDescription>
            {changed
              ? t("sources.hostKey.changed.hint", { name: prompt?.name ?? "", address: prompt?.address ?? "" })
              : t("sources.hostKey.confirm.hint", { address: prompt?.address ?? "" })}
          </DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-3 p-5">
          {changed ? (
            <>
              <FingerprintBox label={t("sources.hostKey.changed.saved")} value={prompt?.saved ?? ""} />
              <FingerprintBox label={t("sources.hostKey.changed.presented")} value={prompt?.fingerprint ?? ""} />
              <p className="flex items-start gap-2 rounded-md bg-warning-soft px-3 py-2.5 text-sm text-warning">
                <TriangleAlert className="mt-0.5 size-4 shrink-0" />
                {t("sources.hostKey.changed.warning")}
              </p>
            </>
          ) : (
            <>
              {prompt?.key_type && (
                <FingerprintBox label={t("sources.hostKey.confirm.keyType")} value={prompt.key_type} />
              )}
              {chain && <FingerprintBox label={t("sources.hostKey.confirm.chain")} value={chain} />}
              <FingerprintBox label={t("sources.hostKey.confirm.fingerprint")} value={prompt?.fingerprint ?? ""} />
              <p className="text-xs text-faint-foreground">{t("sources.hostKey.confirm.verifyHint")}</p>
            </>
          )}
          {error && (
            <p role="alert" className="rounded-md bg-destructive-soft px-3 py-2.5 text-sm text-destructive">
              {error}
            </p>
          )}
        </div>
        <DialogFooter className="border-t bg-sidebar px-5 py-3.5">
          <Button variant="outline" disabled={busy} onClick={onCancel}>
            {t("common.cancel")}
          </Button>
          <Button disabled={busy} onClick={onTrust}>
            <ShieldCheck />
            {busy
              ? t("common.submitting")
              : changed
                ? t("sources.hostKey.changed.trust")
                : t("sources.hostKey.confirm.trust")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function FingerprintBox({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex flex-col gap-1 rounded-md border px-3 py-2.5">
      <span className="text-xs text-muted-foreground">{label}</span>
      <span className="font-mono text-sm break-all">{value}</span>
    </div>
  );
}
