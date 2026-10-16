import { describe, expect, it } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ServersPage } from "./servers";
import { renderPage } from "@/test/render";
import { fail, mockApi, ok, type Call } from "@/test/api-mock";

const FP = "SHA256:p8Wq3nVvJk2mT7rXh0bLc9ZsYd4eGfA1uN6oKiHwE5Q";

const BASTION = {
  id: 1,
  name: "bastion",
  host: "203.0.113.9",
  port: 22,
  username: "ops",
  auth_type: "private_key",
  password: "",
  private_key: "已设置",
  passphrase: "已设置",
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
  auth_type: "password",
  password: "已设置",
  private_key: "",
  passphrase: "",
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
  status: "failed",
  latency_ms: 0,
  fail_since: 1760000000 - 86400 * 3,
  last_error: "连接超时",
  job_count: 1,
};

function setup(extra: Parameters<typeof mockApi>[0] = {}) {
  const m = mockApi({
    "GET /servers": ok({ list: [BASTION, DB_INNER, OLD_NAS] }),
    "PUT /servers/2": (c) => ok({ ...DB_INNER, ...(c.body as object) }),
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

const last = (calls: Call[]) => calls[calls.length - 1];

describe("服务器列表", () => {
  it("显示名称、地址、认证方式、连接路径、状态和任务数", async () => {
    setup();
    const db = (await screen.findByText("db-inner")).closest("li")!;
    expect(within(db).getByText("backup@10.0.3.15:22")).toBeInTheDocument();
    expect(within(db).getByText("密码，3 个任务")).toBeInTheDocument();
    expect(within(db).getByText("proxy.example.com:1080")).toBeInTheDocument();
    expect(within(db).getByText("bastion")).toBeInTheDocument();
    expect(within(db).getByText(/正常/)).toHaveTextContent("134 ms");
    const bastion = screen.getAllByText("bastion")[0].closest("li")!;
    expect(within(bastion).getByText("直连")).toBeInTheDocument();
    expect(within(bastion).getByText("私钥，0 个任务")).toBeInTheDocument();
    const nas = screen.getByText("old-nas").closest("li")!;
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

describe("已设置 凭据", () => {
  it("编辑时不回显凭据，未改动的密码以「已设置」原样提交", async () => {
    const m = setup();
    const user = await openEdit("db-inner");
    const pw = screen.getByLabelText("密码");
    expect(pw).toHaveValue("");
    expect(pw).toHaveAttribute("placeholder", "已设置，留空保持不变");
    await user.clear(screen.getByLabelText("名称"));
    await user.type(screen.getByLabelText("名称"), "db-main");
    await user.click(screen.getByRole("button", { name: "保存" }));
    await waitFor(() => expect(m.find("PUT", "/servers/2")).toHaveLength(1));
    expect(m.find("PUT", "/servers/2")[0].body).toMatchObject({
      name: "db-main",
      auth_type: "password",
      password: "已设置",
      private_key: "",
      passphrase: "",
    });
  });

  it("填写新密码时提交新值", async () => {
    const m = setup();
    const user = await openEdit("db-inner");
    await user.type(screen.getByLabelText("密码"), "NEW-SECRET-1");
    await user.click(screen.getByRole("button", { name: "保存" }));
    await waitFor(() => expect(m.find("PUT", "/servers/2")).toHaveLength(1));
    expect(m.find("PUT", "/servers/2")[0].body).toMatchObject({ password: "NEW-SECRET-1" });
  });

  it.each([
    ["地址", "10.0.3.16"],
    ["端口", "2222"],
    ["用户", "root"],
  ])("改了%s后不再沿用已保存的密码，要求重新填写", async (field, value) => {
    const m = setup();
    const user = await openEdit("db-inner");
    await user.clear(screen.getByLabelText(field));
    await user.type(screen.getByLabelText(field), value);
    expect(screen.getByLabelText("密码")).not.toHaveAttribute("placeholder", "已设置，留空保持不变");
    await user.click(screen.getByRole("button", { name: "测试连接" }));
    expect(await screen.findByText("地址或用户已改动，请重新填写登录密码")).toBeInTheDocument();
    expect(m.find("POST", "/servers/test")).toHaveLength(0);
  });

  it("改了地址又改回原值，仍沿用已保存的密码", async () => {
    const m = setup({ "POST /servers/test": ok({ ok: true, hops: [], fingerprint: "" }) });
    const user = await openEdit("db-inner");
    await user.type(screen.getByLabelText("地址"), "9");
    await user.type(screen.getByLabelText("地址"), "{Backspace}");
    expect(screen.getByLabelText("密码")).toHaveAttribute("placeholder", "已设置，留空保持不变");
    await user.click(screen.getByRole("button", { name: "测试连接" }));
    await waitFor(() => expect(m.find("POST", "/servers/test")).toHaveLength(1));
    expect(m.find("POST", "/servers/test")[0].body).toMatchObject({ host: "10.0.3.15", password: "已设置" });
  });

  it("改了地址并填了新密码时提交新密码", async () => {
    const m = setup({ "POST /servers/test": ok({ ok: true, hops: [], fingerprint: "" }) });
    const user = await openEdit("db-inner");
    await user.type(screen.getByLabelText("地址"), "9");
    await user.type(screen.getByLabelText("密码"), "NEW-SECRET-2");
    await user.click(screen.getByRole("button", { name: "测试连接" }));
    await waitFor(() => expect(m.find("POST", "/servers/test")).toHaveLength(1));
    expect(m.find("POST", "/servers/test")[0].body).toMatchObject({ host: "10.0.3.159", password: "NEW-SECRET-2" });
  });

  it("私钥认证：私钥和口令未改动时都回传「已设置」，密码为空", async () => {
    const m = setup({ "PUT /servers/1": (c) => ok(c.body) });
    const user = await openEdit("bastion");
    expect(screen.getByLabelText("私钥")).toHaveValue("");
    expect(screen.getByLabelText("私钥口令")).toHaveValue("");
    await user.click(screen.getByRole("button", { name: "保存" }));
    await waitFor(() => expect(m.find("PUT", "/servers/1")).toHaveLength(1));
    expect(m.find("PUT", "/servers/1")[0].body).toMatchObject({
      auth_type: "private_key",
      password: "",
      private_key: "已设置",
      passphrase: "已设置",
    });
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
    // 编辑中的已保存服务器带 id，凭据占位由后端取回
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
