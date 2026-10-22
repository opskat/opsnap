import * as React from "react";
import { Check, Copy, Loader2, ShieldCheck } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { Field, FormError, Mono, useCopy } from "@/components/bits";
import { RecoveryKeyBox } from "@/components/recovery-key-box";
import {
  api,
  ApiError,
  errorMessage,
  ErrorCode,
  SECRET_MASKED,
  type RepoItem,
  type RepoType,
  type SFTPHostKey,
} from "@/lib/api";
import {
  buildCreateInput,
  buildUpdateInput,
  emptyRepoForm,
  formFromRepo,
  NOTHING_SAVED,
  REPO_TYPE_LABEL,
  savedCredentialsOf,
  sftpPort,
  type RepoForm,
  type SavedCredentials,
} from "@/lib/repos";
import { useRecoveryKey } from "@/lib/use-recovery-key";

/** 核对过的 SFTP 主机公钥，绑定到核对时的地址；地址一变就作废 */
interface Trusted {
  host: string;
  port: number;
  serverId: number;
  hostKey: string;
}

/** 添加或修改存储位置。添加时后端先测试访问：空位置初始化，已有 restic 仓库则要现有密码。
 * 修改时用已保存的参数预填；凭据不返回内容，留空表示保留。 */
export function RepoDialog({
  repo,
  onClose,
  onSaved,
}: {
  /** 传入表示修改这个存储位置，不传表示添加 */
  repo?: RepoItem;
  onClose: () => void;
  onSaved: () => void;
}) {
  const editing = repo !== undefined;
  const [form, setForm] = React.useState<RepoForm>(() => (repo ? formFromRepo(repo) : emptyRepoForm()));
  const [recovery, setRecovery] = React.useState(true);
  const [trusted, setTrusted] = React.useState<Trusted | null>(null);
  const [needPassword, setNeedPassword] = React.useState(false);
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const rk = useRecoveryKey(!editing && recovery);
  // 已保存且已核对过的主机公钥：地址不变就直接可用，地址一改就作废
  const hostKeySaved =
    editing &&
    repo.type === "sftp" &&
    repo.params.host_key !== "" &&
    repo.params.host === form.host.trim() &&
    (repo.params.port || 22) === sftpPort(form) &&
    repo.params.server_id === form.server_id;
  // 接口只把已保存的凭据回填成占位，据此决定哪些输入框只提示「已设置」
  const saved = editing ? savedCredentialsOf(repo) : NOTHING_SAVED;

  const set = (patch: Partial<RepoForm>) => setForm((f) => ({ ...f, ...patch }));

  const hostKey =
    trusted &&
    trusted.host === form.host.trim() &&
    trusted.port === sftpPort(form) &&
    trusted.serverId === form.server_id
      ? trusted.hostKey
      : "";
  const sftpReady = form.type !== "sftp" || hostKey !== "" || hostKeySaved;
  const canSave = form.name.trim() !== "" && sftpReady && (editing || !recovery || rk.ready) && !busy;

  async function save() {
    setBusy(true);
    setError(null);
    try {
      if (repo) {
        const updated = await api.repos.update(repo.id, buildUpdateInput(form, hostKey, saved));
        toast.success(`已保存 ${updated.name}`);
        onSaved();
        return;
      }
      const created = await api.repos.create(
        buildCreateInput(form, hostKey, { enabled: recovery, value: rk.value, saved: rk.acked })
      );
      // 创建不会填统计，随即重新检查一次
      await api.repos.recheck(created.id).catch(() => undefined);
      if (created.recovery_key_error) {
        toast.error(
          `已添加 ${created.name}，但恢复密钥没有加入：${created.recovery_key_error}。可在「密钥」里重新生成`,
          {
            duration: 15000,
          }
        );
      } else {
        toast.success(created.adopted ? `已接入 ${created.name} 里现有的仓库` : `已添加 ${created.name}`);
      }
      onSaved();
    } catch (err) {
      if (err instanceof ApiError && err.code === ErrorCode.RepoExistingPasswordRequired) {
        setNeedPassword(true);
      }
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open onOpenChange={(v) => !v && !busy && onClose()}>
      <DialogContent className="max-h-[92svh] gap-5 overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{editing ? "修改存储位置" : "添加存储位置"}</DialogTitle>
          <DialogDescription>
            {editing ? "类型不能改；留空的凭据表示保留已保存的值。" : "已有 restic 仓库会直接接入。"}
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-4">
          <div className={editing ? "grid gap-3" : "grid grid-cols-[1fr_180px] gap-3"}>
            <Field label="名称">
              <Input value={form.name} onChange={(e) => set({ name: e.target.value })} placeholder="群晖 DS920+" />
            </Field>
            {editing ? (
              <Field label="类型">
                <div className="rounded-md border border-line bg-wash px-3 py-[9px] text-[13.5px] text-ink-2">
                  {REPO_TYPE_LABEL[form.type] ?? form.type}
                </div>
              </Field>
            ) : (
              <Field label="类型" htmlFor="repo-type">
                <Select value={form.type} onValueChange={(v) => set({ type: v as RepoType })}>
                  <SelectTrigger id="repo-type" className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {(Object.keys(REPO_TYPE_LABEL) as RepoType[]).map((t) => (
                      <SelectItem key={t} value={t}>
                        {REPO_TYPE_LABEL[t]}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
            )}
          </div>

          <TypeFields
            form={form}
            set={set}
            hostKey={hostKey}
            hostKeySaved={hostKeySaved}
            saved={saved}
            onTrust={setTrusted}
          />

          {needPassword && (
            <Field label="现有密码" hint="这个仓库不是 opsnap 建的。密码只用于接入，opsnap 不保存。">
              <Input
                type="password"
                autoComplete="off"
                value={form.existing_password}
                onChange={(e) => set({ existing_password: e.target.value })}
              />
            </Field>
          )}

          {!editing && (
            <div className="rounded-lg border border-line-strong">
              <div className="flex items-center gap-2 border-b border-line px-4 py-3">
                <ShieldCheck className="size-4 text-ok" aria-hidden />
                <span className="text-[13.5px] font-medium">AES-256 加密</span>
                <span className="ml-auto text-xs text-ink-3">opsnap 自动生成并保管密钥</span>
              </div>
              <label className="flex items-center gap-2 px-4 py-3 text-[13.5px]">
                <Checkbox checked={recovery} onCheckedChange={(v) => setRecovery(v === true)} />
                同时生成一把恢复密钥，由你保管
              </label>
              {recovery && (
                <RecoveryKeyBox
                  rk={rk}
                  className="border-t border-line px-4 py-3"
                  note="opsnap 不保存这把密钥。opsnap 所在机器损坏时，用它在任何装了 restic 的机器上恢复。"
                />
              )}
            </div>
          )}
        </div>
        <FormError>{error}</FormError>
        <DialogFooter className="items-center">
          {!sftpReady && <span className="mr-auto text-xs text-ink-3">先获取并核对主机指纹</span>}
          <Button variant="ghost" onClick={onClose} disabled={busy}>
            取消
          </Button>
          <Button disabled={!canSave} onClick={() => void save()}>
            {busy && <Loader2 className="animate-spin" />}
            {editing ? "保存" : "测试并保存"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function TypeFields({
  form,
  set,
  hostKey,
  hostKeySaved,
  saved,
  onTrust,
}: {
  form: RepoForm;
  set: (patch: Partial<RepoForm>) => void;
  hostKey: string;
  /** 已保存且已核对过的主机公钥，地址不变时无需重新核对 */
  hostKeySaved: boolean;
  /** 哪些凭据已有保存的值，留空表示保留 */
  saved: SavedCredentials;
  onTrust: (t: Trusted) => void;
}) {
  const mono = "font-mono";
  switch (form.type) {
    case "local":
      return (
        <Field label="路径" hint="容器内的目录，通常是挂载进来的 NAS 共享。初始化时会写入 .opsnap-repo 标记文件。">
          <Input
            className={mono}
            value={form.path}
            onChange={(e) => set({ path: e.target.value })}
            placeholder="/mnt/nas/opsnap"
          />
        </Field>
      );
    case "sftp":
      return (
        <SftpFields
          form={form}
          set={set}
          hostKey={hostKey}
          hostKeySaved={hostKeySaved}
          saved={saved}
          onTrust={onTrust}
        />
      );
    case "s3":
      return (
        <div className="grid grid-cols-2 gap-3">
          <Field label="地址" className="col-span-2">
            <Input
              className={mono}
              value={form.endpoint}
              onChange={(e) => set({ endpoint: e.target.value })}
              placeholder="http://nas:9000"
            />
          </Field>
          <Field label="Bucket">
            <Input className={mono} value={form.bucket} onChange={(e) => set({ bucket: e.target.value })} />
          </Field>
          <Field label="前缀">
            <Input className={mono} value={form.prefix} onChange={(e) => set({ prefix: e.target.value })} />
          </Field>
          <Field label="Access Key ID">
            <Input
              className={mono}
              value={form.access_key_id}
              onChange={(e) => set({ access_key_id: e.target.value })}
            />
          </Field>
          <Field label="Secret Access Key">
            <Input
              type="password"
              autoComplete="off"
              value={form.secret_access_key}
              placeholder={saved.secret_access_key ? `${SECRET_MASKED}，留空保持不变` : undefined}
              onChange={(e) => set({ secret_access_key: e.target.value })}
            />
          </Field>
          <Field label="区域">
            <Input className={mono} value={form.region} onChange={(e) => set({ region: e.target.value })} />
          </Field>
          <label className="flex items-center gap-2 self-end pb-2 text-[13px]">
            <Checkbox checked={form.path_style} onCheckedChange={(v) => set({ path_style: v === true })} />
            路径风格访问
          </label>
        </div>
      );
    case "rest":
    case "webdav":
      return (
        <div className="grid grid-cols-2 gap-3">
          <Field label="地址" className="col-span-2">
            <Input
              className={mono}
              value={form.url}
              onChange={(e) => set({ url: e.target.value })}
              placeholder="https://nas:8000/opsnap"
            />
          </Field>
          {form.type === "webdav" && (
            <Field label="路径" className="col-span-2">
              <Input className={mono} value={form.path} onChange={(e) => set({ path: e.target.value })} />
            </Field>
          )}
          <Field label="用户名">
            <Input value={form.username} onChange={(e) => set({ username: e.target.value })} />
          </Field>
          <Field label="密码">
            <Input
              type="password"
              autoComplete="off"
              value={form.password}
              placeholder={saved.password ? `${SECRET_MASKED}，留空保持不变` : undefined}
              onChange={(e) => set({ password: e.target.value })}
            />
          </Field>
        </div>
      );
  }
}

function SftpFields({
  form,
  set,
  hostKey,
  hostKeySaved,
  saved,
  onTrust,
}: {
  form: RepoForm;
  set: (patch: Partial<RepoForm>) => void;
  hostKey: string;
  hostKeySaved: boolean;
  saved: SavedCredentials;
  onTrust: (t: Trusted) => void;
}) {
  const [servers, setServers] = React.useState<{ id: number; name: string }[]>([]);
  const [probing, setProbing] = React.useState(false);
  const [probe, setProbe] = React.useState<{ key: string; result: SFTPHostKey } | null>(null);
  const [probeError, setProbeError] = React.useState<string | null>(null);

  React.useEffect(() => {
    let cancelled = false;
    api.servers
      .list()
      .then((r) => !cancelled && setServers((r.list ?? []).map((s) => ({ id: s.id, name: s.name }))))
      .catch(() => undefined); // 取不到服务器列表时只能直连
    return () => {
      cancelled = true;
    };
  }, []);

  const addrKey = `${form.host.trim()}:${sftpPort(form)}:${form.server_id}`;
  const shown = probe && probe.key === addrKey ? probe.result : null;

  async function fetchKey() {
    setProbing(true);
    setProbeError(null);
    try {
      const result = await api.repos.sftpHostKey({
        host: form.host.trim(),
        port: sftpPort(form),
        server_id: form.server_id,
      });
      setProbe({ key: addrKey, result });
    } catch (err) {
      setProbeError(errorMessage(err));
    } finally {
      setProbing(false);
    }
  }

  return (
    <div className="grid grid-cols-[1fr_88px] gap-3">
      <Field label="主机">
        <Input
          className="font-mono"
          value={form.host}
          onChange={(e) => set({ host: e.target.value })}
          placeholder="192.168.1.20"
        />
      </Field>
      <Field label="端口">
        <Input
          className="font-mono"
          inputMode="numeric"
          value={form.port}
          onChange={(e) => set({ port: e.target.value })}
        />
      </Field>
      <Field label="路径" className="col-span-2">
        <Input
          className="font-mono"
          value={form.path}
          onChange={(e) => set({ path: e.target.value })}
          placeholder="/volume1/backup/opsnap"
        />
      </Field>
      <div className="col-span-2 grid grid-cols-2 gap-3">
        <Field label="用户名">
          <Input value={form.username} onChange={(e) => set({ username: e.target.value })} />
        </Field>
        <Field label="密码">
          <Input
            type="password"
            autoComplete="off"
            value={form.password}
            placeholder={saved.password ? `${SECRET_MASKED}，留空保持不变` : undefined}
            onChange={(e) => set({ password: e.target.value })}
          />
        </Field>
      </div>
      <Field label="经由服务器" className="col-span-2" htmlFor="repo-via">
        <Select value={String(form.server_id)} onValueChange={(v) => set({ server_id: Number(v) })}>
          <SelectTrigger id="repo-via" className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="0">直连</SelectItem>
            {servers.map((s) => (
              <SelectItem key={s.id} value={String(s.id)}>
                {s.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Field>
      <Field label="私钥（可选，OpenSSH 格式，无口令）" className="col-span-2">
        <Textarea
          className="min-h-16 font-mono text-[12.5px]"
          value={form.private_key}
          placeholder={saved.private_key ? `${SECRET_MASKED}，留空保持不变` : undefined}
          onChange={(e) => set({ private_key: e.target.value })}
        />
      </Field>
      <div className="col-span-2">
        {hostKey || hostKeySaved ? (
          <span className="flex items-center gap-1.5 text-[13px] text-ok">
            <Check className="size-4" aria-hidden />
            主机指纹已核对
          </span>
        ) : shown ? (
          <HostKeyCheck
            fp={shown.fingerprint}
            onTrust={() =>
              onTrust({
                host: form.host.trim(),
                port: sftpPort(form),
                serverId: form.server_id,
                hostKey: shown.host_key,
              })
            }
          />
        ) : (
          <>
            <Button
              type="button"
              size="sm"
              variant="outline"
              disabled={probing || !form.host.trim()}
              onClick={() => void fetchKey()}
            >
              {probing && <Loader2 className="animate-spin" />}
              获取主机指纹
            </Button>
            <FormError className="mt-2">{probeError}</FormError>
          </>
        )}
      </div>
    </div>
  );
}

function HostKeyCheck({ fp, onTrust }: { fp: string; onTrust: () => void }) {
  const [copied, copy] = useCopy();
  return (
    <section className="rounded-lg border border-warn-fill/50 bg-warn-wash p-4">
      <h3 className="text-[14px] font-semibold">核对 NAS 的主机指纹</h3>
      <p className="mt-1 text-[13px] text-ink-2">
        在 NAS 上运行 <Mono>ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub</Mono>，结果应一致。
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
