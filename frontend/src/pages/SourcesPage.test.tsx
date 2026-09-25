import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

import i18n from "@/i18n";
import type { ChannelItem } from "@/lib/sources";
import { SourcesPage } from "@/pages/SourcesPage";

const ok = (data: unknown) => new Response(JSON.stringify({ code: 0, msg: "success", data }), { status: 200 });
const fail = (code: number, msg: string, status = 400) => new Response(JSON.stringify({ code, msg }), { status });
const now = () => Math.floor(Date.now() / 1000);

const usedBy = (data_sources: { id: number; name: string }[] = [], channels: { id: number; name: string }[] = []) => ({
  data_sources,
  channels,
});

const officeSocks: ChannelItem = {
  id: 1,
  name: "office-socks",
  kind: "socks5",
  host: "10.8.0.1",
  port: 1080,
  username: "ops",
  auth_method: "password",
  has_password: true,
  has_private_key: false,
  has_passphrase: false,
  via_id: 0,
  address: "socks5://10.8.0.1:1080",
  chain: [{ id: 1, name: "office-socks", kind: "socks5", address: "10.8.0.1:1080" }],
  host_key: "",
  presented_host_key: "",
  used_by: usedBy([{ id: 10, name: "db-01" }], [{ id: 2, name: "bastion-prod" }]),
  status: "ok",
  status_message: "",
  checked_at: now() - 300,
  created_at: now() - 10000,
};

const bastionProd: ChannelItem = {
  id: 2,
  name: "bastion-prod",
  kind: "ssh",
  host: "bastion.corp",
  port: 22,
  username: "jump",
  auth_method: "key",
  has_password: false,
  has_private_key: true,
  has_passphrase: true,
  via_id: 1,
  address: "ssh://jump@bastion.corp:22",
  chain: [
    { id: 1, name: "office-socks", kind: "socks5", address: "10.8.0.1:1080" },
    { id: 2, name: "bastion-prod", kind: "ssh", address: "bastion.corp:22" },
  ],
  host_key: "SHA256:3fQ1bWmX0pZrK2sQn8vYt4LhE6cJ9dUa7gRiTfNoa9Kc",
  presented_host_key: "",
  used_by: usedBy(
    [
      { id: 10, name: "db-01" },
      { id: 11, name: "orders" },
    ],
    []
  ),
  status: "ok",
  status_message: "",
  checked_at: now() - 300,
  created_at: now() - 9000,
};

const bastionOld: ChannelItem = {
  id: 3,
  name: "bastion-old",
  kind: "ssh",
  host: "203.0.113.9",
  port: 2222,
  username: "ops",
  auth_method: "password",
  has_password: true,
  has_private_key: false,
  has_passphrase: false,
  via_id: 0,
  address: "ssh://ops@203.0.113.9:2222",
  chain: [{ id: 3, name: "bastion-old", kind: "ssh", address: "203.0.113.9:2222" }],
  host_key: "SHA256:oldSavedFingerprint",
  presented_host_key: "SHA256:Zk8pQe41VbN7mWc0rHs2LtYg5xJ3uKdA9iFoPlT0bw",
  used_by: usedBy([{ id: 12, name: "web-01" }], []),
  status: "host_key_changed",
  status_message: "新指纹 SHA256:Zk8p…T0bw",
  checked_at: now() - 300,
  created_at: now() - 8000,
};

/** deep-ssh 经由 bastion-prod，链路含 office-socks → bastion-prod → deep-ssh：用于成环校验 */
const deepSsh: ChannelItem = {
  id: 4,
  name: "deep-ssh",
  kind: "ssh",
  host: "deep.corp",
  port: 22,
  username: "root",
  auth_method: "password",
  has_password: true,
  has_private_key: false,
  has_passphrase: false,
  via_id: 2,
  address: "ssh://root@deep.corp:22",
  chain: [...bastionProd.chain, { id: 4, name: "deep-ssh", kind: "ssh", address: "deep.corp:22" }],
  host_key: "",
  presented_host_key: "",
  used_by: usedBy(),
  status: "ok",
  status_message: "",
  checked_at: now() - 300,
  created_at: now() - 7000,
};

