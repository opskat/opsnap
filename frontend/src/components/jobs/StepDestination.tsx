import { Cloud, HardDrive, Lock } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";

import { FormField } from "@/components/form/FormField";
import { Segmented } from "@/components/form/Segmented";
import { Button } from "@/components/ui/button";
import type { Compression, JobDraft, WizardErrors } from "@/lib/jobs";
import type { Storage } from "@/lib/storage";

import { Choice, FieldError, LoadError, SectionTitle, StatusBadge } from "./Choice";
import type { Loadable } from "./loadable";

/** 第 3 步：目的地（存储、路径前缀、压缩；加密只读） */
export function StepDestination({
  draft,
  storages,
  locked,
  errors,
  onChange,
  onRetry,
}: {
  draft: JobDraft;
  storages: Loadable<Storage[]>;
  /** 编辑任务时存储与路径前缀不能修改 */
  locked: boolean;
  errors: Pick<WizardErrors, "storage" | "prefix">;
  onChange: (patch: Partial<JobDraft>) => void;
  onRetry: () => void;
}) {
  const { t } = useTranslation();
  const selected = storages.status === "ready" ? storages.data.find((s) => s.id === draft.storageId) : undefined;

  const list = () => {
    if (storages.status === "loading") return <p className="text-sm text-muted-foreground">{t("common.loading")}</p>;
    if (storages.status === "error") {
      return (
        <LoadError message={t("jobs.wizard.destination.loadFailed", { message: storages.message })} onRetry={onRetry} />
      );
    }
    if (storages.data.length === 0) {
      return (
        <div className="flex flex-col items-center gap-2 rounded-lg border bg-card px-6 py-10 text-center">
          <p className="text-md font-medium">{t("storage.empty")}</p>
          <p className="text-sm text-muted-foreground">{t("jobs.wizard.destination.emptyHint")}</p>
          <Button asChild variant="outline" className="mt-2">
            <Link to="/storage">{t("storage.create")}</Link>
          </Button>
        </div>
      );
    }
    return (
      <div
        role="radiogroup"
        aria-label={t("jobs.wizard.destination.storage")}
        className="divide-y overflow-hidden rounded-lg border bg-card"
      >
        {storages.data.map((s) => {
          const checked = s.id === draft.storageId;
          const ready = s.status === "ok";
          return (
            <Choice
              key={s.id}
              checked={checked}
              disabled={locked ? !checked : !ready}
              onSelect={() => onChange({ storageId: s.id })}
              className="px-3.5 py-3"
            >
              <span className="flex items-center gap-3">
                <span className="flex size-8 shrink-0 items-center justify-center rounded-md border bg-background text-muted-foreground [&_svg]:size-4">
                  {s.kind === "s3" ? <Cloud /> : <HardDrive />}
                </span>
                <span className="flex min-w-0 flex-1 flex-col">
                  <span className="truncate text-sm font-medium">{s.name}</span>
                  <span className="truncate text-xs text-muted-foreground">
                    {t(`storage.kind.${s.kind}`)} · <span className="font-mono">{s.location}</span>
                  </span>
                </span>
                <StatusBadge tone={ready ? "ok" : s.status === "wrong_key" ? "warn" : "fail"}>
                  {t(`storage.status.${s.status}`)}
                </StatusBadge>
              </span>
              {!ready && (
                <span className="mt-1.5 block pl-11 text-xs text-destructive">
                  {s.status_message || t(`storage.status.${s.status}`)}
                </span>
              )}
            </Choice>
          );
        })}
      </div>
    );
  };

  return (
    <div className="flex flex-col gap-6">
      <section className="flex flex-col gap-2.5">
        <SectionTitle title={t("jobs.wizard.destination.storage")} hint={t("jobs.wizard.destination.storageHint")} />
        {list()}
        <FieldError>{errors.storage}</FieldError>
      </section>

      <section className="flex flex-col gap-2.5">
        <SectionTitle title={t("jobs.wizard.destination.path")} />
        <div className="flex flex-col gap-2 rounded-lg border bg-card px-4 py-3.5">
          <FormField
            label={t("jobs.wizard.destination.prefix")}
            mono
            value={draft.prefix}
            readOnly={locked}
            hint={locked ? t("jobs.wizard.destination.lockedHint") : t("jobs.wizard.destination.prefixHint")}
            error={errors.prefix}
            onChange={(e) => onChange({ prefix: e.target.value, prefixEdited: true })}
          />
          <p className="flex flex-wrap items-baseline gap-1 text-xs text-muted-foreground">
            <span>{t("jobs.wizard.destination.location")}</span>
            {selected ? (
              <code className="font-mono text-foreground">{`${selected.name}:/${draft.prefix}`}</code>
            ) : (
              <span>{t("jobs.wizard.destination.locationPending")}</span>
            )}
          </p>
        </div>
      </section>

      <section className="flex flex-col gap-2.5">
        <SectionTitle title={t("jobs.wizard.destination.encryptionCompression")} />
        <div className="flex flex-col divide-y rounded-lg border bg-card">
          <div
            role="region"
            aria-label={t("jobs.wizard.destination.encryption")}
            className="flex items-start justify-between gap-4 px-4 py-3.5"
          >
            <div className="flex min-w-0 flex-col gap-0.5">
              <span className="text-sm font-medium">{t("jobs.wizard.destination.encryption")}</span>
              <span className="text-xs text-muted-foreground">{t("jobs.wizard.destination.encryptionHint")}</span>
            </div>
            {selected ? (
              <div className="flex shrink-0 flex-col items-end gap-1">
                <span className="flex items-center gap-1.5 text-sm text-success">
                  <Lock className="size-3.5" />
                  {t("jobs.wizard.destination.encrypted")}
                </span>
                <span className="font-mono text-xs text-muted-foreground">
                  {t("jobs.wizard.destination.fingerprint", { fingerprint: selected.fingerprint })}
                </span>
              </div>
            ) : (
              <span className="shrink-0 text-xs text-muted-foreground">
                {t("jobs.wizard.destination.encryptionPending")}
              </span>
            )}
          </div>
          <div className="flex items-center justify-between gap-4 px-4 py-3.5">
            <div className="flex flex-col gap-0.5">
              <span className="text-sm font-medium">{t("jobs.wizard.destination.compression")}</span>
              <span className="text-xs text-muted-foreground">{t("jobs.wizard.destination.compressionHint")}</span>
            </div>
            <Segmented<Compression>
              label={t("jobs.wizard.destination.compression")}
              value={draft.compression}
              onChange={(compression) => onChange({ compression })}
              options={[
                { value: "none", label: t("jobs.wizard.destination.compressionNone") },
                { value: "gzip", label: "gzip" },
                { value: "zstd", label: "zstd" },
              ]}
            />
          </div>
        </div>
      </section>
    </div>
  );
}
