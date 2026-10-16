import * as React from "react";
import {
  ArrowDown,
  ArrowUp,
  Check,
  Copy,
  KeyRound,
  Laptop,
  Loader2,
  Plus,
  RefreshCw,
  ShieldCheck,
  X,
} from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Dialog, DialogClose, DialogDescription, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { DrawerContent, Field, FormError, Mono, Segmented, useCopy } from "@/components/bits";
import {
  SECRET_MASKED,
  api,
  errorMessage,
  type HopResult,
  type HopType,
  type ServerAuthType,
  type ServerItem,
  type ServerTestResult,
} from "@/lib/api";
import {
  HOP_LABEL,
  buildInput,
  emptyForm,
  formFromItem,
  formSignature,
  isTrusted,
  moveHop,
  newHop,
  passwordSaved,
  reasonText,
  removeHop,
  targetAddr,
  targetResult,
  type HopForm,
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
          <DialogDescription className="sr-only">填写 SSH 连接信息和代理链</DialogDescription>
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
            <div className="mb-3 flex items-center justify-between">
              <h3 className="text-[14px] font-semibold">认证</h3>
              <Segmented<ServerAuthType>
                label="认证方式"
                value={form.authType}
                onChange={(authType) => set({ authType })}
                options={[
                  { value: "password", label: "密码" },
                  { value: "private_key", label: "私钥" },
                ]}
              />
            </div>
            {form.authType === "password" ? (
              <Field label="密码">
                <Input
                  type="password"
                  value={form.password}
                  onChange={(e) => set({ password: e.target.value })}
                  placeholder={passwordSaved(form) ? `${SECRET_MASKED}，留空保持不变` : undefined}
                  autoComplete="new-password"
                  className="bg-surface"
                />
              </Field>
            ) : (
              <div className="grid gap-3">
                <Field label="私钥">
                  <Textarea
                    rows={4}
                    value={form.privateKey}
                    onChange={(e) => set({ privateKey: e.target.value })}
                    placeholder={
                      form.saved.privateKey ? `${SECRET_MASKED}，留空保持不变` : "-----BEGIN OPENSSH PRIVATE KEY-----"
                    }
                    spellCheck={false}
                    className="bg-surface font-mono text-xs"
                  />
                </Field>
                <Field label="私钥口令" hint="没有就留空">
                  <Input
                    type="password"
                    value={form.passphrase}
                    onChange={(e) => set({ passphrase: e.target.value })}
                    placeholder={form.saved.passphrase ? `${SECRET_MASKED}，留空保持不变` : undefined}
                    autoComplete="new-password"
                    className="bg-surface"
                  />
                </Field>
              </div>
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
