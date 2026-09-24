import { request } from "@/lib/api";

export type StorageKind = "local" | "s3";
export type StorageStatus = "ok" | "wrong_key" | "unreachable";
/** 测试连接时目标位置的情况 */
export type ProbeState = "empty" | "repository" | "not_empty";

/** 新建、编辑与测试连接共用的位置参数 */
export interface StorageLocation {
  kind: StorageKind;
  path: string;
  endpoint: string;
  region: string;
  bucket: string;
  prefix: string;
  access_key: string;
  /** 编辑时留空表示沿用已保存的值 */
  secret_key: string;
  use_tls: boolean;
  skip_verify: boolean;
}

export interface Storage {
  id: number;
  name: string;
  kind: StorageKind;
  path: string;
  endpoint: string;
  region: string;
  bucket: string;
  prefix: string;
  access_key: string;
  has_secret_key: boolean;
  use_tls: boolean;
  skip_verify: boolean;
  /** 本地为绝对路径，S3 为 s3://<bucket>/<前缀> */
  location: string;
  fingerprint: string;
  encryption: string;
  status: StorageStatus;
  /** 无法连接或密钥不正确时的原因 */
  status_message: string;
  /** Unix 秒 */
  checked_at: number;
  created_at: number;
}

export interface ProbeResult {
  state: ProbeState;
  /** 仓库格式文件的写入时间（Unix 秒），取不到时为 0 */
  created_at: number;
  location: string;
  /** 编辑时位置是否与已保存的不同 */
  location_changed: boolean;
}

export interface KeyInfo {
  key: string;
  fingerprint: string;
  encryption: string;
}

export const MIN_KEY_LENGTH = 12;

export const emptyLocation = (): StorageLocation => ({
  kind: "local",
  path: "",
  endpoint: "",
  region: "",
  bucket: "",
  prefix: "",
  access_key: "",
  secret_key: "",
  use_tls: true,
  skip_verify: false,
});

export function locationOf(s: Storage): StorageLocation {
  return {
    kind: s.kind,
    path: s.path,
    endpoint: s.endpoint,
    region: s.region,
    bucket: s.bucket,
    prefix: s.prefix,
    access_key: s.access_key,
    secret_key: "",
    use_tls: s.use_tls,
    skip_verify: s.skip_verify,
  };
}

/** 最终位置预览：与后端规范化规则一致，前缀去掉首尾斜杠后以斜杠结尾 */
export function s3Location(bucket: string, prefix: string) {
  const p = prefix.trim().replace(/^\/+|\/+$/g, "");
  return `s3://${bucket.trim()}/${p ? `${p}/` : ""}`;
}

export function listStorages() {
  return request<{ items: Storage[] }>("/storages");
}

export function probeStorage(body: { id?: number; name: string; location: StorageLocation }) {
  return request<ProbeResult>("/storages/probe", { method: "POST", body: JSON.stringify(body) });
}

export function createStorage(body: { name: string; location: StorageLocation; key: string; confirm_saved: boolean }) {
  return request<{ item: Storage; snapshots: number }>("/storages", { method: "POST", body: JSON.stringify(body) });
}

export function updateStorage(
  id: number,
  body: { name: string; location: StorageLocation; key?: string; confirm_location_change: boolean }
) {
  return request<{ item: Storage; snapshots: number }>(`/storages/${id}`, {
    method: "PUT",
    body: JSON.stringify(body),
  });
}

export function testStorage(id: number) {
  return request<{ item: Storage; snapshots: number }>(`/storages/${id}/test`, { method: "POST" });
}

export function unlockStorage(id: number, key: string) {
  return request<{ item: Storage; snapshots: number }>(`/storages/${id}/unlock`, {
    method: "POST",
    body: JSON.stringify({ key }),
  });
}

export function deleteStorage(id: number) {
  return request<object>(`/storages/${id}`, { method: "DELETE" });
}

/** 不传 key 时生成新密钥；传入时计算它的指纹 */
export function keyInfo(key?: string) {
  return request<KeyInfo>("/storages/key", { method: "POST", body: JSON.stringify({ key: key ?? "" }) });
}

