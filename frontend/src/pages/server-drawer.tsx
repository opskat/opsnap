import * as React from "react";
import {
  ArrowDown,
  ArrowUp,
  Check,
  ChevronDown,
  Copy,
  Eye,
  EyeOff,
  FileKey,
  KeyRound,
  Laptop,
  Loader2,
  Plus,
  RefreshCw,
  Search,
  ShieldCheck,
  X,
} from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Popover as PopoverPrimitive } from "radix-ui";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogClose, DialogDescription, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { DrawerContent, Field, FormError, Mono, Segmented, useCopy } from "@/components/bits";
import {
  CREDENTIAL_KIND_LABEL,
  SECRET_MASKED,
  api,
  errorMessage,
  type CredentialItem,
  type HopResult,
  type HopType,
  type ServerAuthType,
  type ServerCredential,
  type ServerItem,
  type ServerTestResult,
} from "@/lib/api";
import {
  AUTH_LABEL,
  HOP_LABEL,
  buildInput,
  emptyForm,
  formFromItem,
  formSignature,
  isTrusted,
  moveHop,
  newHop,
  passwordSaved,
  pickCredential,
  reasonText,
  removeHop,
  switchAuthType,
  switchSource,
  targetAddr,
  targetResult,
  type CredentialSource,
  type HopForm,
  type PickerCredential,
  type ServerForm,
} from "@/lib/servers";
import { cn } from "@/lib/utils";

interface Tested {
  /** 生成这份结果时的表单签名；表单改动后结果作废 */
  sig: string;
  result: ServerTestResult;
}

