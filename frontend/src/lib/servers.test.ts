import { describe, expect, it } from "vitest";
import { SECRET_MASKED, type ServerCredential, type ServerItem } from "./api";
import {
  buildInput,
  credentialLabel,
  emptyForm,
  formFromItem,
  isTrusted,
  passwordSaved,
  pickCredential,
  switchAuthType,
  switchSource,
  targetAddr,
  type PickerCredential,
} from "./servers";

const SHARED_PASSWORD: ServerCredential = {
  id: 4,
  kind: "password",
  name: "生产 root",
  shared: true,
  username: "root",
  note: "",
  has_secret: true,
  has_passphrase: false,
};

const PRIVATE_KEY: ServerCredential = {
  id: 5,
  kind: "ssh_key",
  name: "",
  shared: false,
  username: "",
  note: "",
  has_secret: true,
  has_passphrase: true,
};

function item(credential: ServerCredential | null): ServerItem {
  return {
    id: 2,
    name: "db-inner",
    host: "10.0.3.15",
    port: 22,
    username: "backup",
    credential,
    hops: [],
    host_key_fingerprint: "SHA256:dbfp",
    status: "ok",
    latency_ms: 12,
    fail_since: 0,
    last_error: "",
    last_check_at: 1760000000,
    job_count: 3,
  };
}

const PICKER_OTHER: PickerCredential = {
  id: 6,
  name: "DB 备用",
  kind: "password",
  username: "admin",
  servers: [],
};

describe("formFromItem：凭据来源", () => {
  it("共享密钥 → 用已保存的，认证方式取密钥类型", () => {
    const f = formFromItem(item(SHARED_PASSWORD));
    expect(f).toMatchObject({ source: "saved", credentialId: 4, authType: "password", username: "backup" });
    expect(f.password).toBe("");
    expect(f.saved).toEqual({ privateKey: false, passphrase: false });
    expect(f.savedPassword).toBeNull();
  });

  it("私有密码 → 直接填写，留空表示保留", () => {
    const f = formFromItem(item({ ...SHARED_PASSWORD, id: 7, name: "", shared: false, username: "" }));
    expect(f).toMatchObject({ source: "inline", credentialId: 0, authType: "password" });
    expect(passwordSaved(f)).toBe(true);
  });

  it("私有私钥 → 直接填写，私钥与口令都算已保存", () => {
    const f = formFromItem(item(PRIVATE_KEY));
    expect(f).toMatchObject({ source: "inline", credentialId: 0, authType: "ssh_key" });
    expect(f.privateKey).toBe("");
    expect(f.passphrase).toBe("");
    expect(f.saved).toEqual({ privateKey: true, passphrase: true });
  });

  it("改了地址或用户后不再沿用私有密码", () => {
    const f = formFromItem(item({ ...SHARED_PASSWORD, id: 7, name: "", shared: false, username: "" }));
    expect(passwordSaved({ ...f, username: "root" })).toBe(false);
  });

  it("新表单默认「用已保存的」，什么都没选", () => {
    const f = emptyForm();
    expect(f).toMatchObject({ source: "saved", credentialId: 0, share: false, shareAs: "" });
  });
});

describe("buildInput：用已保存的", () => {
  it("只提交 credential_id，凭据字段留空且不带 share_as", () => {
    const f = formFromItem(item(SHARED_PASSWORD));
    const { input, error } = buildInput(f);
    expect(error).toBeUndefined();
    expect(input).toMatchObject({
      name: "db-inner",
      host: "10.0.3.15",
      port: 22,
      username: "backup",
      credential_id: 4,
      auth_type: "",
      password: "",
      private_key: "",
      passphrase: "",
    });
    expect(input).not.toHaveProperty("share_as");
  });

  it("没选密钥时要求选择，不发请求", () => {
    const f = { ...formFromItem(item(SHARED_PASSWORD)), credentialId: 0 };
    expect(buildInput(f)).toEqual({ error: "选择密码或私钥" });
  });
});

