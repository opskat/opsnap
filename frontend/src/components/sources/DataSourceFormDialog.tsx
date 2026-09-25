import {
  ArrowRight,
  CircleAlert,
  CircleCheck,
  Database,
  Server,
  ShieldAlert,
  TriangleAlert,
  Upload,
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
  DATASOURCE_DEFAULT_PORT,
  MAX_HOPS,
  TLS_MODES,
  createDataSource,
  dataSourceChainPreview,
  dataSourceFormOf,
  emptyDataSourceForm,
  probeDataSource,
  updateDataSource,
  withHostKeyConfirm,
  type ChannelItem,
  type DataSourceForm,
  type DataSourceItem,
  type DataSourceKind,
  type HostKeyPrompt,
  type TLSMode,
} from "@/lib/sources";
import { cn } from "@/lib/utils";

import { HostKeyDialog } from "./HostKeyDialog";

type Field =
  | "name"
  | "host"
  | "port"
  | "username"
  | "password"
  | "privateKey"
  | "passphrase"
  | "tlsCa"
  | "tlsClientCert"
  | "tlsClientKey"
  | "channel";

const fieldOf: Record<number, Field> = {
  [ErrorCode.DataSourceNameInvalid]: "name",
  [ErrorCode.DataSourceNameDuplicate]: "name",
  [ErrorCode.DataSourceHostRequired]: "host",
  [ErrorCode.DataSourcePortInvalid]: "port",
  [ErrorCode.DataSourceUserRequired]: "username",
  [ErrorCode.DataSourcePasswordRequired]: "password",
  [ErrorCode.DataSourcePrivateKeyRequired]: "privateKey",
  [ErrorCode.DataSourcePassphraseMissing]: "passphrase",
  [ErrorCode.DataSourcePassphraseWrong]: "passphrase",
  [ErrorCode.DataSourceKeyInvalid]: "privateKey",
  [ErrorCode.DataSourceCAInvalid]: "tlsCa",
  [ErrorCode.DataSourceClientCertInvalid]: "tlsClientCert",
  [ErrorCode.DataSourceClientKeyInvalid]: "tlsClientKey",
  [ErrorCode.DataSourceChannelNotFound]: "channel",
};

type Result = { kind: "success" | "error"; message: string };

