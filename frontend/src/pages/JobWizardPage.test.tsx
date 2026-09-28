import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

import i18n from "@/i18n";
import type { JobItem } from "@/lib/jobs";
import type { DataSourceItem, ProbeItem } from "@/lib/sources";
import type { Storage } from "@/lib/storage";
import { JobWizardPage } from "@/pages/JobWizardPage";

const ok = (data: unknown) => new Response(JSON.stringify({ code: 0, msg: "success", data }), { status: 200 });
const fail = (code: number, msg: string, status = 400) => new Response(JSON.stringify({ code, msg }), { status });
const now = () => Math.floor(Date.now() / 1000);

const text = (zh: string, en = zh) => ({ zh_cn: zh, en });
const probeItem = (key: string, tier: ProbeItem["tier"], title: string, detail: string, fix = ""): ProbeItem => ({
  key,
  title: text(title),
  tier,
  detail: text(detail),
  fix: text(fix),
});

const baseSource = {
  port: 0,
  username: "backup",
  auth_method: "password",
  has_password: true,
  has_private_key: false,
  has_passphrase: false,
  database: "",
  tls_mode: "prefer" as const,
  tls_ca: "",
  tls_client_cert: "",
  has_tls_client_key: false,
  channel_id: 0,
  host_key: "",
  presented_host_key: "",
  failed_hop: null,
  checked_at: now() - 120,
  created_at: now() - 10000,
};

const dbOrders: DataSourceItem = {
  ...baseSource,
  id: 1,
  name: "db-01 · orders",
  kind: "mysql",
  host: "10.0.1.11",
  address: "mysql://10.0.1.11:3306",
  chain: [{ id: 1, name: "db-01 · orders", kind: "mysql", address: "10.0.1.11:3306" }],
  server: { version: "8.0.36", system: "", tls: null },
  status: "ok",
  status_message: "",
  probe: {
    state: "done",
    ok: 1,
    warn: 1,
    fail: 0,
    time: now() - 120,
    items: [
      probeItem("mysql.gtid", "warn", "GTID 未开启", "增量需要 GTID", "SET GLOBAL gtid_mode = ON"),
      probeItem("mysql.mysqldump", "ok", "主控端 mysqldump", "已找到 mysqldump（8.0.36），版本不低于服务端"),
    ],
  },
};

const pgAnalytics: DataSourceItem = {
  ...baseSource,
  id: 2,
  name: "pg-analytics-02",
  kind: "postgres",
  host: "10.0.2.7",
  address: "postgres://10.0.2.7:5432",
  chain: [{ id: 2, name: "pg-analytics-02", kind: "postgres", address: "10.0.2.7:5432" }],
  server: { version: "16.2", system: "", tls: null },
  status: "ok",
  status_message: "",
  probe: {
    state: "done",
    ok: 1,
    warn: 0,
    fail: 0,
    time: now() - 3600,
    items: [probeItem("postgres.pg_dump", "ok", "主控端 pg_dump", "已找到 pg_dump（16.4），大版本不低于服务端")],
  },
};

const pgNoDump: DataSourceItem = {
  ...pgAnalytics,
  probe: {
    state: "done",
    ok: 0,
    warn: 0,
    fail: 1,
    time: now() - 60,
    items: [
      probeItem(
        "postgres.pg_dump",
        "fail",
        "主控端 pg_dump",
        "本机 pg_dump（14.1）大版本低于服务端（16.2）",
        "安装 PostgreSQL 16 客户端"
      ),
    ],
  },
};

const dbBroken: DataSourceItem = {
  ...baseSource,
  id: 3,
  name: "db-02",
  kind: "mysql",
  host: "10.0.1.12",
  address: "mysql://10.0.1.12:3306",
  chain: [{ id: 3, name: "db-02", kind: "mysql", address: "10.0.1.12:3306" }],
  server: null,
  status: "host_key_changed",
  status_message: "跳板 bastion 的主机密钥已变化",
  probe: null,
};

const webFiles: DataSourceItem = {
  ...baseSource,
  id: 4,
  name: "web-01 文件",
  kind: "server_file",
  host: "web-01",
  address: "ssh://root@web-01:22",
  chain: [{ id: 4, name: "web-01 文件", kind: "server_file", address: "web-01:22" }],
  server: { version: "", system: "Linux x86_64", tls: null },
  status: "ok",
  status_message: "",
  probe: null,
};

const baseStorage = {
  endpoint: "",
  region: "",
  bucket: "",
  prefix: "",
  access_key: "",
  has_secret_key: false,
  use_tls: true,
  skip_verify: false,
  encryption: "AES256-GCM-HMAC-SHA256",
  checked_at: now() - 60,
  created_at: now() - 10000,
};

const localStore: Storage = {
  ...baseStorage,
  id: 7,
  name: "backup-local",
  kind: "local",
  path: "/data/opsnap",
  location: "/data/opsnap",
  fingerprint: "SHA256:k3yF1ngerpr1nt",
  status: "ok",
  status_message: "",
};

const minio: Storage = {
  ...baseStorage,
  id: 8,
  name: "minio-dr",
  kind: "s3",
  path: "",
  bucket: "opsnap-dr",
  location: "s3://opsnap-dr/",
  fingerprint: "SHA256:other",
  status: "unreachable",
  status_message: "连接 10.8.0.5:9000 超时",
};

const existingJob: JobItem = {
  id: 9,
  name: "orders-nightly",
  type: "backup",
  datasource_id: 1,
  datasource_name: "db-01 · orders",
  datasource_kind: "mysql",
  storage_id: 7,
  storage_name: "backup-local",
  prefix: "mysql/db-01-orders",
  location: "backup-local:/mysql/db-01-orders",
  scope: "databases",
  databases: ["orders", "payments"],
  method: "full",
  options: { routines: true, triggers: false, events: true, users: true, globals: false },
  exclude_tables: ["orders.audit_log"],
  compression: "gzip",
  schedule: { kind: "daily", minute: 0, hour: 2, weekdays: [], cron: "", timezone: "Asia/Shanghai" },
  retention: { days: 7, weeks: 4, months: 6 },
  failure: { retries: 2, retry_interval: 10, timeout: 120 },
  enabled: true,
  next_run_at: now() + 3600,
  created_at: now() - 86400,
  updated_at: now() - 86400,
};

