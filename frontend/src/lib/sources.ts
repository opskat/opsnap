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

/** 链路中的一跳，从 OpsNap 出发按顺序排列；数据源链路的最后一跳是数据源本身，
 * kind 为其数据源类型（mysql / postgres / server_file），不限于通道类型 */
export interface ChannelHop {
  id: number;
  name: string;
  kind: string;
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
 * 前面各跳显示“名称 (类型)”，最后一跳（尚未保存的通道或数据源）显示地址。
 */
export function chainPreview(
  items: ChannelItem[],
  viaID: number,
  draft: { host: string; port: number },
  defaultPort: number
): { hopCount: number; tooLong: boolean; text: string } {
  const via = items.find((c) => c.id === viaID);
  const prior = via ? via.chain : [];
  const port = draft.port || defaultPort;
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

// ============ 数据源 ============

/** 数据源类型：MySQL、PostgreSQL、服务器文件 */
export type DataSourceKind = "mysql" | "postgres" | "server_file";
/** TLS 模式：不加密 / 优先加密（默认） / 必须加密 / 校验 CA / 校验 CA 与主机名 */
export type TLSMode = "disable" | "prefer" | "require" | "verify_ca" | "verify_full";
/** 数据源状态与通道共用同一组值 */
export type DataSourceStatus = ChannelStatus;

export const DATASOURCE_DEFAULT_PORT: Record<DataSourceKind, number> = {
  mysql: 3306,
  postgres: 5432,
  server_file: 22,
};

export const TLS_MODES: TLSMode[] = ["disable", "prefer", "require", "verify_ca", "verify_full"];

/** 数据源设置；测试连接、新建与编辑共用 */
export interface DataSourceForm {
  name: string;
  kind: DataSourceKind;
  host: string;
  /** 0 表示使用默认端口 */
  port: number;
  username: string;
  /** 服务器文件的认证方式 password / key；MySQL / PostgreSQL 忽略（只有密码） */
  auth_method: string;
  /** 编辑时留空表示沿用已保存的值 */
  password: string;
  private_key: string;
  passphrase: string;
  /** PostgreSQL 的连接数据库，为空时为 postgres */
  database: string;
  tls_mode: TLSMode;
  /** CA 证书（PEM），非秘密，编辑时已回填已保存的值 */
  tls_ca: string;
  /** 客户端证书（PEM），非秘密，编辑时已回填已保存的值 */
  tls_client_cert: string;
  /** 客户端私钥（PEM）；编辑时留空且仍填写了客户端证书，表示沿用已保存的私钥 */
  tls_client_key: string;
  /** 0 表示从 OpsNap 直连 */
  channel_id: number;
  /** 服务器文件：用户已信任的目标主机密钥指纹 */
  host_key: string;
}

export interface DataSourceTLS {
  version: string;
  verified: boolean;
}

export interface DataSourceServerInfo {
  version: string;
  system: string;
  tls: DataSourceTLS | null;
}

/** 最近一次测试失败的位置 */
export interface DataSourceFailedHop {
  hop: number;
  /** 失败的是链路中的通道时为通道 ID，失败的是数据源本身时为 0 */
  channel_id: number;
  name: string;
  kind: string;
}

/** 一段中英文文案 */
export interface ProbeText {
  zh_cn: string;
  en: string;
}

export type ProbeTier = "ok" | "warn" | "fail";

export interface ProbeItem {
  key: string;
  title: ProbeText;
  tier: ProbeTier;
  detail: ProbeText;
  fix: ProbeText;
  tables?: string[];
  table_count?: number;
}

export type ProbeState = "probing" | "done" | "unprobeable";

/** 能力探测的摘要与结果；数据源尚未探测过时为 null */
export interface DataSourceProbe {
  state: ProbeState;
  ok: number;
  warn: number;
  fail: number;
  /** state 为 unprobeable 时的原因（原文，已去掉秘密） */
  error?: string;
  items?: ProbeItem[];
  /** 本次结果产生的时间；state 为 probing 时是上一次结果的时间（可能为 0） */
  time: number;
}

export interface DataSourceItem {
  id: number;
  name: string;
  kind: DataSourceKind;
  host: string;
  port: number;
  username: string;
  auth_method: string;
  has_password: boolean;
  has_private_key: boolean;
  has_passphrase: boolean;
  database: string;
  tls_mode: TLSMode;
  tls_ca: string;
  tls_client_cert: string;
  has_tls_client_key: boolean;
  channel_id: number;
  /** mysql://host:port、postgres://host:port 或 ssh://user@host:port */
  address: string;
  /** 从 OpsNap 出发的完整链路，最后一跳是数据源本身；直连时只有数据源本身 */
  chain: ChannelHop[];
  server: DataSourceServerInfo | null;
  host_key: string;
  presented_host_key: string;
  status: DataSourceStatus;
  status_message: string;
  failed_hop: DataSourceFailedHop | null;
  checked_at: number;
  created_at: number;
  probe: DataSourceProbe | null;
}

/** 测试或保存的响应：host_key 非空表示暂停等待确认主机密钥，此时没有保存 */
export interface DataSourceSaveResult {
  item: DataSourceItem | null;
  host_key: HostKeyPrompt | null;
}

export const emptyDataSourceForm = (kind: DataSourceKind = "mysql"): DataSourceForm => ({
  name: "",
  kind,
  host: "",
  port: 0,
  username: "",
  auth_method: "password",
  password: "",
  private_key: "",
  passphrase: "",
  database: "",
  tls_mode: "prefer",
  tls_ca: "",
  tls_client_cert: "",
  tls_client_key: "",
  channel_id: 0,
  host_key: "",
});

/** 编辑已有数据源时的初始表单：秘密留空表示沿用已保存的值，CA/客户端证书不是秘密，回填已保存的值 */
export function dataSourceFormOf(item: DataSourceItem): DataSourceForm {
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
    database: item.database,
    tls_mode: item.tls_mode || "prefer",
    tls_ca: item.tls_ca,
    tls_client_cert: item.tls_client_cert,
    tls_client_key: "",
    channel_id: item.channel_id,
    host_key: "",
  };
}

export function listDataSources() {
  return request<{ items: DataSourceItem[] }>("/datasources");
}

export function probeDataSource(body: { id?: number; data_source: DataSourceForm }) {
  return request<{ host_key: HostKeyPrompt | null; chain: ChannelHop[]; server: DataSourceServerInfo | null }>(
    "/datasources/probe",
    { method: "POST", body: JSON.stringify(body) }
  );
}

export function createDataSource(data_source: DataSourceForm) {
  return request<DataSourceSaveResult>("/datasources", { method: "POST", body: JSON.stringify({ data_source }) });
}

export function updateDataSource(id: number, data_source: DataSourceForm) {
  return request<DataSourceSaveResult>(`/datasources/${id}`, {
    method: "PUT",
    body: JSON.stringify({ data_source }),
  });
}

export function testDataSource(id: number) {
  return request<{ item: DataSourceItem; host_key: HostKeyPrompt | null }>(`/datasources/${id}/test`, {
    method: "POST",
  });
}

export function confirmDataSourceHostKey(id: number, fingerprint: string) {
  return request<DataSourceSaveResult>(`/datasources/${id}/host-key`, {
    method: "POST",
    body: JSON.stringify({ fingerprint }),
  });
}

export function deleteDataSource(id: number) {
  return request<object>(`/datasources/${id}`, { method: "DELETE" });
}

/** 数据源表单实时链路预览：经由通道 + 数据源自身（尚未保存）作为最后一跳 */
export function dataSourceChainPreview(
  channels: ChannelItem[],
  channelID: number,
  draft: { kind: DataSourceKind; host: string; port: number }
): { hopCount: number; tooLong: boolean; text: string } {
  return chainPreview(channels, channelID, draft, DATASOURCE_DEFAULT_PORT[draft.kind]);
}

/** 数据源列表行的服务端版本文案：MySQL/PostgreSQL 显示“类型 版本号”，服务器文件显示 uname -sm 的结果；未测试成功时为空 */
export function dataSourceVersionLabel(item: DataSourceItem): string | undefined {
  if (!item.server) return undefined;
  if (item.kind === "server_file") return item.server.system || undefined;
  const label = item.kind === "mysql" ? "MySQL" : "PostgreSQL";
  return item.server.version ? `${label} ${item.server.version}` : undefined;
}

/** 数据源列表行的链路文案：不含数据源自身这一跳；直连时 names 为空 */
export function dataSourceChainNames(chain: ChannelHop[]): string[] {
  return chain.slice(0, -1).map((h) => h.name);
}

export type ProbeSummaryKind = "probing" | "unprobeable" | "ok" | "warn" | "fail";

export interface ProbeSummary {
  kind: ProbeSummaryKind;
  ok: number;
  warn: number;
  fail: number;
}

/** 列表行探测摘要的档位：无结果或正在探测时为 probing，连接失败/超时时为 unprobeable，
 * 否则按是否存在不可用、有风险项归为 fail / warn / ok（全部通过） */
export function probeSummaryOf(probe: DataSourceProbe | null): ProbeSummary {
  if (!probe || probe.state === "probing") return { kind: "probing", ok: 0, warn: 0, fail: 0 };
  if (probe.state === "unprobeable") return { kind: "unprobeable", ok: 0, warn: 0, fail: 0 };
  const { ok, warn, fail } = probe;
  const kind: ProbeSummaryKind = fail > 0 ? "fail" : warn > 0 ? "warn" : "ok";
  return { kind, ok, warn, fail };
}
