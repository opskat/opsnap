import { request } from "@/lib/api";

/** 通道类型：SSH 跳板 / SOCKS5 代理 */
export type ChannelKind = "ssh" | "socks5";
/** SSH 的认证方式 password / key；SOCKS5 为 none / password（按是否填写用户名决定） */
export type ChannelAuthMethod = "password" | "key" | "none";
export type ChannelStatus = "ok" | "unreachable" | "host_key_changed";

export const DEFAULT_PORT: Record<ChannelKind, number> = { ssh: 22, socks5: 1080 };
/** 从 OpsNap 出发的整条链路最多的跳数 */
export const MAX_HOPS = 5;

/** 通道设置；测试连接、新建与编辑共用 */
export interface ChannelForm {
  name: string;
  kind: ChannelKind;
  host: string;
  /** 0 表示使用默认端口 */
  port: number;
  username: string;
  auth_method: string;
  /** 编辑时留空表示沿用已保存的值 */
  password: string;
  private_key: string;
  passphrase: string;
  /** 0 表示从 OpsNap 直连 */
  via_id: number;
  /** 用户已信任的 SSH 主机密钥指纹；为空时编辑沿用已保存的（主机与端口未变时） */
  host_key: string;
}

export interface ChannelRef {
  id: number;
  name: string;
}

export interface ChannelUsedBy {
  data_sources: ChannelRef[];
  channels: ChannelRef[];
}

/** 链路中的一跳，从 OpsNap 出发按顺序排列 */
export interface ChannelHop {
  id: number;
  name: string;
  kind: ChannelKind;
  address: string;
}

/** 待用户确认的 SSH 主机密钥 */
export interface HostKeyPrompt {
  /** 这台主机是链路的第几跳（从 1 开始） */
  hop: number;
  name: string;
  address: string;
  key_type: string;
  fingerprint: string;
  /** true 表示与保存的指纹不一致（saved 为保存的指纹），否则为首次连接 */
  changed: boolean;
  saved: string;
}

export interface ChannelItem {
  id: number;
  name: string;
  kind: ChannelKind;
  host: string;
  port: number;
  username: string;
  auth_method: string;
  has_password: boolean;
  has_private_key: boolean;
  has_passphrase: boolean;
  via_id: number;
  address: string;
  /** 从 OpsNap 出发到本通道（含）的完整链路 */
  chain: ChannelHop[];
  host_key: string;
  presented_host_key: string;
  used_by: ChannelUsedBy;
  status: ChannelStatus;
  status_message: string;
  checked_at: number;
  created_at: number;
}

/** 测试或保存的响应：host_key 非空表示暂停等待确认主机密钥，此时没有保存 */
export interface ChannelSaveResult {
  item: ChannelItem | null;
  host_key: HostKeyPrompt | null;
}

export const emptyChannelForm = (kind: ChannelKind = "ssh"): ChannelForm => ({
  name: "",
  kind,
  host: "",
  port: 0,
  username: "",
  auth_method: kind === "ssh" ? "password" : "none",
  password: "",
  private_key: "",
  passphrase: "",
  via_id: 0,
  host_key: "",
});

/** 编辑已有通道时的初始表单：秘密留空表示沿用已保存的值 */
export function channelFormOf(item: ChannelItem): ChannelForm {
  return {
    name: item.name,
    kind: item.kind,
    host: item.host,
    port: item.port,
    username: item.username,
    auth_method: item.auth_method,
    password: "",
    private_key: "",
    passphrase: "",
    via_id: item.via_id,
    host_key: "",
  };
}

export function listChannels() {
  return request<{ items: ChannelItem[] }>("/channels");
}

export function probeChannel(body: { id?: number; channel: ChannelForm }) {
  return request<{ host_key: HostKeyPrompt | null; chain: ChannelHop[] }>("/channels/probe", {
    method: "POST",
    body: JSON.stringify(body),
  });
}

export function createChannel(channel: ChannelForm) {
  return request<ChannelSaveResult>("/channels", { method: "POST", body: JSON.stringify({ channel }) });
}

export function updateChannel(id: number, channel: ChannelForm) {
  return request<ChannelSaveResult>(`/channels/${id}`, { method: "PUT", body: JSON.stringify({ channel }) });
}

export function testChannel(id: number) {
  return request<{ item: ChannelItem; host_key: HostKeyPrompt | null }>(`/channels/${id}/test`, { method: "POST" });
}

export function confirmChannelHostKey(id: number, fingerprint: string) {
  return request<ChannelSaveResult>(`/channels/${id}/host-key`, {
    method: "POST",
    body: JSON.stringify({ fingerprint }),
  });
}

export function deleteChannel(id: number) {
  return request<object>(`/channels/${id}`, { method: "DELETE" });
}

/** 经由选择框可选项：排除自己，也排除会形成环路的通道（它的链路已经过正在编辑的通道） */
export function availableVia(items: ChannelItem[], editingID?: number): ChannelItem[] {
  return items.filter((c) => c.id !== editingID && !(editingID && c.chain.some((h) => h.id === editingID)));
}

/** 通道之间引用（used_by）之外，供删除保护展示的名称列表 */
export function usedByNames(usedBy: ChannelUsedBy): string[] {
  return [...usedBy.data_sources, ...usedBy.channels].map((r) => r.name);
}

/**
 * 表单实时链路预览：未提交测试前，根据经由通道与草稿字段本地计算，不请求服务端。
 * 前面各跳显示“名称 (类型)”，最后一跳（本通道，尚未保存）显示地址。
 */
export function chainPreview(
  items: ChannelItem[],
  viaID: number,
  draft: { kind: ChannelKind; host: string; port: number }
): { hopCount: number; tooLong: boolean; text: string } {
  const via = items.find((c) => c.id === viaID);
  const prior = via ? via.chain : [];
  const port = draft.port || DEFAULT_PORT[draft.kind];
  const parts = [
    "OpsNap",
    ...prior.map((h) => `${h.name} (${h.kind.toUpperCase()})`),
    draft.host.trim() ? `${draft.host.trim()}:${port}` : "?",
  ];
  return { hopCount: prior.length + 1, tooLong: prior.length + 1 > MAX_HOPS, text: parts.join(" → ") };
}

/**
 * 按主机密钥确认循环调用 run：host_key 非空时向 ask 请求用户确认，
 * 用户信任后带上指纹重试，取消则中止并返回 undefined。
 */
export async function withHostKeyConfirm<T extends { host_key: HostKeyPrompt | null }>(
  run: (hostKey: string) => Promise<T>,
  ask: (prompt: HostKeyPrompt) => Promise<boolean>
): Promise<T | undefined> {
  let hostKey = "";
  for (;;) {
    const res = await run(hostKey);
    if (!res.host_key) return res;
    const trust = await ask(res.host_key);
    if (!trust) return undefined;
    hostKey = res.host_key.fingerprint;
  }
}