const databases = [
  { name: "orders", size: 12 * 1024 ** 3 },
  { name: "payments", size: 300 * 1024 ** 2 },
  { name: "users", size: 2048 },
];

const NEXT_RUNS = ["2026-09-29T02:00:00+08:00", "2026-09-30T02:00:00+08:00", "2026-10-01T02:00:00+08:00"];

type Handler = (init: RequestInit | undefined) => Response | Promise<Response>;
let routes: Record<string, Handler>;
let fetchMock: ReturnType<typeof vi.fn>;

/** 按“方法 路径”返回响应；未登记的请求返回 404，便于发现多余或错误的请求 */
function route(key: string, handler: Handler | Response) {
  routes[key] = typeof handler === "function" ? handler : () => handler.clone();
}
function calls(key: string) {
  return fetchMock.mock.calls.filter(([url, init]) => `${(init as RequestInit)?.method ?? "GET"} ${url}` === key);
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
  routes = {};
  fetchMock = vi.fn((url: string, init?: RequestInit) => {
    const handler = routes[`${init?.method ?? "GET"} ${url}`];
    return Promise.resolve(handler ? handler(init) : fail(404, `unexpected ${url}`, 404));
  });
  vi.stubGlobal("fetch", fetchMock);
  route("GET /api/v1/datasources", ok({ items: [dbOrders, pgAnalytics, dbBroken, webFiles] }));
  route("GET /api/v1/datasources/1", ok({ item: dbOrders }));
  route("GET /api/v1/datasources/1/databases", ok({ databases }));
  route("GET /api/v1/datasources/2", ok({ item: pgAnalytics }));
  route("GET /api/v1/datasources/2/databases", ok({ databases: [{ name: "analytics", size: 1024 }] }));
  route("GET /api/v1/storages", ok({ items: [localStore, minio] }));
  route("GET /api/v1/jobs", ok({ items: [existingJob] }));
  route("POST /api/v1/jobs/schedule-preview", ok({ next_runs: NEXT_RUNS, max_snapshots: 42 }));
});
afterEach(() => vi.unstubAllGlobals());

function renderWizard(path = "/jobs/new") {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/jobs/new" element={<JobWizardPage />} />
        <Route path="/jobs/:id/edit" element={<JobWizardPage />} />
        <Route path="/jobs" element={<div>任务列表页</div>} />
        <Route path="/jobs/:id" element={<div>任务详情页</div>} />
      </Routes>
    </MemoryRouter>
  );
}

const next = () => userEvent.click(screen.getByRole("button", { name: /下一步/ }));
const stepBar = () => screen.getByRole("navigation", { name: "步骤" });
const currentStep = () => within(stepBar()).getByText((_, el) => el?.getAttribute("aria-current") === "step");

async function pickSource(name: RegExp) {
  const list = await screen.findByRole("radiogroup", { name: "数据源" });
  await userEvent.click(within(list).getByRole("radio", { name }));
}

/** 第 1 步选中 db-01 · orders，进入第 2 步并等数据库列表加载完成 */
async function toStep2(name = /db-01 · orders/) {
  renderWizard();
  await pickSource(name);
  await next();
  await screen.findByRole("region", { name: /能力探测/ });
}

async function toStep3() {
  await toStep2();
  await screen.findByRole("checkbox", { name: /orders/ });
  await next();
  return screen.findByRole("textbox", { name: "路径前缀" });
}

/** 第 3 步选择一个不冲突的存储与前缀，进入第 4 步 */
async function toStep4() {
  const prefix = await toStep3();
  await userEvent.click(
    within(await screen.findByRole("radiogroup", { name: "存储" })).getByRole("radio", { name: /backup-local/ })
  );
  await userEvent.clear(prefix);
  await userEvent.type(prefix, "mysql/db-01-orders-v2");
  await next();
  await screen.findByRole("radiogroup", { name: "频率" });
}

/** 进入第 5 步（默认计划与保留都合法，可以直接下一步） */
async function toStep5() {
  await toStep4();
  await next();
  await screen.findByRole("textbox", { name: "任务名称" });
}