let fetchMock: ReturnType<typeof vi.fn>;
function respond(...responses: Response[]) {
  for (const r of responses) fetchMock.mockResolvedValueOnce(r);
}
function call(i: number) {
  const [url, init] = fetchMock.mock.calls[i] as [string, RequestInit | undefined];
  return { url, method: init?.method ?? "GET", body: init?.body ? JSON.parse(init.body as string) : undefined };
}

beforeAll(() => {
  i18n.changeLanguage("zh-CN");
  // happy-dom 未实现的指针方法：Radix Select 依赖它们，测试中不关心真实指针捕获行为
  const proto = Element.prototype as unknown as Record<string, unknown>;
  proto.hasPointerCapture ??= () => false;
  proto.setPointerCapture ??= () => {};
  proto.releasePointerCapture ??= () => {};
  proto.scrollIntoView ??= () => {};
});
beforeEach(() => {
  fetchMock = vi.fn();
  vi.stubGlobal("fetch", fetchMock);
});
afterEach(() => vi.unstubAllGlobals());

function renderPage() {
  render(<SourcesPage />);
}

async function openCreate() {
  await userEvent.click(await screen.findByRole("button", { name: "新建通道" }));
  return screen.findByRole("dialog", { name: "新建网络通道" });
}

async function chooseVia(dialog: HTMLElement, name: string) {
  await userEvent.click(within(dialog).getByRole("combobox", { name: "经由" }));
  await userEvent.click(await screen.findByRole("option", { name }));
}

