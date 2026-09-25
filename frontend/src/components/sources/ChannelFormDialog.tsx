import {
  ArrowRight,
  CircleAlert,
  CircleCheck,
  ShieldAlert,
  TriangleAlert,
  Terminal,
  Upload,
  Waypoints,
} from "lucide-react";
import { useRef, useState, type FormEvent, type ReactNode } from "react";
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
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { ApiError } from "@/lib/api";
import { ErrorCode } from "@/lib/auth";
import {
  MAX_HOPS,
  availableVia,
  chainPreview,
  channelFormOf,
  createChannel,
  emptyChannelForm,
  probeChannel,
  updateChannel,
  withHostKeyConfirm,
  type ChannelForm,
  type ChannelItem,
  type ChannelKind,
  type HostKeyPrompt,
} from "@/lib/sources";
import { cn } from "@/lib/utils";

import { HostKeyDialog } from "./HostKeyDialog";

type Field = "name" | "host" | "port" | "username" | "password" | "privateKey" | "passphrase" | "via";

const fieldOf: Record<number, Field> = {
  [ErrorCode.ChannelNameInvalid]: "name",
  [ErrorCode.ChannelNameDuplicate]: "name",
  [ErrorCode.ChannelHostRequired]: "host",
  [ErrorCode.ChannelPortInvalid]: "port",
  [ErrorCode.ChannelUserRequired]: "username",
  [ErrorCode.ChannelPasswordRequired]: "password",
  [ErrorCode.ChannelPrivateKeyRequired]: "privateKey",
  [ErrorCode.ChannelSOCKS5CredentialPair]: "password",
  [ErrorCode.ChannelPassphraseMissing]: "passphrase",
  [ErrorCode.ChannelPassphraseWrong]: "passphrase",
  [ErrorCode.ChannelKeyInvalid]: "privateKey",
  [ErrorCode.ChannelViaCycle]: "via",
  [ErrorCode.ChannelChainTooLong]: "via",
};

type Result = { kind: "success" | "error"; message: string };