describe("新建任务向导 · 框架", () => {
  it("显示五步步骤条，当前为第 1 步，上一步不可用", async () => {
    renderWizard();
    expect(await screen.findByRole("heading", { name: "新建任务" })).toBeInTheDocument();
    const items = within(stepBar()).getAllByRole("listitem");
    expect(items.map((li) => li.textContent)).toEqual([
      "1类型与数据源",
      "2内容与方式",
      "3目的地",
      "4计划与保留",
      "5确认",
    ]);
    expect(currentStep()).toHaveTextContent("类型与数据源");
    expect(screen.getByRole("button", { name: "上一步" })).toBeDisabled();
  });

  it("未填写任何内容时取消直接回到任务列表，不弹确认", async () => {
    renderWizard();
    await screen.findByRole("radiogroup", { name: "数据源" });
    await userEvent.click(screen.getByRole("button", { name: "取消" }));
    expect(await screen.findByText("任务列表页")).toBeInTheDocument();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("填写过内容后取消需要二次确认：继续编辑保留在向导，放弃后回到任务列表", async () => {
    renderWizard();
    await pickSource(/db-01 · orders/);
    await userEvent.click(screen.getByRole("button", { name: "取消" }));
    let dialog = await screen.findByRole("dialog", { name: "放弃新建任务？" });
    await userEvent.click(within(dialog).getByRole("button", { name: "继续编辑" }));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(screen.queryByText("任务列表页")).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "取消" }));
    dialog = await screen.findByRole("dialog", { name: "放弃新建任务？" });
    await userEvent.click(within(dialog).getByRole("button", { name: "放弃" }));
    expect(await screen.findByText("任务列表页")).toBeInTheDocument();
    expect(fetchMock.mock.calls.every(([, init]) => ((init as RequestInit)?.method ?? "GET") === "GET")).toBe(true);
  });

  it("点击已完成的步骤返回修改，已填的内容不丢失", async () => {
    const prefix = await toStep3();
    await userEvent.clear(prefix);
    await userEvent.type(prefix, "prod/orders");

    await userEvent.click(within(stepBar()).getByRole("button", { name: /内容与方式/ }));
    expect(currentStep()).toHaveTextContent("内容与方式");
    await userEvent.click(screen.getByRole("radio", { name: "指定数据库" }));
    await userEvent.click(await screen.findByRole("checkbox", { name: /payments/ }));

    await userEvent.click(within(stepBar()).getByRole("button", { name: /类型与数据源/ }));
    expect(
      within(screen.getByRole("radiogroup", { name: "数据源" })).getByRole("radio", { name: /db-01 · orders/ })
    ).toHaveAttribute("aria-checked", "true");
    // 后面的步骤不能从步骤条直接跳过去
    expect(within(stepBar()).queryByRole("button", { name: /目的地/ })).not.toBeInTheDocument();

    await next();
    expect(screen.getByRole("radio", { name: "指定数据库" })).toHaveAttribute("aria-checked", "true");
    expect(await screen.findByRole("checkbox", { name: /payments/ })).toBeChecked();
    await next();
    expect(screen.getByRole("textbox", { name: "路径前缀" })).toHaveValue("prod/orders");
  });
});

describe("新建任务向导 · 第 1 步 类型与数据源", () => {
  it("同步类型与服务器文件数据源禁用，标“后续版本支持”；数据源行显示类型、地址、状态与探测摘要", async () => {
    renderWizard();
    const types = await screen.findByRole("radiogroup", { name: "任务类型" });
    expect(within(types).getByRole("radio", { name: /^备份/ })).toHaveAttribute("aria-checked", "true");
    const sync = within(types).getByRole("radio", { name: /^同步/ });
    expect(sync).toBeDisabled();
    expect(sync).toHaveTextContent("后续版本支持");

    const list = screen.getByRole("radiogroup", { name: "数据源" });
    const files = within(list).getByRole("radio", { name: /web-01 文件/ });
    expect(files).toBeDisabled();
    expect(files).toHaveTextContent("后续版本支持");

    const orders = within(list).getByRole("radio", { name: /db-01 · orders/ });
    expect(orders).toHaveTextContent("MySQL");
    expect(orders).toHaveTextContent("mysql://10.0.1.11:3306");
    expect(orders).toHaveTextContent("正常");
    expect(orders).toHaveTextContent("1 通过 · 1 提醒");
  });

  it("未选择数据源时不能进入下一步", async () => {
    renderWizard();
    await screen.findByRole("radiogroup", { name: "数据源" });
    await next();
    expect(screen.getByText("请选择一个数据源")).toBeInTheDocument();
    expect(currentStep()).toHaveTextContent("类型与数据源");
  });

  it("状态异常的数据源可以选中，行内提示原因，但不能进入下一步", async () => {
    renderWizard();
    await pickSource(/db-02/);
    const row = within(screen.getByRole("radiogroup", { name: "数据源" })).getByRole("radio", { name: /db-02/ });
    expect(row).toHaveAttribute("aria-checked", "true");
    expect(row).toHaveTextContent("跳板 bastion 的主机密钥已变化");
    await next();
    expect(screen.getByText(/数据源状态不是“正常”/)).toBeInTheDocument();
    expect(currentStep()).toHaveTextContent("类型与数据源");
    expect(calls("GET /api/v1/datasources/3/databases")).toHaveLength(0);
  });

  it("没有 MySQL / PostgreSQL 数据源时显示空状态，引导到数据源页新建", async () => {
    route("GET /api/v1/datasources", ok({ items: [webFiles] }));
    renderWizard();
    expect(await screen.findByText("没有可用的数据源")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "新建数据源" })).toHaveAttribute("href", "/sources");
  });

  it("数据源列表加载失败时显示原因，重试后恢复", async () => {
    route("GET /api/v1/datasources", fail(-1, "数据库繁忙", 500));
    renderWizard();
    expect(await screen.findByText(/数据库繁忙/)).toBeInTheDocument();
    route("GET /api/v1/datasources", ok({ items: [dbOrders] }));
    await userEvent.click(screen.getByRole("button", { name: "重试" }));
    expect(await screen.findByRole("radio", { name: /db-01 · orders/ })).toBeInTheDocument();
  });
});

