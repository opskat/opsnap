import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router";

import i18n from "@/i18n";
import type { ChannelItem, DataSourceItem } from "@/lib/sources";
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
  presented_host_key: "SHA256:Zk8pQe41VbN7mWc0rHs2LtYg5xJ3uKdA9iFoPlT0bw",
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

/** s3 只被通道 s4 引用（无数据源引用），s4 本身不被任何对象引用，可直接删除：用于 V13 回归 */
const s3: ChannelItem = {
  id: 30,
  name: "s3",
  kind: "socks5",
  host: "10.0.0.3",
  port: 1080,
  username: "",
  auth_method: "none",
  has_password: false,
  has_private_key: false,
  has_passphrase: false,
  via_id: 0,
  address: "socks5://10.0.0.3:1080",
  chain: [{ id: 30, name: "s3", kind: "socks5", address: "10.0.0.3:1080" }],
  host_key: "",
  presented_host_key: "",
  used_by: usedBy([], [{ id: 31, name: "s4" }]),
  status: "ok",
  status_message: "",
  checked_at: now() - 300,
  created_at: now() - 6000,
};

const s4: ChannelItem = {
  id: 31,
  name: "s4",
  kind: "socks5",
  host: "10.0.0.4",
  port: 1080,
  username: "",
  auth_method: "none",
  has_password: false,
  has_private_key: false,
  has_passphrase: false,
  via_id: 30,
  address: "socks5://10.0.0.4:1080",
  chain: [
    { id: 30, name: "s3", kind: "socks5", address: "10.0.0.3:1080" },
    { id: 31, name: "s4", kind: "socks5", address: "10.0.0.4:1080" },
  ],
  host_key: "",
  presented_host_key: "",
  used_by: usedBy(),
  status: "ok",
  status_message: "",
  checked_at: now() - 300,
  created_at: now() - 5000,
};

/** c1 被数据源 ds1 引用：用于验证删除数据源后通道的使用数刷新（V13） */
const c1: ChannelItem = {
  id: 40,
  name: "c1",
  kind: "ssh",
  host: "10.0.0.40",
  port: 22,
  username: "ops",
  auth_method: "password",
  has_password: true,
  has_private_key: false,
  has_passphrase: false,
  via_id: 0,
  address: "ssh://ops@10.0.0.40:22",
  chain: [{ id: 40, name: "c1", kind: "ssh", address: "10.0.0.40:22" }],
  host_key: "",
  presented_host_key: "",
  used_by: usedBy([{ id: 201, name: "ds1" }], []),
  status: "ok",
  status_message: "",
  checked_at: now() - 300,
  created_at: now() - 4000,
};

const ds1: DataSourceItem = {
  id: 201,
  name: "ds1",
  kind: "mysql",
  host: "10.0.0.41",
  port: 3306,
  username: "backup",
  auth_method: "password",
  has_password: true,
  has_private_key: false,
  has_passphrase: false,
  database: "",
  tls_mode: "disable",
  tls_ca: "",
  tls_client_cert: "",
  has_tls_client_key: false,
  channel_id: 40,
  address: "mysql://10.0.0.41:3306",
  chain: [
    { id: 40, name: "c1", kind: "ssh", address: "10.0.0.40:22" },
    { id: 0, name: "ds1", kind: "mysql", address: "10.0.0.41:3306" },
  ],
  server: { version: "8.0.36", system: "", tls: null },
  host_key: "",
  presented_host_key: "",
  status: "ok",
  status_message: "",
  failed_hop: null,
  checked_at: now() - 300,
  created_at: now() - 4000,
  probe: { state: "done", ok: 5, warn: 0, fail: 0, items: [], time: now() - 300 },
};

// ---- 数据源 fixtures ----

const dbOrders: DataSourceItem = {
  id: 101,
  name: "db-01 · orders",
  kind: "mysql",
  host: "10.0.1.11",
  port: 3306,
  username: "backup",
  auth_method: "password",
  has_password: true,
  has_private_key: false,
  has_passphrase: false,
  database: "",
  tls_mode: "verify_full",
  tls_ca: "",
  tls_client_cert: "",
  has_tls_client_key: false,
  channel_id: 2,
  address: "mysql://10.0.1.11:3306",
  chain: [
    { id: 1, name: "office-socks", kind: "socks5", address: "10.8.0.1:1080" },
    { id: 2, name: "bastion-prod", kind: "ssh", address: "bastion.corp:22" },
    { id: 0, name: "db-01 · orders", kind: "mysql", address: "10.0.1.11:3306" },
  ],
  server: { version: "8.0.36", system: "", tls: { version: "TLS 1.3", verified: true } },
  host_key: "",
  presented_host_key: "",
  status: "ok",
  status_message: "",
  failed_hop: null,
  checked_at: now() - 120,
  created_at: now() - 50000,
  probe: { state: "done", ok: 7, warn: 1, fail: 0, items: [], time: now() - 120 },
};

const pgAnalytics: DataSourceItem = {
  id: 102,
  name: "pg-analytics-02",
  kind: "postgres",
  host: "10.0.2.7",
  port: 5432,
  username: "backup",
  auth_method: "password",
  has_password: true,
  has_private_key: false,
  has_passphrase: false,
  database: "postgres",
  tls_mode: "disable",
  tls_ca: "",
  tls_client_cert: "",
  has_tls_client_key: false,
  channel_id: 0,
  address: "postgres://10.0.2.7:5432",
  chain: [{ id: 0, name: "pg-analytics-02", kind: "postgres", address: "10.0.2.7:5432" }],
  server: { version: "16.2", system: "", tls: null },
  host_key: "",
  presented_host_key: "",
  status: "ok",
  status_message: "",
  failed_hop: null,
  checked_at: now() - 3600,
  created_at: now() - 40000,
  probe: { state: "done", ok: 6, warn: 0, fail: 0, items: [], time: now() - 3600 },
};

