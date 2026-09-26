import { Trash2 } from "lucide-react";
import { useState } from "react";
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
import { deleteChannel, deleteDataSource, type ChannelItem, type DataSourceItem } from "@/lib/sources";
import { useRetained } from "@/lib/useRetained";

/** 删除确认弹窗：通道与数据源共用，只删除 OpsNap 中的记录与凭据 */
function DeleteDialog<T extends { id: number; name: string }>({
  target,
  title,
  hint,
  confirmLabel,
  remove,
  onCancel,
  onDeleted,
}: {
  target?: T;
  title: (name: string) => string;
  hint: string;
  confirmLabel: string;
  remove: (id: number) => Promise<unknown>;
  onCancel: () => void;
  onDeleted: () => void;
}) {
  const { t } = useTranslation();
  const [error, setError] = useState<string>();
  const [deleting, setDeleting] = useState(false);
  const shown = useRetained(target);

  const cancel = () => {
    setError(undefined);
    onCancel();
  };

  const confirm = async () => {
    if (!target) return;
    setDeleting(true);
    try {
      await remove(target.id);
      setError(undefined);
      onDeleted();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setDeleting(false);
    }
  };

  return (
    <Dialog open={target !== undefined} onOpenChange={(next) => !next && !deleting && cancel()}>
      <DialogContent className="gap-0 bg-card p-0 sm:max-w-md">
        <DialogHeader className="gap-1.5 border-b px-5 py-4 text-left">
          <DialogTitle>{title(shown?.name ?? "")}</DialogTitle>
          <DialogDescription>{hint}</DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-3 p-5">
          {error && (
            <p role="alert" className="rounded-md bg-destructive-soft px-3 py-2.5 text-sm text-destructive">
              {error}
            </p>
          )}
        </div>
        <DialogFooter className="border-t bg-sidebar px-5 py-3.5">
          <Button variant="outline" disabled={deleting} onClick={cancel}>
            {t("common.cancel")}
          </Button>
          <Button variant="destructive" disabled={deleting} onClick={() => void confirm()}>
            <Trash2 />
            {deleting ? t("common.submitting") : confirmLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** 删除通道：打开前调用方已确认没有引用 */
export function DeleteChannelDialog({
  channel,
  onCancel,
  onDeleted,
}: {
  channel?: ChannelItem;
  onCancel: () => void;
  onDeleted: () => void;
}) {
  const { t } = useTranslation();
  return (
    <DeleteDialog
      target={channel}
      title={(name) => t("sources.channel.delete.title", { name })}
      hint={t("sources.channel.delete.hint")}
      confirmLabel={t("sources.channel.delete.confirm")}
      remove={deleteChannel}
      onCancel={onCancel}
      onDeleted={onDeleted}
    />
  );
}

/** 删除数据源：不影响数据库或服务器 */
export function DeleteDataSourceDialog({
  dataSource,
  onCancel,
  onDeleted,
}: {
  dataSource?: DataSourceItem;
  onCancel: () => void;
  onDeleted: () => void;
}) {
  const { t } = useTranslation();
  return (
    <DeleteDialog
      target={dataSource}
      title={(name) => t("sources.dataSource.delete.title", { name })}
      hint={t("sources.dataSource.delete.hint")}
      confirmLabel={t("sources.dataSource.delete.confirm")}
      remove={deleteDataSource}
      onCancel={onCancel}
      onDeleted={onDeleted}
    />
  );
}