describe("新建任务向导 · 第 2 步 内容与方式", () => {
  it("MySQL：显示探测结果与修复方法、实时数据库列表与数据量、方式与一并备份的默认值", async () => {
    await toStep2();
    const probe = screen.getByRole("region", { name: /能力探测/ });
    expect(within(probe).getByText("GTID 未开启")).toBeInTheDocument();
    expect(within(probe).getByText("SET GLOBAL gtid_mode = ON")).toBeInTheDocument();
    expect(within(probe).getByText(/2 分钟前/)).toBeInTheDocument();

    expect(screen.getByRole("radio", { name: "整个实例" })).toHaveAttribute("aria-checked", "true");
    const orders = await screen.findByRole("checkbox", { name: /orders/ });
    expect(orders.closest("label")).toHaveTextContent("12.0 GB");
    expect(screen.getByRole("checkbox", { name: /payments/ }).closest("label")).toHaveTextContent("300.0 MB");

    const method = screen.getByRole("radiogroup", { name: "备份方式" });
    expect(within(method).getByRole("radio", { name: /仅全量/ })).toHaveAttribute("aria-checked", "true");
    const incr = within(method).getByRole("radio", { name: /全量 \+ 增量（binlog）/ });
    expect(incr).toBeDisabled();
    expect(incr).toHaveTextContent("后续版本支持");

    expect(screen.getByText(/mysqldump（官方）/)).toBeInTheDocument();
    expect(screen.getByRole("checkbox", { name: "存储过程与函数" })).toBeChecked();
    expect(screen.getByRole("checkbox", { name: "触发器" })).toBeChecked();
    expect(screen.getByRole("checkbox", { name: "事件" })).toBeChecked();
    expect(screen.getByRole("checkbox", { name: "账号与权限" })).not.toBeChecked();
    // 设计稿中的“额外 mysqldump 参数”不提供（设计决策 7）
    expect(screen.queryByText(/额外/)).not.toBeInTheDocument();
    expect(calls("GET /api/v1/datasources/1/databases")).toHaveLength(1);
  });

  it("重新探测：请求后端并显示新的探测结果", async () => {
    await toStep2();
    const reprobed: DataSourceItem = {
      ...dbOrders,
      probe: { ...dbOrders.probe!, ok: 2, warn: 0, items: [probeItem("mysql.gtid", "ok", "GTID 已开启", "")] },
    };
    route("POST /api/v1/datasources/1/reprobe", ok({ item: reprobed }));
    await userEvent.click(screen.getByRole("button", { name: "重新探测" }));
    expect(await screen.findByText("GTID 已开启")).toBeInTheDocument();
    expect(screen.queryByText("GTID 未开启")).not.toBeInTheDocument();
    expect(calls("POST /api/v1/datasources/1/reprobe")).toHaveLength(1);
  });

  it("数据库列表读取失败时显示原因与重试", async () => {
    route("GET /api/v1/datasources/1/databases", fail(10617, "读取数据库列表失败：access denied"));
    await toStep2();
    expect(await screen.findByText(/access denied/)).toBeInTheDocument();
    route("GET /api/v1/datasources/1/databases", ok({ databases }));
    await userEvent.click(screen.getByRole("button", { name: "重试" }));
    expect(await screen.findByRole("checkbox", { name: /orders/ })).toBeInTheDocument();
  });

  it("指定数据库时至少选一个库", async () => {
    await toStep2();
    await userEvent.click(screen.getByRole("radio", { name: "指定数据库" }));
    await screen.findByRole("checkbox", { name: /orders/ });
    await next();
    expect(screen.getByText("至少选择一个数据库")).toBeInTheDocument();
    expect(currentStep()).toHaveTextContent("内容与方式");
    await userEvent.click(screen.getByRole("checkbox", { name: /orders/ }));
    expect(screen.queryByText("至少选择一个数据库")).not.toBeInTheDocument();
    expect(screen.getByText(/已选 1 个库/)).toBeInTheDocument();
  });

  it("排除表在高级选项中，默认折叠；MySQL 写作 库.表，格式不对时在字段旁提示并阻止下一步", async () => {
    await toStep2();
    expect(screen.queryByRole("textbox", { name: "排除表" })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /高级选项/ }));
    const exclude = screen.getByRole("textbox", { name: "排除表" });
    await userEvent.type(exclude, "orders.audit_log{enter}orders.public.t1{enter}bad{enter}orders. t2");
    await next();
    // 段内的首尾空白同样不行：导出时按段校验，保存时后端也会拒绝
    expect(screen.getByText(/排除表格式不正确.*orders\.public\.t1、bad、orders\. t2/)).toBeInTheDocument();
    expect(exclude).toHaveAttribute("aria-invalid", "true");
    expect(currentStep()).toHaveTextContent("内容与方式");

    await userEvent.clear(exclude);
    await userEvent.type(exclude, "orders.audit_log{enter}{enter}  payments.tmp  ");
    await next();
    expect(currentStep()).toHaveTextContent("目的地");
  });

  it("PostgreSQL：说明 pg_dump、全局对象默认勾选，另一种方式禁用，排除表写作 库.模式.表", async () => {
    await toStep2(/pg-analytics-02/);
    expect(screen.getByText(/pg_dump（官方）/)).toBeInTheDocument();
    expect(screen.getByRole("checkbox", { name: "角色、表空间等全局对象" })).toBeChecked();
    expect(screen.queryByRole("checkbox", { name: "账号与权限" })).not.toBeInTheDocument();
    const wal = screen.getByRole("radio", { name: /物理全量 \+ WAL 增量/ });
    expect(wal).toBeDisabled();

    await userEvent.click(screen.getByRole("button", { name: /高级选项/ }));
    await userEvent.type(screen.getByRole("textbox", { name: "排除表" }), "analytics.events");
    await next();
    expect(screen.getByText(/排除表格式不正确.*analytics\.events/)).toBeInTheDocument();
    expect(currentStep()).toHaveTextContent("内容与方式");
    await userEvent.clear(screen.getByRole("textbox", { name: "排除表" }));
    await userEvent.type(screen.getByRole("textbox", { name: "排除表" }), "analytics.public.events");
    await next();
    expect(currentStep()).toHaveTextContent("目的地");
  });

  it("探测判定导出工具不可用时，“仅全量”也不可选，显示原因与修复方法，不能进入下一步", async () => {
    route("GET /api/v1/datasources", ok({ items: [pgNoDump] }));
    route("GET /api/v1/datasources/2", ok({ item: pgNoDump }));
    await toStep2(/pg-analytics-02/);
    const full = screen.getByRole("radio", { name: /仅全量/ });
    expect(full).toBeDisabled();
    const method = screen.getByRole("radiogroup", { name: "备份方式" });
    expect(within(method).getByText(/本机 pg_dump（14\.1）大版本低于服务端/)).toBeInTheDocument();
    expect(within(method).getByText("安装 PostgreSQL 16 客户端")).toBeInTheDocument();
    await next();
    expect(screen.getByText(/导出工具不可用/)).toBeInTheDocument();
    expect(currentStep()).toHaveTextContent("内容与方式");
  });

  it("探测进行中时定时刷新，结果出来后显示", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      const probing: DataSourceItem = { ...dbOrders, probe: { state: "probing", ok: 0, warn: 0, fail: 0, time: 0 } };
      route("GET /api/v1/datasources", ok({ items: [probing] }));
      await toStep2();
      expect(screen.getByRole("region", { name: /能力探测/ })).toHaveTextContent("探测中");
      await act(() => vi.advanceTimersByTimeAsync(3500));
      expect(await screen.findByText("GTID 未开启")).toBeInTheDocument();
    } finally {
      vi.useRealTimers();
    }
  });
});

