// 密钥页：维护可复用的服务器登录密码与私钥（共享密钥）。
// 密码、私钥与口令加密后保存在服务端，界面只显示名称、类型、默认用户和「有没有口令」；
// 公钥是明文，列表与抽屉都能直接看到、复制、下载 .pub。
import * as React from "react";
import {
  AlertTriangle,
  Check,
  Copy,
  Download,
  Eye,
  EyeOff,
  FileKey,
  Lock,
  Pencil,
  Plus,
  RefreshCw,
  Search,
  Trash2,
  X,
} from "lucide-react";
import { toast } from "sonner";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Dialog, DialogClose, DialogDescription, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { DrawerContent, Field, FormError, Mono, PageTitle, Segmented, Sheet, Skeleton } from "@/components/bits";
import { SECRET_MASKED, api, errorMessage, CREDENTIAL_KIND_LABEL, type CredentialItem } from "@/lib/api";
import {
  PUBLIC_KEY_HINT,
  PUBLIC_KEY_SOURCE,
  buildInput,
  canDelete,
  deleteBlockedReason,
  emptyForm,
  fingerprintOfPublicKey,
  formFromItem,
  publicKeyFilename,
  publicKeyView,
  revealText,
  secretPreview,
  switchKind,
  serverLabel,
  usedServers,
  writeClipboard,
  type CredentialForm,
} from "@/lib/credentials";
import { downloadText } from "@/lib/download";
import { fmtDate } from "@/lib/format";
import { useLoad } from "@/lib/use-load";
import { cn } from "@/lib/utils";
import { LoadError } from "./settings";

/** 抽屉的状态：null 关闭，"new" 添加，否则编辑这一条 */
type DrawerState = null | "new" | CredentialItem;

export function CredentialsPage() {
  const load = React.useCallback(async () => (await api.credentials.list()).list ?? [], []);
  const { data: items, error, loading, reload } = useLoad(load);
  const [drawer, setDrawer] = React.useState<DrawerState>(null);
  const [deleting, setDeleting] = React.useState<CredentialItem | null>(null);
  const [query, setQuery] = React.useState("");

  const q = query.trim().toLowerCase();
  const shown = (items ?? []).filter(
    (c) => !q || c.name.toLowerCase().includes(q) || c.username.toLowerCase().includes(q)
  );

  const add = (
    <Button onClick={() => setDrawer("new")}>
      <Plus />
      添加密钥
    </Button>
  );

  if (error && !items) {
    return (
      <>
        <PageTitle title="密钥" lead={LEAD} actions={add} />
        <LoadError what="密钥" message={error} onRetry={reload} />
      </>
    );
  }

  if (!items || (loading && !items.length)) {
    return (
      <>
        <PageTitle title="密钥" lead={LEAD} actions={add} />
        <div className="grid gap-3" aria-busy="true" aria-label="正在读取密钥">
          <Skeleton className="h-20" />
          <Skeleton className="h-20" />
        </div>
      </>
    );
  }

  return (
    <>
      <PageTitle title="密钥" lead={LEAD} actions={add} />

      {items.length === 0 ? (
        <Sheet className="px-5 py-10 text-center text-[13.5px] text-ink-2">
          还没有密钥。添加后可以在服务器里选用。
        </Sheet>
      ) : (
        <>
          <div className="mb-4 flex flex-wrap items-end justify-between gap-3">
            <div className="relative w-full sm:w-80">
              <Search
                className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-ink-3"
                aria-hidden
              />
              <Input
                type="search"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder="搜索名称、用户"
                aria-label="搜索密钥"
                className="h-9 bg-surface pl-9"
              />
            </div>
            <span className="text-[13px] text-ink-3 num">共 {shown.length} 条</span>
          </div>

          {shown.length === 0 ? (
            <Sheet className="px-5 py-10 text-center text-[13.5px] text-ink-2">
              没有匹配「{query.trim()}」的密钥。
            </Sheet>
          ) : (
            <>
              <Sheet className="overflow-hidden">
                <div className={cn("hidden gap-6 border-b border-line px-5 py-2.5 text-xs text-ink-3 lg:grid", COLS)}>
                  <span>名称</span>
                  <span>类型 / 用户</span>
                  <span>公钥 / 密码</span>
                  <span>被这些服务器使用</span>
                  <span>最近修改</span>
                  <span className="w-[68px]" />
                </div>
                <ul className="divide-y divide-line">
                  {shown.map((c) => (
                    <CredentialRow key={c.id} item={c} onEdit={() => setDrawer(c)} onDelete={() => setDeleting(c)} />
                  ))}
                </ul>
              </Sheet>
              <p className="mt-3 text-xs text-ink-3">
                公钥是明文，可以直接复制到目标机的 authorized_keys；密码和私钥只能复制，界面上看不到内容。
              </p>
            </>
          )}
        </>
      )}

      {drawer && (
        <CredentialDialog
          key={drawer === "new" ? "new" : drawer.id}
          item={drawer === "new" ? null : drawer}
          onClose={() => setDrawer(null)}
          onSaved={() => {
            setDrawer(null);
            reload();
          }}
        />
      )}
      <DeleteDialog
        item={deleting}
        onClose={() => setDeleting(null)}
        onDeleted={() => {
          setDeleting(null);
          reload();
        }}
      />
    </>
  );
}

