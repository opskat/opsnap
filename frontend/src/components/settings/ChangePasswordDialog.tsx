import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { FormField } from "@/components/form/FormField";
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
import { changePassword, ErrorCode, MIN_PASSWORD_LENGTH, passwordLength } from "@/lib/auth";

type Field = "current" | "next" | "confirm";
type Errors = Partial<Record<Field | "form", string>>;

const empty = { current: "", next: "", confirm: "" };

export function ChangePasswordDialog({
  open,
  onOpenChange,
  onChanged,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onChanged: () => void;
}) {
  const { t } = useTranslation();
  const [values, setValues] = useState(empty);
  const [errors, setErrors] = useState<Errors>({});
  const [submitting, setSubmitting] = useState(false);

  const close = (next: boolean) => {
    if (!next) {
      setValues(empty);
      setErrors({});
    }
    onOpenChange(next);
  };

  const set = (field: Field) => (e: { target: { value: string } }) =>
    setValues((v) => ({ ...v, [field]: e.target.value }));

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const errs: Errors = {};
    if (!values.current) errs.current = t("form.required");
    if (passwordLength(values.next) < MIN_PASSWORD_LENGTH) errs.next = t("setup.passwordRule");
    if (values.confirm !== values.next) errs.confirm = t("setup.passwordMismatch");
    setErrors(errs);
    if (Object.keys(errs).length > 0) return;
    setSubmitting(true);
    try {
      await changePassword({ current_password: values.current, new_password: values.next });
      close(false);
      onChanged();
    } catch (err) {
      if (!(err instanceof ApiError)) setErrors({ form: String(err) });
      else if (err.code === ErrorCode.CurrentPasswordWrong) setErrors({ current: err.message });
      else if (err.code === ErrorCode.PasswordTooShort) setErrors({ next: err.message });
      else setErrors({ form: err.message });
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent className="gap-0 bg-card p-0 sm:max-w-lg">
        <form onSubmit={(e) => void submit(e)} noValidate>
          <DialogHeader className="gap-1.5 border-b px-5 py-4 text-left">
            <DialogTitle>{t("settings.changePassword")}</DialogTitle>
            <DialogDescription>{t("settings.changePasswordHint")}</DialogDescription>
          </DialogHeader>
          <div className="flex flex-col gap-4 p-5">
            {errors.form && (
              <p role="alert" className="rounded-md bg-destructive-soft px-3 py-2.5 text-sm text-destructive">
                {errors.form}
              </p>
            )}
            <FormField
              label={t("settings.currentPassword")}
              type="password"
              error={errors.current}
              value={values.current}
              onChange={set("current")}
              autoComplete="current-password"
              mono
            />
            <FormField
              label={t("settings.newPassword")}
              type="password"
              hint={t("setup.passwordRule")}
              error={errors.next}
              value={values.next}
              onChange={set("next")}
              autoComplete="new-password"
              mono
            />
            <FormField
              label={t("settings.confirmNewPassword")}
              type="password"
              error={errors.confirm}
              value={values.confirm}
              onChange={set("confirm")}
              autoComplete="new-password"
              mono
            />
          </div>
          <DialogFooter className="border-t bg-sidebar px-5 py-3.5">
            <Button type="button" variant="outline" onClick={() => close(false)}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={submitting}>
              {submitting ? t("common.submitting") : t("common.save")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
