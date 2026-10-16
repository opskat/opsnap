import * as React from "react";
import { AlertTriangle } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Field, FormError, Mono } from "@/components/bits";
import { api, errorMessage } from "@/lib/api";

/** 轮换主密钥：用新主密钥重新加密所有保存的凭据；成功后提示把环境变量改成新主密钥 */
export function RotateMasterKeyDialog({
  open,
  onOpenChange,
  onRotated,
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  onRotated: () => void;
}) {
  const [key, setKey] = React.useState("");
  const [confirm, setConfirm] = React.useState("");
  const [error, setError] = React.useState<string | null>(null);
  const [saving, setSaving] = React.useState(false);
  const [done, setDone] = React.useState(false);

  // 关闭时清空：在渲染期间对比上一次的 open，避免在 effect 里同步 setState
  const [prevOpen, setPrevOpen] = React.useState(open);
  if (open !== prevOpen) {
    setPrevOpen(open);
    if (!open) {
      setKey("");
      setConfirm("");
      setError(null);
      setDone(false);
    }
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!key.trim()) {
      setError("填写新的主密钥");
      return;
    }
    if (key !== confirm) {
      setError("两次输入不一致");
      return;
    }
    setSaving(true);
    setError(null);
    try {
      await api.settings.rotateMasterKey(key);
      setDone(true);
      onRotated();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setSaving(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        {done ? (
          <div className="grid gap-4">
            <DialogHeader>
              <DialogTitle>主密钥已轮换</DialogTitle>
              <DialogDescription>所有凭据已用新主密钥重新加密，opsnap 已改用新主密钥。</DialogDescription>
            </DialogHeader>
            <p className="flex gap-2 rounded-lg bg-warn-wash px-3 py-2.5 text-[13px]" role="alert">
              <AlertTriangle className="mt-0.5 size-4 shrink-0 text-warn" aria-hidden />
              <span>
                现在就把 <Mono>OPSNAP_SECRET_KEY</Mono> 改成新主密钥。仍用旧主密钥启动时，opsnap 会拒绝启动。
              </span>
            </p>
            <DialogFooter>
              <Button type="button" onClick={() => onOpenChange(false)}>
                知道了
              </Button>
            </DialogFooter>
          </div>
        ) : (
          <form onSubmit={submit} className="grid gap-4" noValidate>
            <DialogHeader>
              <DialogTitle>轮换主密钥</DialogTitle>
              <DialogDescription>
                用新主密钥重新加密所有保存的密码、私钥和 opsnap 密钥；任何一项失败都保持原状。
              </DialogDescription>
            </DialogHeader>
            <Field label="新主密钥">
              <Input type="password" autoComplete="new-password" value={key} onChange={(e) => setKey(e.target.value)} />
            </Field>
            <Field label="再输入一次">
              <Input
                type="password"
                autoComplete="new-password"
                value={confirm}
                onChange={(e) => setConfirm(e.target.value)}
              />
            </Field>
            <p className="text-xs text-ink-3">
              轮换后要把 <Mono className="text-[11.5px]">OPSNAP_SECRET_KEY</Mono> 改成新主密钥，否则下次启动会被拒绝。
            </p>
            <FormError>{error}</FormError>
            <DialogFooter>
              <Button type="button" variant="ghost" onClick={() => onOpenChange(false)}>
                取消
              </Button>
              <Button type="submit" disabled={saving}>
                轮换
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}
