import { describe, expect, it } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { CredentialsPage } from "./credentials";
import { renderPage } from "@/test/render";
import { PINNED_NOW_SEC, pinClock } from "@/test/clock";
import { fail, mockApi, ok, type Call } from "@/test/api-mock";

const NOW = PINNED_NOW_SEC;
pinClock();

const PROD_ROOT = {
  id: 1,
  name: "生产 root",
  kind: "password",
  username: "root",
  note: "线上集群通用",
  has_secret: true,
  has_passphrase: false,
  servers: [
    { id: 11, name: "web-01", host: "10.0.3.11", port: 22, username: "root" },
    { id: 12, name: "web-02", host: "10.0.3.12", port: 22, username: "root" },
    { id: 13, name: "db-01", host: "10.0.3.21", port: 22, username: "root" },
    { id: 14, name: "nas-ops", host: "10.0.9.5", port: 22, username: "ops" },
  ],
  created_at: NOW,
  updated_at: NOW,
};
const OPS_KEY = {
  id: 2,
  name: "ops 私钥",
  kind: "ssh_key",
  username: "ops",
  note: "ed25519，带口令",
  has_secret: true,
  has_passphrase: true,
  servers: [{ id: 15, name: "jump-bj", host: "203.0.113.7", port: 22, username: "ops" }],
  created_at: NOW,
  updated_at: NOW,
};
const OLD_PW = {
  id: 3,
  name: "旧机房密码",
  kind: "password",
  username: "admin",
  note: "",
  has_secret: true,
  has_passphrase: false,
  servers: [],
  created_at: NOW,
  updated_at: NOW,
};

function setup(extra: Parameters<typeof mockApi>[0] = {}) {
  const api = mockApi({
    "GET /credentials": ok({ list: [PROD_ROOT, OPS_KEY, OLD_PW] }),
    "POST /credentials": (c) => ok({ id: 9, servers: [], created_at: NOW, updated_at: NOW, ...(c.body as object) }),
    "PUT /credentials/1": (c) => ok({ ...PROD_ROOT, ...(c.body as object) }),
    "DELETE /credentials/3": ok({}),
    ...extra,
  });
  renderPage(<CredentialsPage />, { path: "/credentials" });
  return api;
}

async function openCreate(user: ReturnType<typeof userEvent.setup>) {
  await user.click(await screen.findByRole("button", { name: "添加密钥" }));
  return screen.findByRole("dialog", { name: "添加密钥" });
}

async function openEdit(user: ReturnType<typeof userEvent.setup>, name: string) {
  await user.click(await screen.findByRole("button", { name: `编辑 ${name}` }));
  return screen.findByRole("dialog", { name: "编辑密钥" });
}

const row = (name: string) => screen.getByText(name).closest("li") as HTMLElement;
const posts = (api: { find: (m: string, p: string) => Call[] }) => api.find("POST", "/credentials");

describe("密钥列表", () => {
  it("显示名称、类型、默认用户、备注、使用者与最近修改", async () => {
    setup();
    const prod = (await screen.findByText("生产 root")).closest("li") as HTMLElement;
    expect(within(prod).getByText("密码")).toBeInTheDocument();
    expect(within(prod).getByText("root")).toBeInTheDocument();
    expect(within(prod).getByText("线上集群通用")).toBeInTheDocument();
    expect(within(prod).getByText("web-01")).toBeInTheDocument();
    expect(within(prod).getByText("web-02")).toBeInTheDocument();
    expect(within(prod).getByText("db-01")).toBeInTheDocument();
    // 多于 3 台时只列前 3 个并折成「等 N 台」
    expect(within(prod).queryByText("nas-ops")).not.toBeInTheDocument();
    expect(within(prod).getByText("等 1 台")).toBeInTheDocument();

    const key = row("ops 私钥");
    expect(within(key).getByText("私钥")).toBeInTheDocument();
    expect(within(key).getByText("ops")).toBeInTheDocument();
    expect(within(key).getByText("带口令")).toBeInTheDocument();
    expect(within(key).getByText("jump-bj")).toBeInTheDocument();

    expect(within(row("旧机房密码")).getByText("没有共享它的服务器")).toBeInTheDocument();
    expect(screen.getByText("共 3 条")).toBeInTheDocument();
  });

  it("读取失败时写明原因，重试后恢复", async () => {
    let failed = true;
    const api = mockApi({
      "GET /credentials": () => (failed ? fail(500, 10001, "数据库繁忙") : ok({ list: [OLD_PW] })),
    });
    renderPage(<CredentialsPage />, { path: "/credentials" });
    expect(await screen.findByText(/读取密钥失败：数据库繁忙/)).toBeInTheDocument();
    failed = false;
    await userEvent.setup().click(screen.getByRole("button", { name: "重试" }));
    expect(await screen.findByText("旧机房密码")).toBeInTheDocument();
    await waitFor(() => expect(api.find("GET", "/credentials")).toHaveLength(2));
  });

  it("一条都没有时给出空状态", async () => {
    mockApi({ "GET /credentials": ok({ list: null }) });
    renderPage(<CredentialsPage />, { path: "/credentials" });
    expect(await screen.findByText("还没有密钥。添加后可以在服务器里选用。")).toBeInTheDocument();
  });

  it("按名称与默认用户本地过滤，没有匹配时写明关键词", async () => {
    const api = setup();
    const user = userEvent.setup();
    const search = await screen.findByRole("searchbox", { name: "搜索密钥" });

    await user.type(search, "root");
    expect(screen.getByText("生产 root")).toBeInTheDocument();
    expect(screen.queryByText("ops 私钥")).not.toBeInTheDocument();
    expect(screen.getByText("共 1 条")).toBeInTheDocument();

    await user.clear(search);
    await user.type(search, "admin");
    expect(screen.getByText("旧机房密码")).toBeInTheDocument();
    expect(screen.queryByText("生产 root")).not.toBeInTheDocument();

    await user.clear(search);
    await user.type(search, "不存在");
    expect(screen.getByText("没有匹配「不存在」的密钥。")).toBeInTheDocument();
    // 本地过滤不发请求
    await waitFor(() => expect(api.find("GET", "/credentials")).toHaveLength(1));
  });
});

