import { ArrowRight, CircleAlert, CircleCheck, Cloud, Folder, PlugZap } from "lucide-react";
import { useState, type FormEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { FormField } from "@/components/form/FormField";
import { Switch } from "@/components/form/Switch";
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
import {
  emptyLocation,
  locationOf,
  probeStorage,
  s3Location,
  updateStorage,
  type ProbeResult,
  type Storage,
  type StorageKind,
  type StorageLocation,
} from "@/lib/storage";
import { cn } from "@/lib/utils";

/** 表单填写完成、测试通过后交给下一步的内容 */
export interface StorageDraft {
  name: string;
  location: StorageLocation;
}

type Field = "name" | "path" | "endpoint";

const fieldOf: Record<number, Field> = {
  [ErrorCode.StorageNameInvalid]: "name",
  [ErrorCode.StorageNameDuplicate]: "name",
  [ErrorCode.StoragePathRelative]: "path",
  [ErrorCode.StorageEndpointScheme]: "endpoint",
};

type Result = { kind: "success" | "error"; message: string };

/**
 * 新建或编辑存储的第一步：填写并测试连接。
 * 新建时按目标位置交给下一步（设置密钥 / 解锁）；编辑且位置没变时直接保存，位置变了交给调用方确认。
 */
export function StorageFormDialog({
  open,
  editing,
  onOpenChange,
  onNext,
  onSaved,
  browse,
}: {
  open: boolean;
  editing?: Storage;
  onOpenChange: (open: boolean) => void;
  onNext: (draft: StorageDraft, probe: ProbeResult) => void;
  onSaved: (item: Storage) => void;
  /** 本地目录旁的“浏览…”按钮；不传时不显示 */
  browse?: (current: string, pick: (path: string) => void) => void;
}) {
  const { t } = useTranslation();
  const initial = (): StorageDraft => ({
    name: editing?.name ?? "",
    location: editing ? locationOf(editing) : emptyLocation(),
  });
  const [draft, setDraft] = useState(initial);
  const [fieldErrors, setFieldErrors] = useState<Partial<Record<Field, string>>>({});
  const [result, setResult] = useState<Result>();
  const [probe, setProbe] = useState<{ key: string; result: ProbeResult }>();
  const [busy, setBusy] = useState<"test" | "next">();

  const snapshot = JSON.stringify(draft);
  const current = probe?.key === snapshot ? probe.result : undefined;

  const close = () => {
    setDraft(initial());
    setFieldErrors({});
    setResult(undefined);
    setProbe(undefined);
    onOpenChange(false);
  };

  const update = (patch: Partial<StorageLocation>) => {
    setDraft((d) => ({ ...d, location: { ...d.location, ...patch } }));
    setResult(undefined);
  };
  const setKind = (kind: StorageKind) => update({ kind });
  const bind = (field: keyof StorageLocation) => (e: { target: { value: string } }) =>
    update({ [field]: e.target.value });

  const showError = (err: unknown) => {
    const field = err instanceof ApiError ? fieldOf[err.code] : undefined;
    const message = err instanceof Error ? err.message : String(err);
    if (field) setFieldErrors({ [field]: message });
    else setResult({ kind: "error", message });
  };

  const describe = (p: ProbeResult): Result => {
    const moved = !editing || p.location_changed;
    if (p.state === "not_empty") return { kind: "error", message: t("storage.probe.notEmpty") };
    if (!moved) {
      return p.state === "repository"
        ? { kind: "success", message: t("storage.probe.sameRepository") }
        : { kind: "error", message: t("storage.probe.notRepository") };
    }
    if (p.state === "empty") {
      return { kind: "success", message: t(editing ? "storage.probe.emptyMove" : "storage.probe.empty") };
    }
    const created = p.created_at ? t("storage.probe.createdAt", { time: formatDateTime(p.created_at) }) : "";
    return {
      kind: "success",
      message: t(editing ? "storage.probe.repositoryMove" : "storage.probe.repository", { created }),
    };
  };

  const runProbe = async () => {
    setFieldErrors({});
    setResult(undefined);
    try {
      const res = await probeStorage({ id: editing?.id, name: draft.name, location: draft.location });
      setProbe({ key: snapshot, result: res });
      const r = describe(res);
      setResult(r);
      return r.kind === "success" ? res : undefined;
    } catch (err) {
      showError(err);
      return undefined;
    }
  };

  const test = async () => {
    setBusy("test");
    try {
      await runProbe();
    } finally {
      setBusy(undefined);
    }
  };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy("next");
    try {
      const res = current && describe(current).kind === "success" ? current : await runProbe();
      if (!res) return;
      if (editing && !res.location_changed) {
        const saved = await updateStorage(editing.id, {
          name: draft.name,
          location: draft.location,
          confirm_location_change: false,
        });
        onSaved(saved.item);
        close();
        return;
      }
      onNext({ name: draft.name.trim(), location: draft.location }, res);
    } catch (err) {
      showError(err);
    } finally {
      setBusy(undefined);
    }
  };

  const loc = draft.location;
  const nextLabel = editing
    ? t("common.save")
    : current?.state === "empty"
      ? t("storage.form.nextSetKey")
      : current?.state === "repository"
        ? t("storage.form.nextUnlock")
        : t("storage.form.next");

  return (
    <Dialog open={open} onOpenChange={(next) => !next && busy !== "next" && close()}>
      <DialogContent className="gap-0 bg-card p-0 sm:max-w-xl">
        <form onSubmit={(e) => void submit(e)} noValidate>
          <DialogHeader className="gap-1.5 border-b px-5 py-4 text-left">
            <DialogTitle>{editing ? t("storage.form.editTitle") : t("storage.form.createTitle")}</DialogTitle>
            <DialogDescription>{t("storage.form.hint")}</DialogDescription>
          </DialogHeader>
          <div className="flex max-h-[70vh] flex-col gap-4 overflow-y-auto p-5">
            <KindTabs value={loc.kind} onChange={setKind} />
            <FormField
              label={t("storage.form.name")}
              value={draft.name}
              maxLength={64}
              error={fieldErrors.name}
              onChange={(e) => {
                setDraft((d) => ({ ...d, name: e.target.value }));
                setFieldErrors((f) => ({ ...f, name: undefined }));
              }}
            />
            {loc.kind === "local" ? (
              <div className="flex items-end gap-2">
                <div className="min-w-0 flex-1">
                  <FormField
                    label={t("storage.form.path")}
                    value={loc.path}
                    placeholder="/var/backups/opsnap"
                    hint={t("storage.form.pathHint")}
                    error={fieldErrors.path}
                    mono
                    onChange={(e) => {
                      bind("path")(e);
                      setFieldErrors((f) => ({ ...f, path: undefined }));
                    }}
                  />
                </div>
                {browse && (
                  <Button
                    type="button"
                    variant="outline"
                    className="mb-5.5"
                    onClick={() => browse(loc.path, (path) => update({ path }))}
                  >
                    {t("storage.form.browse")}
                  </Button>
                )}
              </div>
            ) : (
              <div className="grid grid-cols-2 gap-x-3 gap-y-4">
                <FormField
                  label="Endpoint"
                  value={loc.endpoint}
                  placeholder="minio.lan:9000"
                  hint={t("storage.form.endpointHint")}
                  error={fieldErrors.endpoint}
                  mono
                  onChange={(e) => {
                    bind("endpoint")(e);
                    setFieldErrors((f) => ({ ...f, endpoint: undefined }));
                  }}
                />
                <FormField label={<Optional label="Region" />} value={loc.region} mono onChange={bind("region")} />
                <FormField label="Bucket" value={loc.bucket} mono onChange={bind("bucket")} />
                <FormField
                  label={<Optional label={t("storage.form.prefix")} />}
                  value={loc.prefix}
                  hint={t("storage.form.finalLocation", { location: s3Location(loc.bucket, loc.prefix) })}
                  mono
                  onChange={bind("prefix")}
                />
                <FormField
                  label="Access Key"
                  value={loc.access_key}
                  autoComplete="off"
                  mono
                  onChange={bind("access_key")}
                />
                <FormField
                  label="Secret Key"
                  type="password"
                  value={loc.secret_key}
                  placeholder={editing?.has_secret_key ? t("storage.form.secretKeep") : undefined}
                  autoComplete="new-password"
                  mono
                  onChange={bind("secret_key")}
                />
                <div className="col-span-2 flex flex-col rounded-md border">
                  <SwitchRow
                    label={t("storage.form.useTLS")}
                    hint={t("storage.form.useTLSHint")}
                    checked={loc.use_tls}
                    onChange={(use_tls) => update({ use_tls })}
                  />
                  <SwitchRow
                    label={t("storage.form.skipVerify")}
                    hint={t("storage.form.skipVerifyHint")}
                    checked={loc.skip_verify}
                    onChange={(skip_verify) => update({ skip_verify })}
                    className="border-t"
                  />
                </div>
              </div>
            )}
            {result && (
              <p
                role={result.kind === "error" ? "alert" : "status"}
                className={cn(
                  "flex items-start gap-2 rounded-md px-3 py-2.5 text-sm",
                  result.kind === "error" ? "bg-destructive-soft text-destructive" : "bg-success-soft text-success"
                )}
              >
                {result.kind === "error" ? (
                  <CircleAlert className="mt-0.5 size-4 shrink-0" />
                ) : (
                  <CircleCheck className="mt-0.5 size-4 shrink-0" />
                )}
                {result.message}
              </p>
            )}
          </div>
          <DialogFooter className="border-t bg-sidebar px-5 py-3.5 sm:justify-between">
            <Button type="button" variant="outline" disabled={busy !== undefined} onClick={() => void test()}>
              <PlugZap />
              {busy === "test" ? t("storage.form.testing") : t("storage.form.test")}
            </Button>
            <div className="flex gap-2">
              <Button type="button" variant="outline" disabled={busy === "next"} onClick={close}>
                {t("common.cancel")}
              </Button>
              <Button type="submit" disabled={busy !== undefined}>
                {busy === "next" ? t("common.submitting") : nextLabel}
                {!editing && busy !== "next" && <ArrowRight />}
              </Button>
            </div>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function KindTabs({ value, onChange }: { value: StorageKind; onChange: (kind: StorageKind) => void }) {
  const { t } = useTranslation();
  const options: { value: StorageKind; label: string; icon: ReactNode }[] = [
    { value: "local", label: t("storage.kind.local"), icon: <Folder /> },
    { value: "s3", label: t("storage.kind.s3"), icon: <Cloud /> },
  ];
  return (
    <div
      role="radiogroup"
      aria-label={t("storage.form.kind")}
      className="inline-flex w-fit gap-0.5 rounded-md bg-accent p-0.75"
    >
      {options.map((o) => {
        const selected = o.value === value;
        return (
          <button
            key={o.value}
            type="button"
            role="radio"
            aria-checked={selected}
            onClick={() => onChange(o.value)}
            className={cn(
              "flex items-center gap-1.5 rounded-sm px-3 py-1.5 text-sm [&_svg]:size-4",
              selected ? "bg-card font-semibold text-foreground" : "text-muted-foreground hover:text-foreground"
            )}
          >
            {o.icon}
            {o.label}
          </button>
        );
      })}
    </div>
  );
}

function Optional({ label }: { label: string }) {
  const { t } = useTranslation();
  return (
    <>
      {label}
      <span className="ml-1.5 text-xs text-faint-foreground">{t("storage.form.optional")}</span>
    </>
  );
}

function SwitchRow({
  label,
  hint,
  checked,
  onChange,
  className,
}: {
  label: string;
  hint: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
  className?: string;
}) {
  return (
    <div className={cn("flex items-center justify-between gap-3 px-3.5 py-2.5", className)}>
      <div className="flex flex-col gap-0.5">
        <span className="text-sm">{label}</span>
        <span className="text-xs text-muted-foreground">{hint}</span>
      </div>
      <Switch checked={checked} onCheckedChange={onChange} aria-label={label} />
    </div>
  );
}