describe("新建任务向导 · 第 2 步 没有探测结果", () => {
  it("数据源还没有探测结果时不当作探测进行中：可以重新探测，也不反复轮询", async () => {
    const unprobed: DataSourceItem = { ...dbOrders, probe: null };
    route("GET /api/v1/datasources", ok({ items: [unprobed, pgAnalytics] }));
    route("GET /api/v1/datasources/1", ok({ item: unprobed }));
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      await toStep2();
      const region = screen.getByRole("region", { name: /能力探测/ });
      expect(within(region).getByRole("button", { name: "重新探测" })).not.toBeDisabled();
      await act(async () => {
        await vi.advanceTimersByTimeAsync(10000);
      });
      expect(calls("GET /api/v1/datasources/1")).toHaveLength(0);
    } finally {
      vi.useRealTimers();
    }
  });
});

describe("新建任务向导 · 第 3 步 目的地", () => {
  it("存储列表：状态异常的不可选并显示原因；默认前缀、仓库内位置、默认 zstd 压缩与加密指纹", async () => {
    const prefix = await toStep3();
    const list = await screen.findByRole("radiogroup", { name: "存储" });
    const bad = within(list).getByRole("radio", { name: /minio-dr/ });
    expect(bad).toBeDisabled();
    expect(bad).toHaveTextContent("连接 10.8.0.5:9000 超时");
    const local = within(list).getByRole("radio", { name: /backup-local/ });
    expect(local).toHaveTextContent("/data/opsnap");

    // 数据源名称中的非法字符（空格、·）替换为 -
    expect(prefix).toHaveValue("mysql/db-01-orders");
    const compression = screen.getByRole("radiogroup", { name: "压缩" });
    expect(within(compression).getByRole("radio", { name: "zstd" })).toHaveAttribute("aria-checked", "true");
    expect(within(compression).getByRole("radio", { name: "不压缩" })).toBeInTheDocument();

    await userEvent.click(local);
    expect(screen.getByText("backup-local:/mysql/db-01-orders")).toBeInTheDocument();
    const encryption = screen.getByRole("region", { name: "加密" });
    expect(encryption).toHaveTextContent("已加密");
    expect(encryption).toHaveTextContent("SHA256:k3yF1ngerpr1nt");
    // 设计稿中的物理文件路径不存在（设计决策 8）
    expect(screen.queryByText(/\.zst/)).not.toBeInTheDocument();
  });

  it("未选择存储时不能进入下一步", async () => {
    await toStep3();
    await screen.findByRole("radiogroup", { name: "存储" });
    await next();
    expect(screen.getByText("请选择一个存储")).toBeInTheDocument();
    expect(currentStep()).toHaveTextContent("目的地");
  });

  it.each([
    ["", "为空"],
    ["/mysql/db", "以 / 开头"],
    ["mysql/db/", "以 / 结尾"],
    ["mysql//db", "连续的 /"],
    ["mysql/../db", "含 .."],
    ["mysql/db 01", "含空格"],
    ["a".repeat(129), "超过 128 个字符"],
  ])("前缀 %j（%s）不合法时在字段旁提示并阻止下一步", async (value) => {
    const prefix = await toStep3();
    await userEvent.click(
      within(await screen.findByRole("radiogroup", { name: "存储" })).getByRole("radio", { name: /backup-local/ })
    );
    await userEvent.clear(prefix);
    if (value) await userEvent.type(prefix, value);
    await next();
    expect(prefix).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByText(/路径前缀须为 1–128 个字符/)).toBeInTheDocument();
    expect(currentStep()).toHaveTextContent("目的地");
  });

  it("与同一存储中其他任务的前缀相同或互为上下级时，提示与哪个任务冲突；只是字符串前缀相同不算冲突", async () => {
    const prefix = await toStep3();
    await userEvent.click(
      within(await screen.findByRole("radiogroup", { name: "存储" })).getByRole("radio", { name: /backup-local/ })
    );
    await next();
    expect(screen.getByText(/与任务“orders-nightly”/)).toBeInTheDocument();
    expect(currentStep()).toHaveTextContent("目的地");

    await userEvent.clear(prefix);
    await userEvent.type(prefix, "mysql");
    await next();
    expect(screen.getByText(/与任务“orders-nightly”/)).toBeInTheDocument();

    await userEvent.clear(prefix);
    await userEvent.type(prefix, "mysql/db-01-orders-v2");
    expect(screen.queryByText(/与任务“orders-nightly”/)).not.toBeInTheDocument();
    expect(screen.getByText("backup-local:/mysql/db-01-orders-v2")).toBeInTheDocument();
    await next();
    expect(currentStep()).toHaveTextContent("计划与保留");
  });

  it("没有存储时显示空状态，引导到存储页新建", async () => {
    route("GET /api/v1/storages", ok({ items: [] }));
    await toStep3();
    expect(await screen.findByText("还没有存储")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "新建存储" })).toHaveAttribute("href", "/storage");
  });
});