const web01: DataSourceItem = {
  id: 103,
  name: "web-01",
  kind: "server_file",
  host: "web-01",
  port: 22,
  username: "deploy",
  auth_method: "key",
  has_password: false,
  has_private_key: true,
  has_passphrase: false,
  database: "",
  tls_mode: "prefer",
  tls_ca: "",
  tls_client_cert: "",
  has_tls_client_key: false,
  channel_id: 2,
  address: "ssh://deploy@web-01:22",
  chain: [
    { id: 1, name: "office-socks", kind: "socks5", address: "10.8.0.1:1080" },
    { id: 2, name: "bastion-prod", kind: "ssh", address: "bastion.corp:22" },
    { id: 0, name: "web-01", kind: "server_file", address: "web-01:22" },
  ],
  server: { version: "", system: "Linux x86_64", tls: null },
  host_key: "SHA256:webSavedFingerprint",
  presented_host_key: "",
  status: "ok",
  status_message: "",
  failed_hop: null,
  checked_at: now() - 30,
  created_at: now() - 30000,
  probe: { state: "done", ok: 3, warn: 0, fail: 1, items: [], time: now() - 30 },
};

const dbLegacy: DataSourceItem = {
  id: 104,
  name: "db-legacy",
  kind: "mysql",
  host: "10.0.9.3",
  port: 3306,
  username: "backup",
  auth_method: "password",
  has_password: true,
  has_private_key: false,
  has_passphrase: false,
  database: "",
  tls_mode: "prefer",
  tls_ca: "",
  tls_client_cert: "",
  has_tls_client_key: false,
  channel_id: 3,
  address: "mysql://10.0.9.3:3306",
  chain: [
    { id: 3, name: "bastion-old", kind: "ssh", address: "203.0.113.9:2222" },
    { id: 0, name: "db-legacy", kind: "mysql", address: "10.0.9.3:3306" },
  ],
  server: { version: "5.7.44", system: "", tls: null },
  host_key: "",
  presented_host_key: "",
  status: "host_key_changed",
  status_message: "跳板 bastion-old 的密钥与保存的不一致",
  failed_hop: { hop: 1, channel_id: 3, name: "bastion-old", kind: "ssh" },
  checked_at: now() - 60,
  created_at: now() - 20000,
  probe: { state: "unprobeable", ok: 0, warn: 0, fail: 0, error: "无法连接", time: now() - 3000 },
};

const pgReport: DataSourceItem = {
  id: 105,
  name: "pg-report",
  kind: "postgres",
  host: "10.0.3.20",
  port: 5432,
  username: "backup",
  auth_method: "password",
  has_password: true,
  has_private_key: false,
  has_passphrase: false,
  database: "postgres",
  tls_mode: "prefer",
  tls_ca: "",
  tls_client_cert: "",
  has_tls_client_key: false,
  channel_id: 0,
  address: "postgres://10.0.3.20:5432",
  chain: [{ id: 0, name: "pg-report", kind: "postgres", address: "10.0.3.20:5432" }],
  server: null,
  host_key: "",
  presented_host_key: "",
  status: "unreachable",
  status_message: "认证失败：password authentication failed",
  failed_hop: { hop: 1, channel_id: 0, name: "pg-report", kind: "postgres" },
  checked_at: now() - 10,
  created_at: now() - 10000,
  probe: { state: "unprobeable", ok: 0, warn: 0, fail: 0, error: "认证失败", time: 0 },
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
  render(
    <MemoryRouter>
      <SourcesPage />
    </MemoryRouter>
  );
}

/** 页面加载时会同时请求数据源与通道两个列表：数据源分页是默认分页，先请求；通道列表随后请求 */
function respondLists(dataSources: DataSourceItem[], channels: ChannelItem[]) {
  respond(ok({ items: dataSources }), ok({ items: channels }));
}

async function openCreateChannel() {
  await userEvent.click(await screen.findByRole("tab", { name: /网络通道/ }));
  await userEvent.click(await screen.findByRole("button", { name: "新建通道" }));
  return screen.findByRole("dialog", { name: "新建网络通道" });
}

// 数据源分页为空时，页头与空状态卡片会同时出现同名的“新建数据源”按钮；
// 按钮查询限定在页头（<header> 的 banner 角色）范围内，避免二义匹配
async function openCreateDataSource() {
  const header = await screen.findByRole("banner");
  await userEvent.click(within(header).getByRole("button", { name: "新建数据源" }));
  return screen.findByRole("dialog", { name: "新建数据源" });
}

async function chooseVia(dialog: HTMLElement, name: string) {
  await userEvent.click(within(dialog).getByRole("combobox", { name: "经由" }));
  await userEvent.click(await screen.findByRole("option", { name }));
}