describe("添加密钥", () => {
  it("名称为空时提示且不发请求", async () => {
    const api = setup();
    const user = userEvent.setup();
    const dialog = await openCreate(user);
    await user.click(within(dialog).getByRole("button", { name: "添加" }));
    expect(within(dialog).getByText("填写密钥名称")).toBeInTheDocument();
    expect(posts(api)).toHaveLength(0);
    expect(screen.getByRole("dialog", { name: "添加密钥" })).toBeInTheDocument();
  });

  it("密码类型缺密码时提示且不发请求", async () => {
    const api = setup();
    const user = userEvent.setup();
    const dialog = await openCreate(user);
    await user.type(within(dialog).getByLabelText("名称"), "生产 root");
    await user.click(within(dialog).getByRole("button", { name: "添加" }));
    expect(within(dialog).getByText("请填写密码")).toBeInTheDocument();
    expect(posts(api)).toHaveLength(0);
  });

  it("私钥类型缺私钥时提示且不发请求", async () => {
    const api = setup();
    const user = userEvent.setup();
    const dialog = await openCreate(user);
    await user.type(within(dialog).getByLabelText("名称"), "ops 私钥");
    await user.click(within(dialog).getByRole("radio", { name: "私钥" }));
    await user.click(within(dialog).getByRole("button", { name: "添加" }));
    expect(within(dialog).getByText("请粘贴私钥")).toBeInTheDocument();
    expect(posts(api)).toHaveLength(0);
  });

  it("密码可以显示与隐藏", async () => {
    setup();
    const user = userEvent.setup();
    const dialog = await openCreate(user);
    const input = within(dialog).getByLabelText("密码");
    expect(input).toHaveAttribute("type", "password");
    await user.click(within(dialog).getByRole("button", { name: "显示密码" }));
    expect(input).toHaveAttribute("type", "text");
    expect(within(dialog).getByRole("button", { name: "隐藏密码" })).toBeInTheDocument();
  });

  it("密码类型成功后提交名称、密码、默认用户与备注", async () => {
    const api = setup();
    const user = userEvent.setup();
    const dialog = await openCreate(user);
    await user.type(within(dialog).getByLabelText("名称"), "  生产 root  ");
    await user.type(within(dialog).getByLabelText("密码"), "SECRET-PW-1");
    await user.type(within(dialog).getByLabelText("默认用户"), " root ");
    await user.type(within(dialog).getByLabelText("备注"), "线上集群通用");
    await user.click(within(dialog).getByRole("button", { name: "添加" }));

    await waitFor(() => expect(posts(api)).toHaveLength(1));
    expect(posts(api)[0].body).toMatchObject({
      name: "生产 root",
      kind: "password",
      password: "SECRET-PW-1",
      private_key: "",
      passphrase: "",
      username: "root",
      note: "线上集群通用",
    });
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    // 保存后重新读取列表
    await waitFor(() => expect(api.find("GET", "/credentials")).toHaveLength(2));
  });

  it("切到私钥再切回密码，只提交密码一侧的字段", async () => {
    const api = setup();
    const user = userEvent.setup();
    const dialog = await openCreate(user);
    await user.type(within(dialog).getByLabelText("名称"), "ops 私钥");
    await user.type(within(dialog).getByLabelText("密码"), "SHOULD-BE-CLEARED");
    await user.click(within(dialog).getByRole("radio", { name: "私钥" }));
    await user.click(within(dialog).getByLabelText("私钥"));
    await user.paste("-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----");
    await user.type(within(dialog).getByLabelText("私钥口令"), "PASS-PHRASE");
    await user.click(within(dialog).getByRole("button", { name: "添加" }));

    await waitFor(() => expect(posts(api)).toHaveLength(1));
    expect(posts(api)[0].body).toMatchObject({
      name: "ops 私钥",
      kind: "ssh_key",
      password: "",
      passphrase: "PASS-PHRASE",
    });
    expect((posts(api)[0].body as { private_key: string }).private_key).toContain("BEGIN OPENSSH PRIVATE KEY");
  });

  it("保存失败时把原因留在抽屉里", async () => {
    setup({ "POST /credentials": fail(409, 12010, "已经有同名的密钥") });
    const user = userEvent.setup();
    const dialog = await openCreate(user);
    await user.type(within(dialog).getByLabelText("名称"), "生产 root");
    await user.type(within(dialog).getByLabelText("密码"), "SECRET-PW-1");
    await user.click(within(dialog).getByRole("button", { name: "添加" }));

    expect(await within(dialog).findByText("已经有同名的密钥")).toBeInTheDocument();
    expect(screen.getByRole("dialog", { name: "添加密钥" })).toBeInTheDocument();
  });
});