describe("SourcesPage", () => {
  it("显示两个分页，默认在「数据源」分页展示占位内容", async () => {
    respond(ok({ items: [officeSocks, bastionProd, bastionOld] }));
    renderPage();
    expect(await screen.findByRole("tab", { name: /数据源/ })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByText("数据源管理即将推出")).toBeInTheDocument();
    // 数据源分页没有新建按钮（task 8 才实现）
    expect(screen.queryByRole("button", { name: "新建通道" })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("tab", { name: /网络通道/ }));
    expect(await screen.findByRole("button", { name: "新建通道" })).toBeInTheDocument();
  });

  it("加载失败时显示原因与重试", async () => {
    respond(fail(-1, "网络错误", 500));
    renderPage();
    await userEvent.click(await screen.findByRole("tab", { name: /网络通道/ }));
    expect(await screen.findByText("无法加载网络通道列表：网络错误")).toBeInTheDocument();
    respond(ok({ items: [] }));
    await userEvent.click(screen.getByRole("button", { name: "重试" }));
    expect(await screen.findByText("还没有通道")).toBeInTheDocument();
  });

  it("空列表显示引导新建", async () => {
    respond(ok({ items: [] }));
    renderPage();
    await userEvent.click(await screen.findByRole("tab", { name: /网络通道/ }));
    expect(await screen.findByText("还没有通道")).toBeInTheDocument();
    expect(screen.getByText("新建一个 SSH 跳板或 SOCKS5 代理，供数据源经由连接。")).toBeInTheDocument();
  });

  it("列表显示地址、经由链路与认证方式、使用情况与状态", async () => {
    respond(ok({ items: [officeSocks, bastionProd, bastionOld] }));
    renderPage();
    await userEvent.click(await screen.findByRole("tab", { name: /网络通道/ }));
    const rows = await screen.findAllByRole("row");
    const socksRow = rows[1];
    expect(within(socksRow).getByText("socks5://10.8.0.1:1080")).toBeInTheDocument();
    expect(within(socksRow).getByText("直连 · 用户名密码认证")).toBeInTheDocument();
    expect(within(socksRow).getByText("1 个数据源 · 1 个通道")).toBeInTheDocument();
    expect(within(socksRow).getByText("正常")).toBeInTheDocument();

    const bastionRow = rows[2];
    expect(within(bastionRow).getByText("ssh://jump@bastion.corp:22")).toBeInTheDocument();
    expect(within(bastionRow).getByText("经 office-socks · 私钥认证")).toBeInTheDocument();
    expect(within(bastionRow).getByText("2 个数据源")).toBeInTheDocument();

    const oldRow = rows[3];
    expect(within(oldRow).getByText("主机密钥已变化")).toBeInTheDocument();
    // 主机密钥已变化的行在“重新确认”之外，仍保留测试连接、编辑与更多菜单（删除通道）
    expect(within(oldRow).getByRole("button", { name: "重新确认 bastion-old 的主机密钥" })).toBeInTheDocument();
    expect(within(oldRow).getByRole("button", { name: "测试连接 bastion-old" })).toBeInTheDocument();
    expect(within(oldRow).getByRole("button", { name: "编辑 bastion-old" })).toBeInTheDocument();
    expect(within(oldRow).getByRole("button", { name: "bastion-old 的更多操作" })).toBeInTheDocument();
  });

  it("经由选择框排除自己与会成环的通道", async () => {
    respond(ok({ items: [officeSocks, bastionProd, bastionOld, deepSsh] }));
    renderPage();
    await userEvent.click(await screen.findByRole("tab", { name: /网络通道/ }));
    await userEvent.click((await screen.findAllByRole("button", { name: "编辑 bastion-prod" }))[0]);
    const dialog = await screen.findByRole("dialog", { name: "编辑网络通道" });
    await userEvent.click(within(dialog).getByRole("combobox", { name: "经由" }));
    expect(await screen.findByRole("option", { name: "不选，由 OpsNap 直连" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "office-socks" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "bastion-old" })).toBeInTheDocument();
    expect(screen.queryByRole("option", { name: "bastion-prod" })).not.toBeInTheDocument();
    expect(screen.queryByRole("option", { name: "deep-ssh" })).not.toBeInTheDocument();
  });

  it("实时显示链路预览，并在超过 5 跳时禁止保存", async () => {
    // hop-5 自身的链路已有 5 跳（OpsNap → hop-1 → … → hop-5）
    const hop5: ChannelItem = {
      ...bastionOld,
      id: 20,
      name: "hop-5",
      via_id: 0,
      chain: Array.from({ length: 5 }, (_, i) => ({
        id: 20 - (4 - i),
        name: `hop-${i + 1}`,
        kind: "ssh" as const,
        address: "h:22",
      })),
      used_by: usedBy(),
      status: "ok",
      status_message: "",
    };
    respond(ok({ items: [hop5] }));
    renderPage();
    await userEvent.click(await screen.findByRole("tab", { name: /网络通道/ }));
    const dialog = await openCreate();
    await userEvent.type(within(dialog).getByLabelText("名称"), "bastion-prod");
    expect(within(dialog).getByText("链路：OpsNap → ?。不能选择会形成环路的通道，链路最多 5 跳。")).toBeInTheDocument();
    await userEvent.type(within(dialog).getByLabelText("主机"), "bastion.corp");
    expect(within(dialog).getByText(/OpsNap → bastion\.corp:22/)).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "保存" })).not.toBeDisabled();

    await chooseVia(dialog, "hop-5");
    expect(
      within(dialog).getByText(
        "链路：OpsNap → hop-1 (SSH) → hop-2 (SSH) → hop-3 (SSH) → hop-4 (SSH) → hop-5 (SSH) → bastion.corp:22。不能选择会形成环路的通道，链路最多 5 跳。"
      )
    ).toBeInTheDocument();
    expect(await within(dialog).findByText("链路共 6 跳，超过上限 5 跳，无法保存。")).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "保存" })).toBeDisabled();
  });

  it("首次连接主机密钥：确认后带指纹重试并保存", async () => {
    respond(ok({ items: [officeSocks] }));
    renderPage();
    await userEvent.click(await screen.findByRole("tab", { name: /网络通道/ }));
    const dialog = await openCreate();
    await userEvent.type(within(dialog).getByLabelText("名称"), "bastion-prod");
    await userEvent.type(within(dialog).getByLabelText("主机"), "bastion.corp");
    await userEvent.type(within(dialog).getByLabelText("用户名"), "jump");

    respond(
      ok({
        host_key: {
          hop: 1,
          name: "bastion-prod",
          address: "bastion.corp:22",
          key_type: "ED25519",
          fingerprint: "SHA256:3fQ1bWmX0pZrK2sQn8vYt4LhE6cJ9dUa7gRiTfNoa9Kc",
          changed: false,
          saved: "",
        },
        chain: [],
      })
    );
    await userEvent.click(within(dialog).getByRole("button", { name: "测试连接" }));

    const confirm = await screen.findByRole("dialog", { name: "确认主机密钥" });
    expect(within(confirm).getByText("ED25519")).toBeInTheDocument();
    expect(within(confirm).getByText("SHA256:3fQ1bWmX0pZrK2sQn8vYt4LhE6cJ9dUa7gRiTfNoa9Kc")).toBeInTheDocument();

    respond(ok({ host_key: null, chain: [{ id: 0, name: "bastion-prod", kind: "ssh", address: "bastion.corp:22" }] }));
    await userEvent.click(within(confirm).getByRole("button", { name: "信任并继续" }));

    await screen.findByText("连接成功，链路已确认。");
    // call(0) 列表、call(1) 首次探测（host_key 为空）、call(2) 带指纹重试
    expect(call(1).body.channel.host_key).toBe("");
    expect(call(2).body.channel.host_key).toBe("SHA256:3fQ1bWmX0pZrK2sQn8vYt4LhE6cJ9dUa7gRiTfNoa9Kc");
    expect(screen.queryByRole("dialog", { name: "确认主机密钥" })).not.toBeInTheDocument();
  });

  it("取消确认主机密钥：不保存，弹窗关闭", async () => {
    respond(ok({ items: [officeSocks] }));
    renderPage();
    await userEvent.click(await screen.findByRole("tab", { name: /网络通道/ }));
    const dialog = await openCreate();
    await userEvent.type(within(dialog).getByLabelText("名称"), "x");
    await userEvent.type(within(dialog).getByLabelText("主机"), "x.corp");
    await userEvent.type(within(dialog).getByLabelText("用户名"), "root");
    respond(
      ok({
        host_key: {
          hop: 1,
          name: "x",
          address: "x.corp:22",
          key_type: "ED25519",
          fingerprint: "SHA256:aaa",
          changed: false,
          saved: "",
        },
        chain: [],
      })
    );
    await userEvent.click(within(dialog).getByRole("button", { name: "测试连接" }));
    const confirm = await screen.findByRole("dialog", { name: "确认主机密钥" });
    await userEvent.click(within(confirm).getByRole("button", { name: "取消" }));
    expect(screen.queryByRole("dialog", { name: "确认主机密钥" })).not.toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledTimes(2); // 列表 + 一次探测，取消后不再重试
  });

  it("编辑时秘密字段显示已保存提示，留空表示不修改", async () => {
    respond(ok({ items: [bastionProd] }));
    renderPage();
    await userEvent.click(await screen.findByRole("tab", { name: /网络通道/ }));
    await userEvent.click(await screen.findByRole("button", { name: "编辑 bastion-prod" }));
    const dialog = await screen.findByRole("dialog", { name: "编辑网络通道" });
    const privateKey = within(dialog).getByLabelText("私钥");
    expect(privateKey).toHaveAttribute("placeholder", "已保存（加密存储），留空表示不修改");
    expect(privateKey).toHaveValue("");
    const passphrase = within(dialog).getByLabelText(/私钥口令/);
    expect(passphrase).toHaveAttribute("placeholder", "已保存（加密存储），留空表示不修改");

    respond(ok({ item: { ...bastionProd, name: "bastion-prod-2" }, host_key: null }));
    await userEvent.clear(within(dialog).getByLabelText("名称"));
    await userEvent.type(within(dialog).getByLabelText("名称"), "bastion-prod-2");
    await userEvent.click(within(dialog).getByRole("button", { name: "保存" }));
    await screen.findByText("bastion-prod-2");
    expect(call(1).body.channel.private_key).toBe("");
    expect(call(1).body.channel.passphrase).toBe("");
    expect(call(1).url).toBe("/api/v1/channels/2");
    expect(call(1).method).toBe("PUT");
  });

  it("主机密钥已变化：并列显示保存与出示的指纹，信任后重新测试", async () => {
    respond(ok({ items: [bastionOld] }));
    renderPage();
    await userEvent.click(await screen.findByRole("tab", { name: /网络通道/ }));
    await userEvent.click(await screen.findByRole("button", { name: "重新确认 bastion-old 的主机密钥" }));
    const dialog = await screen.findByRole("dialog", { name: "主机密钥已变化" });
    expect(within(dialog).getByText("SHA256:oldSavedFingerprint")).toBeInTheDocument();
    expect(within(dialog).getByText("SHA256:Zk8pQe41VbN7mWc0rHs2LtYg5xJ3uKdA9iFoPlT0bw")).toBeInTheDocument();

    respond(ok({ item: { ...bastionOld, status: "ok", host_key: bastionOld.presented_host_key }, host_key: null }));
    await userEvent.click(within(dialog).getByRole("button", { name: "信任新密钥" }));
    expect(call(1).url).toBe("/api/v1/channels/3/host-key");
    expect(call(1).body.fingerprint).toBe(bastionOld.presented_host_key);
    await screen.findByText("正常");
    expect(screen.queryByRole("dialog", { name: "主机密钥已变化" })).not.toBeInTheDocument();
  });

  it("编辑表单也能打开「主机密钥已变化」弹窗", async () => {
    respond(ok({ items: [bastionOld] }));
    renderPage();
    await userEvent.click(await screen.findByRole("tab", { name: /网络通道/ }));
    await userEvent.click(await screen.findByRole("button", { name: "编辑 bastion-old" }));
    const form = await screen.findByRole("dialog", { name: "编辑网络通道" });
    await userEvent.click(within(form).getByRole("button", { name: "重新确认" }));
    const changed = await screen.findByRole("dialog", { name: "主机密钥已变化" });
    expect(within(changed).getByText("SHA256:oldSavedFingerprint")).toBeInTheDocument();
    expect(within(changed).getByText("SHA256:Zk8pQe41VbN7mWc0rHs2LtYg5xJ3uKdA9iFoPlT0bw")).toBeInTheDocument();
  });

  it("删除受保护：显示引用它的对象，菜单项不可用；无引用时可删除", async () => {
    respond(ok({ items: [officeSocks, deepSsh] }));
    renderPage();
    await userEvent.click(await screen.findByRole("tab", { name: /网络通道/ }));
    await userEvent.click(await screen.findByRole("button", { name: "office-socks 的更多操作" }));
    const menuItem = await screen.findByRole("menuitem");
    expect(menuItem).toHaveAttribute("aria-disabled", "true");
    expect(within(menuItem).getByText("仍被 db-01、bastion-prod 使用，不能删除")).toBeInTheDocument();
    await userEvent.keyboard("{Escape}");

    await userEvent.click(await screen.findByRole("button", { name: "deep-ssh 的更多操作" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "删除通道" }));
    const confirmDialog = await screen.findByRole("dialog", { name: "删除通道「deep-ssh」？" });
    respond(ok({}));
    await userEvent.click(within(confirmDialog).getByRole("button", { name: "删除通道" }));
    await vi.waitFor(() => expect(screen.queryByText("deep-ssh")).not.toBeInTheDocument());
    expect(screen.getByText("office-socks")).toBeInTheDocument();
    // call(0) 列表，call(1) 删除请求
    expect(call(1)).toMatchObject({ url: "/api/v1/channels/4", method: "DELETE" });
  });
});
