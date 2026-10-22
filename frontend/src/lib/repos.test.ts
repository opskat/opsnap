import { describe, expect, it } from "vitest";
import { SECRET_MASKED, type RepoItem } from "./api";
import { buildUpdateInput, formFromRepo, savedCredentialsOf, sftpPort } from "./repos";

const REPO: RepoItem = {
  id: 1,
  name: "群晖 DS920+",
  type: "sftp",
  params: {
    path: "/volume1/backup/opsnap",
    host: "192.168.1.20",
    port: 22,
    username: "backup",
    password: SECRET_MASKED,
    private_key: "",
    host_key: "ssh-ed25519 AAAAC3 nas",
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
  },
  address: "sftp:backup@192.168.1.20:/volume1/backup/opsnap",
  encryption: "AES-256",
  status: "ok",
  status_message: "",
  status_at: 0,
  stored_size: 0,
  raw_size: 0,
  dedup_ratio: 0,
  snapshot_count: 0,
  free_space: null,
  key_count: 2,
  stats_at: 0,
  no_recovery_key: false,
  last_check_at: 0,
  last_check_ok: false,
  last_check_message: "",
  last_prune_at: 0,
  last_prune_ok: false,
  last_prune_message: "",
  maintenance: "",
  job_count: 0,
  created_at: 0,
  updated_at: 0,
};

describe("formFromRepo", () => {
  it("带出已保存的地址参数，凭据留空", () => {
    const f = formFromRepo(REPO);
    expect(f).toMatchObject({
      name: "群晖 DS920+",
      type: "sftp",
      host: "192.168.1.20",
      port: "22",
      path: "/volume1/backup/opsnap",
      username: "backup",
      server_id: 0,
    });
    // 凭据和恢复密钥相关的字段都不预填
    expect(f.password).toBe("");
    expect(f.private_key).toBe("");
    expect(f.existing_password).toBe("");
  });

  it("端口为 0 时按默认的 22 显示", () => {
    const f = formFromRepo({ ...REPO, params: { ...REPO.params, port: 0 } });
    expect(f.port).toBe("22");
    expect(sftpPort(f)).toBe(22);
  });
});

describe("buildUpdateInput", () => {
  it("留空的凭据换成占位，表示保留已保存的值", () => {
    const input = buildUpdateInput(formFromRepo(REPO), "", savedCredentialsOf(REPO));
    expect(input).toEqual({
      name: "群晖 DS920+",
      params: {
        host: "192.168.1.20",
        port: 22,
        path: "/volume1/backup/opsnap",
        username: "backup",
        password: SECRET_MASKED,
        private_key: "",
        server_id: 0,
        host_key: "",
      },
    });
  });

  it("填了新凭据就提交新值；只带当前类型的参数", () => {
    const f = { ...formFromRepo(REPO), password: "SECRET-new" };
    expect(buildUpdateInput(f, "", savedCredentialsOf(REPO))).toMatchObject({
      params: { password: "SECRET-new", host_key: "" },
    });
    // 别的类型的残留输入不提交
    expect(Object.keys(buildUpdateInput(f, "", savedCredentialsOf(REPO)).params).sort()).toEqual(
      ["host", "host_key", "password", "path", "port", "private_key", "server_id", "username"].sort()
    );
  });

  it("只对已保存的凭据回传占位：没用私钥的 SFTP 不会凭空提交一个私钥占位", () => {
    const f = formFromRepo(REPO);
    // REPO 只存了密码，没有私钥
    expect(buildUpdateInput(f, "", savedCredentialsOf(REPO)).params).toMatchObject({
      password: SECRET_MASKED,
      private_key: "",
    });

    const withKey: RepoItem = { ...REPO, params: { ...REPO.params, private_key: SECRET_MASKED } };
    expect(buildUpdateInput(formFromRepo(withKey), "", savedCredentialsOf(withKey)).params).toMatchObject({
      password: SECRET_MASKED,
      private_key: SECRET_MASKED,
    });
  });

  it("S3 只改地址时沿用已保存的 Secret Access Key", () => {
    const s3: RepoItem = {
      ...REPO,
      type: "s3",
      params: {
        ...REPO.params,
        endpoint: "http://192.168.8.136:9000",
        bucket: "opsnap",
        region: "us-east-1",
        access_key_id: "opsnap-9d62217d",
        secret_access_key: SECRET_MASKED,
        path_style: true,
      },
    };
    const f = { ...formFromRepo(s3), endpoint: "https://rustfs.icodef.com" };
    expect(buildUpdateInput(f, "", savedCredentialsOf(s3))).toEqual({
      name: "群晖 DS920+",
      params: {
        endpoint: "https://rustfs.icodef.com",
        bucket: "opsnap",
        prefix: "",
        region: "us-east-1",
        access_key_id: "opsnap-9d62217d",
        secret_access_key: SECRET_MASKED,
        path_style: true,
      },
    });
  });
});
