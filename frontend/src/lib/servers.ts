// 服务器表单的数据模型：从接口数据生成表单、校验并生成提交体、代理链的调整。
// 纯函数，不依赖界面。
import {
  SECRET_MASKED,
  CREDENTIAL_KIND_LABEL,
  type CredentialKind,
  type HopFailReason,
  type HopInput,
  type HopResult,
  type HopType,
  type HopView,
  type ServerAuthType,
  type ServerCredential,
  type ServerInput,
  type ServerItem,
  type ServerTestResult,
} from "./api";

export const HOP_LABEL: Record<HopType, string> = { ssh: "SSH 跳板", socks5: "SOCKS5", http: "HTTP" };
export const AUTH_LABEL: Record<ServerAuthType, string> = { password: "密码", ssh_key: "私钥" };

/** 凭据来源：用已保存的共享密钥，或者直接填写在这台服务器上 */
export type CredentialSource = "saved" | "inline";

/** 代理链中的一跳（表单里的形态） */
export interface HopForm {
  /** 稳定的列表 key，调整顺序时不变 */
  key: number;
  type: HopType;
  /** SSH 跳板选中的服务器，0 表示还没选 */
  serverId: number;
  /** 代理地址，host:port */
  addr: string;
  username: string;
  password: string;
  /** 已保存的代理密码对应的「类型|地址|用户名」；这三项都没变且没填新密码时回传占位 */
  savedPassword: string | null;
}

/** 管理员核对过的目标主机指纹，以及它是在哪个地址上核对的 */
export interface TrustedFingerprint {
  fp: string;
  addr: string;
}

export interface ServerForm {
  name: string;
  host: string;
  port: string;
  username: string;
  /** 凭据来源：saved 用共享密钥，inline 直接填写 */
  source: CredentialSource;
  /** 选中的共享密钥，0 表示还没选 */
  credentialId: number;
  /** 直接填写时的认证方式；用共享密钥时由密钥的类型决定，这里不参与提交 */
  authType: ServerAuthType;
  password: string;
  privateKey: string;
  passphrase: string;
  /** 私钥和口令已经保存过：留空表示保留 */
  saved: { privateKey: boolean; passphrase: boolean };
  /**
   * 已保存的登录密码对应的「地址|端口|用户」；这三项都没变且没填新密码时回传占位。
   * 后端拒绝把已保存的密码带到别的地址或用户上。
   */
  savedPassword: string | null;
  /** 「同时存成密钥」：勾选后这份私有凭据进密钥库 */
  share: boolean;
  /** 存成密钥时的名称；留空时用服务器名 */
  shareAs: string;
  hops: HopForm[];
  trusted: TrustedFingerprint | null;
}

/** 组合框里的一项共享密钥：接口条目加上使用者数量 */
export interface PickerCredential {
  id: number;
  name: string;
  kind: CredentialKind;
  username: string;
  servers: { id: number; name: string }[] | null;
  /** 私钥有没有口令，用于选中后的摘要 */
  hasPassphrase?: boolean;
}

let hopSeq = 0;

export function newHop(type: HopType = "ssh"): HopForm {
  return { key: ++hopSeq, type, serverId: 0, addr: "", username: "", password: "", savedPassword: null };
}

export function emptyForm(): ServerForm {
  return {
    name: "",
    host: "",
    port: "22",
    username: "",
    source: "saved",
    credentialId: 0,
    authType: "password",
    password: "",
    privateKey: "",
    passphrase: "",
    saved: { privateKey: false, passphrase: false },
    savedPassword: null,
    share: false,
    shareAs: "",
    hops: [],
    trusted: null,
  };
}

function hostPort(host: string, port: number | string) {
  const h = host.trim();
  return `${h.includes(":") ? `[${h}]` : h}:${port}`;
}

function hopFromView(v: HopView): HopForm {
  const f = newHop(v.type);
  if (v.type === "ssh") {
    f.serverId = v.server_id ?? 0;
    return f;
  }
  f.addr = hostPort(v.host, v.port);
  f.username = v.username;
  f.savedPassword = v.password === SECRET_MASKED ? hopSig(f) : null;
  return f;
}

function hopSig(h: HopForm) {
  return `${h.type}|${h.addr.trim()}|${h.username.trim()}`;
}

