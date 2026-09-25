import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router";
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

import i18n from "@/i18n";
import type { ChannelItem, DataSourceItem, ProbeItem } from "@/lib/sources";
import { SourceDetailPage } from "@/pages/SourceDetailPage";

const ok = (data: unknown) => new Response(JSON.stringify({ code: 0, msg: "success", data }), { status: 200 });
const fail = (code: number, msg: string, status = 400) => new Response(JSON.stringify({ code, msg }), { status });
const now = () => Math.floor(Date.now() / 1000);

const usedBy = () => ({ data_sources: [], channels: [] });

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
  used_by: usedBy(),
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
  used_by: usedBy(),
  status: "ok",
  status_message: "",
  checked_at: now() - 300,
  created_at: now() - 9000,
};

const mysqlProbeItems: ProbeItem[] = [
  {
    key: "mysql.version",
    title: { zh_cn: "版本", en: "Version" },
    tier: "ok",
    detail: { zh_cn: "MySQL 8.0.36 (Community Server)", en: "MySQL 8.0.36 (Community Server)" },
    fix: { zh_cn: "", en: "" },
  },
  {
    key: "mysql.binlog",
    title: { zh_cn: "binlog 已开启", en: "Binlog enabled" },
    tier: "ok",
    detail: { zh_cn: "log_bin = ON", en: "log_bin = ON" },
    fix: { zh_cn: "", en: "" },
  },
  {
    key: "mysql.binlog_format",
    title: { zh_cn: "binlog 格式为 ROW", en: "Binlog format is ROW" },
    tier: "ok",
    detail: { zh_cn: "binlog_format = ROW", en: "binlog_format = ROW" },
    fix: { zh_cn: "", en: "" },
  },
  {
    key: "mysql.gtid",
    title: { zh_cn: "已开启 GTID", en: "GTID enabled" },
    tier: "ok",
    detail: { zh_cn: "gtid_mode = ON", en: "gtid_mode = ON" },
    fix: { zh_cn: "", en: "" },
  },
  {
    key: "mysql.binlog_expire",
    title: { zh_cn: "binlog 只保留 1 天", en: "Binlog retained for 1 day" },
    tier: "warn",
    detail: {
      zh_cn: "binlog_expire_logs_seconds = 86400。增量中断超过 1 天就无法续传。",
      en: "binlog_expire_logs_seconds = 86400. Incremental resumes fail after 1 day.",
    },
    fix: {
      zh_cn: "在 MySQL 上执行：SET PERSIST binlog_expire_logs_seconds = 604800;",
      en: "On MySQL, run: SET PERSIST binlog_expire_logs_seconds = 604800;",
    },
  },
  {
    key: "mysql.repl_priv",
    title: { zh_cn: "账号具备复制权限", en: "Account has replication privileges" },
    tier: "ok",
    detail: { zh_cn: "REPLICATION SLAVE, REPLICATION CLIENT", en: "REPLICATION SLAVE, REPLICATION CLIENT" },
    fix: { zh_cn: "", en: "" },
  },
  {
    key: "mysql.non_innodb",
    title: { zh_cn: "存在非 InnoDB 表", en: "Non-InnoDB tables found" },
    tier: "warn",
    detail: { zh_cn: "全量时需要锁表", en: "Locks tables during a full backup" },
    fix: { zh_cn: "", en: "" },
    tables: ["app.t1", "app.t2", "app.t3", "app.t4", "app.t5"],
    table_count: 7,
  },
  {
    key: "mysql.mysqldump",
    title: { zh_cn: "主控端 mysqldump 与服务端匹配", en: "Controller mysqldump matches the server" },
    tier: "ok",
    detail: { zh_cn: "/usr/bin/mysqldump 8.0.36", en: "/usr/bin/mysqldump 8.0.36" },
    fix: { zh_cn: "", en: "" },
  },
];

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
  tls_client_cert: "-----BEGIN CERTIFICATE-----\nMII...\n-----END CERTIFICATE-----",
  has_tls_client_key: true,
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
  probe: { state: "done", ok: 6, warn: 2, fail: 0, items: mysqlProbeItems, time: now() - 120 },
};

