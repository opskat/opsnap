import { SECRET_MASKED } from "./api";
import type { RepoCreateInput, RepoItem, RepoParams, RepoType, RepoUpdateInput } from "./api";

export const REPO_TYPE_LABEL: Record<RepoType, string> = {
  local: "本地目录",
  sftp: "SFTP",
  s3: "S3 兼容",
  rest: "REST Server",
  webdav: "WebDAV",
};

/** 添加表单里所有文本输入（端口也按文本编辑） */
export interface RepoForm {
  name: string;
  type: RepoType;
  path: string;
  host: string;
  port: string;
  username: string;
  password: string;
  private_key: string;
  server_id: number;
  endpoint: string;
  bucket: string;
  prefix: string;
  region: string;
  access_key_id: string;
  secret_access_key: string;
  path_style: boolean;
  url: string;
  vendor: string;
  existing_password: string;
}

export function emptyRepoForm(): RepoForm {
  return {
    name: "",
    type: "sftp",
    path: "",
    host: "",
    port: "22",
    username: "",
    password: "",
    private_key: "",
    server_id: 0,
    endpoint: "",
    bucket: "",
    prefix: "",
    region: "",
    access_key_id: "",
    secret_access_key: "",
    path_style: false,
    url: "",
    vendor: "",
    existing_password: "",
  };
}

export function sftpPort(f: Pick<RepoForm, "port">): number {
  const n = Number.parseInt(f.port, 10);
  return Number.isFinite(n) && n > 0 ? n : 22;
}

/** 用已保存的存储位置预填表单；凭据不返回内容，留空表示保留 */
export function formFromRepo(r: RepoItem): RepoForm {
  const p = r.params;
  return {
    ...emptyRepoForm(),
    name: r.name,
    type: r.type,
    path: p.path,
    host: p.host,
    port: String(p.port || 22),
    username: p.username,
    server_id: p.server_id,
    endpoint: p.endpoint,
    bucket: p.bucket,
    prefix: p.prefix,
    region: p.region,
    access_key_id: p.access_key_id,
    path_style: p.path_style,
    url: p.url,
    vendor: p.vendor,
  };
}

/** 哪些凭据已经有保存的值；修改时留空才换成占位，否则留空就是不设 */
export interface SavedCredentials {
  password: boolean;
  private_key: boolean;
  secret_access_key: boolean;
}

export const NOTHING_SAVED: SavedCredentials = { password: false, private_key: false, secret_access_key: false };

/** 接口只把已保存的凭据回填成占位，据此判断哪些字段留空表示保留 */
export function savedCredentialsOf(r: RepoItem): SavedCredentials {
  const p = r.params;
  return {
    password: p.password === SECRET_MASKED,
    private_key: p.private_key === SECRET_MASKED,
    secret_access_key: p.secret_access_key === SECRET_MASKED,
  };
}

/** 只带当前类型用到的地址参数，别的类型的残留输入不提交。saved 里为真的凭据留空时换成占位，表示保留已保存的值 */
export function buildParams(
  f: RepoForm,
  hostKey: string,
  saved: SavedCredentials = NOTHING_SAVED
): Partial<RepoParams> {
  const keep = (v: string, isSaved: boolean) => (isSaved && v === "" ? SECRET_MASKED : v);
  switch (f.type) {
    case "local":
      return { path: f.path.trim() };
    case "sftp":
      return {
        host: f.host.trim(),
        port: sftpPort(f),
        path: f.path.trim(),
        username: f.username.trim(),
        password: keep(f.password, saved.password),
        private_key: keep(f.private_key, saved.private_key),
        server_id: f.server_id,
        host_key: hostKey,
      };
    case "s3":
      return {
        endpoint: f.endpoint.trim(),
        bucket: f.bucket.trim(),
        prefix: f.prefix.trim(),
        region: f.region.trim(),
        access_key_id: f.access_key_id.trim(),
        secret_access_key: keep(f.secret_access_key, saved.secret_access_key),
        path_style: f.path_style,
      };
    case "rest":
      return { url: f.url.trim(), username: f.username.trim(), password: keep(f.password, saved.password) };
    case "webdav":
      return {
        url: f.url.trim(),
        path: f.path.trim(),
        vendor: f.vendor.trim(),
        username: f.username.trim(),
        password: keep(f.password, saved.password),
      };
  }
}

export function buildCreateInput(
  f: RepoForm,
  hostKey: string,
  recovery: { enabled: boolean; value: string | null; saved: boolean }
): RepoCreateInput {
  const input: RepoCreateInput = {
    name: f.name.trim(),
    type: f.type,
    params: buildParams(f, hostKey),
    recovery_key: recovery.enabled,
    recovery_key_value: recovery.enabled ? (recovery.value ?? "") : "",
    recovery_key_saved: recovery.enabled && recovery.saved,
  };
  if (f.existing_password) input.existing_password = f.existing_password;
  return input;
}

/** 修改存储位置：类型和恢复密钥都不变，留空的凭据换成占位以保留已保存的值 */
export function buildUpdateInput(f: RepoForm, hostKey: string, saved: SavedCredentials): RepoUpdateInput {
  return { name: f.name.trim(), params: buildParams(f, hostKey, saved) };
}
