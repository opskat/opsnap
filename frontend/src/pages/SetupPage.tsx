import { useEffect, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { Navigate, useNavigate } from "react-router";

import { FormField } from "@/components/form/FormField";
import { AuthLayout } from "@/components/layout/AuthLayout";
import { Button } from "@/components/ui/button";
import { ApiError } from "@/lib/api";
import { ErrorCode, getAuthStatus, MIN_PASSWORD_LENGTH, passwordLength, setup, USERNAME_PATTERN } from "@/lib/auth";

type Field = "setupCode" | "username" | "password" | "confirm";
type Errors = Partial<Record<Field | "form", string>>;

export function SetupPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [initialized, setInitialized] = useState<boolean>();
  const [values, setValues] = useState<Record<Field, string>>({
    setupCode: "",
    username: "",
    password: "",
    confirm: "",
  });
  const [errors, setErrors] = useState<Errors>({});
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    let cancelled = false;
    getAuthStatus()
      .then((s) => !cancelled && setInitialized(s.initialized))
      // 状态读取失败时仍显示表单；提交时会得到明确的错误
      .catch(() => !cancelled && setInitialized(false));
    return () => {
      cancelled = true;
    };
  }, []);

  if (initialized) return <Navigate to="/login" replace />;

  const set = (field: Field) => (e: { target: { value: string } }) =>
    setValues((v) => ({ ...v, [field]: e.target.value }));

  const validate = (): Errors => {
    const errs: Errors = {};
    if (!values.setupCode.trim()) errs.setupCode = t("form.required");
    if (!USERNAME_PATTERN.test(values.username)) errs.username = t("setup.usernameRule");
    if (passwordLength(values.password) < MIN_PASSWORD_LENGTH) errs.password = t("setup.passwordRule");
    if (values.confirm !== values.password) errs.confirm = t("setup.passwordMismatch");
    return errs;
  };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const errs = validate();
    setErrors(errs);
    if (Object.keys(errs).length > 0) return;
    setSubmitting(true);
    try {
      await setup({ setup_code: values.setupCode, username: values.username, password: values.password });
      await navigate("/", { replace: true });
    } catch (err) {
      if (!(err instanceof ApiError)) {
        setErrors({ form: String(err) });
      } else if (err.code === ErrorCode.AlreadyInitialized) {
        await navigate("/login", { replace: true });
      } else if (err.code === ErrorCode.SetupCodeInvalid) {
        setErrors({ setupCode: err.message });
      } else if (err.code === ErrorCode.UsernameInvalid) {
        setErrors({ username: err.message });
      } else if (err.code === ErrorCode.PasswordTooShort) {
        setErrors({ password: err.message });
      } else {
        setErrors({ form: err.message });
      }
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <AuthLayout title={t("setup.title")} subtitle={t("setup.subtitle")} footer={t("setup.footnote")}>
      <form className="flex flex-col gap-4" onSubmit={(e) => void submit(e)} noValidate>
        {errors.form && (
          <p role="alert" className="rounded-md bg-destructive-soft px-3 py-2.5 text-sm text-destructive">
            {errors.form}
          </p>
        )}
        <FormField
          label={t("setup.setupCode")}
          hint={t("setup.setupCodeHint")}
          error={errors.setupCode}
          value={values.setupCode}
          onChange={set("setupCode")}
          autoComplete="off"
          mono
        />
        <FormField
          label={t("auth.username")}
          error={errors.username}
          value={values.username}
          onChange={set("username")}
          autoComplete="username"
          mono
        />
        <FormField
          label={t("auth.password")}
          type="password"
          hint={t("setup.passwordRule")}
          error={errors.password}
          value={values.password}
          onChange={set("password")}
          autoComplete="new-password"
          mono
        />
        <FormField
          label={t("setup.confirmPassword")}
          type="password"
          error={errors.confirm}
          value={values.confirm}
          onChange={set("confirm")}
          autoComplete="new-password"
          mono
        />
        <Button type="submit" disabled={submitting}>
          {submitting ? t("common.submitting") : t("setup.submit")}
        </Button>
      </form>
    </AuthLayout>
  );
}