const web01Probing: DataSourceItem = {
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
  channel_id: 0,
  address: "ssh://deploy@web-01:22",
  chain: [{ id: 0, name: "web-01", kind: "server_file", address: "web-01:22" }],
  server: { version: "", system: "Linux x86_64", tls: null },
  host_key: "SHA256:webSavedFingerprint",
  presented_host_key: "",
  status: "ok",
  status_message: "",
  failed_hop: null,
  checked_at: now() - 30,
  created_at: now() - 30000,
  probe: { state: "probing", ok: 0, warn: 0, fail: 0, time: now() - 9000 },
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
  probe: { state: "unprobeable", ok: 0, warn: 0, fail: 0, error: "认证失败：password authentication failed", time: 0 },
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

function renderDetail(id: number | string) {
  return render(
    <MemoryRouter initialEntries={[`/sources/${id}`]}>
      <Routes>
        <Route path="/sources/:id" element={<SourceDetailPage />} />
        <Route path="/sources" element={<div>数据源列表页</div>} />
      </Routes>
    </MemoryRouter>
  );
}

/** 详情页加载时先请求数据源详情，再请求通道列表（供链路指纹与经由通道重新确认使用） */
function respondDetail(item: DataSourceItem, channels: ChannelItem[]) {
  respond(ok({ item }), ok({ items: channels }));
}

describe("数据源详情页", () => {
  it("加载中显示提示，随后显示头部名称、状态、版本地址与最近测试时间", async () => {
    respondDetail(dbOrders, [officeSocks, bastionProd]);
    renderDetail(101);
    expect(screen.getByText("加载中…")).toBeInTheDocument();
    expect(await screen.findByRole("heading", { name: "db-01 · orders" })).toBeInTheDocument();
    const header = screen.getByRole("banner");
    expect(within(header).getByText("正常")).toBeInTheDocument();
    expect(within(header).getByText(/MySQL 8\.0\.36/)).toBeInTheDocument();
    expect(within(header).getByText(/mysql:\/\/10\.0\.1\.11:3306/)).toBeInTheDocument();
    expect(within(header).getByText(/2 分钟前测试/)).toBeInTheDocument();
    expect(call(0)).toMatchObject({ url: "/api/v1/datasources/101", method: "GET" });
    expect(call(1)).toMatchObject({ url: "/api/v1/channels", method: "GET" });
  });

  it("连接信息与逐跳链路：从 OpsNap 开始，SSH 跳显示主机密钥指纹", async () => {
    respondDetail(dbOrders, [officeSocks, bastionProd]);
    renderDetail(101);
    await screen.findByRole("heading", { name: "db-01 · orders" });

    const connection = screen.getByRole("region", { name: "连接" });
    expect(within(connection).getByText("MySQL")).toBeInTheDocument();
    expect(within(connection).getByText("backup")).toBeInTheDocument();
    expect(within(connection).getByText(/校验 CA 与主机名/)).toBeInTheDocument();
    expect(within(connection).getByText(/TLS 1\.3/)).toBeInTheDocument();
    expect(within(connection).getByText(/客户端证书/)).toBeInTheDocument();

    const chain = screen.getByRole("list", { name: "网络链路" });
    const hops = within(chain).getAllByRole("listitem");
    expect(hops).toHaveLength(4);
    expect(within(hops[0]).getByText("OpsNap")).toBeInTheDocument();
    expect(within(hops[1]).getByText("office-socks")).toBeInTheDocument();
    expect(within(hops[2]).getByText("bastion-prod")).toBeInTheDocument();
    expect(within(hops[2]).getByText(/SHA256:3fQ1bWmX0pZrK2sQn8vYt4LhE6cJ9dUa7gRiTfNoa9Kc/)).toBeInTheDocument();
    expect(within(hops[3]).getByText("db-01 · orders")).toBeInTheDocument();
  });

  it("能力探测：摘要、探测时间，逐项按顺序显示三档与说明，表名最多 5 个并显示总数", async () => {
    respondDetail(dbOrders, [officeSocks, bastionProd]);
    renderDetail(101);
    await screen.findByRole("heading", { name: "db-01 · orders" });

    const probe = screen.getByRole("region", { name: "能力探测" });
    expect(within(probe).getByText(/6 通过/)).toBeInTheDocument();
    expect(within(probe).getByText(/2 提醒/)).toBeInTheDocument();
    expect(within(probe).getByText(/探测于 2 分钟前/)).toBeInTheDocument();

    const items = within(probe).getAllByRole("listitem");
    expect(items).toHaveLength(8);
    expect(within(items[0]).getByText("版本")).toBeInTheDocument();
    expect(within(items[4]).getByText("binlog 只保留 1 天")).toBeInTheDocument();
    expect(within(items[6]).getByText("存在非 InnoDB 表")).toBeInTheDocument();
    expect(within(items[6]).getByText(/app\.t1.*app\.t5/)).toBeInTheDocument();
    expect(within(items[6]).getByText(/共 7 张/)).toBeInTheDocument();
    expect(within(items[7]).getByText("主控端 mysqldump 与服务端匹配")).toBeInTheDocument();
  });

  it("有风险项的修复方法可复制，复制后显示反馈", async () => {
    respondDetail(dbOrders, [officeSocks, bastionProd]);
    renderDetail(101);
    await screen.findByRole("heading", { name: "db-01 · orders" });

    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });

    const probe = screen.getByRole("region", { name: "能力探测" });
    const item = within(probe).getAllByRole("listitem")[4];
    await userEvent.click(within(item).getByRole("button", { name: "复制" }));
    expect(writeText).toHaveBeenCalledWith("在 MySQL 上执行：SET PERSIST binlog_expire_logs_seconds = 604800;");
    expect(await within(item).findByText("已复制")).toBeInTheDocument();
  });

  it("探测中时禁用重新探测，且不显示旧的探测结果", async () => {
    respondDetail(web01Probing, []);
    renderDetail(103);
    await screen.findByRole("heading", { name: "web-01" });

    expect(screen.getByRole("button", { name: "重新探测" })).toBeDisabled();
    const probe = screen.getByRole("region", { name: "能力探测" });
    expect(within(probe).getByText("探测中")).toBeInTheDocument();
    expect(within(probe).queryAllByRole("listitem")).toHaveLength(0);
  });

  it("组件卸载后停止轮询，不再请求详情", async () => {
    respondDetail(web01Probing, []);
    const view = renderDetail(103);
    await screen.findByRole("heading", { name: "web-01" });
    const callsBeforeUnmount = fetchMock.mock.calls.length;
    view.unmount();

    vi.useFakeTimers();
    try {
      await vi.advanceTimersByTimeAsync(10000);
    } finally {
      vi.useRealTimers();
    }
    expect(fetchMock.mock.calls.length).toBe(callsBeforeUnmount);
  });

  it("无法探测时显示原因，不显示旧结果", async () => {
    respondDetail(pgReport, []);
    renderDetail(105);
    await screen.findByRole("heading", { name: "pg-report" });

    const probe = screen.getByRole("region", { name: "能力探测" });
    expect(within(probe).getByText("无法探测")).toBeInTheDocument();
    expect(within(probe).getByText(/认证失败/)).toBeInTheDocument();
    expect(within(probe).queryAllByRole("listitem")).toHaveLength(0);
  });

  it("点击重新探测后进入探测中，轮询直到结果落定", async () => {
    respondDetail(dbOrders, [officeSocks, bastionProd]);
    renderDetail(101);
    await screen.findByRole("heading", { name: "db-01 · orders" });

    // 只在触发轮询之后切到假定时器：既能让页面正常加载完成，又能推进探测中的轮询间隔；
    // 用 fireEvent 而非 userEvent 点击，避免 userEvent 内部延迟依赖真实定时器导致假定时器下卡死
    vi.useFakeTimers();
    try {
      respond(
        ok({ item: { ...dbOrders, probe: { state: "probing", ok: 0, warn: 0, fail: 0, time: dbOrders.probe!.time } } })
      );
      await act(async () => {
        fireEvent.click(screen.getByRole("button", { name: "重新探测" }));
        await vi.advanceTimersByTimeAsync(0);
      });
      expect(screen.getByRole("button", { name: "重新探测" })).toBeDisabled();
      expect(call(2)).toMatchObject({ url: "/api/v1/datasources/101/reprobe", method: "POST" });

      respond(ok({ item: dbOrders }));
      await act(async () => {
        await vi.advanceTimersByTimeAsync(6000);
      });
      expect(screen.getByRole("button", { name: "重新探测" })).not.toBeDisabled();
      expect(call(3)).toMatchObject({ url: "/api/v1/datasources/101", method: "GET" });
    } finally {
      vi.useRealTimers();
    }
  });

  it("数据源不存在时显示提示，并提供返回列表入口", async () => {
    respond(fail(10600, "数据源不存在", 404), ok({ items: [] }));
    renderDetail(999);
    expect(await screen.findByText("数据源不存在")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("link", { name: "返回列表" }));
    expect(await screen.findByText("数据源列表页")).toBeInTheDocument();
  });

  it("测试连接：链路中的通道密钥变化时弹出确认，信任新密钥后重新测试并刷新详情", async () => {
    respondDetail(dbOrders, [officeSocks, bastionProd]);
    renderDetail(101);
    await screen.findByRole("heading", { name: "db-01 · orders" });

    const hostKeyPrompt = {
      hop: 2,
      name: "bastion-prod",
      address: "ssh://jump@bastion.corp:22",
      key_type: "ED25519",
      fingerprint: "SHA256:newPresentedFingerprint",
      changed: true,
      saved: "SHA256:3fQ1bWmX0pZrK2sQn8vYt4LhE6cJ9dUa7gRiTfNoa9Kc",
    };
    respond(
      ok({
        item: {
          ...dbOrders,
          status: "host_key_changed",
          status_message: "跳板 bastion-prod 的密钥与保存的不一致",
          failed_hop: { hop: 2, channel_id: 2, name: "bastion-prod", kind: "ssh" },
        },
        host_key: hostKeyPrompt,
      })
    );
    await userEvent.click(screen.getByRole("button", { name: "测试连接" }));

    const dialog = await screen.findByRole("dialog", { name: "主机密钥已变化" });
    expect(within(dialog).getByText(hostKeyPrompt.saved)).toBeInTheDocument();
    expect(within(dialog).getByText(hostKeyPrompt.fingerprint)).toBeInTheDocument();

    respond(ok({ item: { ...bastionProd, host_key: hostKeyPrompt.fingerprint }, host_key: null }));
    respond(ok({ item: dbOrders }));
    await userEvent.click(within(dialog).getByRole("button", { name: "信任新密钥" }));

    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(call(2)).toMatchObject({ url: "/api/v1/datasources/101/test", method: "POST" });
    expect(call(3)).toMatchObject({
      url: "/api/v1/channels/2/host-key",
      method: "POST",
      body: { fingerprint: hostKeyPrompt.fingerprint },
    });
    expect(call(4)).toMatchObject({ url: "/api/v1/datasources/101", method: "GET" });
    expect(await screen.findByText("正常")).toBeInTheDocument();
  });

  it("编辑：打开已保存数据源的表单弹窗", async () => {
    respondDetail(dbOrders, [officeSocks, bastionProd]);
    renderDetail(101);
    await screen.findByRole("heading", { name: "db-01 · orders" });

    await userEvent.click(screen.getByRole("button", { name: "编辑" }));
    const dialog = await screen.findByRole("dialog", { name: "编辑数据源" });
    expect(within(dialog).getByDisplayValue("db-01 · orders")).toBeInTheDocument();
    expect(within(dialog).getByDisplayValue("10.0.1.11")).toBeInTheDocument();
  });

  it("界面文案支持中英文：切换语言后探测详情显示英文", async () => {
    respondDetail(dbOrders, [officeSocks, bastionProd]);
    renderDetail(101);
    await screen.findByRole("heading", { name: "db-01 · orders" });

    await act(async () => {
      await i18n.changeLanguage("en");
    });
    expect(await screen.findByText("Binlog retained for 1 day")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Reprobe" })).toBeInTheDocument();
  });
});

afterAll(() => i18n.changeLanguage("zh-CN"));
