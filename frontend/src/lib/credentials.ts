// 密钥表单的数据模型：从接口数据生成表单、校验并生成提交体，以及列表里的使用者摘要。
// 纯函数，不依赖界面（只有写剪贴板与算指纹需要浏览器 API）。
import { copyText } from "./clipboard";
import {
  errorMessage,
  type CredentialInput,
  type CredentialItem,
  type CredentialKind,
  type CredentialServerRef,
} from "./api";

/** 名称最长字数 */
export const CREDENTIAL_NAME_MAX = 64;
/** 默认用户最长字数 */
export const CREDENTIAL_USERNAME_MAX = 64;
/** 备注最长字数 */
export const CREDENTIAL_NOTE_MAX = 200;
/** 列表里最多显示几个使用者，更多的折成「等 N 台」 */
export const USED_SERVERS_SHOWN = 3;

export interface CredentialForm {
  name: string;
  kind: CredentialKind;
  password: string;
  privateKey: string;
  passphrase: string;
  username: string;
  note: string;
  /**
   * 已经保存过的内容，按内容分别记：编辑时对应输入框留空表示保留。
   * 分类型记是为了切换类型后切回来仍然知道原来保存了什么。
   */
  saved: { password: boolean; privateKey: boolean; passphrase: boolean };
}

export function emptyForm(): CredentialForm {
  return {
    name: "",
    kind: "password",
    password: "",
    privateKey: "",
    passphrase: "",
    username: "",
    note: "",
    saved: { password: false, privateKey: false, passphrase: false },
  };
}

export function formFromItem(c: CredentialItem): CredentialForm {
  return {
    name: c.name,
    kind: c.kind,
    password: "",
    privateKey: "",
    passphrase: "",
    username: c.username,
    note: c.note,
    saved: {
      password: c.kind === "password" && c.has_secret,
      privateKey: c.kind === "ssh_key" && c.has_secret,
      passphrase: c.kind === "ssh_key" && c.has_passphrase,
    },
  };
}

/** 切换类型：清空不属于新类型的内容，另一侧的内容和已保存标记都不再提交 */
export function switchKind(f: CredentialForm, kind: CredentialKind): CredentialForm {
  if (f.kind === kind) return f;
  if (kind === "password") return { ...f, kind, privateKey: "", passphrase: "" };
  return { ...f, kind, password: "" };
}

/** 名称按字（码点）计数，一个汉字算一个字 */
function len(s: string): number {
  return [...s].length;
}

/** 校验并生成提交体；留空的内容按空串提交，由服务端理解成「保留已保存的值」 */
export function buildInput(f: CredentialForm): { input?: CredentialInput; error?: string } {
  const name = f.name.trim();
  if (!name) return { error: "填写密钥名称" };
  if (len(name) > CREDENTIAL_NAME_MAX) return { error: `名称最长 ${CREDENTIAL_NAME_MAX} 个字` };
  const username = f.username.trim();
  if (len(username) > CREDENTIAL_USERNAME_MAX) return { error: `默认用户最长 ${CREDENTIAL_USERNAME_MAX} 个字` };
  const note = f.note.trim();
  if (len(note) > CREDENTIAL_NOTE_MAX) return { error: `备注最长 ${CREDENTIAL_NOTE_MAX} 个字` };

  if (f.kind === "password") {
    if (!f.password && !f.saved.password) return { error: "请填写密码" };
    return {
      input: { name, kind: "password", password: f.password, private_key: "", passphrase: "", username, note },
    };
  }
  if (!f.privateKey && !f.saved.privateKey) return { error: "请粘贴私钥" };
  return {
    input: {
      name,
      kind: "ssh_key",
      password: "",
      private_key: f.privateKey,
      passphrase: f.passphrase,
      username,
      note,
    },
  };
}

/** 列表里的使用者：最多 3 个名字，其余折成「等 N 台」 */
export function usedServers(c: CredentialItem): { names: string[]; more: number; total: number } {
  const all = c.servers ?? [];
  return {
    names: all.slice(0, USED_SERVERS_SHOWN).map((s) => s.name),
    more: Math.max(0, all.length - USED_SERVERS_SHOWN),
    total: all.length,
  };
}

/** 一台使用者的「用户@地址」，抽屉里用来说明是哪台机器 */
export function serverLabel(s: CredentialServerRef): string {
  return `${s.username}@${s.host}${s.port ? `:${s.port}` : ""}`;
}

