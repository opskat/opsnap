import type { RepoCreateInput, RepoParams, RepoType } from "./api";

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

/** 只带当前类型用到的地址参数，别的类型的残留输入不提交 */
export function buildParams(f: RepoForm, hostKey: string): Partial<RepoParams> {
  switch (f.type) {
    case "local":
      return { path: f.path.trim() };
    case "sftp":
      return {
        host: f.host.trim(),
        port: sftpPort(f),
        path: f.path.trim(),
        username: f.username.trim(),
        password: f.password,
        private_key: f.private_key,
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
        secret_access_key: f.secret_access_key,
        path_style: f.path_style,
      };
    case "rest":
      return { url: f.url.trim(), username: f.username.trim(), password: f.password };
    case "webdav":
      return {
        url: f.url.trim(),
        path: f.path.trim(),
        vendor: f.vendor.trim(),
        username: f.username.trim(),
        password: f.password,
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
