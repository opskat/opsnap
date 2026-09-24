import { CircleAlert, CircleCheck, Info, KeyRound, Upload } from "lucide-react";
import { useRef, useState } from "react";
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
import { formatDateTime } from "@/lib/format";
import { parseKeyFile } from "@/lib/storage";

import { useRetained } from "./useRetained";

type Source = "paste" | "file";

export interface UnlockTarget {
  /** 仓库位置（展示用） */
  location: string;
  /** 仓库格式文件的写入时间（Unix 秒），0 表示取不到 */
  createdAt: number;
  /** 用密钥解锁；成功时返回仓库中的快照数 */
  unlock: (key: string) => Promise<number>;
}

/**
 * 解锁已有仓库：粘贴密钥或上传密钥文件。
 * 失败次数只统计当前对话框内的尝试，关闭即清零；成功后显示快照数，点“完成”关闭。
 */
export function UnlockDialog({
  target,
  onClose,
  onDone,
}: {
  target?: UnlockTarget;
  onClose: () => void;
  onDone: () => void;
}) {
  const { t } = useTranslation();
  const [source, setSource] = useState<Source>("paste");
  const [pasted, setPasted] = useState("");
  const [file, setFile] = useState<{ name: string; key: string }>();
  const [failures, setFailures] = useState(0);
  const [error, setError] = useState<string>();
  const [submitting, setSubmitting] = useState(false);
  const [snapshots, setSnapshots] = useState<number>();
  const fileInput = useRef<HTMLInputElement>(null);
  const shown = useRetained(target);

  const reset = () => {
    setSource("paste");
    setPasted("");
    setFile(undefined);
    setFailures(0);
    setError(undefined);
    setSnapshots(undefined);
  };

  const close = () => {
    const done = snapshots !== undefined;
    reset();
    if (done) onDone();
    else onClose();
  };

  const key = (source === "paste" ? pasted : (file?.key ?? "")).trim();

  const submit = async () => {
    if (!target || !key) return;
    setSubmitting(true);
    setError(undefined);
    try {
      setSnapshots(await target.unlock(key));
    } catch (err) {
      if (err instanceof ApiError && err.code === ErrorCode.StorageKeyInvalid) {
        setFailures((n) => n + 1);
      } else {
        setError(err instanceof Error ? err.message : String(err));
      }
    } finally {
      setSubmitting(false);
    }
  };

  const readFile = async (f: File | undefined) => {
    if (!f) return;
    setFile({ name: f.name, key: parseKeyFile(await f.text()) });
  };

  const keyError = failures > 0 ? t("storage.unlock.wrongKey", { count: failures }) : undefined;

  return (
    <Dialog open={target !== undefined} onOpenChange={(next) => !next && !submitting && close()}>
      <DialogContent className="gap-0 bg-card p-0 sm:max-w-lg">
        <DialogHeader className="gap-1.5 border-b px-5 py-4 text-left">
          <DialogTitle>{t("storage.unlock.title")}</DialogTitle>
          <DialogDescription>
            {shown?.createdAt
              ? t("storage.unlock.hintCreated", { location: shown.location, time: formatDateTime(shown.createdAt) })
              : t("storage.unlock.hint", { location: shown?.location ?? "" })}
          </DialogDescription>
        </DialogHeader>
        {snapshots !== undefined ? (
          <>
            <p
              role="status"
              className="m-5 flex items-start gap-2 rounded-md bg-success-soft px-3 py-2.5 text-sm text-success"
            >
              <CircleCheck className="mt-0.5 size-4 shrink-0" />
              {t("storage.unlock.done", { count: snapshots })}
            </p>
            <DialogFooter className="border-t bg-sidebar px-5 py-3.5">
              <Button type="button" onClick={close}>
                {t("storage.unlock.finish")}
              </Button>
            </DialogFooter>
          </>
        ) : (
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void submit();
            }}
          >
            <div className="flex flex-col gap-4 p-5">
              <Segmented
                label={t("storage.unlock.source")}
                options={[
                  { value: "paste" as Source, label: t("storage.unlock.paste") },
                  { value: "file" as Source, label: t("storage.unlock.upload") },
                ]}
                value={source}
                onChange={setSource}
              />
              {source === "paste" ? (
                <FormField
                  label={t("storage.key.label")}
                  type="password"
                  value={pasted}
                  error={keyError}
                  autoComplete="off"
                  mono
                  onChange={(e) => setPasted(e.target.value)}
                />
              ) : (
                <div className="flex flex-col gap-1.5">
                  <input
                    ref={fileInput}
                    type="file"
                    accept=".txt,text/plain"
                    className="sr-only"
                    aria-label={t("storage.unlock.upload")}
                    onChange={(e) => void readFile(e.target.files?.[0])}
                  />
                  <Button type="button" variant="outline" className="w-fit" onClick={() => fileInput.current?.click()}>
                    <Upload />
                    {t("storage.unlock.chooseFile")}
                  </Button>
                  <span className="text-xs text-faint-foreground">
                    {file ? t("storage.unlock.fileRead", { name: file.name }) : t("storage.unlock.fileHint")}
                  </span>
                  {keyError && (
                    <span role="alert" className="text-xs text-destructive">
                      {keyError}
                    </span>
                  )}
                </div>
              )}
              <p className="flex items-start gap-2 rounded-md bg-running-soft px-3 py-2.5 text-sm text-running">
                <Info className="mt-0.5 size-4 shrink-0" />
                {t("storage.unlock.notice")}
              </p>
              {error && (
                <p
                  role="alert"
                  className="flex items-start gap-2 rounded-md bg-destructive-soft px-3 py-2.5 text-sm text-destructive"
                >
                  <CircleAlert className="mt-0.5 size-4 shrink-0" />
                  {error}
                </p>
              )}
            </div>
            <DialogFooter className="border-t bg-sidebar px-5 py-3.5">
              <Button type="button" variant="outline" disabled={submitting} onClick={close}>
                {t("common.cancel")}
              </Button>
              <Button type="submit" disabled={!key || submitting}>
                <KeyRound />
                {submitting ? t("common.submitting") : t("storage.unlock.submit")}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}