/** 被服务器使用时删除受阻的原因；可以删除时为 null */
export function deleteBlockedReason(c: CredentialItem): string | null {
  const names = (c.servers ?? []).map((s) => s.name);
  if (!names.length) return null;
  return `有 ${names.length} 台服务器使用这条密钥：${names.join("、")}。先在那些服务器上换成别的凭据，再删除。`;
}

/** 没有被服务器使用时才能删除 */
export function canDelete(c: CredentialItem): boolean {
  return deleteBlockedReason(c) === null;
}

// ---- 「公钥 / 密码」列的复制与公钥块 ----

/** 列表里密码类型的掩码文字 */
export const PASSWORD_MASKED = "密码 ·••••••••";

/**
 * 「公钥 / 密码」列要显示的内容：私钥显示公钥明文，密码显示掩码。
 * 没有内容时为 null，界面显示「—」并禁用复制。
 */
export function secretPreview(c: CredentialItem): string | null {
  if (!c.has_secret) return null;
  if (c.kind === "ssh_key") return c.public_key || null;
  return PASSWORD_MASKED;
}

/** 从私钥算公钥的结果：成功给出一行 authorized_keys；失败说明原因 */
export type PublicKeyResult =
  | { ok: true; publicKey: string }
  /** format：内容不像私钥；unsupported：前端不认识的私钥格式（例如 PEM 的 RSA） */
  | { ok: false; reason: "format" | "unsupported" };

const OPENSSH_MAGIC = "openssh-key-v1\0";

/** 读一个 length-prefixed 字段（OpenSSH 的字符串编码：4 字节大端长度 + 内容） */
function readSSHString(b: Uint8Array, at: number): { bytes: Uint8Array; next: number } | null {
  if (at + 4 > b.length) return null;
  const len = new DataView(b.buffer, b.byteOffset, b.byteLength).getUint32(at);
  if (at + 4 + len > b.length) return null;
  return { bytes: b.subarray(at + 4, at + 4 + len), next: at + 4 + len };
}

function base64Of(b: Uint8Array): string {
  let s = "";
  // 分块拼接，私钥不大但避免一次 spread 太多参数
  for (let i = 0; i < b.length; i += 4096) s += String.fromCharCode(...b.subarray(i, i + 4096));
  return btoa(s);
}

/**
 * 由 OpenSSH 私钥算出 authorized_keys 一行。
 * OpenSSH 格式把公钥放在加密段之外，所以带口令的私钥也能算出公钥，不需要口令。
 * 不认识的格式（PEM 的 RSA 等）返回 unsupported，交给后端在保存时报错，前端不硬猜。
 */
export function publicKeyOfPrivateKey(privateKey: string): PublicKeyResult {
  const text = privateKey.trim();
  const m = /^-----BEGIN ([A-Z0-9 ]+)-----[\s\S]*-----END \1-----$/.exec(text);
  if (!m) return { ok: false, reason: "format" };
  if (m[1] !== "OPENSSH PRIVATE KEY") return { ok: false, reason: "unsupported" };

  const body = text.replace(/-----[^-]+-----/g, "").replace(/\s+/g, "");
  let raw: Uint8Array;
  try {
    const bin = atob(body);
    raw = new Uint8Array(bin.length);
    for (let i = 0; i < bin.length; i++) raw[i] = bin.charCodeAt(i);
  } catch {
    return { ok: false, reason: "format" };
  }

  const magic = new TextDecoder().decode(raw.subarray(0, OPENSSH_MAGIC.length));
  if (magic !== OPENSSH_MAGIC) return { ok: false, reason: "format" };

  // 跳过 cipher、kdf、kdf options，再读公钥条数与该条的 blob
  let at = OPENSSH_MAGIC.length;
  for (let i = 0; i < 3; i++) {
    const f = readSSHString(raw, at);
    if (!f) return { ok: false, reason: "format" };
    at = f.next;
  }
  if (at + 4 > raw.length) return { ok: false, reason: "format" };
  const count = new DataView(raw.buffer, raw.byteOffset, raw.byteLength).getUint32(at);
  if (count !== 1) return { ok: false, reason: "format" };
  const blob = readSSHString(raw, at + 4);
  if (!blob) return { ok: false, reason: "format" };
  const typeField = readSSHString(blob.bytes, 0);
  if (!typeField) return { ok: false, reason: "format" };
  const type = new TextDecoder().decode(typeField.bytes);
  if (!type) return { ok: false, reason: "format" };
  return { ok: true, publicKey: `${type} ${base64Of(blob.bytes)}` };
}

