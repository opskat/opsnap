import { Archive, Ban, FolderPlus, Lock, Trash2, TriangleAlert } from "lucide-react";
import { useState, type ReactNode } from "react";
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
import { deleteStorage, type Storage } from "@/lib/storage";

/** 更改存储位置前的二次确认：原位置的仓库保持不动，按新位置的情况继续 */
export function ChangeLocationDialog({
  change,
  onCancel,
  onConfirm,
}: {
  change?: { from: string; to: string };
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const { t } = useTranslation();
  return (
    <Dialog open={change !== undefined} onOpenChange={(next) => !next && onCancel()}>
      <DialogContent className="gap-0 bg-card p-0 sm:max-w-lg">
        <DialogHeader className="gap-1.5 border-b px-5 py-4 text-left">
          <DialogTitle>{t("storage.move.title")}</DialogTitle>
          <DialogDescription>
            {t("storage.move.hint", { from: change?.from ?? "", to: change?.to ?? "" })}
          </DialogDescription>
        </DialogHeader>
        <ul className="flex flex-col gap-2.5 p-5 text-sm">
          <Line icon={<Archive />}>{t("storage.move.keepOld", { from: change?.from ?? "" })}</Line>
          <Line icon={<FolderPlus />}>{t("storage.move.empty")}</Line>
          <Line icon={<Lock />}>{t("storage.move.repository")}</Line>
          <Line icon={<Ban />}>{t("storage.move.notEmpty")}</Line>
        </ul>
        <DialogFooter className="border-t bg-sidebar px-5 py-3.5">
          <Button variant="outline" onClick={onCancel}>
            {t("common.cancel")}
          </Button>
          <Button onClick={onConfirm}>{t("storage.move.confirm")}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function Line({ icon, children }: { icon: ReactNode; children: ReactNode }) {
  return (
    <li className="flex items-start gap-2.5 [&_svg]:mt-0.5 [&_svg]:size-4 [&_svg]:shrink-0 [&_svg]:text-muted-foreground">
      {icon}
      <span>{children}</span>
    </li>
  );
}

/** 删除存储：只删 OpsNap 中的记录与托管的密钥，数据保留 */
export function DeleteStorageDialog({
  storage,
  onCancel,
  onDeleted,
  downloadKey,
}: {
  storage?: Storage;
  onCancel: () => void;
  onDeleted: () => void;
  /** “先下载密钥文件”入口（需要验证身份）；不传时不显示 */
  downloadKey?: (storage: Storage) => void;
}) {
  const { t } = useTranslation();
  const [error, setError] = useState<string>();
  const [deleting, setDeleting] = useState(false);

  const cancel = () => {
    setError(undefined);
    onCancel();
  };

  const confirm = async () => {
    if (!storage) return;
    setDeleting(true);
    try {
      await deleteStorage(storage.id);
      setError(undefined);
      onDeleted();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setDeleting(false);
    }
  };

  return (
    <Dialog open={storage !== undefined} onOpenChange={(next) => !next && cancel()}>
      <DialogContent className="gap-0 bg-card p-0 sm:max-w-md">
        <DialogHeader className="gap-1.5 border-b px-5 py-4 text-left">
          <DialogTitle>{t("storage.delete.title", { name: storage?.name ?? "" })}</DialogTitle>
          <DialogDescription>{t("storage.delete.hint")}</DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-3 p-5">
          <p className="flex items-start gap-2 rounded-md bg-warning-soft px-3 py-2.5 text-sm text-warning">
            <TriangleAlert className="mt-0.5 size-4 shrink-0" />
            {t("storage.delete.warning")}
          </p>
          {downloadKey && storage && (
            <div className="flex items-center gap-2">
              <Button variant="outline" size="sm" onClick={() => downloadKey(storage)}>
                {t("storage.delete.downloadFirst")}
              </Button>
              <span className="text-xs text-faint-foreground">{t("storage.delete.needsVerify")}</span>
            </div>
          )}
          {error && (
            <p role="alert" className="rounded-md bg-destructive-soft px-3 py-2.5 text-sm text-destructive">
              {error}
            </p>
          )}
        </div>
        <DialogFooter className="border-t bg-sidebar px-5 py-3.5">
          <Button variant="outline" onClick={cancel}>
            {t("common.cancel")}
          </Button>
          <Button variant="destructive" disabled={deleting} onClick={() => void confirm()}>
            <Trash2 />
            {deleting ? t("common.submitting") : t("storage.delete.confirm")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
