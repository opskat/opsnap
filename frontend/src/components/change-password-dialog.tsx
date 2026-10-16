import * as React from "react";
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
import { Input } from "@/components/ui/input";
import { Field, FormError } from "@/components/bits";
import { api, errorMessage } from "@/lib/api";

/** 与后端 auth_svc 的密码规则一致 */
export const MIN_PASSWORD_LEN = 8;
export const MAX_PASSWORD_BYTES = 72;

/** 校验新密码，返回错误说明或 null */
export function checkNewPassword(pw: string, confirm: string, label = "新密码"): string | null {
  if ([...pw].length < MIN_PASSWORD_LEN) return `${label}至少 ${MIN_PASSWORD_LEN} 位`;
  if (new TextEncoder().encode(pw).length > MAX_PASSWORD_BYTES) return `${label}不能超过 ${MAX_PASSWORD_BYTES} 字节`;
  if (pw !== confirm) return `两次输入的${label}不一致`;
  return null;
}

/** 修改管理员密码；成功后其他会话失效，当前会话保留 */
export function ChangePasswordDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (v: boolean) => void }) {
  const [oldPw, setOldPw] = React.useState("");
  const [newPw, setNewPw] = React.useState("");
  const [confirm, setConfirm] = React.useState("");
  const [error, setError] = React.useState<string | null>(null);
  const [saving, setSaving] = React.useState(false);

  // 关闭时清空表单：在渲染期间对比上一次的 open，避免在 effect 里同步 setState
  const [prevOpen, setPrevOpen] = React.useState(open);
  if (open !== prevOpen) {
    setPrevOpen(open);
    if (!open) {
      setOldPw("");
      setNewPw("");
      setConfirm("");
      setError(null);
    }
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!oldPw) {
      setError("填写原密码");
      return;
    }
    const invalid = checkNewPassword(newPw, confirm);
    if (invalid) {
      setError(invalid);
      return;
    }
    setSaving(true);
    setError(null);
    try {
      await api.auth.changePassword({ old_password: oldPw, new_password: newPw });
      toast.success("密码已修改，其他设备上的登录已退出");
      onOpenChange(false);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setSaving(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-sm">
        <form onSubmit={submit} className="grid gap-4" noValidate>
          <DialogHeader>
            <DialogTitle>修改密码</DialogTitle>
            <DialogDescription>修改后，其他设备上的登录会退出。</DialogDescription>
          </DialogHeader>
          <Field label="原密码">
            <Input
              type="password"
              autoComplete="current-password"
              value={oldPw}
              onChange={(e) => setOldPw(e.target.value)}
            />
          </Field>
          <Field label="新密码" hint={`至少 ${MIN_PASSWORD_LEN} 位`}>
            <Input
              type="password"
              autoComplete="new-password"
              value={newPw}
              onChange={(e) => setNewPw(e.target.value)}
            />
          </Field>
          <Field label="再输一次">
            <Input
              type="password"
              autoComplete="new-password"
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
            />
          </Field>
          <FormError>{error}</FormError>
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={() => onOpenChange(false)}>
              取消
            </Button>
            <Button type="submit" disabled={saving}>
              修改密码
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