/**
 * 公钥的 SHA256 指纹，与 `ssh-keygen -lf` 一致：对公钥 blob（base64 解出来的那一段）
 * 求 sha256，写成 `SHA256:<base64 去掉尾部等号>`。算不出来时为 null，不显示假的。
 */
export async function fingerprintOfPublicKey(publicKey: string): Promise<string | null> {
  const b64 = publicKey.trim().split(/\s+/)[1];
  if (!b64) return null;
  try {
    const bin = atob(b64);
    const raw = new Uint8Array(bin.length);
    for (let i = 0; i < bin.length; i++) raw[i] = bin.charCodeAt(i);
    const digest = await crypto.subtle.digest("SHA-256", raw);
    return `SHA256:${btoa(String.fromCharCode(...new Uint8Array(digest))).replace(/=+$/, "")}`;
  } catch {
    return null;
  }
}

/** 抽屉里公钥块显示什么；来源标注决定用哪一句说明 */
export type PublicKeyView =
  | { kind: "empty" }
  /** 编辑时私钥框留空：显示已保存私钥对应的公钥 */
  | { kind: "saved"; publicKey: string }
  /** 由输入框里的私钥算出，还没保存 */
  | { kind: "derived"; publicKey: string }
  /** 粘贴的内容不像私钥 */
  | { kind: "invalid" }
  /** 前端不认识的私钥格式，没法自己算公钥 */
  | { kind: "unsupported" };

/** 公钥块右上角的来源标注：说清这块公钥是从哪来的 */
export const PUBLIC_KEY_SOURCE = {
  saved: "已保存的私钥对应的公钥",
  derived: "由刚粘贴的私钥算出，还没保存",
} as const;

/** 公钥块里没有公钥可显示时的说明 */
export const PUBLIC_KEY_HINT = {
  empty: "粘贴私钥后这里会显示公钥",
  invalid: "私钥格式看起来不对",
  unsupported: "前端没法从这种私钥算公钥，保存后这里会显示",
} as const;

/**
 * 公钥块的状态：私钥框有内容就按它算；留空时编辑显示已保存的公钥，新建则提示粘贴。
 * 私钥框有内容但解析失败时，绝不显示旧公钥，避免把错的公钥贴到目标机。
 */
export function publicKeyView({
  item,
  privateKey,
}: {
  item: CredentialItem | null;
  privateKey: string;
}): PublicKeyView {
  if (privateKey.trim()) {
    const r = publicKeyOfPrivateKey(privateKey);
    if (r.ok) return { kind: "derived", publicKey: r.publicKey };
    return { kind: r.reason === "format" ? "invalid" : "unsupported" };
  }
  if (item?.public_key) return { kind: "saved", publicKey: item.public_key };
  return { kind: "empty" };
}

/** 下载 .pub 的文件名：用密钥名，去掉文件系统里的非法字符 */
export function publicKeyFilename(name: string): string {
  const base = name
    .trim()
    .replace(/[\\/:*?"<>|]+/g, "-")
    .replace(/^-+|-+$/g, "");
  return `${base || "id_ed25519"}.pub`;
}

/** 写剪贴板的结果；失败时 error 是可以直接展示的中文说明 */
export type CopyResult = { ok: true } | { ok: false; error: string };

/**
 * 取内容并写进剪贴板。内容由 get 现取（密码要经 reveal 接口拿明文），
 * 取不到或浏览器不允许写剪贴板都返回原因，由界面提示，不像 useCopy 那样静默。
 * 写剪贴板本身见 copyText：http 访问时没有 Clipboard API，会退回旧通路。
 */
export async function writeClipboard(get: () => string | Promise<string>): Promise<CopyResult> {
  let text: string;
  try {
    text = await get();
  } catch (err) {
    return { ok: false, error: `复制失败：${errorMessage(err)}` };
  }
  if (!text) return { ok: false, error: "复制失败：没有可复制的内容" };
  if (!(await copyText(text))) {
    return { ok: false, error: "复制失败：浏览器不允许访问剪贴板，请手动选中后复制" };
  }
  return { ok: true };
}

/** reveal 的响应里取这一条的可复制内容：密码或私钥，没有内容时为空串 */
export function revealText(r: { password?: string; private_key?: string }): string {
  return r.password || r.private_key || "";
}
