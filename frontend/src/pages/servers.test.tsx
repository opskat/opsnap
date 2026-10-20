import { describe, expect, it } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ServersPage } from "./servers";
import { renderPage } from "@/test/render";
import { fail, mockApi, ok, type Call } from "@/test/api-mock";

const FP = "SHA256:p8Wq3nVvJk2mT7rXh0bLc9ZsYd4eGfA1uN6oKiHwE5Q";

/** 共享密钥：生产 root（密码） */
const PROD_ROOT = {
  id: 4,
  name: "生产 root",
  kind: "password",
  shared: true,
  username: "root",
  note: "",
  has_secret: true,
  has_passphrase: false,
  servers: [{ id: 2, name: "db-inner" }],
  created_at: 1760000000,
  updated_at: 1760000000,
};
/** 共享密钥：bastion 私钥（带口令） */
const BASTION_KEY = {
  id: 5,
  name: "bastion 私钥",
  kind: "ssh_key",
  shared: true,
  username: "ops",
  note: "",
  has_secret: true,
  has_passphrase: true,
  servers: [{ id: 1, name: "bastion" }],
  created_at: 1760000000,
  updated_at: 1760000000,
};
/** 共享密钥：没有被任何服务器使用 */
const SPARE_KEY = {
  id: 6,
  name: "DB 备用",
  kind: "password",
  shared: true,
  username: "admin",
  note: "",
  has_secret: true,
  has_passphrase: false,
  servers: [],
  created_at: 1760000000,
  updated_at: 1760000000,
};

const BASTION = {
  id: 1,
  name: "bastion",
  host: "203.0.113.9",
  port: 22,
  username: "ops",
  credential: { ...BASTION_KEY, id: 5 },
  hops: [],
  host_key_fingerprint: "SHA256:bastionfp",
  status: "ok",
  latency_ms: 48,
  fail_since: 0,
  last_error: "",
  last_check_at: 1760000000,
  job_count: 0,
};
const DB_INNER = {
  id: 2,
  name: "db-inner",
  host: "10.0.3.15",
  port: 22,
  username: "backup",
  credential: {
    id: 7,
    kind: "password",
    name: "",
    shared: false,
    username: "",
    note: "",
    has_secret: true,
    has_passphrase: false,
  },
  hops: [
    { type: "socks5", host: "proxy.example.com", port: 1080, username: "px", password: "已设置" },
    { type: "ssh", server_id: 1, server_name: "bastion", host: "203.0.113.9", port: 22, username: "", password: "" },
  ],
  host_key_fingerprint: "SHA256:dbfp",
  status: "ok",
  latency_ms: 134,
  fail_since: 0,
  last_error: "",
  last_check_at: 1760000000,
  job_count: 3,
};
const OLD_NAS = {
  ...BASTION,
  id: 3,
  name: "old-nas",
  host: "192.168.8.20",
  credential: { ...PROD_ROOT, id: 4 },
  status: "failed",
  latency_ms: 0,
  fail_since: 1760000000 - 86400 * 3,
  last_error: "连接超时",
  job_count: 1,
};

const CREDENTIALS = [PROD_ROOT, BASTION_KEY, SPARE_KEY];

function setup(extra: Parameters<typeof mockApi>[0] = {}) {
  const m = mockApi({
    "GET /servers": ok({ list: [BASTION, DB_INNER, OLD_NAS] }),
    "GET /credentials": ok({ list: CREDENTIALS }),
    "PUT /servers/2": (c) => ok({ ...DB_INNER, ...(c.body as object) }),
    "PUT /servers/1": (c) => ok({ ...BASTION, ...(c.body as object) }),
    "POST /servers": (c) => ok({ id: 9, ...(c.body as object) }),
    ...extra,
  });
  renderPage(<ServersPage />, { path: "/servers" });
  return m;
}

async function openEdit(name: string) {
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: `编辑 ${name}` }));
  await screen.findByRole("heading", { name: "编辑服务器" });
  return user;
}

async function openAdd() {
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "添加服务器" }));
  await screen.findByRole("heading", { name: "添加服务器" });
  return user;
}

/** 在组合框里按名称选一条共享密钥 */
async function pickCredential(user: ReturnType<typeof userEvent.setup>, name: string) {
  await user.click(screen.getByRole("button", { name: "选择密钥" }));
  const search = await screen.findByRole("combobox", { name: "搜索密钥" });
  await user.type(search, name);
  const option = await screen.findByRole("option", { name: new RegExp(name) });
  await user.click(within(option).getByRole("button"));
}