/** 目标的「地址|端口|用户」，判断已保存的登录密码是否还适用 */
function targetSig(f: Pick<ServerForm, "host" | "port" | "username">) {
  return `${f.host.trim()}|${parsePort(f.port) ?? f.port.trim()}|${f.username.trim()}`;
}

/** 已保存的登录密码仍然适用：地址、端口、用户都没改 */
export function passwordSaved(f: ServerForm): boolean {
  return f.source === "inline" && f.savedPassword !== null && f.savedPassword === targetSig(f);
}

/** 从接口数据生成表单：共享密钥是「用已保存的」，私有凭据是「直接填写」 */
export function formFromItem(s: ServerItem): ServerForm {
  const target = { host: s.host, port: String(s.port), username: s.username };
  const cred = s.credential;
  const shared = cred !== null && cred.shared;
  return {
    name: s.name,
    host: s.host,
    port: String(s.port),
    username: s.username,
    source: shared ? "saved" : "inline",
    credentialId: shared ? cred.id : 0,
    authType: cred ? cred.kind : "password",
    password: "",
    privateKey: "",
    passphrase: "",
    saved: {
      privateKey: cred !== null && cred.kind === "ssh_key" && cred.has_secret,
      passphrase: cred !== null && cred.kind === "ssh_key" && cred.has_passphrase,
    },
    savedPassword: !shared && cred !== null && cred.kind === "password" && cred.has_secret ? targetSig(target) : null,
    share: false,
    shareAs: "",
    hops: (s.hops ?? []).map(hopFromView),
    trusted: s.host_key_fingerprint ? { fp: s.host_key_fingerprint, addr: hostPort(s.host, s.port) } : null,
  };
}

/** 选中一条共享密钥：用户为空时填它的默认用户，已有的内容不动；直接填写的内容清空 */
export function pickCredential(f: ServerForm, c: PickerCredential): ServerForm {
  return {
    ...f,
    source: "saved",
    credentialId: c.id,
    authType: c.kind,
    username: f.username.trim() ? f.username : c.username,
    password: "",
    privateKey: "",
    passphrase: "",
    saved: { privateKey: false, passphrase: false },
    savedPassword: null,
    share: false,
  };
}

/** 切换凭据来源；切到直接填写时从空白开始，不带出共享密钥的值 */
export function switchSource(f: ServerForm, source: CredentialSource): ServerForm {
  if (f.source === source) return f;
  if (source === "saved") return { ...f, source };
  return {
    ...f,
    source,
    credentialId: 0,
    password: "",
    privateKey: "",
    passphrase: "",
    saved: { privateKey: false, passphrase: false },
    savedPassword: null,
    share: false,
  };
}

/** 切换认证方式：清空不属于新类型的内容与已保存标记 */
export function switchAuthType(f: ServerForm, authType: ServerAuthType): ServerForm {
  if (f.authType === authType) return f;
  return {
    ...f,
    authType,
    password: "",
    privateKey: "",
    passphrase: "",
    saved: { privateKey: false, passphrase: false },
    savedPassword: null,
  };
}

/** 解析 host:port 或 [ipv6]:port */
export function parseAddr(s: string): { host: string; port: number } | null {
  const m = /^\[([^\]\s]+)\]:(\d+)$/.exec(s.trim()) ?? /^([^:\s[\]]+):(\d+)$/.exec(s.trim());
  if (!m) return null;
  const port = Number(m[2]);
  return port >= 1 && port <= 65535 ? { host: m[1], port } : null;
}

function parsePort(s: string): number | null {
  const n = /^\d+$/.test(s.trim()) ? Number(s.trim()) : NaN;
  return n >= 1 && n <= 65535 ? n : null;
}

/** 目标当前的地址，用来判断已核对的指纹是否仍然适用 */
export function targetAddr(f: ServerForm): string {
  return hostPort(f.host, f.port.trim());
}

/** 指纹已核对，且是在当前地址上核对的 */
export function isTrusted(f: ServerForm): boolean {
  return f.trusted !== null && f.trusted.addr === targetAddr(f);
}

/** 「同时存成密钥」时用的名称：留空用服务器名 */
export function shareName(f: ServerForm): string {
  return f.shareAs.trim() || f.name.trim();
}

