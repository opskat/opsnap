import { Copy, TriangleAlert } from "lucide-react";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";

import { FormField } from "@/components/form/FormField";
import { Segmented } from "@/components/form/Segmented";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { ApiError } from "@/lib/api";
import { ErrorCode } from "@/lib/auth";
import { formatDate } from "@/lib/format";
import { createToken, DEFAULT_EXPIRY, EXPIRY_OPTIONS, type ApiToken } from "@/lib/tokens";

function expiryLabel(t: TFunction, days: number) {
  if (days === 0) return t("tokens.never");
  if (days === 365) return t("tokens.oneYear");
  return t("tokens.days", { count: days });
}

/** 生成令牌：填名称与有效期；成功后在同一弹窗里显示一次完整令牌 */
export function CreateTokenDialog({
  open,
  onOpenChange,
  onCreated,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onCreated: () => void;
}) {
  const { t } = useTranslation();
  const [name, setName] = useState("");
  const [days, setDays] = useState<number>(DEFAULT_EXPIRY);
  const [errors, setErrors] = useState<{ name?: string; form?: string }>({});
  const [submitting, setSubmitting] = useState(false);
  const [created, setCreated] = useState<{ item: ApiToken; token: string }>();
  const [copied, setCopied] = useState(false);
  // 过期日期预览以打开页面的时间为基准，精确到天即可
  const [now] = useState(() => Date.now() / 1000);

  const close = () => {
    const wasCreated = created !== undefined;
    setName("");
    setDays(DEFAULT_EXPIRY);
    setErrors({});
    setCreated(undefined);
    setCopied(false);
    onOpenChange(false);
    if (wasCreated) onCreated();
  };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (!name.trim()) {
      setErrors({ name: t("form.required") });
      return;
    }
    setSubmitting(true);
    setErrors({});
    try {
      setCreated(await createToken({ name: name.trim(), expires_in_days: days }));
    } catch (err) {
      if (
        err instanceof ApiError &&
        (err.code === ErrorCode.TokenNameDuplicate || err.code === ErrorCode.TokenNameInvalid)
      ) {
        setErrors({ name: err.message });
      } else {
        setErrors({ form: err instanceof Error ? err.message : String(err) });
      }
    } finally {
      setSubmitting(false);
    }
  };

  const copy = async () => {
    if (!created) return;
    try {
      await navigator.clipboard.writeText(created.token);
      setCopied(true);
    } catch {
      // 剪贴板不可用时用户仍可手动选中复制
    }
  };

  const expiresPreview =
    days === 0 ? t("tokens.never") : t("tokens.expiresOn", { date: formatDate(now + days * 86400) });

  return (
    <Dialog open={open} onOpenChange={(next) => !next && close()}>
      <DialogContent className="gap-0 bg-card p-0 sm:max-w-lg">
        {created ? (
          <>
            <DialogHeader className="gap-1.5 border-b px-5 py-4 text-left">
              <DialogTitle>{t("tokens.createdTitle")}</DialogTitle>
              <DialogDescription>
                {created.item.name} ·{" "}
                {created.item.expires_at
                  ? t("tokens.expiresOn", { date: formatDate(created.item.expires_at) })
                  : t("tokens.never")}
              </DialogDescription>
            </DialogHeader>
            <div className="flex flex-col gap-4 p-5">
              <div className="flex flex-col gap-3 rounded-md border bg-background p-3.5">
                <code className="font-mono text-base font-semibold break-all select-all">{created.token}</code>
                <Button type="button" variant="outline" size="sm" className="w-fit" onClick={() => void copy()}>
                  <Copy />
                  {copied ? t("common.copied") : t("common.copy")}
                </Button>
              </div>
              <p className="flex gap-2 rounded-md bg-warning-soft px-3 py-2.5 text-sm text-warning">
                <TriangleAlert className="mt-0.5 size-4 shrink-0" />
                {t("tokens.showOnce")}
              </p>
            </div>
            <DialogFooter className="border-t bg-sidebar px-5 py-3.5">
              <Button type="button" onClick={close}>
                {t("tokens.copiedClose")}
              </Button>
            </DialogFooter>
          </>
        ) : (
          <form onSubmit={(e) => void submit(e)} noValidate>
            <DialogHeader className="gap-1.5 border-b px-5 py-4 text-left">
              <DialogTitle>{t("tokens.createTitle")}</DialogTitle>
              <DialogDescription>{t("tokens.createHint")}</DialogDescription>
            </DialogHeader>
            <div className="flex flex-col gap-4 p-5">
              {errors.form && (
                <p role="alert" className="rounded-md bg-destructive-soft px-3 py-2.5 text-sm text-destructive">
                  {errors.form}
                </p>
              )}
              <FormField
                label={t("tokens.name")}
                hint={t("tokens.nameHint")}
                error={errors.name}
                value={name}
                maxLength={64}
                onChange={(e) => setName(e.target.value)}
                mono
              />
              <div className="flex flex-col gap-1.5">
                <span className="text-sm text-muted-foreground">{t("tokens.validity")}</span>
                <Segmented
                  label={t("tokens.validity")}
                  options={EXPIRY_OPTIONS.map((d) => ({ value: d as number, label: expiryLabel(t, d) }))}
                  value={days}
                  onChange={setDays}
                />
                <span className="font-mono text-xs text-faint-foreground">{expiresPreview}</span>
              </div>
            </div>
            <DialogFooter className="border-t bg-sidebar px-5 py-3.5">
              <Button type="button" variant="outline" onClick={close}>
                {t("common.cancel")}
              </Button>
              <Button type="submit" disabled={submitting}>
                {submitting ? t("common.submitting") : t("tokens.create")}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}