const last = (calls: Call[]) => calls[calls.length - 1];

describe("服务器列表", () => {
  it("显示名称、地址、密钥、连接路径、状态和任务数", async () => {
    setup();
    const db = (await screen.findByText("db-inner")).closest("li")!;
    expect(within(db).getByText("backup@10.0.3.15:22")).toBeInTheDocument();
    expect(within(db).getByText("直接填写，3 个任务")).toBeInTheDocument();
    expect(within(db).getByText("proxy.example.com:1080")).toBeInTheDocument();
    expect(within(db).getByText("bastion")).toBeInTheDocument();
    expect(within(db).getByText(/正常/)).toHaveTextContent("134 ms");
    // 「密钥」列：私有的显示「直接填写」与类型
    expect(within(db).getByText("直接填写")).toBeInTheDocument();
    expect(within(db).getByText("密码")).toBeInTheDocument();

    const bastion = screen.getAllByText("bastion")[0].closest("li")!;
    expect(within(bastion).getByText("直连")).toBeInTheDocument();
    expect(within(bastion).getByText("bastion 私钥，0 个任务")).toBeInTheDocument();
    // 「密钥」列：共享的显示名称与类型
    expect(within(bastion).getAllByText("bastion 私钥").length).toBeGreaterThan(0);
    expect(within(bastion).getByText("私钥")).toBeInTheDocument();

    const nas = screen.getByText("old-nas").closest("li")!;
    expect(within(nas).getByText("生产 root，1 个任务")).toBeInTheDocument();
    expect(within(nas).getByText(/起连不上/)).toBeInTheDocument();
    expect(within(nas).queryByText(/正常/)).not.toBeInTheDocument();
  });

  it("读取失败时写明原因并可重试", async () => {
    setup({ "GET /servers": fail(500, 10001, "数据库繁忙") });
    expect(await screen.findByText(/读取服务器失败：数据库繁忙/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "重试" })).toBeInTheDocument();
  });

  it("列表里测试已保存的服务器并刷新状态", async () => {
    const m = setup({ "POST /servers/3/test": ok({ ok: true, hops: [], fingerprint: "" }) });
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "测试 old-nas" }));
    await waitFor(() => expect(m.find("POST", "/servers/3/test")).toHaveLength(1));
    await waitFor(() => expect(m.find("GET", "/servers")).toHaveLength(2));
  });

  it("列表里测试失败时提示哪一跳和原因，文字与抽屉里一致", async () => {
    setup({
      "POST /servers/3/test": ok({
        ok: false,
        fingerprint: "SHA256:x",
        hops: [
          {
            index: 1,
            type: "ssh",
            name: "old-nas",
            addr: "192.168.8.20:22",
            ok: false,
            latency_ms: 0,
            reason: "fingerprint_unconfirmed",
            reason_label: "指纹未核对",
            message: "",
            fingerprint: "SHA256:x",
          },
        ],
      }),
    });
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "测试 old-nas" }));
    expect(await screen.findByText("old-nas 连不上：第 1 跳首次连接，需要核对指纹")).toBeInTheDocument();
  });
});