/** 新建或编辑网络通道：SSH 跳板 / SOCKS5 代理，先测试后保存 */
export function ChannelFormDialog({
  open,
  editing,
  channels,
  onOpenChange,
  onSaved,
  onReconfirm,
}: {
  open: boolean;
  editing?: ChannelItem;
  /** 已有通道列表：供“经由”选择框与链路预览使用 */
  channels: ChannelItem[];
  onOpenChange: (open: boolean) => void;
  onSaved: (item: ChannelItem) => void;
  /** 正在编辑的通道主机密钥已变化时，打开「主机密钥已变化」弹窗 */
  onReconfirm?: (item: ChannelItem) => void;
}) {
  const { t } = useTranslation();
  const initial = (): ChannelForm => (editing ? channelFormOf(editing) : emptyChannelForm());
  const [draft, setDraft] = useState(initial);
  const [fieldErrors, setFieldErrors] = useState<Partial<Record<Field, string>>>({});
  const [result, setResult] = useState<Result>();
  const [busy, setBusy] = useState<"test" | "save">();
  const [hostKeyRequest, setHostKeyRequest] = useState<{ prompt: HostKeyPrompt; resolve: (trust: boolean) => void }>();
  const fileInput = useRef<HTMLInputElement>(null);

  const close = () => {
    setDraft(initial());
    setFieldErrors({});
    setResult(undefined);
    onOpenChange(false);
  };

  const update = (patch: Partial<ChannelForm>) => {
    setDraft((d) => ({ ...d, ...patch }));
    setResult(undefined);
  };
  const clearFieldError = (field: Field) => setFieldErrors((f) => ({ ...f, [field]: undefined }));

  const setKind = (kind: ChannelKind) =>
    update({ kind, auth_method: kind === "ssh" ? "password" : "none", password: "", private_key: "", passphrase: "" });

  const askHostKey = (prompt: HostKeyPrompt) =>
    new Promise<boolean>((resolve) => {
      setHostKeyRequest({ prompt, resolve });
    });
  const trustHostKey = () => {
    hostKeyRequest?.resolve(true);
    setHostKeyRequest(undefined);
  };
  const cancelHostKey = () => {
    hostKeyRequest?.resolve(false);
    setHostKeyRequest(undefined);
  };

  const showError = (err: unknown) => {
    const field = err instanceof ApiError ? fieldOf[err.code] : undefined;
    const message = err instanceof Error ? err.message : String(err);
    if (field) setFieldErrors({ [field]: message });
    else setResult({ kind: "error", message });
  };

  const preview = chainPreview(channels, draft.via_id, draft);

  const test = async () => {
    setBusy("test");
    setFieldErrors({});
    setResult(undefined);
    try {
      const res = await withHostKeyConfirm(
        (hostKey) => probeChannel({ id: editing?.id, channel: { ...draft, host_key: hostKey } }),
        askHostKey
      );
      if (!res) return;
      setResult({ kind: "success", message: t("sources.channel.result.testSuccess") });
    } catch (err) {
      showError(err);
    } finally {
      setBusy(undefined);
    }
  };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (preview.tooLong) return;
    setBusy("save");
    setFieldErrors({});
    setResult(undefined);
    try {
      const res = await withHostKeyConfirm(
        (hostKey) =>
          editing
            ? updateChannel(editing.id, { ...draft, host_key: hostKey })
            : createChannel({ ...draft, host_key: hostKey }),
        askHostKey
      );
      if (!res) return;
      if (res.item) {
        onSaved(res.item);
        close();
      }
    } catch (err) {
      showError(err);
    } finally {
      setBusy(undefined);
    }
  };

  const readPrivateKeyFile = (file: File) => {
    const reader = new FileReader();
    reader.onload = () => update({ private_key: String(reader.result ?? "") });
    reader.readAsText(file);
  };

  const via = availableVia(channels, editing?.id);

  return (
    <>
      <Dialog open={open} onOpenChange={(next) => !next && busy === undefined && !hostKeyRequest && close()}>
        <DialogContent className="gap-0 bg-card p-0 sm:max-w-xl">
          <form onSubmit={(e) => void submit(e)} noValidate>
            <DialogHeader className="gap-1.5 border-b px-5 py-4 text-left">
              <DialogTitle>
                {editing ? t("sources.channel.form.editTitle") : t("sources.channel.form.createTitle")}
              </DialogTitle>
              <DialogDescription>{t("sources.channel.form.hint")}</DialogDescription>
            </DialogHeader>
            <div className="flex max-h-[70vh] flex-col gap-4 overflow-y-auto p-5">
              {editing?.status === "host_key_changed" && (
                <p className="flex items-center justify-between gap-3 rounded-md bg-warning-soft px-3 py-2.5 text-sm text-warning">
                  <span className="flex items-center gap-2">
                    <ShieldAlert className="size-4 shrink-0" />
                    {t("sources.channel.form.hostKeyChangedHint")}
                  </span>
                  <Button type="button" variant="outline" size="sm" onClick={() => onReconfirm?.(editing)}>
                    {t("sources.channel.list.reconfirm")}
                  </Button>
                </p>
              )}
              <KindTabs value={draft.kind} onChange={setKind} />
              <FormField
                label={t("sources.channel.form.name")}
                value={draft.name}
                maxLength={64}
                error={fieldErrors.name}
                onChange={(e) => {
                  update({ name: e.target.value });
                  clearFieldError("name");
                }}
              />
              <div className="grid grid-cols-[1fr_8rem] gap-3">
                <FormField
                  label={t("sources.channel.form.host")}
                  value={draft.host}
                  error={fieldErrors.host}
                  mono
                  onChange={(e) => {
                    update({ host: e.target.value });
                    clearFieldError("host");
                  }}
                />
                <FormField
                  label={t("sources.channel.form.port")}
                  value={draft.port || ""}
                  placeholder={String(draft.kind === "ssh" ? 22 : 1080)}
                  error={fieldErrors.port}
                  mono
                  onChange={(e) => {
                    update({ port: Number(e.target.value) || 0 });
                    clearFieldError("port");
                  }}
                />
              </div>
              <FormField
                label={
                  draft.kind === "ssh" ? (
                    t("sources.channel.form.username")
                  ) : (
                    <Optional label={t("sources.channel.form.username")} />
                  )
                }
                value={draft.username}
                error={fieldErrors.username}
                autoComplete="off"
                onChange={(e) => {
                  update({ username: e.target.value });
                  clearFieldError("username");
                }}
              />
              {draft.kind === "ssh" ? (
                <>
                  <div className="flex flex-col gap-1.5">
                    <Label className="font-normal text-muted-foreground">{t("sources.channel.form.authMethod")}</Label>
                    <Segmented
                      label={t("sources.channel.form.authMethod")}
                      value={draft.auth_method}
                      options={[
                        { value: "password", label: t("sources.channel.auth.password") },
                        { value: "key", label: t("sources.channel.auth.key") },
                      ]}
                      onChange={(auth_method) => update({ auth_method, password: "", private_key: "", passphrase: "" })}
                    />
                  </div>
                  {draft.auth_method === "password" ? (
                    <FormField
                      label={t("sources.channel.form.password")}
                      type="password"
                      value={draft.password}
                      placeholder={editing?.has_password ? t("sources.channel.form.secretKeep") : undefined}
                      autoComplete="new-password"
                      error={fieldErrors.password}
                      onChange={(e) => {
                        update({ password: e.target.value });
                        clearFieldError("password");
                      }}
                    />
                  ) : (
                    <>
                      <div className="flex flex-col gap-1.5">
                        <div className="flex items-center justify-between">
                          <Label className="font-normal text-muted-foreground">
                            {t("sources.channel.form.privateKey")}
                          </Label>
                          <button
                            type="button"
                            className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
                            onClick={() => fileInput.current?.click()}
                          >
                            <Upload className="size-3.5" />
                            {t("sources.channel.form.upload")}
                          </button>
                          <input
                            ref={fileInput}
                            type="file"
                            className="hidden"
                            onChange={(e) => {
                              const file = e.target.files?.[0];
                              if (file) readPrivateKeyFile(file);
                              e.target.value = "";
                            }}
                          />
                        </div>
                        <textarea
                          value={draft.private_key}
                          placeholder={editing?.has_private_key ? t("sources.channel.form.secretKeep") : undefined}
                          aria-label={t("sources.channel.form.privateKey")}
                          aria-invalid={fieldErrors.privateKey ? true : undefined}
                          rows={4}
                          className={cn(
                            "w-full rounded-md border border-input bg-background px-3 py-2 font-mono text-xs shadow-xs outline-none",
                            "focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50"
                          )}
                          onChange={(e) => {
                            update({ private_key: e.target.value });
                            clearFieldError("privateKey");
                          }}
                        />
                        {fieldErrors.privateKey ? (
                          <p className="text-xs text-destructive">{fieldErrors.privateKey}</p>
                        ) : (
                          <p className="text-xs text-faint-foreground">{t("sources.channel.form.privateKeyHint")}</p>
                        )}
                      </div>
                      <FormField
                        label={<Optional label={t("sources.channel.form.passphrase")} />}
                        type="password"
                        value={draft.passphrase}
                        placeholder={editing?.has_passphrase ? t("sources.channel.form.secretKeep") : undefined}
                        hint={t("sources.channel.form.passphraseHint")}
                        autoComplete="new-password"
                        error={fieldErrors.passphrase}
                        onChange={(e) => {
                          update({ passphrase: e.target.value });
                          clearFieldError("passphrase");
                        }}
                      />
                    </>
                  )}
                </>
              ) : (
                <FormField
                  label={<Optional label={t("sources.channel.form.password")} />}
                  type="password"
                  value={draft.password}
                  placeholder={editing?.has_password ? t("sources.channel.form.secretKeep") : undefined}
                  hint={t("sources.channel.form.socks5CredentialHint")}
                  autoComplete="new-password"
                  error={fieldErrors.password}
                  onChange={(e) => {
                    update({ password: e.target.value });
                    clearFieldError("password");
                  }}
                />
              )}
              <div className="flex flex-col gap-1.5">
                <Label className="font-normal text-muted-foreground">{t("sources.channel.form.via")}</Label>
                <Select
                  value={String(draft.via_id)}
                  onValueChange={(v) => {
                    update({ via_id: Number(v) });
                    clearFieldError("via");
                  }}
                >
                  <SelectTrigger
                    aria-label={t("sources.channel.form.via")}
                    aria-invalid={fieldErrors.via ? true : undefined}
                  >
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="0">{t("sources.channel.form.viaNone")}</SelectItem>
                    {via.map((c) => (
                      <SelectItem key={c.id} value={String(c.id)}>
                        {c.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <p className="text-xs text-muted-foreground">
                {t("sources.channel.form.chain", { chain: preview.text })}
              </p>
              {preview.tooLong && (
                <p
                  role="alert"
                  className="flex items-start gap-2 rounded-md bg-destructive-soft px-3 py-2.5 text-sm text-destructive"
                >
                  <TriangleAlert className="mt-0.5 size-4 shrink-0" />
                  {t("sources.channel.form.chainTooLong", { count: preview.hopCount, max: MAX_HOPS })}
                </p>
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
                {busy === "test" ? t("sources.channel.form.testing") : t("sources.channel.form.test")}
              </Button>
              <div className="flex gap-2">
                <Button type="button" variant="outline" disabled={busy === "save"} onClick={close}>
                  {t("common.cancel")}
                </Button>
                <Button type="submit" disabled={busy !== undefined || preview.tooLong}>
                  {busy === "save" ? t("common.submitting") : t("common.save")}
                  {busy !== "save" && <ArrowRight />}
                </Button>
              </div>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      <HostKeyDialog request={hostKeyRequest} onCancel={cancelHostKey} onTrust={trustHostKey} />
    </>
  );
}

function KindTabs({ value, onChange }: { value: ChannelKind; onChange: (kind: ChannelKind) => void }) {
  const { t } = useTranslation();
  const options: { value: ChannelKind; label: string; icon: ReactNode }[] = [
    { value: "ssh", label: t("sources.channel.kind.ssh"), icon: <Terminal /> },
    { value: "socks5", label: t("sources.channel.kind.socks5"), icon: <Waypoints /> },
  ];
  return (
    <div
      role="radiogroup"
      aria-label={t("sources.channel.form.kind")}
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

function Optional({ label }: { label: ReactNode }) {
  const { t } = useTranslation();
  return (
    <>
      {label}
      <span className="ml-1.5 text-xs text-faint-foreground">{t("sources.channel.form.optional")}</span>
    </>
  );
}
