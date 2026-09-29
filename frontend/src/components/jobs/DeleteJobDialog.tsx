import { Trash2 } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";

import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import { deleteJob, type DeleteJobResult, type JobItem } from "@/lib/jobs";
import { useResetOnOpen, useRetained } from "@/lib/useRetained";

import { errorMessage } from "./loadable";

/**
 * 删除任务的二次确认：显示任务名称，以及一个默认不勾的“同时删除该任务的 N 份快照”。
 * 后端在任务正在运行或排队时拒绝删除（docs/specs/2026-09-27-backup-jobs.md「编辑、暂停、删除」）。
 */
export function DeleteJobDialog({
  job,
  onCancel,
  onDeleted,
}: {
  job?: JobItem;
  onCancel: () => void;
  onDeleted: (id: number, result: DeleteJobResult) => void;
}) {
  const { t } = useTranslation();
  const [deleteSnapshots, setDeleteSnapshots] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const shown = useRetained(job);

  useResetOnOpen(job !== undefined, () => {
    setDeleteSnapshots(false);
    setError(undefined);
  });

  const cancel = () => {
    if (busy) return;
    onCancel();
  };

  const confirm = async () => {
    if (!job) return;
    setBusy(true);
    setError(undefined);
    try {
      const result = await deleteJob(job.id, deleteSnapshots);
      onDeleted(job.id, result);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={job !== undefined} onOpenChange={(next) => !next && cancel()}>
      <DialogContent className="gap-0 bg-card p-0 sm:max-w-md">
        <DialogHeader className="gap-1.5 border-b px-5 py-4 text-left">
          <DialogTitle>{t("jobs.list.delete.title", { name: shown?.name ?? "" })}</DialogTitle>
          <DialogDescription>{t("jobs.list.delete.hint")}</DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-3 p-5">
          <div className="flex items-start gap-2">
            <Checkbox
              id="delete-job-snapshots"
              checked={deleteSnapshots}
              onCheckedChange={(v) => setDeleteSnapshots(v === true)}
            />
            <Label htmlFor="delete-job-snapshots" className="font-normal">
              {t("jobs.list.delete.deleteSnapshots", { count: shown?.snapshot_count ?? 0 })}
            </Label>
          </div>
          {error && (
            <p role="alert" className="rounded-md bg-destructive-soft px-3 py-2.5 text-sm text-destructive">
              {error}
            </p>
          )}
        </div>
        <DialogFooter className="border-t bg-sidebar px-5 py-3.5">
          <Button variant="outline" disabled={busy} onClick={cancel}>
            {t("common.cancel")}
          </Button>
          <Button variant="destructive" disabled={busy} onClick={() => void confirm()}>
            <Trash2 />
            {busy ? t("common.submitting") : t("jobs.list.delete.confirm")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