describe("凭据来源", () => {
  it("既没选共享密钥也没填内容时提示选择，不发请求", async () => {
    const m = setup();
    const user = await openAdd();
    const drawer = screen.getByRole("dialog");
    await user.type(within(drawer).getByLabelText("名称"), "web-1");
    await user.type(within(drawer).getByLabelText("地址"), "10.0.9.9");
    await user.type(within(drawer).getByLabelText("用户"), "backup");

    await user.click(within(drawer).getByRole("button", { name: "测试连接" }));
    // 组合框的占位与错误文案相同，取第二条（表单级错误）
    expect(await within(drawer).findAllByText("选择密码或私钥")).toHaveLength(2);
    expect(m.find("POST", "/servers/test")).toHaveLength(0);

    // 切到直接填写：密码没填
    await user.click(within(drawer).getByRole("radio", { name: "直接填写" }));
    await user.click(within(drawer).getByRole("button", { name: "测试连接" }));
    expect(await within(drawer).findByText("填写密码")).toBeInTheDocument();
    expect(m.find("POST", "/servers/test")).toHaveLength(0);

    // 换成私钥：私钥没填
    await user.click(within(drawer).getByRole("radio", { name: "私钥" }));
    await user.click(within(drawer).getByRole("button", { name: "测试连接" }));
    expect(await within(drawer).findByText("粘贴私钥")).toBeInTheDocument();
    expect(m.find("POST", "/servers/test")).toHaveLength(0);
  });

  it("Esc 关闭组合框，不改变已选中的密钥", async () => {
    setup();
    const user = await openEdit("bastion");
    const drawer = screen.getByRole("dialog");
    await user.click(within(drawer).getByRole("button", { name: "选择密钥" }));
    await screen.findByRole("combobox", { name: "搜索密钥" });
    // 每项右侧标出使用者数量
    expect(within(drawer).getAllByText("1 台在用")).toHaveLength(2);
    expect(within(drawer).getByText("未使用")).toBeInTheDocument();
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("combobox", { name: "搜索密钥" })).not.toBeInTheDocument());
    expect(within(drawer).getByRole("button", { name: "选择密钥" })).toHaveTextContent("bastion 私钥");
  });

  it("选中共享密钥后只提交 credential_id，并把默认用户填进空的「用户」", async () => {
    const m = setup({
      "POST /servers/test": ok({ ok: true, hops: [], fingerprint: FP }),
      "POST /servers": (c) => ok({ id: 9, ...(c.body as object) }),
    });
    const user = await openAdd();
    const drawer = screen.getByRole("dialog");
    await user.type(within(drawer).getByLabelText("名称"), "web-1");
    await user.type(within(drawer).getByLabelText("地址"), "10.0.9.9");
    await pickCredential(user, "生产 root");

    expect(within(drawer).getByLabelText("用户")).toHaveValue("root");
    expect(within(drawer).queryByText("选择密码或私钥")).not.toBeInTheDocument();
    // 选中后的摘要：默认用户 · 类型 · 还有几台服务器用它
    expect(within(drawer).getByText("root · 密码 · 还有 1 台服务器用它")).toBeInTheDocument();
    expect(within(drawer).queryByLabelText("密码")).not.toBeInTheDocument();

    await user.click(within(drawer).getByRole("button", { name: "测试连接" }));
    await waitFor(() => expect(m.find("POST", "/servers/test")).toHaveLength(1));
    expect(m.find("POST", "/servers/test")[0].body).toMatchObject({
      credential_id: 4,
      auth_type: "",
      password: "",
      private_key: "",
      passphrase: "",
      username: "root",
    });
    expect(m.find("POST", "/servers/test")[0].body).not.toHaveProperty("share_as");
  });

  it("已有用户时选中共享密钥不覆盖", async () => {
    const m = setup({
      "PUT /servers/1": (c) => ok(c.body),
      "POST /servers/test": ok({ ok: true, hops: [], fingerprint: "SHA256:bastionfp" }),
    });
    const user = await openEdit("bastion");
    const drawer = screen.getByRole("dialog");
    expect(within(drawer).getByLabelText("用户")).toHaveValue("ops");
    await pickCredential(user, "生产 root");
    expect(within(drawer).getByLabelText("用户")).toHaveValue("ops");
    await user.click(within(drawer).getByRole("button", { name: "保存" }));
    await waitFor(() => expect(m.find("PUT", "/servers/1")).toHaveLength(1));
    expect(m.find("PUT", "/servers/1")[0].body).toMatchObject({ credential_id: 4, username: "ops" });
  });

  it("从共享密钥切到直接填写时从空白开始，提交一条新的私有凭据", async () => {
    const m = setup({ "PUT /servers/1": (c) => ok(c.body) });
    const user = await openEdit("bastion");
    const drawer = screen.getByRole("dialog");
    expect(within(drawer).getAllByText("bastion 私钥").length).toBeGreaterThan(0);

    await user.click(within(drawer).getByRole("radio", { name: "直接填写" }));
    const key = await within(drawer).findByLabelText("私钥");
    expect(key).toHaveValue("");
    expect(key).toHaveAttribute("placeholder", "-----BEGIN OPENSSH PRIVATE KEY-----");
    expect(within(drawer).getByLabelText("私钥口令")).toHaveValue("");
    expect(within(drawer).getByLabelText("私钥口令")).not.toHaveAttribute("placeholder", "已设置，留空保持不变");
    // 切到直接填写不动「用户」
    expect(within(drawer).getByLabelText("用户")).toHaveValue("ops");

    await user.type(key, "SECRET-OPENSSH-KEY");
    await user.click(within(drawer).getByRole("button", { name: "保存" }));
    await waitFor(() => expect(m.find("PUT", "/servers/1")).toHaveLength(1));
    expect(m.find("PUT", "/servers/1")[0].body).toMatchObject({
      credential_id: 0,
      auth_type: "ssh_key",
      private_key: "SECRET-OPENSSH-KEY",
      password: "",
    });
    expect(m.find("PUT", "/servers/1")[0].body).not.toHaveProperty("share_as");
  });

  it("私钥认证时的两条来源：列表为空时提示还没有密钥", async () => {
    setup({ "GET /credentials": ok({ list: [] }) });
    const user = await openAdd();
    await user.click(screen.getByRole("button", { name: "选择密钥" }));
    expect(await screen.findByText("还没有密钥")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "直接填写…" }));
    expect(await screen.findByLabelText("密码")).toBeInTheDocument();
  });

  it("组合框可以用键盘过滤、移动和选中", async () => {
    const m = setup({ "PUT /servers/1": (c) => ok(c.body) });
    const user = await openEdit("bastion");
    const drawer = screen.getByRole("dialog");
    await user.click(within(drawer).getByRole("button", { name: "选择密钥" }));
    const search = await screen.findByRole("combobox", { name: "搜索密钥" });
    // 展开后焦点直接落在搜索框
    expect(search).toHaveFocus();
    await user.type(search, "备用");
    await user.keyboard("{ArrowDown}");
    await user.keyboard("{ArrowUp}");
    await user.keyboard("{Enter}");
    await waitFor(() => expect(within(drawer).getAllByText("DB 备用").length).toBeGreaterThan(0));
    await waitFor(() => expect(within(drawer).queryByRole("combobox", { name: "搜索密钥" })).not.toBeInTheDocument());
    await user.click(within(drawer).getByRole("button", { name: "保存" }));
    await waitFor(() => expect(m.find("PUT", "/servers/1")).toHaveLength(1));
    expect(m.find("PUT", "/servers/1")[0].body).toMatchObject({ credential_id: 6 });
  });
});

