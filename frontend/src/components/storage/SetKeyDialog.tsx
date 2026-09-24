import { Lock, RefreshCw, TriangleAlert } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

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
import { createStorage, keyInfo, MIN_KEY_LENGTH, type KeyInfo, type Storage } from "@/lib/storage";

import { KeyActions } from "./KeyActions";
import type { StorageDraft } from "./StorageFormDialog";
import { useRetained } from "./useRetained";

type Mode = "generated" | "custom";

/** 目标位置为空时设置加密密钥：勾选确认后才建库并保存存储 */
export function SetKeyDialog({
  draft,
  onCancel,
  onCreated,
  onBecameRepository,
}: {
  /** 为空时对话框关闭 */
  draft?: StorageDraft;
  onCancel: () => void;
  onCreated: (item: Storage) => void;
  /**
   * 启用时发现位置在测试之后已变成 kopia 仓库（没有覆盖）：由调用方重新判断位置。
   * 返回 true 表示已转入下一步（此对话框随之关闭）；抛出的错误显示在对话框中
   */
  onBecameRepository?: (draft: StorageDraft) => Promise<boolean>;
}) {
  const { t } = useTranslation();
  const [mode, setMode] = useState<Mode>("generated");
  const [generated, setGenerated] = useState<KeyInfo>();
  const [custom, setCustom] = useState({ key: "", confirm: "" });
  const [confirmed, setConfirmed] = useState(false);
  const [error, setError] = useState<string>();
  const [submitting, setSubmitting] = useState(false);
  const [generation, setGeneration] = useState(0);
  const open = draft !== undefined;
  const shown = useRetained(draft);

  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    keyInfo()
      .then((k) => !cancelled && setGenerated(k))
      .catch((err: unknown) => !cancelled && setError(err instanceof Error ? err.message : String(err)));
    return () => {
      cancelled = true;
    };
  }, [open, generation]);

  const reset = () => {
    setMode("generated");
    setGenerated(undefined);
    setCustom({ key: "", confirm: "" });
    setConfirmed(false);
    setError(undefined);
  };

  const cancel = () => {
    reset();
    onCancel();
  };

  const customError =
    custom.key && custom.key.length < MIN_KEY_LENGTH
      ? t("storage.key.tooShort", { min: MIN_KEY_LENGTH })
      : custom.confirm && custom.confirm !== custom.key
        ? t("storage.key.mismatch")
        : undefined;
  const key = mode === "generated" ? generated?.key : custom.key;
  const ready =
    confirmed &&
    (mode === "generated" ? !!generated : custom.key.length >= MIN_KEY_LENGTH && custom.confirm === custom.key);

  const submit = async () => {
    if (!draft || !key || !ready) return;
    setSubmitting(true);
    setError(undefined);
    try {
      const res = await createStorage({ name: draft.name, location: draft.location, key, confirm_saved: true });
      reset();
      onCreated(res.item);
    } catch (err) {
      try {
        if (
          err instanceof ApiError &&
          err.code === ErrorCode.StorageAlreadyRepository &&
          onBecameRepository &&
          (await onBecameRepository(draft))
        ) {
          reset();
          return;
        }
        setError(err instanceof Error ? err.message : String(err));
      } catch (again) {
        setError(again instanceof Error ? again.message : String(again));
      }
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(next) => !next && !submitting && cancel()}>
      <DialogContent className="gap-0 bg-card p-0 sm:max-w-lg">
        <DialogHeader className="gap-1.5 border-b px-5 py-4 text-left">
          <DialogTitle>{t("storage.key.setTitle")}</DialogTitle>
          <DialogDescription>{t("storage.key.setHint", { name: shown?.name ?? "" })}</DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-4 p-5">
          <Segmented
            label={t("storage.key.mode")}
            options={[
              { value: "generated" as Mode, label: t("storage.key.generated") },
              { value: "custom" as Mode, label: t("storage.key.custom") },
            ]}
            value={mode}
            onChange={(m) => {
              setMode(m);
              setConfirmed(false);
            }}
          />
          {mode === "generated" ? (
            <div className="flex flex-col gap-3 rounded-md border bg-background p-3.5">
              <code aria-label={t("storage.key.label")} className="font-mono text-md break-all">
                {generated?.key ?? t("common.loading")}
              </code>
              {generated && shown && (
                <div className="flex flex-wrap gap-2">
                  <KeyActions
                    name={shown.name}
                    location={shown.location}
                    keyText={generated.key}
                    fingerprint={generated.fingerprint}
                  />
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    onClick={() => {
                      setConfirmed(false);
                      setGeneration((n) => n + 1);
                    }}
                  >
                    <RefreshCw />
                    {t("storage.key.regenerate")}
                  </Button>
                </div>
              )}
              {generated && (
                <p className="font-mono text-xs text-muted-foreground">
                  {t("storage.key.fingerprintLine", {
                    fingerprint: generated.fingerprint,
                    encryption: generated.encryption,
                  })}
                </p>
              )}
            </div>
          ) : (
            <div className="flex flex-col gap-3">
              <FormField
                label={t("storage.key.customPassword")}
                type="password"
                value={custom.key}
                hint={t("storage.key.tooShort", { min: MIN_KEY_LENGTH })}
                autoComplete="new-password"
                mono
                onChange={(e) => setCustom((c) => ({ ...c, key: e.target.value }))}
              />
              <FormField
                label={t("storage.key.customConfirm")}
                type="password"
                value={custom.confirm}
                error={customError}
                autoComplete="new-password"
                mono
                onChange={(e) => setCustom((c) => ({ ...c, confirm: e.target.value }))}
              />
            </div>
          )}
          <p className="flex items-start gap-2 rounded-md bg-destructive-soft px-3 py-2.5 text-sm text-destructive">
            <TriangleAlert className="mt-0.5 size-4 shrink-0" />
            {t("storage.key.warning")}
          </p>
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              className="size-4 accent-primary"
              checked={confirmed}
              onChange={(e) => setConfirmed(e.target.checked)}
            />
            {t("storage.key.confirmSaved")}
          </label>
          {error && (
            <p role="alert" className="rounded-md bg-destructive-soft px-3 py-2.5 text-sm text-destructive">
              {error}
            </p>
          )}
        </div>
        <DialogFooter className="border-t bg-sidebar px-5 py-3.5">
          <Button type="button" variant="outline" disabled={submitting} onClick={cancel}>
            {t("common.cancel")}
          </Button>
          <Button type="button" disabled={!ready || submitting} onClick={() => void submit()}>
            <Lock />
            {submitting ? t("common.submitting") : t("storage.key.enable")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