describe("buildInput：直接填写", () => {
  it("未改动的私有密码回传占位", () => {
    const f = formFromItem(item({ ...SHARED_PASSWORD, id: 7, name: "", shared: false, username: "" }));
    expect(buildInput(f).input).toMatchObject({
      credential_id: 0,
      auth_type: "password",
      password: SECRET_MASKED,
      private_key: "",
      passphrase: "",
    });
  });

  it("未改动的私钥与口令都回传占位", () => {
    const f = formFromItem(item(PRIVATE_KEY));
    const { input } = buildInput(f);
    expect(input).toMatchObject({
      credential_id: 0,
      auth_type: "ssh_key",
      password: "",
      private_key: SECRET_MASKED,
      passphrase: SECRET_MASKED,
    });
  });

  it.each([
    [{ authType: "password" as const, host: "" }, "填写服务器地址"],
    [{ authType: "password" as const }, "填写密码"],
    [{ authType: "ssh_key" as const }, "粘贴私钥"],
  ])("内容不全时给出原因：%o", (patch, message) => {
    const f = {
      ...emptyForm(),
      ...{ name: "web-1", host: "10.0.9.9", username: "backup", source: "inline" as const },
      ...patch,
    };
    expect(buildInput(f)).toEqual({ error: message });
  });

  it("勾选「同时存成密钥」后带 share_as，名称留空时用服务器名", () => {
    const f = {
      ...emptyForm(),
      source: "inline" as const,
      name: "web-1",
      host: "10.0.9.9",
      username: "backup",
      password: "SECRET-PW",
      share: true,
    };
    expect(buildInput(f).input).toMatchObject({ credential_id: 0, password: "SECRET-PW", share_as: "web-1" });
    expect(buildInput({ ...f, shareAs: " web-生产 " }).input).toMatchObject({ share_as: "web-生产" });
  });

  it("不勾选「同时存成密钥」时保持私有，不带 share_as", () => {
    const f = {
      ...emptyForm(),
      source: "inline" as const,
      name: "web-1",
      host: "10.0.9.9",
      username: "backup",
      password: "SECRET-PW",
    };
    expect(buildInput(f).input).not.toHaveProperty("share_as");
  });
});

describe("选中共享密钥", () => {
  it("用户为空时填密钥的默认用户", () => {
    const f = pickCredential({ ...emptyForm(), name: "web-1" }, PICKER_OTHER);
    expect(f).toMatchObject({ source: "saved", credentialId: 6, authType: "password", username: "admin" });
  });

  it("用户已有内容时不动，并清掉直接填写的内容", () => {
    const f = pickCredential({ ...emptyForm(), username: "backup", password: "SECRET-PW", share: true }, PICKER_OTHER);
    expect(f.username).toBe("backup");
    expect(f).toMatchObject({ password: "", share: false });
  });
});

describe("切换来源与类型", () => {
  it("从共享密钥切到直接填写时从空白开始", () => {
    const f = switchSource(formFromItem(item(SHARED_PASSWORD)), "inline");
    expect(f).toMatchObject({ source: "inline", credentialId: 0, password: "", privateKey: "", passphrase: "" });
    expect(passwordSaved(f)).toBe(false);
  });

  it("类型从密码切到私钥时清空密码，切回来也不残留", () => {
    const f = switchAuthType({ ...emptyForm(), password: "SECRET-PW" }, "ssh_key");
    expect(f).toMatchObject({ authType: "ssh_key", password: "" });
    expect(switchAuthType(f, "password")).toMatchObject({ authType: "password", privateKey: "", passphrase: "" });
  });
});

describe("列表里的凭据摘要", () => {
  it("共享密钥显示名称，私有凭据显示「直接填写」", () => {
    expect(credentialLabel(SHARED_PASSWORD)).toBe("生产 root");
    expect(credentialLabel(PRIVATE_KEY)).toBe("直接填写");
    expect(credentialLabel(null)).toBe("直接填写");
  });
});

describe("指纹", () => {
  it("只在核对过指纹的地址上算已信任", () => {
    const f = formFromItem(item(SHARED_PASSWORD));
    expect(isTrusted(f)).toBe(true);
    expect(isTrusted({ ...f, host: "10.0.3.16" })).toBe(false);
    expect(targetAddr(f)).toBe("10.0.3.15:22");
  });
});