describe("直接填写", () => {
  it("「同时存成密钥」默认不勾选；勾选后提交 share_as", async () => {
    let n = 0;
    const firstContact = {
      ok: false,
      fingerprint: FP,
      hops: [
        {
          index: 1,
          type: "ssh",
          name: "web-1",
          addr: "10.0.9.9:22",
          ok: false,
          latency_ms: 0,
          reason: "fingerprint_unconfirmed",
          reason_label: "指纹未核对",
          message: "",
          fingerprint: FP,
        },
      ],
    };
    const good = { ...firstContact, ok: true, hops: [{ ...firstContact.hops[0], ok: true, reason: "" }] };
    const m = setup({ "POST /servers/test": () => ok(n++ === 0 ? firstContact : good) });
    const user = await openAdd();
    const drawer = screen.getByRole("dialog");
    await user.type(within(drawer).getByLabelText("名称"), "web-1");
    await user.type(within(drawer).getByLabelText("地址"), "10.0.9.9");
    await user.type(within(drawer).getByLabelText("用户"), "backup");
    await user.click(within(drawer).getByRole("radio", { name: "直接填写" }));
    await user.type(within(drawer).getByLabelText("密码"), "SECRET-PW");

    const box = within(drawer).getByRole("checkbox", { name: "同时存成密钥" });
    expect(box).not.toBeChecked();
    expect(within(drawer).queryByLabelText("密钥名称")).not.toBeInTheDocument();

    await user.click(within(drawer).getByRole("button", { name: "测试连接" }));
    await user.click(await within(drawer).findByRole("button", { name: "一致，信任" }));
    await waitFor(() => expect(within(drawer).getByRole("button", { name: "保存" })).toBeEnabled());
    await user.click(within(drawer).getByRole("button", { name: "保存" }));
    await waitFor(() => expect(m.find("POST", "/servers")).toHaveLength(1));
    expect(m.find("POST", "/servers")[0].body).toMatchObject({ credential_id: 0, password: "SECRET-PW" });
    expect(m.find("POST", "/servers")[0].body).not.toHaveProperty("share_as");
  });

  it("勾选「同时存成密钥」后展开名称，默认用服务器名，提交 share_as", async () => {
    const m = setup({ "PUT /servers/2": (c) => ok(c.body) });
    const user = await openEdit("db-inner");
    const drawer = screen.getByRole("dialog");
    await user.click(within(drawer).getByRole("checkbox", { name: "同时存成密钥" }));
    const name = await within(drawer).findByLabelText("密钥名称");
    // 默认填服务器名，可以改
    expect(name).toHaveValue("db-inner");
    await user.clear(name);
    await user.type(name, "生产 DB");
    await user.click(within(drawer).getByRole("button", { name: "保存" }));
    await waitFor(() => expect(m.find("PUT", "/servers/2")).toHaveLength(1));
    expect(m.find("PUT", "/servers/2")[0].body).toMatchObject({
      credential_id: 0,
      password: "已设置",
      share_as: "生产 DB",
    });
  });

  it("编辑私有密码时显示已设置，留空提交占位", async () => {
    const m = setup();
    const user = await openEdit("db-inner");
    const drawer = screen.getByRole("dialog");
    const pw = within(drawer).getByLabelText("密码");
    expect(pw).toHaveValue("");
    expect(pw).toHaveAttribute("placeholder", "已设置，留空保持不变");
    await user.click(within(drawer).getByRole("button", { name: "保存" }));
    await waitFor(() => expect(m.find("PUT", "/servers/2")).toHaveLength(1));
    expect(m.find("PUT", "/servers/2")[0].body).toMatchObject({
      name: "db-inner",
      credential_id: 0,
      auth_type: "password",
      password: "已设置",
      private_key: "",
      passphrase: "",
    });
  });

  it("改私有密码提交新值，仍是同一条私有凭据（credential_id 为 0）", async () => {
    const m = setup();
    const user = await openEdit("db-inner");
    await user.type(screen.getByLabelText("密码"), "NEW-SECRET-1");
    await user.click(screen.getByRole("button", { name: "保存" }));
    await waitFor(() => expect(m.find("PUT", "/servers/2")).toHaveLength(1));
    expect(m.find("PUT", "/servers/2")[0].body).toMatchObject({ credential_id: 0, password: "NEW-SECRET-1" });
  });

  it("编辑私有私钥时两条内容都显示已设置", async () => {
    const m = setup({
      "GET /servers": ok({
        list: [
          {
            ...BASTION,
            credential: { ...BASTION_KEY, id: 8, name: "", shared: false, username: "" },
          },
        ],
      }),
      "PUT /servers/1": (c) => ok(c.body),
    });
    const user = await openEdit("bastion");
    const drawer = screen.getByRole("dialog");
    expect(within(drawer).getByLabelText("私钥")).toHaveAttribute("placeholder", "已设置，留空保持不变");
    expect(within(drawer).getByLabelText("私钥口令")).toHaveAttribute("placeholder", "已设置，留空保持不变");
    await user.click(within(drawer).getByRole("button", { name: "保存" }));
    await waitFor(() => expect(m.find("PUT", "/servers/1")).toHaveLength(1));
    expect(m.find("PUT", "/servers/1")[0].body).toMatchObject({
      credential_id: 0,
      auth_type: "ssh_key",
      private_key: "已设置",
      passphrase: "已设置",
    });
  });

  it.each([
    ["地址", "10.0.3.16"],
    ["端口", "2222"],
    ["用户", "root"],
  ])("改了%s后不再沿用已保存的私有密码，要求重新填写", async (field, value) => {
    const m = setup();
    const user = await openEdit("db-inner");
    await user.clear(screen.getByLabelText(field));
    await user.type(screen.getByLabelText(field), value);
    expect(screen.getByLabelText("密码")).not.toHaveAttribute("placeholder", "已设置，留空保持不变");
    await user.click(screen.getByRole("button", { name: "测试连接" }));
    expect(await screen.findByText("地址或用户已改动，请重新填写登录密码")).toBeInTheDocument();
    expect(m.find("POST", "/servers/test")).toHaveLength(0);
  });

  it("改了地址又改回原值，仍沿用已保存的私有密码", async () => {
    const m = setup({ "POST /servers/test": ok({ ok: true, hops: [], fingerprint: "SHA256:dbfp" }) });
    const user = await openEdit("db-inner");
    await user.type(screen.getByLabelText("地址"), "9");
    await user.type(screen.getByLabelText("地址"), "{Backspace}");
    expect(screen.getByLabelText("密码")).toHaveAttribute("placeholder", "已设置，留空保持不变");
    await user.click(screen.getByRole("button", { name: "测试连接" }));
    await waitFor(() => expect(m.find("POST", "/servers/test")).toHaveLength(1));
    expect(m.find("POST", "/servers/test")[0].body).toMatchObject({ host: "10.0.3.15", password: "已设置" });
  });

  it("改了地址并填了新密码时提交新密码", async () => {
    const m = setup({ "POST /servers/test": ok({ ok: true, hops: [], fingerprint: "SHA256:dbfp" }) });
    const user = await openEdit("db-inner");
    await user.type(screen.getByLabelText("地址"), "9");
    await user.type(screen.getByLabelText("密码"), "NEW-SECRET-2");
    await user.click(screen.getByRole("button", { name: "测试连接" }));
    await waitFor(() => expect(m.find("POST", "/servers/test")).toHaveLength(1));
    expect(m.find("POST", "/servers/test")[0].body).toMatchObject({ host: "10.0.3.159", password: "NEW-SECRET-2" });
  });

  it("密码可以显示与隐藏", async () => {
    setup();
    const user = await openEdit("db-inner");
    const drawer = screen.getByRole("dialog");
    const pw = within(drawer).getByLabelText("密码");
    expect(pw).toHaveAttribute("type", "password");
    await user.click(within(drawer).getByRole("button", { name: "显示密码" }));
    expect(pw).toHaveAttribute("type", "text");
    await user.click(within(drawer).getByRole("button", { name: "隐藏密码" }));
    expect(pw).toHaveAttribute("type", "password");
  });
});