/** 新建或编辑数据源：MySQL / PostgreSQL / 服务器文件，先测试后保存 */
export function DataSourceFormDialog({
  open,
  editing,
  channels,
  onOpenChange,
  onSaved,
  onReconfirm,
}: {
  open: boolean;
  editing?: DataSourceItem;
  /** 通道列表：供“网络通道”选择框与链路预览使用 */
  channels: ChannelItem[];
  onOpenChange: (open: boolean) => void;
  onSaved: (item: DataSourceItem) => void;
  /** 正在编辑的数据源目标主机密钥已变化时，打开「主机密钥已变化」弹窗 */
  onReconfirm?: (item: DataSourceItem) => void;
}) {
  const { t } = useTranslation();
  const initial = (): DataSourceForm => (editing ? dataSourceFormOf(editing) : emptyDataSourceForm());
  const [draft, setDraft] = useState(initial);
  const [fieldErrors, setFieldErrors] = useState<Partial<Record<Field, string>>>({});
  const [result, setResult] = useState<Result>();
  const [busy, setBusy] = useState<"test" | "save">();
  const [hostKeyRequest, setHostKeyRequest] = useState<{ prompt: HostKeyPrompt; resolve: (trust: boolean) => void }>();
  const keyFileInput = useRef<HTMLInputElement>(null);
  const caFileInput = useRef<HTMLInputElement>(null);
  const certFileInput = useRef<HTMLInputElement>(null);
  const clientKeyFileInput = useRef<HTMLInputElement>(null);

  const close = () => {
    setDraft(initial());
    setFieldErrors({});
    setResult(undefined);
    onOpenChange(false);
  };

  const update = (patch: Partial<DataSourceForm>) => {
    setDraft((d) => ({ ...d, ...patch }));
    setResult(undefined);
  };
  const clearFieldError = (field: Field) => setFieldErrors((f) => ({ ...f, [field]: undefined }));

  const setKind = (kind: DataSourceKind) =>
    update({
      kind,
      auth_method: "password",
      password: "",
      private_key: "",
      passphrase: "",
      database: "",
    });

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

  const preview = dataSourceChainPreview(channels, draft.channel_id, draft);

  const test = async () => {
    setBusy("test");
    setFieldErrors({});
    setResult(undefined);
    try {
      const res = await withHostKeyConfirm(
        (hostKey) => probeDataSource({ id: editing?.id, data_source: { ...draft, host_key: hostKey } }),
        askHostKey
      );
      if (!res) return;
      setResult({ kind: "success", message: t("sources.dataSource.result.testSuccess") });
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
            ? updateDataSource(editing.id, { ...draft, host_key: hostKey })
            : createDataSource({ ...draft, host_key: hostKey }),
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

  const readFileInto = (file: File, apply: (text: string) => void) => {
    const reader = new FileReader();
    reader.onload = () => apply(String(reader.result ?? ""));
    reader.readAsText(file);
  };

  return (
    <>
      <Dialog open={open} onOpenChange={(next) => !next && busy === undefined && !hostKeyRequest && close()}>
        <DialogContent className="gap-0 bg-card p-0 sm:max-w-xl">
          <form onSubmit={(e) => void submit(e)} noValidate>
            <DialogHeader className="gap-1.5 border-b px-5 py-4 text-left">
              <DialogTitle>
                {editing ? t("sources.dataSource.form.editTitle") : t("sources.dataSource.form.createTitle")}
              </DialogTitle>
              <DialogDescription>{t("sources.dataSource.form.hint")}</DialogDescription>
            </DialogHeader>
            <div className="flex max-h-[70vh] flex-col gap-4 overflow-y-auto p-5">
              {editing?.status === "host_key_changed" && (
                <p className="flex items-center justify-between gap-3 rounded-md bg-warning-soft px-3 py-2.5 text-sm text-warning">
                  <span className="flex items-center gap-2">
                    <ShieldAlert className="size-4 shrink-0" />
                    {t("sources.dataSource.form.hostKeyChangedHint")}
                  </span>
                  <Button type="button" variant="outline" size="sm" onClick={() => onReconfirm?.(editing)}>
                    {t("sources.dataSource.list.reconfirm")}
                  </Button>
                </p>
              )}
              <KindTabs value={draft.kind} onChange={setKind} />
              <FormField
                label={t("sources.dataSource.form.name")}
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
                  label={t("sources.dataSource.form.host")}
                  value={draft.host}
                  error={fieldErrors.host}
                  mono
                  onChange={(e) => {
                    update({ host: e.target.value });
                    clearFieldError("host");
                  }}
                />
                <FormField
                  label={t("sources.dataSource.form.port")}
                  value={draft.port || ""}
                  placeholder={String(DATASOURCE_DEFAULT_PORT[draft.kind])}
                  error={fieldErrors.port}
                  mono
                  onChange={(e) => {
                    update({ port: Number(e.target.value) || 0 });
                    clearFieldError("port");
                  }}
                />
              </div>
              {draft.kind === "server_file" ? (
                <ServerFileFields
                  draft={draft}
                  editing={editing}
                  fieldErrors={fieldErrors}
                  update={update}
                  clearFieldError={clearFieldError}
                  keyFileInput={keyFileInput}
                  onReadPrivateKey={(file) => readFileInto(file, (text) => update({ private_key: text }))}
                />
              ) : (
                <DatabaseFields
                  draft={draft}
                  editing={editing}
                  fieldErrors={fieldErrors}
                  update={update}
                  clearFieldError={clearFieldError}
                  caFileInput={caFileInput}
                  certFileInput={certFileInput}
                  clientKeyFileInput={clientKeyFileInput}
                  onReadCA={(file) => readFileInto(file, (text) => update({ tls_ca: text }))}
                  onReadClientCert={(file) => readFileInto(file, (text) => update({ tls_client_cert: text }))}
                  onReadClientKey={(file) => readFileInto(file, (text) => update({ tls_client_key: text }))}
                />
              )}
              <div className="flex flex-col gap-1.5">
                <Label className="font-normal text-muted-foreground">{t("sources.dataSource.form.channel")}</Label>
                <Select
                  value={String(draft.channel_id)}
                  onValueChange={(v) => {
                    update({ channel_id: Number(v) });
                    clearFieldError("channel");
                  }}
                >
                  <SelectTrigger
                    aria-label={t("sources.dataSource.form.channel")}
                    aria-invalid={fieldErrors.channel ? true : undefined}
                  >
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="0">{t("sources.dataSource.form.channelNone")}</SelectItem>
                    {channels.map((c) => (
                      <SelectItem key={c.id} value={String(c.id)}>
                        {c.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <p className="text-xs text-muted-foreground">
                {t("sources.dataSource.form.chain", { chain: preview.text })}
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
                {busy === "test" ? t("sources.dataSource.form.testing") : t("sources.dataSource.form.test")}
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

function KindTabs({ value, onChange }: { value: DataSourceKind; onChange: (kind: DataSourceKind) => void }) {
  const { t } = useTranslation();
  const options: { value: DataSourceKind; label: string; icon: ReactNode }[] = [
    { value: "mysql", label: t("sources.dataSource.kind.mysql"), icon: <Database /> },
    { value: "postgres", label: t("sources.dataSource.kind.postgres"), icon: <Database /> },
    { value: "server_file", label: t("sources.dataSource.kind.server_file"), icon: <Server /> },
  ];
  return (
    <div
      role="radiogroup"
      aria-label={t("sources.dataSource.form.kind")}
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
      <span className="ml-1.5 text-xs text-faint-foreground">{t("sources.dataSource.form.optional")}</span>
    </>
  );
}

/** MySQL / PostgreSQL：用户名密码、可选连接数据库、TLS 与 mTLS */
function DatabaseFields({
  draft,
  editing,
  fieldErrors,
  update,
  clearFieldError,
  caFileInput,
  certFileInput,
  clientKeyFileInput,
  onReadCA,
  onReadClientCert,
  onReadClientKey,
}: {
  draft: DataSourceForm;
  editing?: DataSourceItem;
  fieldErrors: Partial<Record<Field, string>>;
  update: (patch: Partial<DataSourceForm>) => void;
  clearFieldError: (field: Field) => void;
  caFileInput: React.RefObject<HTMLInputElement | null>;
  certFileInput: React.RefObject<HTMLInputElement | null>;
  clientKeyFileInput: React.RefObject<HTMLInputElement | null>;
  onReadCA: (file: File) => void;
  onReadClientCert: (file: File) => void;
  onReadClientKey: (file: File) => void;
}) {
  const { t } = useTranslation();
  return (
    <>
      <div className="grid grid-cols-2 gap-3">
        <FormField
          label={t("sources.dataSource.form.username")}
          value={draft.username}
          error={fieldErrors.username}
          autoComplete="off"
          onChange={(e) => {
            update({ username: e.target.value });
            clearFieldError("username");
          }}
        />
        <FormField
          label={t("sources.dataSource.form.password")}
          type="password"
          value={draft.password}
          placeholder={editing?.has_password ? t("sources.dataSource.form.secretKeep") : undefined}
          autoComplete="new-password"
          error={fieldErrors.password}
          onChange={(e) => {
            update({ password: e.target.value });
            clearFieldError("password");
          }}
        />
      </div>
      {draft.kind === "postgres" && (
        <FormField
          label={<Optional label={t("sources.dataSource.form.database")} />}
          value={draft.database}
          placeholder="postgres"
          onChange={(e) => update({ database: e.target.value })}
        />
      )}
      <div className="flex flex-col gap-3 rounded-md border p-3.5">
        <Label className="font-normal text-muted-foreground">{t("sources.dataSource.form.tls")}</Label>
        <Segmented
          label={t("sources.dataSource.form.tls")}
          value={draft.tls_mode}
          options={TLS_MODES.map((m) => ({ value: m, label: t(`sources.dataSource.form.tlsMode.${m}`) }))}
          onChange={(tls_mode: TLSMode) => update({ tls_mode })}
        />
        {(draft.tls_mode === "verify_ca" || draft.tls_mode === "verify_full") && (
          <div className="flex flex-col gap-1.5">
            <div className="flex items-center justify-between">
              <Label className="font-normal text-muted-foreground">
                <Optional label={t("sources.dataSource.form.tlsCA")} />
              </Label>
              <button
                type="button"
                className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
                onClick={() => caFileInput.current?.click()}
              >
                <Upload className="size-3.5" />
                {t("sources.dataSource.form.upload")}
              </button>
              <input
                ref={caFileInput}
                type="file"
                className="hidden"
                onChange={(e) => {
                  const file = e.target.files?.[0];
                  if (file) onReadCA(file);
                  e.target.value = "";
                }}
              />
            </div>
            <textarea
              value={draft.tls_ca}
              aria-label={t("sources.dataSource.form.tlsCA")}
              aria-invalid={fieldErrors.tlsCa ? true : undefined}
              rows={2}
              className={cn(
                "w-full rounded-md border border-input bg-background px-3 py-2 font-mono text-xs shadow-xs outline-none",
                "focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50"
              )}
              onChange={(e) => {
                update({ tls_ca: e.target.value });
                clearFieldError("tlsCa");
              }}
            />
            {fieldErrors.tlsCa ? (
              <p className="text-xs text-destructive">{fieldErrors.tlsCa}</p>
            ) : (
              <p className="text-xs text-faint-foreground">{t("sources.dataSource.form.tlsCAHint")}</p>
            )}
          </div>
        )}
        <div className="grid grid-cols-2 gap-3">
          <div className="flex flex-col gap-1.5">
            <div className="flex items-center justify-between">
              <Label className="font-normal text-muted-foreground">
                <Optional label={t("sources.dataSource.form.tlsClientCert")} />
              </Label>
              <button
                type="button"
                className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
                onClick={() => certFileInput.current?.click()}
              >
                <Upload className="size-3.5" />
                {t("sources.dataSource.form.upload")}
              </button>
              <input
                ref={certFileInput}
                type="file"
                className="hidden"
                onChange={(e) => {
                  const file = e.target.files?.[0];
                  if (file) onReadClientCert(file);
                  e.target.value = "";
                }}
              />
            </div>
            <textarea
              value={draft.tls_client_cert}
              aria-label={t("sources.dataSource.form.tlsClientCert")}
              aria-invalid={fieldErrors.tlsClientCert ? true : undefined}
              rows={2}
              className={cn(
                "w-full rounded-md border border-input bg-background px-3 py-2 font-mono text-xs shadow-xs outline-none",
                "focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50"
              )}
              onChange={(e) => {
                update({ tls_client_cert: e.target.value });
                clearFieldError("tlsClientCert");
              }}
            />
            {fieldErrors.tlsClientCert && <p className="text-xs text-destructive">{fieldErrors.tlsClientCert}</p>}
          </div>
          <div className="flex flex-col gap-1.5">
            <div className="flex items-center justify-between">
              <Label className="font-normal text-muted-foreground">
                <Optional label={t("sources.dataSource.form.tlsClientKey")} />
              </Label>
              <button
                type="button"
                className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
                onClick={() => clientKeyFileInput.current?.click()}
              >
                <Upload className="size-3.5" />
                {t("sources.dataSource.form.upload")}
              </button>
              <input
                ref={clientKeyFileInput}
                type="file"
                className="hidden"
                onChange={(e) => {
                  const file = e.target.files?.[0];
                  if (file) onReadClientKey(file);
                  e.target.value = "";
                }}
              />
            </div>
            <textarea
              value={draft.tls_client_key}
              placeholder={editing?.has_tls_client_key ? t("sources.dataSource.form.secretKeep") : undefined}
              aria-label={t("sources.dataSource.form.tlsClientKey")}
              aria-invalid={fieldErrors.tlsClientKey ? true : undefined}
              rows={2}
              className={cn(
                "w-full rounded-md border border-input bg-background px-3 py-2 font-mono text-xs shadow-xs outline-none",
                "focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50"
              )}
              onChange={(e) => {
                update({ tls_client_key: e.target.value });
                clearFieldError("tlsClientKey");
              }}
            />
            {fieldErrors.tlsClientKey && <p className="text-xs text-destructive">{fieldErrors.tlsClientKey}</p>}
          </div>
        </div>
        <p className="text-xs text-faint-foreground">{t("sources.dataSource.form.tlsClientCertHint")}</p>
      </div>
    </>
  );
}

/** 服务器文件：用户名 + 密码/私钥认证，与 SSH 跳板通道相同 */
function ServerFileFields({
  draft,
  editing,
  fieldErrors,
  update,
  clearFieldError,
  keyFileInput,
  onReadPrivateKey,
}: {
  draft: DataSourceForm;
  editing?: DataSourceItem;
  fieldErrors: Partial<Record<Field, string>>;
  update: (patch: Partial<DataSourceForm>) => void;
  clearFieldError: (field: Field) => void;
  keyFileInput: React.RefObject<HTMLInputElement | null>;
  onReadPrivateKey: (file: File) => void;
}) {
  const { t } = useTranslation();
  return (
    <>
      <FormField
        label={t("sources.dataSource.form.username")}
        value={draft.username}
        error={fieldErrors.username}
        autoComplete="off"
        onChange={(e) => {
          update({ username: e.target.value });
          clearFieldError("username");
        }}
      />
      <div className="flex flex-col gap-1.5">
        <Label className="font-normal text-muted-foreground">{t("sources.dataSource.form.authMethod")}</Label>
        <Segmented
          label={t("sources.dataSource.form.authMethod")}
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
          label={t("sources.dataSource.form.password")}
          type="password"
          value={draft.password}
          placeholder={editing?.has_password ? t("sources.dataSource.form.secretKeep") : undefined}
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
              <Label className="font-normal text-muted-foreground">{t("sources.dataSource.form.privateKey")}</Label>
              <button
                type="button"
                className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
                onClick={() => keyFileInput.current?.click()}
              >
                <Upload className="size-3.5" />
                {t("sources.dataSource.form.upload")}
              </button>
              <input
                ref={keyFileInput}
                type="file"
                className="hidden"
                onChange={(e) => {
                  const file = e.target.files?.[0];
                  if (file) onReadPrivateKey(file);
                  e.target.value = "";
                }}
              />
            </div>
            <textarea
              value={draft.private_key}
              placeholder={editing?.has_private_key ? t("sources.dataSource.form.secretKeep") : undefined}
              aria-label={t("sources.dataSource.form.privateKey")}
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
              <p className="text-xs text-faint-foreground">{t("sources.dataSource.form.privateKeyHint")}</p>
            )}
          </div>
          <FormField
            label={<Optional label={t("sources.dataSource.form.passphrase")} />}
            type="password"
            value={draft.passphrase}
            placeholder={editing?.has_passphrase ? t("sources.dataSource.form.secretKeep") : undefined}
            hint={t("sources.dataSource.form.passphraseHint")}
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
  );
}