const LEAD = "服务器登录用的密码和私钥。只在这里维护一份，添加或编辑服务器时直接选用。";
const COLS =
  "lg:grid-cols-[minmax(170px,1.1fr)_minmax(130px,0.8fr)_minmax(190px,1.2fr)_minmax(150px,0.9fr)_100px_68px]";

function CredentialRow({
  item: c,
  onEdit,
  onDelete,
}: {
  item: CredentialItem;
  onEdit: () => void;
  onDelete: () => void;
}) {
  const used = usedServers(c);
  const blocked = deleteBlockedReason(c);
  const preview = secretPreview(c);
  const isKey = c.kind === "ssh_key";
  return (
    <li className={cn("grid grid-cols-[1fr_auto] items-center gap-x-6 gap-y-2 px-5 py-3.5", COLS)}>
      <div className="col-start-1 row-start-1 flex min-w-0 items-start gap-3">
        <span className="mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-md border border-line bg-sunken text-ink-2">
          {isKey ? <FileKey className="size-4" aria-hidden /> : <Lock className="size-4" aria-hidden />}
        </span>
        <span className="min-w-0">
          <span className="block truncate font-semibold">{c.name}</span>
          <span className="block truncate text-xs text-ink-3">{c.note || "—"}</span>
        </span>
      </div>

      <div className="col-span-2 row-start-2 flex flex-wrap items-center gap-2 text-[13px] lg:col-span-1 lg:row-start-auto">
        <span className="rounded-md border border-line-strong px-1.5 text-[11px] text-ink-2">
          {CREDENTIAL_KIND_LABEL[c.kind]}
        </span>
        <span className={cn("truncate", c.username ? "" : "text-ink-3")}>{c.username || "—"}</span>
        {isKey && c.has_passphrase && (
          <span className="rounded-md bg-sunken px-1.5 text-[11px] text-ink-2">带口令</span>
        )}
      </div>

      {/* 公钥 / 密码：私钥直接显示公钥明文，密码只显示掩码；都有复制按钮 */}
      <div className="col-span-2 row-start-3 flex min-w-0 items-center gap-1.5 text-[13px] lg:col-span-1 lg:row-start-auto">
        {preview === null ? (
          <span className="text-ink-3">—</span>
        ) : isKey ? (
          <Mono className="min-w-0 flex-1 truncate text-ink-2" title={preview}>
            {preview}
          </Mono>
        ) : (
          <span className="min-w-0 flex-1 truncate text-ink-2">{preview}</span>
        )}
        <CredentialCopyButton item={c} disabled={preview === null} />
      </div>

      <div className="col-span-2 row-start-4 min-w-0 text-[13px] lg:col-span-1 lg:row-start-auto">
        {used.total === 0 ? (
          <span className="text-ink-3">没有共享它的服务器</span>
        ) : (
          <span className="flex flex-wrap items-center gap-1.5">
            {used.names.map((n) => (
              <span key={n} className="rounded-md bg-sunken px-1.5 text-[12px] text-ink-2">
                {n}
              </span>
            ))}
            {used.more > 0 && <span className="text-xs text-ink-3">等 {used.more} 台</span>}
          </span>
        )}
      </div>

      <div className="col-span-2 row-start-5 text-[13px] text-ink-2 num lg:col-span-1 lg:row-start-auto">
        {fmtDate(c.updated_at)}
      </div>

      <div className="col-start-2 row-start-1 flex justify-end gap-1 lg:col-start-auto lg:row-start-auto">
        <Button variant="ghost" size="icon-sm" aria-label={`编辑 ${c.name}`} title="编辑" onClick={onEdit}>
          <Pencil />
        </Button>
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label={`删除 ${c.name}`}
          title={blocked ?? "删除"}
          onClick={onDelete}
        >
          <Trash2 />
        </Button>
      </div>
    </li>
  );
}