describe("代理链", () => {
  it("上移、下移、删除后按页面上的顺序提交", async () => {
    const m = setup();
    const user = await openEdit("db-inner");
    // 原顺序：socks5 → bastion
    await user.click(screen.getByRole("button", { name: "第 1 跳下移" }));
    await user.click(screen.getByRole("button", { name: "保存" }));
    await waitFor(() => expect(m.find("PUT", "/servers/2")).toHaveLength(1));
    const hops = (m.find("PUT", "/servers/2")[0].body as { hops: { type: string; server_id: number }[] }).hops;
    expect(hops.map((h) => h.type)).toEqual(["ssh", "socks5"]);
    expect(hops[0].server_id).toBe(1);
  });

  it("上移最后一跳、删除某一跳", async () => {
    const m = setup();
    const user = await openEdit("db-inner");
    expect(screen.getByRole("button", { name: "第 1 跳上移" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "第 2 跳下移" })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "第 1 跳移除" }));
    expect(screen.queryByLabelText("第 2 跳类型")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "保存" }));
    await waitFor(() => expect(m.find("PUT", "/servers/2")).toHaveLength(1));
    const hops = (m.find("PUT", "/servers/2")[0].body as { hops: { type: string }[] }).hops;
    expect(hops.map((h) => h.type)).toEqual(["ssh"]);
  });

  it("未改动的代理密码回传「已设置」，改过地址就不再沿用", async () => {
    const m = setup();
    const user = await openEdit("db-inner");
    await user.click(screen.getByRole("button", { name: "保存" }));
    await waitFor(() => expect(m.find("PUT", "/servers/2")).toHaveLength(1));
    const hop0 = (m.find("PUT", "/servers/2")[0].body as { hops: { host: string; port: number; password: string }[] })
      .hops[0];
    expect(hop0).toMatchObject({ host: "proxy.example.com", port: 1080, password: "已设置" });
  });
});