describe("编辑任务", () => {
  it("载入已有任务的设置；数据源、存储与路径前缀不能修改", async () => {
    route("GET /api/v1/jobs/9", ok({ item: existingJob }));
    renderWizard("/jobs/9/edit");
    expect(await screen.findByRole("heading", { name: "编辑任务" })).toBeInTheDocument();
    const list = await screen.findByRole("radiogroup", { name: "数据源" });
    expect(within(list).getByRole("radio", { name: /db-01 · orders/ })).toHaveAttribute("aria-checked", "true");
    expect(within(list).getByRole("radio", { name: /pg-analytics-02/ })).toBeDisabled();
    expect(screen.getByText("数据源、存储和路径前缀创建后不能修改")).toBeInTheDocument();

    await next();
    await screen.findByRole("region", { name: /能力探测/ });
    expect(screen.getByRole("radio", { name: "指定数据库" })).toHaveAttribute("aria-checked", "true");
    expect(await screen.findByRole("checkbox", { name: /payments/ })).toBeChecked();
    expect(screen.getByRole("checkbox", { name: /users/ })).not.toBeChecked();
    expect(screen.getByRole("checkbox", { name: "触发器" })).not.toBeChecked();
    expect(screen.getByRole("checkbox", { name: "账号与权限" })).toBeChecked();

    await next();
    const prefix = await screen.findByRole("textbox", { name: "路径前缀" });
    expect(prefix).toHaveValue("mysql/db-01-orders");
    expect(prefix).toHaveAttribute("readonly");
    // 存储与前缀两处都说明创建后不能修改
    expect(screen.getAllByText("数据源、存储和路径前缀创建后不能修改")).toHaveLength(2);
    const storages = screen.getByRole("radiogroup", { name: "存储" });
    expect(within(storages).getByRole("radio", { name: /backup-local/ })).toHaveAttribute("aria-checked", "true");
    expect(
      within(screen.getByRole("radiogroup", { name: "压缩" })).getByRole("radio", { name: "gzip" })
    ).toHaveAttribute("aria-checked", "true");
    // 自己的前缀不算冲突
    await next();
    expect(currentStep()).toHaveTextContent("计划与保留");
  });

  it("数据源当前状态异常时仍可编辑：编辑不重新检查数据源状态（与后端一致）", async () => {
    route("GET /api/v1/jobs/9", ok({ item: existingJob }));
    route(
      "GET /api/v1/datasources",
      ok({ items: [{ ...dbOrders, status: "unreachable", status_message: "连接超时" }, pgAnalytics] })
    );
    renderWizard("/jobs/9/edit");
    await screen.findByRole("radiogroup", { name: "数据源" });
    await next();
    expect(await screen.findByRole("region", { name: /能力探测/ })).toBeInTheDocument();
    expect(currentStep()).toHaveTextContent("内容与方式");
  });

  it("任务的时区不在浏览器的时区列表中时，下拉框仍显示并保留它", async () => {
    const spy = vi.spyOn(Intl, "supportedValuesOf").mockReturnValue(["Asia/Shanghai", "Europe/Berlin"]);
    try {
      route(
        "GET /api/v1/jobs/9",
        ok({ item: { ...existingJob, schedule: { ...existingJob.schedule, timezone: "UTC" } } })
      );
      renderWizard("/jobs/9/edit");
      await screen.findByRole("radiogroup", { name: "数据源" });
      await next();
      await screen.findByRole("region", { name: /能力探测/ });
      await next();
      await screen.findByRole("textbox", { name: "路径前缀" });
      await next();
      expect(await screen.findByRole("combobox", { name: "时区" })).toHaveValue("UTC");
    } finally {
      spy.mockRestore();
    }
  });

  it("任务不存在时提示并提供返回任务列表的入口", async () => {
    route("GET /api/v1/jobs/9", fail(10700, "任务不存在", 404));
    renderWizard("/jobs/9/edit");
    expect(await screen.findByText("任务不存在")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "返回任务列表" })).toHaveAttribute("href", "/jobs");
  });

  it("载入已有任务的计划与保留；数据源、存储、前缀不参与请求体（0 / 空串表示不变）", async () => {
    route("GET /api/v1/jobs/9", ok({ item: existingJob }));
    renderWizard("/jobs/9/edit");
    await screen.findByRole("radiogroup", { name: "数据源" });
    await next();
    await screen.findByRole("region", { name: /能力探测/ });
    await next();
    await screen.findByRole("textbox", { name: "路径前缀" });
    await next();

    expect(await screen.findByRole("radio", { name: "每天" })).toHaveAttribute("aria-checked", "true");
    expect(screen.getByRole("spinbutton", { name: "保留最近 N 天内的全部快照" })).toHaveValue(7);
    expect(screen.getByRole("spinbutton", { name: "更早的，每周保留最后一份" })).toHaveValue(4);
    expect(screen.getByRole("spinbutton", { name: "每月保留最后一份" })).toHaveValue(6);
    expect(screen.getByRole("spinbutton", { name: "重试间隔（分钟）" })).toHaveValue(10);
    expect(await screen.findByText("按当前计划，最多保留约 42 份快照")).toBeInTheDocument();

    await next();
    const nameField = await screen.findByRole("textbox", { name: "任务名称" });
    expect(nameField).toHaveValue("orders-nightly");
    // 编辑时不显示“创建后”的选择
    expect(screen.queryByText("立即执行一次")).not.toBeInTheDocument();

    const putHandler = vi.fn(() => ok({ item: { ...existingJob, name: "orders-nightly" } }));
    route("PUT /api/v1/jobs/9", putHandler);
    await userEvent.click(screen.getByRole("button", { name: "保存" }));
    expect(await screen.findByText("任务详情页")).toBeInTheDocument();
    const [, init] = fetchMock.mock.calls.find(
      ([url, i]) => url === "/api/v1/jobs/9" && (i as RequestInit)?.method === "PUT"
    )!;
    const body = JSON.parse((init as RequestInit).body as string) as Record<string, unknown>;
    expect(body).toMatchObject({ datasource_id: 0, storage_id: 0, prefix: "", name: "orders-nightly" });
  });
});

