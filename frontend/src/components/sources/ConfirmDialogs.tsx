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
import { deleteChannel, type ChannelItem } from "@/lib/sources";

import { useRetained } from "./useRetained";

/** 删除通道：只删除 OpsNap 中的记录与凭据；打开前调用方已确认没有引用 */
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
  const [error, setError] = useState<string>();
  const [deleting, setDeleting] = useState(false);
  const shown = useRetained(channel);

  const cancel = () => {
    setError(undefined);
    onCancel();
  };

  const confirm = async () => {
    if (!channel) return;
    setDeleting(true);
    try {
      await deleteChannel(channel.id);
      setError(undefined);
      onDeleted();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setDeleting(false);
    }
  };

  return (
    <Dialog open={channel !== undefined} onOpenChange={(next) => !next && !deleting && cancel()}>
      <DialogContent className="gap-0 bg-card p-0 sm:max-w-md">
        <DialogHeader className="gap-1.5 border-b px-5 py-4 text-left">
          <DialogTitle>{t("sources.channel.delete.title", { name: shown?.name ?? "" })}</DialogTitle>
          <DialogDescription>{t("sources.channel.delete.hint")}</DialogDescription>
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
            {deleting ? t("common.submitting") : t("sources.channel.delete.confirm")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