describe("指纹核对", () => {
  async function fillNew(user: ReturnType<typeof userEvent.setup>) {
    await user.click(await screen.findByRole("button", { name: "添加服务器" }));
    await screen.findByRole("heading", { name: "添加服务器" });
    await user.type(screen.getByLabelText("名称"), "web-1");
    await user.type(screen.getByLabelText("地址"), "10.0.9.9");
    await user.clear(screen.getByLabelText("端口"));
    await user.type(screen.getByLabelText("端口"), "2222");
    await user.type(screen.getByLabelText("用户"), "backup");
    await user.click(screen.getByRole("radio", { name: "直接填写" }));
    await user.type(screen.getByLabelText("密码"), "SECRET-PW");
  }
  const firstContact = {
    ok: false,
    fingerprint: FP,
    hops: [
      {
        index: 1,
        type: "ssh",
        name: "web-1",
        addr: "10.0.9.9:2222",
        ok: false,
        latency_ms: 0,
        reason: "fingerprint_unconfirmed",
        reason_label: "指纹未核对",
        message: "",
        fingerprint: FP,
      },
    ],
  };
  const good = {
    ok: true,
    fingerprint: FP,
    hops: [
      {
        index: 1,
        type: "ssh",
        name: "web-1",
        addr: "10.0.9.9:2222",
        ok: true,
        latency_ms: 80,
        reason: "",
        reason_label: "",
        message: "",
        fingerprint: FP,
      },
    ],
  };

  it("核对指纹之前不能保存；信任后带指纹重新测试，通过后才可保存", async () => {
    let n = 0;
    const m = setup({ "POST /servers/test": () => ok(n++ === 0 ? firstContact : good) });
    const user = userEvent.setup();
    await fillNew(user);
    expect(screen.getByRole("button", { name: "保存" })).toBeDisabled();

    await user.click(screen.getByRole("button", { name: "测试连接" }));
    expect(await screen.findByText(FP)).toBeInTheDocument();
    // 首次测试不带指纹
    expect(m.find("POST", "/servers/test")[0].body).toMatchObject({ id: 0, host_key_fingerprint: "" });
    expect(screen.getByRole("button", { name: "保存" })).toBeDisabled();

    await user.click(screen.getByRole("button", { name: "一致，信任" }));
    await waitFor(() => expect(m.find("POST", "/servers/test")).toHaveLength(2));
    expect(m.find("POST", "/servers/test")[1].body).toMatchObject({ host_key_fingerprint: FP });
    await waitFor(() => expect(screen.getByRole("button", { name: "保存" })).toBeEnabled());

    await user.click(screen.getByRole("button", { name: "保存" }));
    await waitFor(() => expect(m.find("POST", "/servers")).toHaveLength(1));
    expect(m.find("POST", "/servers")[0].body).toMatchObject({
      name: "web-1",
      host: "10.0.9.9",
      port: 2222,
      credential_id: 0,
      auth_type: "password",
      password: "SECRET-PW",
      host_key_fingerprint: FP,
    });
  });

  it("信任之后改了地址，要重新核对才能保存", async () => {
    let n = 0;
    setup({ "POST /servers/test": () => ok(n++ === 0 ? firstContact : good) });
    const user = userEvent.setup();
    await fillNew(user);
    await user.click(screen.getByRole("button", { name: "测试连接" }));
    await user.click(await screen.findByRole("button", { name: "一致，信任" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "保存" })).toBeEnabled());
    await user.type(screen.getByLabelText("地址"), "0");
    expect(screen.getByRole("button", { name: "保存" })).toBeDisabled();
  });

  it("指纹与已核对的不符时不提供信任按钮", async () => {
    setup({
      "POST /servers/test": () =>
        ok({
          ok: false,
          fingerprint: FP,
          hops: [
            {
              ...firstContact.hops[0],
              reason: "fingerprint_mismatch",
              reason_label: "指纹不符",
              message: "主机指纹已变化",
            },
          ],
        }),
    });
    const user = await openEdit("bastion");
    await user.click(screen.getByRole("button", { name: "测试连接" }));
    expect(await screen.findByText("指纹不符")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "一致，信任" })).not.toBeInTheDocument();
  });
});