export type DirStatus = "empty" | "repository" | "not_empty" | "not_writable" | "no_access";

export interface Dir {
  name: string;
  path: string;
  status: DirStatus;
}

/** 查看密钥（仅浏览器会话）：密码登录开启时需提交管理员密码，关闭时需先完成 OIDC 再次验证 */
export function revealKey(id: number, password = "") {
  return request<{ key: string; fingerprint: string }>(`/storages/${id}/reveal`, {
    method: "POST",
    body: JSON.stringify({ password }),
  });
}

/** OIDC 再次验证：浏览器跳转到这里，回来后回到 next，5 分钟内可以查看一次密钥 */
export function reauthURL(next: string) {
  return `/api/v1/auth/oidc/reauth?next=${encodeURIComponent(next)}`;
}

/** 浏览 OpsNap 主机上的目录；path 为空或不存在时打开数据目录的上级目录 */
export function listDirs(path: string) {
  return request<{ path: string; parent: string; dirs: Dir[] }>(`/storages/dirs?path=${encodeURIComponent(path)}`);
}

export function makeDir(parent: string, name: string) {
  return request<{ path: string }>("/storages/dirs", { method: "POST", body: JSON.stringify({ parent, name }) });
}

/** 密钥文件中密钥所在行的固定前缀，上传密钥文件时据此取出密钥 */
const KEY_LINE = "Key: ";

export function keyFileName(storageName: string) {
  return `opsnap-${storageName.trim().replace(/[\\/:*?"<>|\s]+/g, "-")}-key.txt`;
}

/** 用 kopia 命令行连接仓库的示例命令；Secret Key 以占位符代替 */
export function kopiaConnectCommand(loc: StorageLocation) {
  if (loc.kind === "local") return `kopia repository connect filesystem --path ${loc.path.trim()}`;
  const prefix = loc.prefix.trim().replace(/^\/+|\/+$/g, "");
  const args = [
    "kopia repository connect s3",
    `--endpoint ${loc.endpoint.trim()}`,
    `--bucket ${loc.bucket.trim()}`,
    prefix && `--prefix ${prefix}/`,
    loc.region.trim() && `--region ${loc.region.trim()}`,
    `--access-key ${loc.access_key.trim()}`,
    "--secret-access-key <Secret Key>",
    !loc.use_tls && "--disable-tls",
    loc.skip_verify && "--disable-tls-verification",
  ];
  return args.filter(Boolean).join(" ");
}

/** 密钥文件的文本；labels 为界面语言的说明文字 */
export function keyFileContent(
  labels: { title: string; storage: string; location: string; fingerprint: string; command: string },
  info: { name: string; location: StorageLocation; key: string; fingerprint: string }
) {
  const loc = info.location;
  const where =
    loc.kind === "local" ? loc.path.trim() : `${s3Location(loc.bucket, loc.prefix)} (${loc.endpoint.trim()})`;
  return [
    `# ${labels.title}`,
    "",
    `${labels.storage}: ${info.name}`,
    `${labels.location}: ${where}`,
    `${labels.fingerprint}: ${info.fingerprint}`,
    "",
    `${KEY_LINE}${info.key}`,
    "",
    `# ${labels.command}`,
    kopiaConnectCommand(loc),
    "",
  ].join("\n");
}

/** 从上传的文件中取出密钥：OpsNap 导出的密钥文件取 "Key: " 行，否则整个文件（去掉首尾空白）就是密钥 */
export function parseKeyFile(text: string) {
  const line = text.split(/\r?\n/).find((l) => l.startsWith(KEY_LINE));
  return (line ? line.slice(KEY_LINE.length) : text).trim();
}

/** 让浏览器下载一个文本文件 */
export function downloadText(filename: string, content: string) {
  const url = URL.createObjectURL(new Blob([content], { type: "text/plain;charset=utf-8" }));
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  a.click();
  URL.revokeObjectURL(url);
}