/**
 * 列表里「公钥 / 密码」的复制按钮。
 * 私钥的公钥是明文，直接复制；密码要先向服务端要一次明文（reveal）。
 * 复制失败不静默：用 toast 写明原因（例如浏览器不允许写剪贴板）。
 */
function CredentialCopyButton({ item: c, disabled }: { item: CredentialItem; disabled: boolean }) {
  const [state, setState] = React.useState<"idle" | "copied">("idle");
  const timer = React.useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  React.useEffect(() => () => clearTimeout(timer.current), []);

  // 私钥复制的是公钥（明文），密码复制的是密码（要 reveal）
  const what = c.kind === "ssh_key" ? "公钥" : "密码";
  const label = `复制 ${c.name} 的${what}`;

  async function copy() {
    const result = await writeClipboard(async () => {
      if (c.kind === "ssh_key") return c.public_key;
      return revealText(await api.credentials.reveal(c.id));
    });
    if (!result.ok) {
      toast.error(result.error);
      return;
    }
    setState("copied");
    clearTimeout(timer.current);
    timer.current = setTimeout(() => setState("idle"), 2000);
  }

  return (
    <Button
      variant="ghost"
      size="icon-xs"
      aria-label={label}
      title={label}
      disabled={disabled}
      onClick={() => void copy()}
      className={cn("shrink-0", state === "copied" && "text-ok")}
    >
      {state === "copied" ? <Check /> : <Copy />}
      {state === "copied" && <span className="text-[12px]">已复制</span>}
    </Button>
  );
}