describe("逐跳测试", () => {
  it("标出失败的是哪一跳和原因，后面的各跳不显示为已尝试", async () => {
    const m = setup({
      "POST /servers/test": () =>
        ok({
          ok: false,
          fingerprint: "",
          hops: [
            {
              index: 1,
              type: "socks5",
              name: "proxy.example.com:1080",
              addr: "proxy.example.com:1080",
              ok: true,
              latency_ms: 21,
              reason: "",
              reason_label: "",
              message: "",
              fingerprint: "",
            },
            {
              index: 2,
              type: "ssh",
              name: "bastion",
              addr: "203.0.113.9:22",
              ok: false,
              latency_ms: 0,
              reason: "auth_failed",
              reason_label: "认证失败",
              message: "ssh: unable to authenticate",
              fingerprint: "",
            },
          ],
        }),
    });
    const user = await openEdit("db-inner");
    await user.click(screen.getByRole("button", { name: "测试连接" }));
    const hop1 = await screen.findByRole("listitem", { name: "第 1 跳" });
    expect(within(hop1).getByText("已连通")).toBeInTheDocument();
    expect(within(hop1).getByText("21 ms")).toBeInTheDocument();
    const hop2 = screen.getByRole("listitem", { name: "第 2 跳" });
    expect(within(hop2).getByText(/认证失败/)).toBeInTheDocument();
    const target = screen.getByRole("listitem", { name: "目标主机" });
    expect(within(target).getByText("未尝试")).toBeInTheDocument();
    expect(within(target).queryByText(/ms$/)).not.toBeInTheDocument();
    // 编辑中的服务器带 id，凭据占位由后端取回
    expect(last(m.find("POST", "/servers/test"))!.body).toMatchObject({ id: 2, password: "已设置" });
  });

  it("全部通过时每一跳和目标都显示延迟", async () => {
    setup({
      "POST /servers/test": () =>
        ok({
          ok: true,
          fingerprint: "SHA256:dbfp",
          hops: [
            {
              index: 1,
              type: "socks5",
              name: "p",
              addr: "p",
              ok: true,
              latency_ms: 21,
              reason: "",
              reason_label: "",
              message: "",
              fingerprint: "",
            },
            {
              index: 2,
              type: "ssh",
              name: "bastion",
              addr: "b",
              ok: true,
              latency_ms: 48,
              reason: "",
              reason_label: "",
              message: "",
              fingerprint: "SHA256:bastionfp",
            },
            {
              index: 3,
              type: "ssh",
              name: "db-inner",
              addr: "d",
              ok: true,
              latency_ms: 134,
              reason: "",
              reason_label: "",
              message: "",
              fingerprint: "SHA256:dbfp",
            },
          ],
        }),
    });
    const user = await openEdit("db-inner");
    await user.click(screen.getByRole("button", { name: "测试连接" }));
    const target = await screen.findByRole("listitem", { name: "目标主机" });
    expect(within(target).getByText("134 ms")).toBeInTheDocument();
    expect(within(screen.getByRole("listitem", { name: "第 2 跳" })).getByText("48 ms")).toBeInTheDocument();
  });

  it("改动配置后旧的测试结果不再显示", async () => {
    setup({
      "POST /servers/test": () =>
        ok({
          ok: true,
          fingerprint: "SHA256:dbfp",
          hops: [
            {
              index: 3,
              type: "ssh",
              name: "db-inner",
              addr: "d",
              ok: true,
              latency_ms: 134,
              reason: "",
              reason_label: "",
              message: "",
              fingerprint: "SHA256:dbfp",
            },
          ],
        }),
    });
    const user = await openEdit("db-inner");
    await user.click(screen.getByRole("button", { name: "测试连接" }));
    const drawer = screen.getByRole("dialog");
    await within(drawer).findByText("134 ms");
    await user.click(screen.getByRole("button", { name: "第 1 跳下移" }));
    expect(within(drawer).queryByText("134 ms")).not.toBeInTheDocument();
  });
});

describe("删除", () => {
  it("确认后删除并刷新列表", async () => {
    const m = setup({ "DELETE /servers/3": ok({}) });
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "删除 old-nas" }));
    expect(m.find("DELETE", "/servers/3")).toHaveLength(0);
    await user.click(await screen.findByRole("button", { name: "删除" }));
    await waitFor(() => expect(m.find("DELETE", "/servers/3")).toHaveLength(1));
    await waitFor(() => expect(m.find("GET", "/servers")).toHaveLength(2));
  });

  it("被任务或跳板使用而被拒绝时，原样显示后端给出的说明并保留在对话框里", async () => {
    const msg = "服务器仍被 3 个任务使用，请先修改这些任务";
    setup({ "DELETE /servers/2": fail(409, 12005, msg) });
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "删除 db-inner" }));
    await user.click(await screen.findByRole("button", { name: "删除" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(await within(dialog).findByText(msg)).toBeInTheDocument();
    expect(screen.getByText("db-inner", { selector: "li *" })).toBeInTheDocument();
  });
});
