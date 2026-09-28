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

/** 取消向导的二次确认：确认后放弃，不保存任何内容 */
export function CancelWizardDialog({
  open,
  editing,
  onKeep,
  onDiscard,
}: {
  open: boolean;
  editing: boolean;
  onKeep: () => void;
  onDiscard: () => void;
}) {
  const { t } = useTranslation();
  return (
    <Dialog open={open} onOpenChange={(next) => !next && onKeep()}>
      <DialogContent className="gap-0 bg-card p-0 sm:max-w-md">
        <DialogHeader className="gap-1.5 border-b px-5 py-4 text-left">
          <DialogTitle>{t(editing ? "jobs.wizard.cancel.titleEdit" : "jobs.wizard.cancel.title")}</DialogTitle>
          <DialogDescription>{t("jobs.wizard.cancel.hint")}</DialogDescription>
        </DialogHeader>
        <DialogFooter className="bg-sidebar px-5 py-3.5">
          <Button variant="outline" onClick={onKeep}>
            {t("jobs.wizard.cancel.keep")}
          </Button>
          <Button variant="destructive" onClick={onDiscard}>
            {t("jobs.wizard.cancel.discard")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