/** 添加 / 编辑服务器的右侧抽屉。item 为空表示添加。 */
export function ServerDrawer({
  item,
  servers,
  onClose,
  onSaved,
}: {
  item: ServerItem | null;
  /** 已添加的服务器，供选择 SSH 跳板 */
  servers: ServerItem[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const [form, setForm] = React.useState<ServerForm>(() => (item ? formFromItem(item) : emptyForm()));
  const [tested, setTested] = React.useState<Tested | null>(null);
  const [testing, setTesting] = React.useState(false);
  const [saving, setSaving] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [creds, setCreds] = React.useState<CredentialItem[] | null>(null);
  const [credsError, setCredsError] = React.useState<string | null>(null);

  // 「用已保存的」要列出可选的共享密钥；私有凭据不会出现在这里
  React.useEffect(() => {
    let cancelled = false;
    api.credentials
      .list()
      .then((r) => {
        if (!cancelled) setCreds(r.list ?? []);
      })
      .catch((err) => {
        if (!cancelled) setCredsError(errorMessage(err));
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const pickerItems: PickerCredential[] = (creds ?? []).map((c) => ({
    id: c.id,
    name: c.name,
    kind: c.kind,
    username: c.username,
    servers: c.servers,
    hasPassphrase: c.has_passphrase,
  }));
  const selectedShared = selectedCredential(form, pickerItems, item?.credential ?? null);
  const set = (patch: Partial<ServerForm>) => setForm((f) => ({ ...f, ...patch }));
  const setHops = (fn: (h: HopForm[]) => HopForm[]) => setForm((f) => ({ ...f, hops: fn(f.hops) }));
  const setHop = (key: number, patch: Partial<HopForm>) =>
    setHops((hs) => hs.map((h) => (h.key === key ? { ...h, ...patch } : h)));

  const sig = formSignature(form);
  const current = tested && tested.sig === sig ? tested.result : null;
  const trusted = isTrusted(form);
  const hopCount = form.hops.length;
  const target = current ? targetResult(current, hopCount) : undefined;
  const needsConfirm =
    current !== null &&
    !trusted &&
    target?.reason === "fingerprint_unconfirmed" &&
    current.fingerprint !== "" &&
    current.fingerprint === target.fingerprint;

  async function runTest(f: ServerForm) {
    const { input, error: invalid } = buildInput(f);
    setError(invalid ?? null);
    if (!input) return;
    setTesting(true);
    try {
      const result = await api.servers.test(item?.id ?? 0, input);
      setTested({ sig: formSignature(f), result });
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setTesting(false);
    }
  }

  function trust() {
    if (!current) return;
    const next: ServerForm = { ...form, trusted: { fp: current.fingerprint, addr: targetAddr(form) } };
    setForm(next);
    void runTest(next);
  }

  async function save() {
    const { input, error: invalid } = buildInput(form);
    setError(invalid ?? null);
    if (!input) return;
    setSaving(true);
    try {
      if (item) await api.servers.update(item.id, input);
      else await api.servers.create(input);
      toast.success(item ? "服务器已保存" : "服务器已添加");
      onSaved();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setSaving(false);
    }
  }

  const title = item ? "编辑服务器" : "添加服务器";
  const jumpChoices = servers.filter((s) => s.id !== item?.id);
  const resultFor = (index: number) => current?.hops?.find((h) => h.index === index);

  return (
    <Dialog open onOpenChange={(v) => !v && onClose()}>
      <DrawerContent showCloseButton={false}>
        <div className="flex items-center justify-between border-b border-line px-6 py-4">
          <DialogTitle className="text-[17px] font-semibold">{title}</DialogTitle>
          <DialogDescription className="sr-only">
            填写 SSH 连接信息、凭据来源和代理链；直接填写的凭据只用于这台服务器，也可以顺手存成一条密钥
          </DialogDescription>
          <DialogClose asChild>
            <Button variant="ghost" size="icon-sm" aria-label="关闭">
              <X />
            </Button>
          </DialogClose>
        </div>

        <div className="flex-1 overflow-y-auto px-6 py-6">
          <section className="grid grid-cols-[1fr_96px] gap-3">
            <Field label="名称" className="col-span-2">
              <Input value={form.name} onChange={(e) => set({ name: e.target.value })} className="bg-surface" />
            </Field>
            <Field label="地址">
              <Input
                value={form.host}
                onChange={(e) => set({ host: e.target.value })}
                className="bg-surface font-mono"
                autoComplete="off"
                spellCheck={false}
              />
            </Field>
            <Field label="端口">
              <Input
                value={form.port}
                onChange={(e) => set({ port: e.target.value })}
                inputMode="numeric"
                className="bg-surface font-mono"
              />
            </Field>
            <Field label="用户" className="col-span-2">
              <Input
                value={form.username}
                onChange={(e) => set({ username: e.target.value })}
                className="bg-surface"
                autoComplete="off"
              />
            </Field>
          </section>

          <section className="mt-8">
            <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
              <h3 className="text-[14px] font-semibold">认证</h3>
              <Segmented<CredentialSource>
                label="凭据来源"
                value={form.source}
                onChange={(source) => setForm((f) => switchSource(f, source))}
                options={[
                  { value: "saved", label: "用已保存的" },
                  { value: "inline", label: "直接填写" },
                ]}
              />
            </div>
            {form.source === "inline" && <p className="mb-3 text-xs text-ink-3">类型决定这次连接用密码还是私钥</p>}
            {form.source === "saved" ? (
              <SavedCredential
                items={pickerItems}
                loading={creds === null && credsError === null}
                error={credsError}
                selected={selectedShared}
                onSelect={(c) => setForm((f) => pickCredential(f, c))}
                onInline={() => setForm((f) => switchSource(f, "inline"))}
              />
            ) : (
              <InlineCredential
                form={form}
                onPatch={set}
                onSwitchType={(authType) => setForm((f) => switchAuthType(f, authType))}
              />
            )}
          </section>

          <section className="mt-8">
            <div className="mb-3 flex items-center justify-between">
              <h3 className="text-[14px] font-semibold">代理链</h3>
              <span className="text-xs text-ink-3">按顺序逐跳连接</span>
            </div>
            <ol className="relative">
              <ChainNode icon={<Laptop className="size-3.5" />} title="opsnap" sub="本机" />
              {form.hops.map((h, i) => (
                <li key={h.key} aria-label={`第 ${i + 1} 跳`} className="relative pb-3 pl-9">
                  <span className="absolute top-0 bottom-0 left-[13px] w-px bg-line-strong" />
                  <span className="absolute top-4 left-[7px] flex size-[13px] items-center justify-center rounded-full border-2 border-brand bg-paper" />
                  <div className="rounded-lg border border-line-strong bg-surface p-3">
                    <div className="flex items-center gap-2">
                      <span className="w-5 text-xs text-ink-3 num">{i + 1}</span>
                      <Select
                        value={h.type}
                        onValueChange={(type) => setHop(h.key, { type: type as HopType, serverId: 0, addr: "" })}
                      >
                        <SelectTrigger className="h-8 w-32 bg-surface" aria-label={`第 ${i + 1} 跳类型`}>
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          {(Object.keys(HOP_LABEL) as HopType[]).map((t) => (
                            <SelectItem key={t} value={t}>
                              {HOP_LABEL[t]}
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                      {h.type === "ssh" ? (
                        <Select
                          value={h.serverId ? String(h.serverId) : ""}
                          onValueChange={(v) => setHop(h.key, { serverId: Number(v) })}
                        >
                          <SelectTrigger className="h-8 flex-1 bg-surface" aria-label={`第 ${i + 1} 跳服务器`}>
                            <SelectValue placeholder="选择服务器" />
                          </SelectTrigger>
                          <SelectContent>
                            {jumpChoices.map((s) => (
                              <SelectItem key={s.id} value={String(s.id)}>
                                {s.name}
                              </SelectItem>
                            ))}
                          </SelectContent>
                        </Select>
                      ) : (
                        <Input
                          value={h.addr}
                          onChange={(e) => setHop(h.key, { addr: e.target.value })}
                          placeholder="host:port"
                          className="h-8 flex-1 bg-surface font-mono text-[13px]"
                          aria-label={`第 ${i + 1} 跳地址`}
                          spellCheck={false}
                        />
                      )}
                      <div className="flex">
                        <Button
                          variant="ghost"
                          size="icon-xs"
                          disabled={i === 0}
                          onClick={() => setHops((hs) => moveHop(hs, i, -1))}
                          aria-label={`第 ${i + 1} 跳上移`}
                        >
                          <ArrowUp />
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon-xs"
                          disabled={i === hopCount - 1}
                          onClick={() => setHops((hs) => moveHop(hs, i, 1))}
                          aria-label={`第 ${i + 1} 跳下移`}
                        >
                          <ArrowDown />
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon-xs"
                          onClick={() => setHops((hs) => removeHop(hs, i))}
                          aria-label={`第 ${i + 1} 跳移除`}
                        >
                          <X />
                        </Button>
                      </div>
                    </div>
                    {h.type !== "ssh" && (
                      <div className="mt-2 grid grid-cols-2 gap-2 pl-7">
                        <Input
                          value={h.username}
                          onChange={(e) => setHop(h.key, { username: e.target.value })}
                          placeholder="代理用户（可选）"
                          aria-label={`第 ${i + 1} 跳代理用户`}
                          autoComplete="off"
                          className="h-8 bg-surface text-[13px]"
                        />
                        <Input
                          type="password"
                          value={h.password}
                          onChange={(e) => setHop(h.key, { password: e.target.value })}
                          placeholder={h.savedPassword !== null ? `${SECRET_MASKED}，留空保持不变` : "代理密码（可选）"}
                          aria-label={`第 ${i + 1} 跳代理密码`}
                          autoComplete="new-password"
                          className="h-8 bg-surface text-[13px]"
                        />
                      </div>
                    )}
                    {current && <HopOutcome hop={h} result={resultFor(i + 1)} />}
                  </div>
                </li>
              ))}
              <li className="relative pb-3 pl-9">
                <span className="absolute top-0 bottom-0 left-[13px] w-px bg-line-strong" />
                <Button
                  variant="ghost"
                  size="sm"
                  className="text-brand-ink"
                  onClick={() => setHops((hs) => [...hs, newHop()])}
                >
                  <Plus />
                  添加一跳
                </Button>
              </li>
              <ChainNode
                icon={<KeyRound className="size-3.5" />}
                title={form.name.trim() || "目标主机"}
                sub={form.host.trim() ? targetAddr(form) : "—"}
                label="目标主机"
                last
                status={current && <Outcome result={target} confirmed={trusted} />}
              />
            </ol>
          </section>

          {needsConfirm && target && (
            <FingerprintCheck name={form.name.trim() || "目标主机"} fp={target.fingerprint} onTrust={trust} />
          )}
        </div>

        <div className="border-t border-line bg-surface px-6 py-4">
          <FormError className="mb-3">{error}</FormError>
          <div className="flex items-center gap-2">
            <Button variant="outline" onClick={() => void runTest(form)} disabled={testing || saving}>
              {testing ? <Loader2 className="animate-spin" /> : <RefreshCw />}
              测试连接
            </Button>
            <DialogClose asChild>
              <Button variant="ghost" className="ml-auto">
                取消
              </Button>
            </DialogClose>
            <Button onClick={() => void save()} disabled={!trusted || testing || saving}>
              保存
            </Button>
          </div>
          {!trusted && <p className="mt-2 text-right text-xs text-ink-3">测试连接并核对主机指纹后才能保存</p>}
        </div>
      </DrawerContent>
    </Dialog>
  );
}

/** 选中的共享密钥：优先看列表里的那一项，列表还没读到或已经不在列表里时用服务器上的凭据兜底 */
function selectedCredential(
  f: ServerForm,
  items: PickerCredential[],
  view: ServerCredential | null
): PickerCredential | null {
  if (f.source !== "saved" || !f.credentialId) return null;
  const found = items.find((c) => c.id === f.credentialId);
  if (found) return found;
  if (view && view.shared && view.id === f.credentialId) {
    return {
      id: view.id,
      name: view.name,
      kind: view.kind,
      username: view.username,
      servers: [],
      hasPassphrase: view.has_passphrase,
    };
  }
  return { id: f.credentialId, name: `密钥 ${f.credentialId}`, kind: f.authType, username: "", servers: [] };
}

/** 密钥类型图标 */
function CredentialIcon({ kind }: { kind: PickerCredential["kind"] }) {
  return (
    <span className="flex size-7 shrink-0 items-center justify-center rounded-md bg-sunken text-ink-2" aria-hidden>
      {kind === "ssh_key" ? <FileKey className="size-4" /> : <KeyRound className="size-4" />}
    </span>
  );
}

function KindTag({ kind }: { kind: PickerCredential["kind"] }) {
  return (
    <span className="rounded bg-sunken px-1.5 py-0.5 text-[11px] whitespace-nowrap text-ink-2">
      {CREDENTIAL_KIND_LABEL[kind]}
    </span>
  );
}

/** 使用某条密钥的服务器数量 */
function usageText(c: PickerCredential): string {
  const n = c.servers?.length ?? 0;
  return n ? `${n} 台在用` : "未使用";
}

/**
 * 「用已保存的」：可搜索的共享密钥组合框（Popover + 输入框 + 列表）。
 * 键盘：输入过滤，↓/↑ 移动，Enter 选中，Esc 关闭，底部固定「直接填写…」。
 */
function SavedCredential({
  items,
  loading,
  error,
  selected,
  onSelect,
  onInline,
}: {
  items: PickerCredential[];
  loading: boolean;
  error: string | null;
  selected: PickerCredential | null;
  onSelect: (c: PickerCredential) => void;
  onInline: () => void;
}) {
  const [open, setOpen] = React.useState(false);
  const [query, setQuery] = React.useState("");
  const [active, setActive] = React.useState(0);
  const inputRef = React.useRef<HTMLInputElement>(null);
  const listRef = React.useRef<HTMLUListElement>(null);
  const labelId = React.useId();

  const q = query.trim().toLowerCase();
  const shown = items.filter((c) => !q || c.name.toLowerCase().includes(q) || c.username.toLowerCase().includes(q));

  function openPicker() {
    setQuery("");
    setActive(0);
    setOpen(true);
  }

  function choose(c: PickerCredential) {
    onSelect(c);
    setOpen(false);
  }

  function onKeyDown(e: React.KeyboardEvent<HTMLInputElement>) {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setActive((i) => Math.min(i + 1, Math.max(shown.length - 1, 0)));
      return;
    }
    if (e.key === "ArrowUp") {
      e.preventDefault();
      setActive((i) => Math.max(i - 1, 0));
      return;
    }
    if (e.key === "Enter") {
      e.preventDefault();
      const c = shown[active];
      if (c) choose(c);
      return;
    }
    if (e.key === "Escape") {
      e.preventDefault();
      setOpen(false);
      return;
    }
  }

  React.useEffect(() => {
    if (!open) return;
    const el = listRef.current?.children[active] as HTMLElement | undefined;
    el?.scrollIntoView({ block: "nearest" });
  }, [open, active]);

  return (
    <div className="grid gap-2">
      <span id={labelId} className="text-[13px] font-medium text-ink">
        密码 / 私钥
      </span>
      <PopoverPrimitive.Root open={open} onOpenChange={(v) => (v ? openPicker() : setOpen(false))}>
        <PopoverPrimitive.Trigger asChild>
          <button
            type="button"
            aria-label="选择密钥"
            aria-describedby={labelId}
            aria-haspopup="listbox"
            aria-expanded={open}
            onClick={() => (open ? setOpen(false) : openPicker())}
            className={cn(
              "flex w-full items-center gap-2.5 rounded-lg border border-line-strong bg-surface px-3 py-2 text-left hover:border-ink-3",
              "focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-none"
            )}
          >
            {selected ? (
              <>
                <CredentialIcon kind={selected.kind} />
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-[13.5px] font-medium">{selected.name}</span>
                </span>
                <KindTag kind={selected.kind} />
              </>
            ) : (
              <>
                <CredentialIcon kind="password" />
                <span className="flex-1 text-[13.5px] text-ink-3">选择密码或私钥</span>
              </>
            )}
            <ChevronDown
              className={cn("size-4 shrink-0 text-ink-3 transition-transform", open && "rotate-180")}
              aria-hidden
            />
          </button>
        </PopoverPrimitive.Trigger>
        <PopoverPrimitive.Content
          align="start"
          sideOffset={6}
          className="z-50 w-[--radix-popover-trigger-width] min-w-[320px] overflow-hidden rounded-lg border border-line bg-surface p-0 shadow-lg"
          onOpenAutoFocus={(e) => {
            // 焦点交给搜索框，不用 Popover 默认的第一项
            e.preventDefault();
            inputRef.current?.focus();
          }}
        >
          <div className="relative border-b border-line p-2">
            <Search
              className="pointer-events-none absolute top-1/2 left-4 size-4 -translate-y-1/2 text-ink-3"
              aria-hidden
            />
            <Input
              ref={inputRef}
              value={query}
              onChange={(e) => {
                setQuery(e.target.value);
                setActive(0);
              }}
              onKeyDown={onKeyDown}
              placeholder="搜索名称、用户"
              aria-label="搜索密钥"
              role="combobox"
              aria-expanded
              aria-controls="credential-options"
              aria-autocomplete="list"
              className="h-9 bg-surface pl-9"
              autoComplete="off"
            />
          </div>
          <ul
            id="credential-options"
            ref={listRef}
            role="listbox"
            aria-label="密钥"
            className="max-h-64 overflow-y-auto py-1"
          >
            {shown.map((c, i) => (
              <li key={c.id} role="option" aria-selected={i === active}>
                <button
                  type="button"
                  onClick={() => choose(c)}
                  onMouseEnter={() => setActive(i)}
                  className={cn("flex w-full items-center gap-2.5 px-3 py-2 text-left", i === active && "bg-sunken")}
                >
                  <CredentialIcon kind={c.kind} />
                  <span className="min-w-0 flex-1">
                    <span className="flex items-center gap-1.5">
                      <span className="truncate text-[13.5px] font-medium">{c.name}</span>
                      <KindTag kind={c.kind} />
                    </span>
                    {c.username && <span className="block truncate text-xs text-ink-3">{c.username}</span>}
                  </span>
                  <span className={cn("text-xs whitespace-nowrap", c.servers?.length ? "text-ink-2" : "text-ink-3")}>
                    {usageText(c)}
                  </span>
                </button>
              </li>
            ))}
            {!shown.length && (
              <li className="px-3 py-6 text-center text-[13px] text-ink-2">
                {loading
                  ? "正在读取密钥"
                  : error
                    ? `读取密钥失败：${error}`
                    : items.length
                      ? `没有匹配「${query.trim()}」的密钥`
                      : "还没有密钥"}
              </li>
            )}
          </ul>
          <div className="border-t border-line">
            <button
              type="button"
              onClick={() => {
                setOpen(false);
                onInline();
              }}
              className="flex w-full items-center gap-2 px-3 py-2.5 text-left text-[13px] text-brand-ink hover:bg-sunken"
            >
              <Plus className="size-4" aria-hidden />
              直接填写…
            </button>
          </div>
        </PopoverPrimitive.Content>
      </PopoverPrimitive.Root>
      {selected && (
        <p className="text-xs text-ink-3">
          {[
            selected.username,
            selected.kind === "ssh_key" ? (selected.hasPassphrase ? "私钥带口令" : "私钥没有口令") : "密码",
            selected.servers?.length ? `还有 ${selected.servers.length} 台服务器用它` : "没有别的服务器用它",
          ]
            .filter(Boolean)
            .join(" · ")}
        </p>
      )}
    </div>
  );
}

/** 「直接填写」：类型、密码或私钥，以及「同时存成密钥」 */
function InlineCredential({
  form,
  onPatch,
  onSwitchType,
}: {
  form: ServerForm;
  onPatch: (patch: Partial<ServerForm>) => void;
  onSwitchType: (authType: ServerAuthType) => void;
}) {
  const [reveal, setReveal] = React.useState(false);
  const [revealPassphrase, setRevealPassphrase] = React.useState(false);
  const isKey = form.authType === "ssh_key";
  const keep = `${SECRET_MASKED}，留空保持不变`;
  // 控件包在用来放显示/隐藏按钮的容器里，Field 无法自动关联，这里显式给 id
  const passwordId = React.useId();
  const passphraseId = React.useId();

  return (
    <div className="grid gap-3">
      <div className="flex justify-end">
        <Segmented<ServerAuthType>
          label="认证方式"
          value={form.authType}
          onChange={onSwitchType}
          options={(Object.keys(AUTH_LABEL) as ServerAuthType[]).map((t) => ({ value: t, label: AUTH_LABEL[t] }))}
        />
      </div>
      {isKey ? (
        <>
          <Field label="私钥" hint="OpenSSH 或 PEM 格式">
            <Textarea
              rows={4}
              value={form.privateKey}
              onChange={(e) => onPatch({ privateKey: e.target.value })}
              placeholder={form.saved.privateKey ? keep : "-----BEGIN OPENSSH PRIVATE KEY-----"}
              spellCheck={false}
              className="bg-surface font-mono text-xs"
            />
          </Field>
          <Field label="私钥口令" hint="没有就留空" htmlFor={passphraseId}>
            <div className="relative">
              <Input
                id={passphraseId}
                type={revealPassphrase ? "text" : "password"}
                value={form.passphrase}
                onChange={(e) => onPatch({ passphrase: e.target.value })}
                placeholder={form.saved.passphrase ? keep : undefined}
                autoComplete="new-password"
                className="bg-surface pr-10"
              />
              <Button
                type="button"
                variant="ghost"
                size="icon-xs"
                className="absolute top-1/2 right-1 -translate-y-1/2"
                aria-label={revealPassphrase ? "隐藏私钥口令" : "显示私钥口令"}
                onClick={() => setRevealPassphrase((v) => !v)}
              >
                {revealPassphrase ? <EyeOff /> : <Eye />}
              </Button>
            </div>
          </Field>
        </>
      ) : (
        <Field label="密码" htmlFor={passwordId}>
          <div className="relative">
            <Input
              id={passwordId}
              type={reveal ? "text" : "password"}
              value={form.password}
              onChange={(e) => onPatch({ password: e.target.value })}
              placeholder={passwordSaved(form) ? keep : undefined}
              autoComplete="new-password"
              className="bg-surface pr-10"
            />
            <Button
              type="button"
              variant="ghost"
              size="icon-xs"
              className="absolute top-1/2 right-1 -translate-y-1/2"
              aria-label={reveal ? "隐藏密码" : "显示密码"}
              onClick={() => setReveal((v) => !v)}
            >
              {reveal ? <EyeOff /> : <Eye />}
            </Button>
          </div>
        </Field>
      )}

      <div className="rounded-lg border border-line bg-surface p-3">
        <label className="flex items-start gap-2.5">
          <Checkbox
            checked={form.share}
            onCheckedChange={(v) =>
              // 默认填服务器名；已经写过的名称不动
              onPatch({ share: v === true, shareAs: v === true ? form.shareAs || form.name.trim() : "" })
            }
            aria-label="同时存成密钥"
            className="mt-0.5"
          />
          <span className="min-w-0">
            <span className="block text-[13px] font-medium">同时存成密钥</span>
            <span className="mt-0.5 block text-xs text-ink-3">
              勾选后这份凭据进密钥库，别的服务器也能选它；不勾选只属于这台服务器，在「密钥」页看不到。
            </span>
          </span>
        </label>
        {form.share && (
          <div className="mt-3 pl-6">
            <Field label="密钥名称" hint="重名会自动加序号，之后可以在「密钥」页改名">
              <Input
                value={form.shareAs}
                onChange={(e) => onPatch({ shareAs: e.target.value })}
                placeholder={form.name.trim()}
                className="bg-surface"
                autoComplete="off"
              />
            </Field>
          </div>
        )}
      </div>
    </div>
  );
}

function ChainNode({
  icon,
  title,
  sub,
  label,
  last,
  status,
}: {
  icon: React.ReactNode;
  title: string;
  sub: string;
  label?: string;
  last?: boolean;
  status?: React.ReactNode;
}) {
  return (
    <li aria-label={label} className={cn("relative flex items-start gap-3 pl-0", !last && "pb-3")}>
      {!last && <span className="absolute top-7 bottom-0 left-[13px] w-px bg-line-strong" />}
      <span className="relative flex size-7 shrink-0 items-center justify-center rounded-full bg-ink text-paper">
        {icon}
      </span>
      <span className="min-w-0 pt-0.5">
        <span className="block text-[13.5px] font-medium">{title}</span>
        <span className="block font-mono text-xs break-all text-ink-3">{sub}</span>
        {status}
      </span>
    </li>
  );
}

/** 代理链中间某一跳的测试结果；没有结果说明前面已经失败，这一跳没有尝试 */
function HopOutcome({ hop, result }: { hop: HopForm; result: HopResult | undefined }) {
  return (
    <div className="mt-2 pl-7">
      <Outcome result={result} okText={hop.type === "ssh" ? "已登录，指纹已信任" : "已连通"} />
    </div>
  );
}

function Outcome({
  result,
  okText = "已登录，指纹已信任",
  confirmed,
}: {
  result?: HopResult;
  okText?: string;
  confirmed?: boolean;
}) {
  if (!result) return <span className="block text-xs text-ink-3">未尝试</span>;
  if (result.ok) {
    return (
      <span className="flex items-center gap-1.5 text-xs text-ok">
        <Check className="size-3.5" />
        {confirmed === false ? "已登录" : okText}
        <span className="text-ink-3 num">{result.latency_ms} ms</span>
      </span>
    );
  }
  const unconfirmed = result.reason === "fingerprint_unconfirmed";
  return (
    <span className={cn("block text-xs", unconfirmed ? "text-warn" : "text-fail")}>
      <span className="font-medium">{reasonText(result)}</span>
      {!unconfirmed && result.message && <span className="ml-1.5 text-ink-3">{result.message}</span>}
    </span>
  );
}

function FingerprintCheck({ name, fp, onTrust }: { name: string; fp: string; onTrust: () => void }) {
  const [copied, copy] = useCopy();
  return (
    <section className="mt-6 rounded-lg border border-warn-fill/50 bg-warn-wash p-4">
      <h3 className="text-[14px] font-semibold">核对 {name} 的主机指纹</h3>
      <p className="mt-1 text-[13px] text-ink-2">
        在服务器上运行 <Mono>ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub</Mono>，结果应一致。
      </p>
      <div className="mt-3 flex items-center gap-2 rounded-md bg-surface px-3 py-2">
        <Mono className="flex-1 text-[12.5px] break-all select-text">{fp}</Mono>
        <Button variant="ghost" size="icon-xs" aria-label="复制指纹" onClick={() => void copy(fp)}>
          {copied ? <Check /> : <Copy />}
        </Button>
      </div>
      <div className="mt-3 flex justify-end">
        <Button size="sm" onClick={onTrust}>
          <ShieldCheck />
          一致，信任
        </Button>
      </div>
    </section>
  );
}