describe("新建任务向导 · 第 4 步 计划与保留", () => {
  it("默认每天 02:00、浏览器时区；显示接下来三次执行时间与最多保留份数", async () => {
    await toStep4();
    expect(screen.getByRole("radio", { name: "每天" })).toHaveAttribute("aria-checked", "true");
    expect(screen.getByLabelText("执行时刻")).toHaveValue("02:00");
    expect(screen.getByRole("combobox", { name: "时区" })).toHaveValue(
      Intl.DateTimeFormat().resolvedOptions().timeZone
    );
    expect(screen.getByRole("spinbutton", { name: "保留最近 N 天内的全部快照" })).toHaveValue(7);
    expect(screen.getByRole("spinbutton", { name: "更早的，每周保留最后一份" })).toHaveValue(4);
    expect(screen.getByRole("spinbutton", { name: "每月保留最后一份" })).toHaveValue(6);
    expect(screen.getByRole("spinbutton", { name: "失败重试次数" })).toHaveValue(2);
    expect(screen.getByRole("spinbutton", { name: "重试间隔（分钟）" })).toHaveValue(5);
    expect(screen.getByRole("spinbutton", { name: "超时时长（分钟）" })).toHaveValue(120);

    expect(await screen.findByText("2026-09-29 02:00")).toBeInTheDocument();
    expect(screen.getByText("2026-09-30 02:00")).toBeInTheDocument();
    expect(screen.getByText("2026-10-01 02:00")).toBeInTheDocument();
    expect(screen.getByText("按当前计划，最多保留约 42 份快照")).toBeInTheDocument();
    expect(screen.getByText("本任务最新的一份成功快照始终保留")).toBeInTheDocument();
  });

  it("保留与失败处理只接受整数：小数在字段旁提示，不能进入下一步", async () => {
    await toStep4();
    const days = screen.getByRole("spinbutton", { name: "保留最近 N 天内的全部快照" });
    await userEvent.clear(days);
    await userEvent.type(days, "1.5");
    await next();
    expect(screen.getByText("保留天数须为 1–365 的整数")).toBeInTheDocument();
    expect(currentStep()).toHaveTextContent("计划与保留");
  });

  it("每小时：选择第几分钟；每周：至少选一个星期几才能下一步", async () => {
    await toStep4();
    await userEvent.click(screen.getByRole("radio", { name: "每小时" }));
    expect(screen.queryByLabelText("执行时刻")).not.toBeInTheDocument();
    await userEvent.selectOptions(screen.getByRole("combobox", { name: "第几分钟执行" }), "30");

    await userEvent.click(screen.getByRole("radio", { name: "每周" }));
    await next();
    expect(screen.getByText("至少选择一个星期几")).toBeInTheDocument();
    expect(currentStep()).toHaveTextContent("计划与保留");
    await userEvent.click(screen.getByRole("checkbox", { name: "周一" }));
    expect(screen.queryByText("至少选择一个星期几")).not.toBeInTheDocument();
    await next();
    expect(currentStep()).toHaveTextContent("确认");
  });

  it("自定义 Cron：留空提示需要输入；服务端判定不合法时在字段旁显示原因，修正后恢复", async () => {
    await toStep4();
    await userEvent.click(screen.getByRole("radio", { name: "自定义 Cron" }));
    await next();
    expect(screen.getByText("请输入 Cron 表达式")).toBeInTheDocument();
    expect(currentStep()).toHaveTextContent("计划与保留");

    route("POST /api/v1/jobs/schedule-preview", fail(10716, "执行计划不正确：无法识别的字段"));
    await userEvent.type(screen.getByRole("textbox", { name: "Cron 表达式" }), "bad cron");
    expect(await screen.findByText("执行计划不正确：无法识别的字段")).toBeInTheDocument();

    route("POST /api/v1/jobs/schedule-preview", ok({ next_runs: NEXT_RUNS, max_snapshots: 10 }));
    await userEvent.clear(screen.getByRole("textbox", { name: "Cron 表达式" }));
    await userEvent.type(screen.getByRole("textbox", { name: "Cron 表达式" }), "*/5 * * * *");
    await waitFor(() => expect(screen.queryByText("执行计划不正确：无法识别的字段")).not.toBeInTheDocument());
    expect(await screen.findByText("按当前计划，最多保留约 10 份快照")).toBeInTheDocument();
    await next();
    expect(currentStep()).toHaveTextContent("确认");
  });

  it("计划在本地就不合法时不显示上一次计划的预览；预览请求失败时显示原因", async () => {
    await toStep4();
    expect(await screen.findByText("2026-09-29 02:00")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("radio", { name: "每周" }));
    expect(screen.queryByText("2026-09-29 02:00")).not.toBeInTheDocument();
    expect(screen.queryByText("按当前计划，最多保留约 42 份快照")).not.toBeInTheDocument();

    route("POST /api/v1/jobs/schedule-preview", fail(-1, "服务繁忙", 500));
    await userEvent.click(screen.getByRole("radio", { name: "每小时" }));
    expect(await screen.findByText("无法计算执行时间：服务繁忙")).toBeInTheDocument();
    expect(screen.queryByText("计算中…")).not.toBeInTheDocument();
  });

  it.each([
    ["保留最近 N 天内的全部快照", "0", "保留天数须为 1–365 的整数"],
    ["更早的，每周保留最后一份", "521", "保留周数须为 0–520 的整数"],
    ["每月保留最后一份", "-1", "保留月数须为 0–120 的整数"],
    ["失败重试次数", "6", "失败重试须为 0–5 的整数"],
    ["重试间隔（分钟）", "0", "重试间隔须为 1–120 分钟"],
    ["超时时长（分钟）", "9", "超时时长须为 10–2880 分钟（10 分钟到 48 小时）"],
  ])("%s 为 %s 时阻止下一步并提示原因", async (label, value, message) => {
    await toStep4();
    const field = screen.getByRole("spinbutton", { name: label });
    // 用 fireEvent 直接改值：数字输入框逐字符敲负号在 jsdom 下不可靠
    fireEvent.change(field, { target: { value } });
    await next();
    expect(screen.getByText(message)).toBeInTheDocument();
    expect(currentStep()).toHaveTextContent("计划与保留");
  });
});