describe("SourcesPage · 网络通道", () => {
  it("显示两个分页，默认在「数据源」分页", async () => {
    // 数据源分页非空，避免页头与空状态卡片同时出现同名按钮
    respondLists([dbOrders], [officeSocks, bastionProd, bastionOld]);
    renderPage();
    expect(await screen.findByRole("tab", { name: /数据源/ })).toHaveAttribute("aria-selected", "true");
    expect(await screen.findByRole("button", { name: "新建数据源" })).toBeInTheDocument();
    await userEvent.click(screen.getByRole("tab", { name: /网络通道/ }));
    expect(await screen.findByRole("button", { name: "新建通道" })).toBeInTheDocument();
  });

  it("加载失败时显示原因与重试", async () => {
    respond(ok({ items: [] }), fail(-1, "网络错误", 500));
    renderPage();
    await userEvent.click(await screen.findByRole("tab", { name: /网络通道/ }));
    expect(await screen.findByText("无法加载网络通道列表：网络错误")).toBeInTheDocument();
    respond(ok({ items: [] }));
    await userEvent.click(screen.getByRole("button", { name: "重试" }));
    expect(await screen.findByText("还没有通道")).toBeInTheDocument();
  });

  it("空列表显示引导新建", async () => {
    respondLists([], []);
    renderPage();
    await userEvent.click(await screen.findByRole("tab", { name: /网络通道/ }));
    expect(await screen.findByText("还没有通道")).toBeInTheDocument();
    expect(screen.getByText("新建一个 SSH 跳板或 SOCKS5 代理，供数据源经由连接。")).toBeInTheDocument();
  });

  it("列表显示地址、经由链路与认证方式、使用情况与状态", async () => {
    respondLists([], [officeSocks, bastionProd, bastionOld]);
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

  it("经由多跳的通道显示完整的经由链路", async () => {
    respondLists([], [officeSocks, bastionProd, deepSsh]);
    renderPage();
    await userEvent.click(await screen.findByRole("tab", { name: /网络通道/ }));
    const rows = await screen.findAllByRole("row");
    expect(within(rows[3]).getByText("经 office-socks → bastion-prod · 密码认证")).toBeInTheDocument();
  });

  it("经由选择框排除自己与会成环的通道", async () => {
    respondLists([], [officeSocks, bastionProd, bastionOld, deepSsh]);
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
    respondLists([], [hop5]);
    renderPage();
    await userEvent.click(await screen.findByRole("tab", { name: /网络通道/ }));
    const dialog = await screen.findByRole("button", { name: "新建通道" }).then(async (btn) => {
      await userEvent.click(btn);
      return screen.findByRole("dialog", { name: "新建网络通道" });
    });
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

  it("服务端拒绝经由（例如经过它的通道会使链路超过 5 跳）时，在经由选择框下显示原因", async () => {
    respondLists([], [officeSocks]);
    renderPage();
    const dialog = await openCreateChannel();
    await userEvent.type(within(dialog).getByLabelText("名称"), "edge");
    await userEvent.type(within(dialog).getByLabelText("主机"), "edge.corp");
    await userEvent.type(within(dialog).getByLabelText("用户名"), "ops");
    await userEvent.type(within(dialog).getByLabelText("密码"), "pw");
    await chooseVia(dialog, "office-socks");
    respond(fail(10515, "链路共 6 跳，超过上限 5 跳"));
    await userEvent.click(within(dialog).getByRole("button", { name: "保存" }));
    expect(await within(dialog).findByText("链路共 6 跳，超过上限 5 跳")).toBeInTheDocument();
  });

  it("首次连接主机密钥：确认后带指纹重试并保存", async () => {
    respondLists([], [officeSocks]);
    renderPage();
    const dialog = await openCreateChannel();
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
    // 弹窗显示经由的链路（与表单下方的链路预览一致）
    expect(within(confirm).getByText("OpsNap → bastion.corp:22")).toBeInTheDocument();

    respond(ok({ host_key: null, chain: [{ id: 0, name: "bastion-prod", kind: "ssh", address: "bastion.corp:22" }] }));
    await userEvent.click(within(confirm).getByRole("button", { name: "信任并继续" }));

    await screen.findByText("连接成功，链路已确认。");
    // call(0) 数据源列表、call(1) 通道列表、call(2) 首次探测（host_key 为空）、call(3) 带指纹重试
    expect(call(2).body.channel.host_key).toBe("");
    expect(call(3).body.channel.host_key).toBe("SHA256:3fQ1bWmX0pZrK2sQn8vYt4LhE6cJ9dUa7gRiTfNoa9Kc");
    expect(screen.queryByRole("dialog", { name: "确认主机密钥" })).not.toBeInTheDocument();
  });

  it("取消确认主机密钥：不保存，弹窗关闭", async () => {
    respondLists([], [officeSocks]);
    renderPage();
    const dialog = await openCreateChannel();
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
    expect(fetchMock).toHaveBeenCalledTimes(3); // 数据源列表 + 通道列表 + 一次探测，取消后不再重试
  });

  it("编辑时秘密字段显示已保存提示，留空表示不修改", async () => {
    respondLists([], [bastionProd]);
    renderPage();
    await userEvent.click(await screen.findByRole("tab", { name: /网络通道/ }));
    await userEvent.click(await screen.findByRole("button", { name: "编辑 bastion-prod" }));
    const dialog = await screen.findByRole("dialog", { name: "编辑网络通道" });
    const privateKey = within(dialog).getByLabelText("私钥");
    expect(privateKey).toHaveAttribute("placeholder", "已保存（加密存储），留空表示不修改");
    expect(privateKey).toHaveValue("");
    const passphrase = within(dialog).getByLabelText(/私钥口令/);
    expect(passphrase).toHaveAttribute("placeholder", "已保存（加密存储），留空表示不修改");

    // call(2) 保存请求，call(3) 保存后刷新通道列表
    const renamed = { ...bastionProd, name: "bastion-prod-2" };
    respond(ok({ item: renamed, host_key: null }), ok({ items: [renamed] }));
    await userEvent.clear(within(dialog).getByLabelText("名称"));
    await userEvent.type(within(dialog).getByLabelText("名称"), "bastion-prod-2");
    await userEvent.click(within(dialog).getByRole("button", { name: "保存" }));
    await screen.findByText("bastion-prod-2");
    expect(call(2).body.channel.private_key).toBe("");
    expect(call(2).body.channel.passphrase).toBe("");
    expect(call(2).url).toBe("/api/v1/channels/2");
    expect(call(2).method).toBe("PUT");
  });

  it("主机密钥已变化：并列显示保存与出示的指纹，信任后重新测试", async () => {
    respondLists([], [bastionOld]);
    renderPage();
    await userEvent.click(await screen.findByRole("tab", { name: /网络通道/ }));
    await userEvent.click(await screen.findByRole("button", { name: "重新确认 bastion-old 的主机密钥" }));
    const dialog = await screen.findByRole("dialog", { name: "主机密钥已变化" });
    expect(within(dialog).getByText("SHA256:oldSavedFingerprint")).toBeInTheDocument();
    expect(within(dialog).getByText("SHA256:Zk8pQe41VbN7mWc0rHs2LtYg5xJ3uKdA9iFoPlT0bw")).toBeInTheDocument();

    respond(ok({ item: { ...bastionOld, status: "ok", host_key: bastionOld.presented_host_key }, host_key: null }));
    await userEvent.click(within(dialog).getByRole("button", { name: "信任新密钥" }));
    expect(call(2).url).toBe("/api/v1/channels/3/host-key");
    expect(call(2).body.fingerprint).toBe(bastionOld.presented_host_key);
    await screen.findByText("正常");
    expect(screen.queryByRole("dialog", { name: "主机密钥已变化" })).not.toBeInTheDocument();
  });

  it("编辑表单也能打开「主机密钥已变化」弹窗", async () => {
    respondLists([], [bastionOld]);
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
    respondLists([], [officeSocks, deepSsh]);
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
    // call(2) 删除请求，call(3) 删除后刷新通道列表
    respond(ok({}), ok({ items: [officeSocks] }));
    await userEvent.click(within(confirmDialog).getByRole("button", { name: "删除通道" }));
    await vi.waitFor(() => expect(screen.queryByText("deep-ssh")).not.toBeInTheDocument());
    expect(screen.getByText("office-socks")).toBeInTheDocument();
    // call(0) 数据源列表，call(1) 通道列表，call(2) 删除请求
    expect(call(2)).toMatchObject({ url: "/api/v1/channels/4", method: "DELETE" });
  });

  it("删除下游通道后，上游通道的使用数与删除保护立即刷新，无需重新加载页面", async () => {
    respondLists([], [s3, s4]);
    renderPage();
    await userEvent.click(await screen.findByRole("tab", { name: /网络通道/ }));
    let rows = await screen.findAllByRole("row");
    expect(within(rows[1]).getByText("1 个通道")).toBeInTheDocument();

    await userEvent.click(await screen.findByRole("button", { name: "s4 的更多操作" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "删除通道" }));
    const confirmDialog = await screen.findByRole("dialog", { name: "删除通道「s4」？" });
    // call(2) 删除请求，call(3) 删除后刷新通道列表：接口已返回 s3 的 used_by 为空
    respond(ok({}), ok({ items: [{ ...s3, used_by: usedBy() }] }));
    await userEvent.click(within(confirmDialog).getByRole("button", { name: "删除通道" }));
    await vi.waitFor(() => expect(screen.queryByText("s4")).not.toBeInTheDocument());

    rows = await screen.findAllByRole("row");
    expect(within(rows[1]).getByText("未被使用")).toBeInTheDocument();
    await userEvent.click(within(rows[1]).getByRole("button", { name: "s3 的更多操作" }));
    expect(await screen.findByRole("menuitem", { name: "删除通道" })).not.toHaveAttribute("aria-disabled", "true");
  });

  it("新建经由上游通道的通道后，上游通道的使用数与删除保护立即刷新", async () => {
    const s3Unused = { ...s3, used_by: usedBy() };
    respondLists([], [s3Unused]);
    renderPage();
    const dialog = await openCreateChannel();
    await userEvent.click(within(dialog).getByRole("radio", { name: "SOCKS5 代理" }));
    await userEvent.type(within(dialog).getByLabelText("名称"), "s4");
    await userEvent.type(within(dialog).getByLabelText("主机"), "10.0.0.4");
    await chooseVia(dialog, "s3");
    // call(2) 保存请求，call(3) 保存后刷新通道列表：接口已返回 s3 被 s4 使用
    respond(ok({ item: s4, host_key: null }), ok({ items: [s3, s4] }));
    await userEvent.click(within(dialog).getByRole("button", { name: "保存" }));
    await vi.waitFor(() => expect(screen.queryByRole("dialog", { name: "新建网络通道" })).not.toBeInTheDocument());

    const rows = await screen.findAllByRole("row");
    expect(await within(rows[1]).findByText("1 个通道")).toBeInTheDocument();
    await userEvent.click(within(rows[1]).getByRole("button", { name: "s3 的更多操作" }));
    expect(await screen.findByRole("menuitem", { name: /删除通道/ })).toHaveAttribute("aria-disabled", "true");
  });

  it("英文界面中使用数为 1 时使用单数形式", async () => {
    respondLists(
      [],
      [{ ...officeSocks, used_by: usedBy([{ id: 10, name: "db-01" }], [{ id: 2, name: "bastion-prod" }]) }]
    );
    renderPage();
    await userEvent.click(await screen.findByRole("tab", { name: /网络通道/ }));
    await screen.findAllByRole("row");
    await act(async () => {
      await i18n.changeLanguage("en");
    });
    try {
      expect(await screen.findByText("1 data source · 1 channel")).toBeInTheDocument();
    } finally {
      await act(async () => {
        await i18n.changeLanguage("zh-CN");
      });
    }
  });
});

describe("SourcesPage · 数据源", () => {
  it("加载失败时显示原因与重试", async () => {
    respond(fail(-1, "网络错误", 500), ok({ items: [] }));
    renderPage();
    expect(await screen.findByText("无法加载数据源列表：网络错误")).toBeInTheDocument();
    respond(ok({ items: [] }));
    await userEvent.click(screen.getByRole("button", { name: "重试" }));
    expect(await screen.findByText("还没有数据源")).toBeInTheDocument();
  });

  it("空列表显示引导新建", async () => {
    respondLists([], []);
    renderPage();
    expect(await screen.findByText("还没有数据源")).toBeInTheDocument();
    expect(screen.getByText("新建一个 MySQL、PostgreSQL 或服务器文件数据源。")).toBeInTheDocument();
  });

  it("列表显示地址、服务端版本与链路、探测摘要与状态，且点击名称进入详情页", async () => {
    respondLists([dbOrders, pgAnalytics, web01, dbLegacy, pgReport], []);
    renderPage();
    const rows = await screen.findAllByRole("row");
    const header = rows[0];
    expect(within(header).getByRole("columnheader", { name: "名称" })).toBeInTheDocument();
    expect(within(header).getByRole("columnheader", { name: "地址" })).toBeInTheDocument();
    expect(within(header).getByRole("columnheader", { name: "能力探测" })).toBeInTheDocument();
    expect(within(header).getByRole("columnheader", { name: "状态" })).toBeInTheDocument();
    expect(within(header).getByRole("columnheader", { name: "操作" })).toBeInTheDocument();

    const ordersRow = rows[1];
    expect(within(ordersRow).getByText("mysql://10.0.1.11:3306")).toBeInTheDocument();
    expect(within(ordersRow).getByText("MySQL 8.0.36 · 经 office-socks → bastion-prod")).toBeInTheDocument();
    expect(within(ordersRow).getByText("7 通过 · 1 提醒")).toBeInTheDocument();
    expect(within(ordersRow).getByText("正常")).toBeInTheDocument();
    const nameLink = within(ordersRow).getByRole("link", { name: "db-01 · orders" });
    expect(nameLink).toHaveAttribute("href", "/sources/101");

    const pgRow = rows[2];
    expect(within(pgRow).getByText("PostgreSQL 16.2 · 直连")).toBeInTheDocument();
    expect(within(pgRow).getByText("6 项全部通过")).toBeInTheDocument();

    const webRow = rows[3];
    expect(within(webRow).getByText("Linux x86_64 · 经 office-socks → bastion-prod")).toBeInTheDocument();
    expect(within(webRow).getByText("3 通过 · 1 不可用")).toBeInTheDocument();

    const legacyRow = rows[4];
    expect(within(legacyRow).getByText("主机密钥已变化")).toBeInTheDocument();
    expect(within(legacyRow).getByText("无法探测")).toBeInTheDocument();
    expect(within(legacyRow).getByRole("button", { name: "重新确认 db-legacy 的主机密钥" })).toBeInTheDocument();

    const reportRow = rows[5];
    expect(within(reportRow).getByText("无法连接")).toBeInTheDocument();
    expect(within(reportRow).getByText("认证失败：password authentication failed")).toBeInTheDocument();
    expect(within(reportRow).getByText("直连")).toBeInTheDocument();
  });

  it("行操作包含测试连接、编辑，以及更多菜单中的查看详情与删除数据源", async () => {
    respondLists([dbOrders], []);
    renderPage();
    const row = (await screen.findAllByRole("row"))[1];
    expect(within(row).getByRole("button", { name: "测试连接 db-01 · orders" })).toBeInTheDocument();
    expect(within(row).getByRole("button", { name: "编辑 db-01 · orders" })).toBeInTheDocument();
    await userEvent.click(within(row).getByRole("button", { name: "db-01 · orders 的更多操作" }));
    const detailLink = await screen.findByRole("menuitem", { name: "查看详情" });
    expect(detailLink.querySelector("a")?.getAttribute("href") ?? detailLink.getAttribute("href")).toBe("/sources/101");
    expect(screen.getByRole("menuitem", { name: "删除数据源" })).toBeInTheDocument();
  });

  it("新建数据源：三种类型切换默认端口，MySQL 显示密码与 TLS，服务器文件显示认证方式", async () => {
    respondLists([], [officeSocks, bastionProd]);
    renderPage();
    const dialog = await openCreateDataSource();
    expect(within(dialog).getByLabelText("端口")).toHaveAttribute("placeholder", "3306");
    expect(within(dialog).getByLabelText("密码")).toBeInTheDocument();
    expect(within(dialog).getByText("TLS")).toBeInTheDocument();

    await userEvent.click(within(dialog).getByRole("radio", { name: "PostgreSQL" }));
    expect(within(dialog).getByLabelText("端口")).toHaveAttribute("placeholder", "5432");
    expect(within(dialog).getByLabelText(/连接数据库/)).toHaveAttribute("placeholder", "postgres");

    await userEvent.click(within(dialog).getByRole("radio", { name: "服务器文件" }));
    expect(within(dialog).getByLabelText("端口")).toHaveAttribute("placeholder", "22");
    expect(within(dialog).queryByText("TLS")).not.toBeInTheDocument();
    expect(within(dialog).getByRole("radiogroup", { name: "认证方式" })).toBeInTheDocument();
  });

  it("TLS 默认优先加密，校验模式下显示 CA 证书，mTLS 证书与私钥可上传文件", async () => {
    respondLists([], []);
    renderPage();
    const dialog = await openCreateDataSource();
    expect(within(dialog).getByRole("radio", { name: "优先加密" })).toHaveAttribute("aria-checked", "true");
    expect(within(dialog).queryByLabelText("CA 证书")).not.toBeInTheDocument();

    await userEvent.click(within(dialog).getByRole("radio", { name: "校验 CA 与主机名" }));
    expect(within(dialog).getByLabelText("CA 证书")).toBeInTheDocument();
    expect(within(dialog).getByLabelText("客户端证书")).toBeInTheDocument();
    expect(within(dialog).getByLabelText("客户端私钥")).toBeInTheDocument();
  });

  it("字段错误落在当前不显示的字段上时（非校验模式下的 CA 证书），改为整体提示", async () => {
    respondLists([], []);
    renderPage();
    const dialog = await openCreateDataSource();
    await userEvent.type(within(dialog).getByLabelText("名称"), "orders");
    await userEvent.type(within(dialog).getByLabelText("主机"), "10.0.1.11");
    await userEvent.type(within(dialog).getByLabelText("用户名"), "backup");
    await userEvent.type(within(dialog).getByLabelText("密码"), "pw");
    await userEvent.click(within(dialog).getByRole("radio", { name: "校验 CA" }));
    await userEvent.type(within(dialog).getByLabelText("CA 证书"), "junk");
    await userEvent.click(within(dialog).getByRole("radio", { name: "优先加密" }));
    respond(fail(10613, "无法解析 CA 证书"));
    await userEvent.click(within(dialog).getByRole("button", { name: "保存" }));
    expect(await within(dialog).findByText("无法解析 CA 证书")).toBeInTheDocument();
  });

  it("经由的通道已被删除时，在网络通道选择框下显示原因", async () => {
    respondLists([], [officeSocks]);
    renderPage();
    const dialog = await openCreateDataSource();
    await userEvent.type(within(dialog).getByLabelText("名称"), "orders");
    await userEvent.type(within(dialog).getByLabelText("主机"), "10.0.1.11");
    await userEvent.type(within(dialog).getByLabelText("用户名"), "backup");
    await userEvent.type(within(dialog).getByLabelText("密码"), "pw");
    await userEvent.click(within(dialog).getByRole("combobox", { name: "网络通道" }));
    await userEvent.click(await screen.findByRole("option", { name: "office-socks" }));
    respond(fail(10616, "经由的通道不存在"));
    await userEvent.click(within(dialog).getByRole("button", { name: "保存" }));
    expect(await within(dialog).findByText("经由的通道不存在")).toBeInTheDocument();
  });

  it("经由通道时实时显示链路预览", async () => {
    respondLists([], [officeSocks, bastionProd]);
    renderPage();
    const dialog = await openCreateDataSource();
    await userEvent.type(within(dialog).getByLabelText("主机"), "10.0.1.11");
    expect(within(dialog).getByText("链路：OpsNap → 10.0.1.11:3306")).toBeInTheDocument();

    await userEvent.click(within(dialog).getByRole("combobox", { name: "网络通道" }));
    await userEvent.click(await screen.findByRole("option", { name: "bastion-prod" }));
    expect(
      within(dialog).getByText("链路：OpsNap → office-socks (SOCKS5) → bastion-prod (SSH) → 10.0.1.11:3306")
    ).toBeInTheDocument();
  });

  it("首次连接目标主机密钥：确认后带指纹重试并保存（服务器文件）", async () => {
    respondLists([], []);
    renderPage();
    const dialog = await openCreateDataSource();
    await userEvent.click(within(dialog).getByRole("radio", { name: "服务器文件" }));
    await userEvent.type(within(dialog).getByLabelText("名称"), "web-02");
    await userEvent.type(within(dialog).getByLabelText("主机"), "web-02.corp");
    await userEvent.type(within(dialog).getByLabelText("用户名"), "deploy");

    respond(
      ok({
        host_key: {
          hop: 1,
          name: "web-02",
          address: "web-02.corp:22",
          key_type: "ED25519",
          fingerprint: "SHA256:webNewFingerprint",
          changed: false,
          saved: "",
        },
        chain: [],
        server: null,
      })
    );
    await userEvent.click(within(dialog).getByRole("button", { name: "测试连接" }));
    const confirm = await screen.findByRole("dialog", { name: "确认主机密钥" });

    respond(ok({ host_key: null, chain: [], server: { version: "", system: "Linux x86_64", tls: null } }));
    await userEvent.click(within(confirm).getByRole("button", { name: "信任并继续" }));
    await screen.findByText("连接成功：Linux x86_64。保存后将自动探测。");
    // call(0) 数据源列表、call(1) 通道列表、call(2) 首次探测、call(3) 带指纹重试
    expect(call(2).body.data_source.host_key).toBe("");
    expect(call(3).body.data_source.host_key).toBe("SHA256:webNewFingerprint");
  });

  it("测试连接成功时报告服务端版本、TLS 版本与是否校验了证书", async () => {
    respondLists([], []);
    renderPage();
    const dialog = await openCreateDataSource();
    await userEvent.type(within(dialog).getByLabelText("名称"), "orders");
    await userEvent.type(within(dialog).getByLabelText("主机"), "10.0.1.11");
    await userEvent.type(within(dialog).getByLabelText("用户名"), "backup");

    respond(
      ok({
        host_key: null,
        chain: [],
        server: { version: "8.0.36", system: "", tls: { version: "TLSv1.3", verified: true } },
      })
    );
    await userEvent.click(within(dialog).getByRole("button", { name: "测试连接" }));
    expect(
      await within(dialog).findByText("连接成功：MySQL 8.0.36 · TLSv1.3，已校验证书。保存后将自动探测。")
    ).toBeInTheDocument();

    respond(ok({ host_key: null, chain: [], server: { version: "16.2", system: "", tls: null } }));
    await userEvent.click(within(dialog).getByRole("radio", { name: "PostgreSQL" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "测试连接" }));
    expect(
      await within(dialog).findByText("连接成功：PostgreSQL 16.2 · 未加密。保存后将自动探测。")
    ).toBeInTheDocument();
  });

  it("有数据源正在探测时定时刷新列表，探测结束后显示探测摘要", async () => {
    const probing: DataSourceItem = { ...pgAnalytics, probe: { state: "probing", ok: 0, warn: 0, fail: 0, time: 0 } };
    // 页面加载后才开始轮询：假定时器在渲染前启用，shouldAdvanceTime 让加载阶段的等待照常推进
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      respondLists([probing], []);
      renderPage();
      const rows = await screen.findAllByRole("row");
      expect(within(rows[1]).getByText("探测中")).toBeInTheDocument();

      respond(ok({ items: [pgAnalytics] }));
      await act(async () => {
        await vi.advanceTimersByTimeAsync(6000);
      });
      expect(call(2)).toMatchObject({ url: "/api/v1/datasources", method: "GET" });
      expect(within(screen.getAllByRole("row")[1]).getByText("6 项全部通过")).toBeInTheDocument();

      // 没有正在探测的数据源后不再轮询
      await act(async () => {
        await vi.advanceTimersByTimeAsync(10000);
      });
      expect(fetchMock.mock.calls.length).toBe(3);
    } finally {
      vi.useRealTimers();
    }
  });

  it("编辑时密码显示已保存提示，留空表示不修改", async () => {
    respondLists([dbOrders], []);
    renderPage();
    const row = (await screen.findAllByRole("row"))[1];
    await userEvent.click(within(row).getByRole("button", { name: "编辑 db-01 · orders" }));
    const dialog = await screen.findByRole("dialog", { name: "编辑数据源" });
    expect(within(dialog).getByLabelText("密码")).toHaveAttribute("placeholder", "已保存（加密存储），留空表示不修改");
    expect(within(dialog).getByLabelText("密码")).toHaveValue("");

    respond(ok({ item: { ...dbOrders, name: "db-01 · orders-2" }, host_key: null }));
    await userEvent.clear(within(dialog).getByLabelText("名称"));
    await userEvent.type(within(dialog).getByLabelText("名称"), "db-01 · orders-2");
    await userEvent.click(within(dialog).getByRole("button", { name: "保存" }));
    await screen.findByText("db-01 · orders-2");
    expect(call(2).body.data_source.password).toBe("");
    expect(call(2).url).toBe("/api/v1/datasources/101");
    expect(call(2).method).toBe("PUT");
  });

  it("主机密钥已变化（数据源自身目标主机）：重新确认并列显示指纹，信任后重新测试", async () => {
    const selfChanged: DataSourceItem = {
      ...web01,
      status: "host_key_changed",
      status_message: "主机密钥已变化",
      failed_hop: { hop: 1, channel_id: 0, name: "web-01", kind: "server_file" },
      presented_host_key: "SHA256:webNewFingerprint",
      channel_id: 0,
      chain: [{ id: 0, name: "web-01", kind: "server_file", address: "web-01:22" }],
    };
    respondLists([selfChanged], []);
    renderPage();
    await userEvent.click(await screen.findByRole("button", { name: "重新确认 web-01 的主机密钥" }));
    const dialog = await screen.findByRole("dialog", { name: "主机密钥已变化" });
    expect(within(dialog).getByText("SHA256:webSavedFingerprint")).toBeInTheDocument();
    expect(within(dialog).getByText("SHA256:webNewFingerprint")).toBeInTheDocument();

    respond(ok({ item: { ...selfChanged, status: "ok", host_key: selfChanged.presented_host_key }, host_key: null }));
    await userEvent.click(within(dialog).getByRole("button", { name: "信任新密钥" }));
    expect(call(2).url).toBe("/api/v1/datasources/103/host-key");
    expect(call(2).body.fingerprint).toBe("SHA256:webNewFingerprint");
    await screen.findByText("正常");
  });

  it("编辑表单也能打开「主机密钥已变化」弹窗", async () => {
    const selfChanged: DataSourceItem = {
      ...web01,
      status: "host_key_changed",
      status_message: "主机密钥已变化",
      failed_hop: { hop: 1, channel_id: 0, name: "web-01", kind: "server_file" },
      presented_host_key: "SHA256:webNewFingerprint",
      channel_id: 0,
      chain: [{ id: 0, name: "web-01", kind: "server_file", address: "web-01:22" }],
    };
    respondLists([selfChanged], []);
    renderPage();
    await userEvent.click(await screen.findByRole("button", { name: "编辑 web-01" }));
    const form = await screen.findByRole("dialog", { name: "编辑数据源" });
    await userEvent.click(within(form).getByRole("button", { name: "重新确认" }));
    const changed = await screen.findByRole("dialog", { name: "主机密钥已变化" });
    expect(within(changed).getByText("SHA256:webSavedFingerprint")).toBeInTheDocument();
    expect(within(changed).getByText("SHA256:webNewFingerprint")).toBeInTheDocument();
  });

  it("主机密钥已变化（链路中的通道）：本地通道列表指纹过期时，先刷新通道列表再显示当前指纹", async () => {
    // 数据源的测试在通道被加载之后才发现密钥变化：页面上的通道条目还没有出示的指纹
    const staleBastionOld: ChannelItem = { ...bastionOld, presented_host_key: "" };
    respondLists([dbLegacy], [staleBastionOld]);
    renderPage();
    const reconfirmBtn = await screen.findByRole("button", { name: "重新确认 db-legacy 的主机密钥" });
    // 重新确认时先重新拉取通道列表，拿到后台已经记录的最新指纹
    respond(ok({ items: [bastionOld] }));
    await userEvent.click(reconfirmBtn);
    expect(call(2).url).toBe("/api/v1/channels");
    const dialog = await screen.findByRole("dialog", { name: "主机密钥已变化" });
    // 并列显示的是 bastion-old 这个通道保存与出示的指纹，而不是数据源自身的，且不是空指纹
    expect(within(dialog).getByText("SHA256:oldSavedFingerprint")).toBeInTheDocument();
    expect(within(dialog).getByText("SHA256:Zk8pQe41VbN7mWc0rHs2LtYg5xJ3uKdA9iFoPlT0bw")).toBeInTheDocument();
  });

  it("主机密钥已变化（链路中的通道）：刷新后仍拿不到指纹时显示错误，不打开空白弹窗", async () => {
    const staleBastionOld: ChannelItem = { ...bastionOld, presented_host_key: "" };
    respondLists([dbLegacy], [staleBastionOld]);
    renderPage();
    const reconfirmBtn = await screen.findByRole("button", { name: "重新确认 db-legacy 的主机密钥" });
    // 刷新后通道仍未出示新指纹（例如还没有人访问过这台通道）
    respond(ok({ items: [staleBastionOld] }));
    await userEvent.click(reconfirmBtn);
    await screen.findByText("暂时无法获取通道 bastion-old 现在出示的密钥，请稍后重试。");
    expect(screen.queryByRole("dialog", { name: "主机密钥已变化" })).not.toBeInTheDocument();
  });

  it("主机密钥已变化（链路中的通道）：在该通道上重新确认，成功后刷新数据源列表", async () => {
    respondLists([dbLegacy], [bastionOld]);
    renderPage();
    const reconfirmBtn = await screen.findByRole("button", { name: "重新确认 db-legacy 的主机密钥" });
    // 重新确认前先刷新通道列表（此处通道条目已经是最新的）
    respond(ok({ items: [bastionOld] }));
    await userEvent.click(reconfirmBtn);
    const dialog = await screen.findByRole("dialog", { name: "主机密钥已变化" });
    // 并列显示的是 bastion-old 这个通道保存与出示的指纹，而不是数据源自身的
    expect(within(dialog).getByText("SHA256:oldSavedFingerprint")).toBeInTheDocument();
    expect(within(dialog).getByText("SHA256:Zk8pQe41VbN7mWc0rHs2LtYg5xJ3uKdA9iFoPlT0bw")).toBeInTheDocument();

    // 通道确认成功且没有新的 host_key 待确认时，会在同一次交互中接着重新拉取数据源列表；
    // 两个响应先一起入队，避免第二次请求先于测试排队而拿不到响应
    respond(
      ok({ item: { ...bastionOld, status: "ok", host_key: bastionOld.presented_host_key }, host_key: null }),
      ok({ items: [{ ...dbLegacy, status: "ok", failed_hop: null }] })
    );
    await userEvent.click(within(dialog).getByRole("button", { name: "信任新密钥" }));
    expect(call(3).url).toBe("/api/v1/channels/3/host-key");
    expect(call(3).body.fingerprint).toBe(bastionOld.presented_host_key);
    await screen.findByText("正常");
    expect(call(4).url).toBe("/api/v1/datasources");
  });

  it("删除数据源：二次确认后从列表移除", async () => {
    respondLists([dbOrders], []);
    renderPage();
    const row = (await screen.findAllByRole("row"))[1];
    await userEvent.click(within(row).getByRole("button", { name: "db-01 · orders 的更多操作" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "删除数据源" }));
    const confirmDialog = await screen.findByRole("dialog", { name: "删除数据源「db-01 · orders」？" });
    expect(within(confirmDialog).getByText("只删除 OpsNap 中的记录与凭据，不影响数据库或服务器。")).toBeInTheDocument();
    // call(2) 删除请求，call(3) 删除后刷新通道列表（数据源也计入通道使用数）
    respond(ok({}), ok({ items: [] }));
    await userEvent.click(within(confirmDialog).getByRole("button", { name: "删除数据源" }));
    await vi.waitFor(() => expect(screen.queryByText("db-01 · orders")).not.toBeInTheDocument());
    expect(call(2)).toMatchObject({ url: "/api/v1/datasources/101", method: "DELETE" });
  });

  it("删除数据源后，被它使用的通道的使用数刷新", async () => {
    respondLists([ds1], [c1]);
    renderPage();
    const row = (await screen.findAllByRole("row"))[1];
    await userEvent.click(within(row).getByRole("button", { name: "ds1 的更多操作" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "删除数据源" }));
    const confirmDialog = await screen.findByRole("dialog", { name: "删除数据源「ds1」？" });
    // call(2) 删除请求，call(3) 删除后刷新通道列表：接口已返回 c1 的 used_by 为空
    respond(ok({}), ok({ items: [{ ...c1, used_by: usedBy() }] }));
    await userEvent.click(within(confirmDialog).getByRole("button", { name: "删除数据源" }));
    await vi.waitFor(() => expect(screen.queryByText("ds1")).not.toBeInTheDocument());

    await userEvent.click(screen.getByRole("tab", { name: /网络通道/ }));
    const chRow = (await screen.findAllByRole("row"))[1];
    expect(within(chRow).getByText("未被使用")).toBeInTheDocument();
  });

  it("保存经由通道的数据源后，该通道的使用数刷新", async () => {
    respondLists([], [{ ...c1, used_by: usedBy() }]);
    renderPage();
    const dialog = await openCreateDataSource();
    await userEvent.type(within(dialog).getByLabelText("名称"), "ds1");
    await userEvent.type(within(dialog).getByLabelText("主机"), "10.0.0.41");
    await userEvent.type(within(dialog).getByLabelText("用户名"), "backup");
    await userEvent.type(within(dialog).getByLabelText("密码"), "pw");
    await userEvent.click(within(dialog).getByRole("combobox", { name: "网络通道" }));
    await userEvent.click(await screen.findByRole("option", { name: "c1" }));
    // call(2) 保存请求，call(3) 保存后刷新通道列表：接口已返回 c1 被 ds1 使用
    respond(ok({ item: ds1, host_key: null }), ok({ items: [c1] }));
    await userEvent.click(within(dialog).getByRole("button", { name: "保存" }));
    await vi.waitFor(() => expect(screen.queryByRole("dialog", { name: "新建数据源" })).not.toBeInTheDocument());

    await userEvent.click(screen.getByRole("tab", { name: /网络通道/ }));
    const chRow = (await screen.findAllByRole("row"))[1];
    expect(await within(chRow).findByText("1 个数据源")).toBeInTheDocument();
  });
});