/** 校验并生成提交体；直接填写时 secret 未改动且已保存时回传「已设置」 */
export function buildInput(f: ServerForm): { input?: ServerInput; error?: string } {
  const name = f.name.trim();
  const host = f.host.trim();
  const username = f.username.trim();
  if (!name) return { error: "填写服务器名称" };
  if (!host) return { error: "填写服务器地址" };
  const port = parsePort(f.port);
  if (port === null) return { error: "端口须在 1 到 65535 之间" };
  if (!username) return { error: "填写登录用户" };

  const hops: HopInput[] = [];
  for (const [i, h] of f.hops.entries()) {
    if (h.type === "ssh") {
      if (!h.serverId) return { error: `第 ${i + 1} 跳：选择一台已添加的服务器` };
      hops.push({ type: "ssh", server_id: h.serverId, host: "", port: 0, username: "", password: "" });
      continue;
    }
    const a = parseAddr(h.addr);
    if (!a) return { error: `第 ${i + 1} 跳：代理地址写成 主机:端口` };
    const keep = h.password === "" && h.savedPassword !== null && h.savedPassword === hopSig(h);
    hops.push({
      type: h.type,
      server_id: 0,
      host: a.host,
      port: a.port,
      username: h.username.trim(),
      password: keep ? SECRET_MASKED : h.password,
    });
  }

  const base = {
    name,
    host,
    port,
    username,
    hops,
    host_key_fingerprint: isTrusted(f) ? f.trusted!.fp : "",
  };

  // 用已保存的：只提交 credential_id，凭据字段留空，也不带 share_as
  if (f.source === "saved") {
    if (!f.credentialId) return { error: "选择密码或私钥" };
    return {
      input: {
        ...base,
        credential_id: f.credentialId,
        auth_type: "",
        password: "",
        private_key: "",
        passphrase: "",
      },
    };
  }

  const secret = (value: string, saved: boolean) => (value !== "" ? value : saved ? SECRET_MASKED : "");
  const isKey = f.authType === "ssh_key";
  const password = isKey ? "" : secret(f.password, passwordSaved(f));
  const privateKey = isKey ? secret(f.privateKey, f.saved.privateKey) : "";
  const passphrase = isKey ? secret(f.passphrase, f.saved.passphrase) : "";
  if (!isKey && !password) {
    return { error: f.savedPassword !== null ? "地址或用户已改动，请重新填写登录密码" : "填写密码" };
  }
  if (isKey && !privateKey) return { error: "粘贴私钥" };

  const input: ServerInput = {
    ...base,
    credential_id: 0,
    auth_type: f.authType,
    password,
    private_key: privateKey,
    passphrase,
  };
  // share_as 只在管理员勾选「同时存成密钥」时填，空串表示保持私有
  if (f.share) input.share_as = shareName(f);
  return { input };
}

/** 表单内容的签名（不含指纹核对状态）；测试结果只对生成它的那份内容有效 */
export function formSignature(f: ServerForm): string {
  return JSON.stringify({ ...f, trusted: null });
}

/** 服务器列表里「密钥」列的摘要：共享的用名称，私有的写「直接填写」 */
export function credentialLabel(c: ServerCredential | null): string {
  return c && c.shared ? c.name : "直接填写";
}

/** 「密钥」列的类型标签；没有凭据时为空串 */
export function credentialKindLabel(c: ServerCredential | null): string {
  return c ? CREDENTIAL_KIND_LABEL[c.kind] : "";
}

/** 把第 i 跳移动 d 位（-1 上移，+1 下移）；越界时原样返回 */
export function moveHop<T>(hops: T[], i: number, d: -1 | 1): T[] {
  const j = i + d;
  if (i < 0 || i >= hops.length || j < 0 || j >= hops.length) return hops;
  const next = [...hops];
  [next[i], next[j]] = [next[j], next[i]];
  return next;
}

export function removeHop<T>(hops: T[], i: number): T[] {
  return hops.filter((_, j) => j !== i);
}

/** 目标主机（最后一跳）的测试结果 */
export function targetResult(result: ServerTestResult, hopCount: number): HopResult | undefined {
  return result.hops?.find((h) => h.index === hopCount + 1);
}

const FAIL_REASONS: Partial<Record<HopFailReason, string>> = { fingerprint_unconfirmed: "首次连接，需要核对指纹" };

/** 失败原因的文字：有后端给的标签用标签，首次连接单独写 */
export function reasonText(r: HopResult): string {
  return (r.reason && FAIL_REASONS[r.reason]) || r.reason_label || "连接失败";
}