describe("编辑密钥", () => {
  it("不回显已保存的内容，留空时提交空串表示保留", async () => {
    const api = setup();
    const user = userEvent.setup();
    const dialog = await openEdit(user, "生产 root");
    const pw = within(dialog).getByLabelText("密码");
    expect(pw).toHaveValue("");
    expect(pw).toHaveAttribute("placeholder", "已设置，留空保持不变");
    expect(within(dialog).getByLabelText("名称")).toHaveValue("生产 root");
    expect(within(dialog).getByLabelText("默认用户")).toHaveValue("root");

    await user.click(within(dialog).getByRole("button", { name: "保存" }));
    await waitFor(() => expect(api.find("PUT", "/credentials/1")).toHaveLength(1));
    expect(api.find("PUT", "/credentials/1")[0].body).toMatchObject({
      name: "生产 root",
      kind: "password",
      password: "",
      private_key: "",
      passphrase: "",
      username: "root",
    });
  });

  it("私钥类型留空时私钥与口令都以空串提交", async () => {
    const api = setup({ "PUT /credentials/2": (c) => ok({ ...OPS_KEY, ...(c.body as object) }) });
    const user = userEvent.setup();
    const dialog = await openEdit(user, "ops 私钥");
    expect(within(dialog).getByLabelText("私钥")).toHaveAttribute("placeholder", "已设置，留空保持不变");
    expect(within(dialog).getByLabelText("私钥口令")).toHaveAttribute("placeholder", "已设置，留空保持不变");
    await user.click(within(dialog).getByRole("button", { name: "保存" }));
    await waitFor(() => expect(api.find("PUT", "/credentials/2")).toHaveLength(1));
    expect(api.find("PUT", "/credentials/2")[0].body).toMatchObject({
      kind: "ssh_key",
      password: "",
      private_key: "",
      passphrase: "",
    });
  });

  it("被服务器使用时在抽屉底部列出这些服务器", async () => {
    setup();
    const user = userEvent.setup();
    const dialog = await openEdit(user, "生产 root");
    expect(within(dialog).getByText("被这些服务器使用")).toBeInTheDocument();
    expect(within(dialog).getByText(/web-01/)).toBeInTheDocument();
    expect(within(dialog).getByText(/nas-ops/)).toBeInTheDocument();
    // 使用者后面跟着「用户@地址」，一眼看出是哪台机器
    expect(within(dialog).getByText("root@10.0.3.11:22")).toBeInTheDocument();
    expect(within(dialog).getByText("ops@10.0.9.5:22")).toBeInTheDocument();
    expect(within(dialog).getByText(/改这里的密码或私钥，上面这些服务器会一起生效/)).toBeInTheDocument();
  });

  it("没有被使用时抽屉底部不显示使用者一节", async () => {
    setup();
    const user = userEvent.setup();
    const dialog = await openEdit(user, "旧机房密码");
    expect(within(dialog).queryByText("被这些服务器使用")).not.toBeInTheDocument();
  });
});

describe("删除密钥", () => {
  it("没有被使用时二次确认后删除并刷新", async () => {
    const api = setup();
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "删除 旧机房密码" }));
    const confirm = await screen.findByRole("alertdialog");
    expect(within(confirm).getByText("删除密钥「旧机房密码」？")).toBeInTheDocument();
    expect(within(confirm).getByText("删除后不能恢复。")).toBeInTheDocument();
    expect(api.find("DELETE", "/credentials/3")).toHaveLength(0);
    await user.click(within(confirm).getByRole("button", { name: "删除" }));
    await waitFor(() => expect(api.find("DELETE", "/credentials/3")).toHaveLength(1));
    await waitFor(() => expect(api.find("GET", "/credentials")).toHaveLength(2));
  });

  it("被服务器使用时主按钮不可用、写明原因且不发 DELETE", async () => {
    const api = setup();
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "删除 生产 root" }));
    const confirm = await screen.findByRole("alertdialog");
    expect(within(confirm).getByText(/有 4 台服务器使用这条密钥：web-01、web-02、db-01、nas-ops/)).toBeInTheDocument();
    const remove = within(confirm).getByRole("button", { name: "删除" });
    expect(remove).toBeDisabled();
    await user.click(remove);
    expect(api.find("DELETE", "/credentials/1")).toHaveLength(0);
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
  });
});