describe("新建任务向导 · 第 5 步 确认", () => {
  it("默认任务名为“<数据源名称> 全量备份”；四组摘要与“修改”入口；估算所选库的源数据量", async () => {
    await toStep5();
    const name = screen.getByRole("textbox", { name: "任务名称" });
    expect(name).toHaveValue("db-01 · orders 全量备份");

    expect(screen.getByText("备份 · db-01 · orders")).toBeInTheDocument();
    // 第 2 步没有切换到“指定数据库”，默认整个实例
    expect(screen.getByText("整个实例")).toBeInTheDocument();
    expect(screen.getByText("backup-local:/mysql/db-01-orders-v2")).toBeInTheDocument();
    expect(screen.getByText("每天 02:00")).toBeInTheDocument();

    // 整个实例：估算所有库（orders + payments + users）的总量
    expect(await screen.findByText("首次为全量备份，所选库的源数据量约 12.3 GB")).toBeInTheDocument();

    expect(screen.getByRole("radio", { name: "立即执行一次" })).toHaveAttribute("aria-checked", "true");
    await userEvent.click(screen.getByRole("radio", { name: "等下一次计划" }));
    expect(await screen.findByText("下次执行：2026-09-29 02:00")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "修改目的地" }));
    expect(currentStep()).toHaveTextContent("目的地");
    expect(screen.getByRole("textbox", { name: "路径前缀" })).toHaveValue("mysql/db-01-orders-v2");
  });

  it("源数据量读取失败时显示“无法估算”，不影响创建", async () => {
    // 第 2 步用同一个接口列出可选库，须先成功一次向导才能往后走；第 5 步自己的估算再取一次，这次失败
    let calls = 0;
    route("GET /api/v1/datasources/1/databases", () => {
      calls += 1;
      return calls === 1 ? ok({ databases }) : fail(-1, "读取失败");
    });
    await toStep5();
    expect(await screen.findByText("无法估算")).toBeInTheDocument();
  });

  it("源数据量读取中显示“估算中…”，读取完成前不显示“无法估算”", async () => {
    let calls = 0;
    route("GET /api/v1/datasources/1/databases", () => {
      calls += 1;
      return calls === 1 ? ok({ databases }) : new Promise<Response>(() => {});
    });
    await toStep5();
    expect(await screen.findByText("估算中…")).toBeInTheDocument();
    expect(screen.queryByText("无法估算")).not.toBeInTheDocument();
  });

  it("创建任务：提交完整字段、加载状态防止重复提交、成功后进入任务详情页", async () => {
    await toStep5();
    let resolveCreate: (r: Response) => void = () => {};
    const createHandler = vi.fn(
      () =>
        new Promise<Response>((resolve) => {
          resolveCreate = resolve;
        })
    );
    route("POST /api/v1/jobs", createHandler);

    const submit = screen.getByRole("button", { name: "创建任务" });
    await userEvent.click(submit);
    await userEvent.click(submit);
    expect(await screen.findByRole("button", { name: "提交中…" })).toBeDisabled();
    expect(createHandler).toHaveBeenCalledTimes(1);

    resolveCreate(ok({ item: { ...existingJob, id: 42 } }));
    expect(await screen.findByText("任务详情页")).toBeInTheDocument();

    const [, init] = fetchMock.mock.calls.find(
      ([url, i]) => url === "/api/v1/jobs" && (i as RequestInit)?.method === "POST"
    )!;
    const body = JSON.parse((init as RequestInit).body as string) as Record<string, unknown>;
    expect(body).toMatchObject({
      name: "db-01 · orders 全量备份",
      type: "backup",
      datasource_id: 1,
      storage_id: 7,
      prefix: "mysql/db-01-orders-v2",
      scope: "instance",
      databases: [],
      method: "full",
      compression: "zstd",
      run_now: true,
    });
    expect(body.schedule).toMatchObject({ kind: "daily", hour: 2, minute: 0 });
    expect(body.retention).toMatchObject({ days: 7, weeks: 4, months: 6 });
    expect(body.failure).toMatchObject({ retries: 2, retry_interval: 5, timeout: 120 });
  });

  it("创建失败：任务重名映射到名称字段，停在第 5 步", async () => {
    await toStep5();
    route("POST /api/v1/jobs", fail(10702, "已有同名的任务"));
    await userEvent.click(screen.getByRole("button", { name: "创建任务" }));
    expect(await screen.findByText("已有同名的任务")).toBeInTheDocument();
    expect(currentStep()).toHaveTextContent("确认");
  });

  it("创建失败：路径前缀冲突时停在第 5 步显示原因，回到第 3 步时前缀字段旁也有提示", async () => {
    await toStep5();
    const message = "路径前缀与任务“orders-nightly”在同一存储中重复或互为上下级";
    route("POST /api/v1/jobs", fail(10714, message));
    await userEvent.click(screen.getByRole("button", { name: "创建任务" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(message);
    expect(currentStep()).toHaveTextContent("确认");

    await userEvent.click(screen.getByRole("button", { name: "修改目的地" }));
    expect(currentStep()).toHaveTextContent("目的地");
    expect(screen.getByText(message)).toBeInTheDocument();
  });

  it("创建失败：无法归到具体字段的原因显示在表单顶部，停在第 5 步", async () => {
    await toStep5();
    route("POST /api/v1/jobs", fail(-1, "数据库繁忙", 500));
    await userEvent.click(screen.getByRole("button", { name: "创建任务" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("数据库繁忙");
    expect(currentStep()).toHaveTextContent("确认");
  });
});
