// 密钥表单的数据模型：从接口数据生成表单、校验并生成提交体，以及列表里的使用者摘要。
// 纯函数，不依赖界面。
import type { CredentialInput, CredentialItem, CredentialKind } from "./api";

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