/** 添加 / 编辑密钥的右侧抽屉。item 为空表示添加。 */
function CredentialDialog({
  item,
  onClose,
  onSaved,
}: {
  item: CredentialItem | null;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [form, setForm] = React.useState<CredentialForm>(() => (item ? formFromItem(item) : emptyForm()));
  const [reveal, setReveal] = React.useState({ secret: false, passphrase: false });
  const [saving, setSaving] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  /** 正在生成的是哪一项：随机生成要等后端回，期间按钮转圈并禁用 */
  const [generating, setGenerating] = React.useState<"password" | "privateKey" | "passphrase" | null>(null);

  const set = (patch: Partial<CredentialForm>) => setForm((f) => ({ ...f, ...patch }));
  const isPassword = form.kind === "password";
  const used = item ? (item.servers ?? []) : [];

  async function save() {
    const { input, error: invalid } = buildInput(form);
    setError(invalid ?? null);
    if (!input) return;
    setSaving(true);
    try {
      if (item) await api.credentials.update(item.id, input);
      else await api.credentials.create(input);
      toast.success(item ? "密钥已保存" : "密钥已添加");
      onSaved();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setSaving(false);
    }
  }

  /**
   * 随机生成。内容由后端用 crypto/rand 生成（前端不用 Math.random 造凭据），
   * 只填进表单，不写库；保存仍然走原来的添加 / 保存。
   */
  async function generate(what: "password" | "privateKey" | "passphrase") {
    setGenerating(what);
    try {
      if (what === "password") {
        const r = await api.credentials.generate({ kind: "password" });
        if (!r.password) throw new Error("没有生成出密码，请重试");
        // 填进输入框并换成显示状态，可以直接看到、选中复制
        setForm((f) => ({ ...f, password: r.password ?? "" }));
        setReveal((v) => ({ ...v, secret: true }));
        setError(null);
        return;
      }
      // 口令在 OpenSSH 私钥的加密段里，口令与私钥必须成对：只换口令会让已填的私钥解不开，
      // 所以「随机口令」是整套换 —— 生成一把带口令的新私钥，连同它的口令一起填进去。
      const withPassphrase = what === "passphrase";
      const r = await api.credentials.generate({ kind: "ssh_key", passphrase: withPassphrase });
      if (!r.private_key) throw new Error("没有生成出私钥，请重试");
      if (withPassphrase) {
        if (!r.passphrase) throw new Error("没有生成出口令，请重试");
        setForm((f) => ({ ...f, privateKey: (r.private_key ?? "").trim(), passphrase: r.passphrase ?? "" }));
        setReveal((v) => ({ ...v, passphrase: true }));
        toast.success("已生成一把带口令的新私钥，口令与它是一对");
      } else {
        setForm((f) => ({ ...f, privateKey: (r.private_key ?? "").trim() }));
      }
      setError(null);
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setGenerating(null);
    }
  }

  const keep = (saved: boolean) => (saved ? `${SECRET_MASKED}，留空保持不变` : undefined);

  return (
    <Dialog open onOpenChange={(v) => !v && onClose()}>
      <DrawerContent showCloseButton={false}>
        <div className="flex items-start justify-between gap-4 border-b border-line px-6 py-4">
          <div>
            <DialogTitle className="text-[17px] font-semibold">{item ? "编辑密钥" : "添加密钥"}</DialogTitle>
            <DialogDescription className="mt-1 text-[13px] text-ink-2">
              密码和私钥加密后保存在本机，界面不再回显
            </DialogDescription>
          </div>
          <DialogClose asChild>
            <Button variant="ghost" size="icon-sm" aria-label="关闭">
              <X />
            </Button>
          </DialogClose>
        </div>

        <div className="flex-1 overflow-y-auto px-6 py-6">
          <Field label="名称" hint="选的时候看到的就是这个名字">
            <Input
              value={form.name}
              onChange={(e) => set({ name: e.target.value })}
              placeholder="例如：生产 root、ops 私钥"
              autoComplete="off"
              className="bg-surface"
            />
          </Field>

          <div className="mt-5 flex items-center justify-between">
            <span className="text-[13px] font-medium text-ink">类型</span>
            <Segmented
              label="类型"
              value={form.kind}
              onChange={(kind) => setForm((f) => switchKind(f, kind))}
              options={[
                { value: "password", label: CREDENTIAL_KIND_LABEL.password },
                { value: "ssh_key", label: CREDENTIAL_KIND_LABEL.ssh_key },
              ]}
            />
          </div>

          <div className="mt-4">
            {isPassword ? (
              <Field label="密码" htmlFor="credential-secret" hint="加密保存，之后不再显示">
                <div className="flex items-start gap-2">
                  <div className="relative min-w-0 flex-1">
                    <Input
                      id="credential-secret"
                      type={reveal.secret ? "text" : "password"}
                      value={form.password}
                      onChange={(e) => set({ password: e.target.value })}
                      placeholder={keep(form.saved.password)}
                      autoComplete="new-password"
                      className="bg-surface pr-10"
                    />
                    <RevealButton
                      shown={reveal.secret}
                      what="密码"
                      onToggle={() => setReveal((r) => ({ ...r, secret: !r.secret }))}
                    />
                  </div>
                  <Button
                    type="button"
                    variant="outline"
                    className="h-9 shrink-0"
                    disabled={generating !== null}
                    onClick={() => void generate("password")}
                  >
                    <RefreshCw className={cn(generating === "password" && "animate-spin")} />
                    随机生成
                  </Button>
                </div>
                <p className="mt-1.5 text-xs text-ink-3">
                  32 位，含大小写字母、数字和符号，去掉了容易看错的 0 O 1 l I，不满意就再点一次
                </p>
              </Field>
            ) : (
              <div className="grid gap-4">
                <Field
                  label="私钥"
                  htmlFor="credential-private-key"
                  hint="PEM 或 OpenSSH 格式；粘贴后立刻算出公钥，方便加到目标机的 authorized_keys"
                >
                  <Textarea
                    id="credential-private-key"
                    rows={5}
                    value={form.privateKey}
                    onChange={(e) => set({ privateKey: e.target.value })}
                    placeholder={keep(form.saved.privateKey) ?? "-----BEGIN OPENSSH PRIVATE KEY-----"}
                    spellCheck={false}
                    className="bg-surface font-mono text-xs"
                  />
                  <div className="flex justify-end">
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      disabled={generating !== null}
                      onClick={() => void generate("privateKey")}
                    >
                      <RefreshCw className={cn(generating === "privateKey" && "animate-spin")} />
                      随机生成私钥
                    </Button>
                  </div>
                </Field>

                <PublicKeyBlock item={item} name={form.name} privateKey={form.privateKey} />

                <Field label="私钥口令" htmlFor="credential-passphrase" hint="没有就留空；不修改时保留已保存的口令">
                  <div className="flex items-start gap-2">
                    <div className="relative min-w-0 flex-1">
                      <Input
                        id="credential-passphrase"
                        type={reveal.passphrase ? "text" : "password"}
                        value={form.passphrase}
                        onChange={(e) => set({ passphrase: e.target.value })}
                        placeholder={keep(form.saved.passphrase)}
                        autoComplete="new-password"
                        className="bg-surface pr-10"
                      />
                      <RevealButton
                        shown={reveal.passphrase}
                        what="私钥口令"
                        onToggle={() => setReveal((r) => ({ ...r, passphrase: !r.passphrase }))}
                      />
                    </div>
                    <Button
                      type="button"
                      variant="outline"
                      className="h-9 shrink-0"
                      aria-label="随机口令"
                      title="生成一把带 16 位口令的新私钥（口令与私钥必须成对）"
                      disabled={generating !== null}
                      onClick={() => void generate("passphrase")}
                    >
                      <RefreshCw className={cn(generating === "passphrase" && "animate-spin")} />
                      随机
                    </Button>
                  </div>
                </Field>
              </div>
            )}
          </div>

          <Field label="默认用户" className="mt-5" hint="选了这条密钥时，服务器上为空的「用户」自动填它">
            <Input
              value={form.username}
              onChange={(e) => set({ username: e.target.value })}
              placeholder="可选"
              autoComplete="off"
              className="bg-surface"
            />
          </Field>

          <Field label="备注" className="mt-5" hint="可选，只在本页显示">
            <Input
              value={form.note}
              onChange={(e) => set({ note: e.target.value })}
              placeholder="例如：线上集群通用"
              autoComplete="off"
              className="bg-surface"
            />
          </Field>

          {item && used.length > 0 && (
            <section className="mt-6 rounded-lg border border-line bg-surface p-4">
              <h3 className="text-[14px] font-semibold">被这些服务器使用</h3>
              <ul className="mt-2 flex flex-wrap gap-1.5">
                {used.map((s) => (
                  <li key={s.id} className="flex items-center gap-1.5 rounded-md bg-sunken px-1.5 py-0.5">
                    <span className="text-[12px] text-ink-2">{s.name}</span>
                    <Mono className="text-[11px] text-ink-3">{serverLabel(s)}</Mono>
                  </li>
                ))}
              </ul>
              <p className="mt-2 text-xs text-ink-2">改这里的密码或私钥，上面这些服务器会一起生效。</p>
            </section>
          )}
        </div>

        <div className="border-t border-line bg-surface px-6 py-4">
          <FormError className="mb-3">{error}</FormError>
          <div className="flex items-center gap-2">
            <DialogClose asChild>
              <Button variant="ghost" className="ml-auto">
                取消
              </Button>
            </DialogClose>
            <Button onClick={() => void save()} disabled={saving}>
              {item ? "保存" : "添加"}
            </Button>
          </div>
        </div>
      </DrawerContent>
    </Dialog>
  );
}

/**
 * 抽屉里的「公钥」块：私钥类型时固定在私钥下方。
 * 公钥是明文，一直可以看、复制、下载 .pub；右侧显示 SHA256 指纹。
 * 私钥框为空时，新建提示粘贴，编辑则显示已保存私钥对应的公钥。
 */
function PublicKeyBlock({ item, name, privateKey }: { item: CredentialItem | null; name: string; privateKey: string }) {
  const view = publicKeyView({ item, privateKey });
  const publicKey = view.kind === "saved" || view.kind === "derived" ? view.publicKey : null;
  const hint =
    view.kind === "empty" || view.kind === "invalid" || view.kind === "unsupported" ? PUBLIC_KEY_HINT[view.kind] : null;
  const [copied, setCopied] = React.useState(false);
  const timer = React.useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  React.useEffect(() => () => clearTimeout(timer.current), []);

  async function copy() {
    if (!publicKey) return;
    const r = await writeClipboard(() => publicKey);
    if (!r.ok) {
      toast.error(r.error);
      return;
    }
    setCopied(true);
    clearTimeout(timer.current);
    timer.current = setTimeout(() => setCopied(false), 2000);
  }

  return (
    <section
      className={cn(
        "rounded-lg border p-3",
        view.kind === "invalid" || view.kind === "unsupported"
          ? "border-warn-fill/60 bg-warn-wash"
          : "border-line bg-surface"
      )}
      aria-label="公钥"
    >
      <div className="flex items-baseline justify-between gap-3">
        <h3 className="text-[13px] font-semibold">公钥</h3>
        {(view.kind === "saved" || view.kind === "derived") && (
          <span className="text-xs text-ink-3">{PUBLIC_KEY_SOURCE[view.kind]}</span>
        )}
      </div>

      {publicKey ? (
        <>
          <p className="mt-1.5 rounded-md bg-sunken px-2.5 py-2 font-mono text-[12px] leading-relaxed break-all select-text">
            {publicKey}
          </p>
          <div className="mt-2 flex flex-wrap items-center gap-1.5">
            <Button type="button" variant="outline" size="sm" onClick={() => void copy()}>
              {copied ? <Check className="text-ok" /> : <Copy />}
              {copied ? "已复制" : "复制公钥"}
            </Button>
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => downloadText(publicKeyFilename(name), `${publicKey}\n`)}
            >
              <Download />
              下载 .pub
            </Button>
            <PublicKeyFingerprint publicKey={publicKey} />
          </div>
        </>
      ) : (
        <p className="mt-1.5 text-xs text-ink-3">{hint}</p>
      )}
    </section>
  );
}

/** 公钥的 SHA256 指纹；算不出来时什么都不显示，不造假 */
function PublicKeyFingerprint({ publicKey }: { publicKey: string }) {
  const [fingerprint, setFingerprint] = React.useState<string | null>(null);
  const [prevKey, setPrevKey] = React.useState(publicKey);
  if (prevKey !== publicKey) {
    setPrevKey(publicKey);
    setFingerprint(null);
  }

  React.useEffect(() => {
    let cancelled = false;
    void fingerprintOfPublicKey(publicKey).then((fp) => {
      if (!cancelled) setFingerprint(fp);
    });
    return () => {
      cancelled = true;
    };
  }, [publicKey]);

  if (!fingerprint) return null;
  return (
    <span className="ml-auto truncate text-xs text-ink-3" title={fingerprint}>
      指纹 <Mono className="text-ink-2">{fingerprint}</Mono>
    </span>
  );
}

/** 密码 / 口令输入框右侧的显示与隐藏切换 */
function RevealButton({ shown, what, onToggle }: { shown: boolean; what: string; onToggle: () => void }) {
  return (
    <button
      type="button"
      onClick={onToggle}
      aria-label={`${shown ? "隐藏" : "显示"}${what}`}
      title={shown ? `隐藏${what}` : `显示${what}`}
      className="absolute top-1/2 right-2 -translate-y-1/2 rounded p-1 text-ink-3 hover:text-ink"
    >
      {shown ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
    </button>
  );
}

/** 删除密钥的二次确认；被服务器使用时主按钮不可用并写明原因 */
function DeleteDialog({
  item,
  onClose,
  onDeleted,
}: {
  item: CredentialItem | null;
  onClose: () => void;
  onDeleted: () => void;
}) {
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  // 换一条密钥时清掉上一次的错误：在渲染期间对比
  const [prev, setPrev] = React.useState(item);
  if (prev !== item) {
    setPrev(item);
    setError(null);
  }

  const blocked = item && !canDelete(item) ? deleteBlockedReason(item) : null;

  async function remove() {
    if (!item) return;
    setBusy(true);
    setError(null);
    try {
      await api.credentials.remove(item.id);
      toast.success(`已删除密钥「${item.name}」`);
      onDeleted();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <AlertDialog open={item !== null} onOpenChange={(v) => !v && onClose()}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>删除密钥「{item?.name ?? ""}」？</AlertDialogTitle>
          <AlertDialogDescription>
            {blocked ? (
              <span className="flex items-start gap-1.5">
                <AlertTriangle className="mt-0.5 size-4 shrink-0 text-fail" aria-hidden />
                {blocked}
              </span>
            ) : (
              "删除后不能恢复。"
            )}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <FormError>{error}</FormError>
        <AlertDialogFooter>
          <AlertDialogCancel>取消</AlertDialogCancel>
          <AlertDialogAction
            disabled={busy || blocked !== null}
            className="bg-destructive text-white hover:bg-destructive/90"
            onClick={(e) => {
              e.preventDefault();
              void remove();
            }}
          >
            删除
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
