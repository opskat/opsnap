import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

import i18n from "@/i18n";
import type { JobItem, Run } from "@/lib/jobs";
import { JobsPage } from "@/pages/JobsPage";

const ok = (data: unknown) => new Response(JSON.stringify({ code: 0, msg: "success", data }), { status: 200 });
const fail = (code: number, msg: string, status = 400) => new Response(JSON.stringify({ code, msg }), { status });
const now = () => Math.floor(Date.now() / 1000);

const successRun: Run = {
  id: 101,
  job_id: 1,
  status: "success",
  trigger: "schedule",
  retry_attempt: 0,
  retry_total: 0,
  scheduled_at: now() - 7200,
  created_at: now() - 7200,
  started_at: now() - 7200,
  finished_at: now() - 7200 + 252,
  duration_ms: 252000,
  exported_bytes: 200 * 1024 * 1024,
  uploaded_bytes: 186 * 1024 * 1024,
  snapshot_id: "k1a2b3",
  failed_step: "",
  reason: "",
};

const runningRun: Run = {
  id: 202,
  job_id: 2,
  status: "running",
  trigger: "manual",
  retry_attempt: 0,
  retry_total: 0,
  scheduled_at: 0,
  created_at: now() - 123,
  started_at: now() - 123,
  finished_at: 0,
  duration_ms: 123000,
  exported_bytes: 1024 ** 3,
  uploaded_bytes: 0,
  snapshot_id: "",
  failed_step: "",
  reason: "",
};

const queuedRun: Run = {
  id: 303,
  job_id: 3,
  status: "queued",
  trigger: "manual",
  retry_attempt: 0,
  retry_total: 0,
  scheduled_at: 0,
  created_at: now() - 5,
  started_at: 0,
  finished_at: 0,
  duration_ms: 0,
  exported_bytes: 0,
  uploaded_bytes: 0,
  snapshot_id: "",
  failed_step: "",
  reason: "",
};

const oldSuccessRun: Run = { ...successRun, id: 404, job_id: 4, started_at: now() - 604800 };

const ordersProd: JobItem = {
  id: 1,
  name: "orders-prod 全量备份",
  type: "backup",
  datasource_id: 10,
  datasource_name: "db-01 · orders",
  datasource_kind: "mysql",
  storage_id: 20,
  storage_name: "阿里云 OSS",
  prefix: "prod/mysql/db-01",
  location: "阿里云 OSS:/prod/mysql/db-01",
  scope: "databases",
  databases: ["orders", "payments", "users", "logs"],
  method: "full",
  options: { routines: true, triggers: true, events: true, users: false, globals: false },
  exclude_tables: [],
  compression: "zstd",
  schedule: { kind: "daily", minute: 0, hour: 2, weekdays: [], cron: "", timezone: "Asia/Shanghai" },
  retention: { days: 7, weeks: 4, months: 6 },
  failure: { retries: 2, retry_interval: 5, timeout: 120 },
  enabled: true,
  next_run_at: now() + 3600,
  created_at: now() - 864000,
  updated_at: now() - 864000,
  last_run: successRun,
  snapshot_count: 12,
};

const pgAnalytics: JobItem = {
  ...ordersProd,
  id: 2,
  name: "pg-analytics 每小时",
  datasource_id: 11,
  datasource_name: "pg-analytics-02",
  datasource_kind: "postgres",
  storage_name: "MinIO",
  location: "MinIO:/prod/pg/analytics",
  scope: "instance",
  databases: [],
  schedule: { kind: "hourly", minute: 15, hour: 0, weekdays: [], cron: "", timezone: "Asia/Shanghai" },
  last_run: runningRun,
  snapshot_count: 168,
};

const ordersDev: JobItem = {
  ...ordersProd,
  id: 3,
  name: "orders-dev 备份",
  storage_name: "MinIO",
  location: "MinIO:/dev/mysql",
  scope: "databases",
  databases: ["orders"],
  last_run: queuedRun,
  snapshot_count: 3,
};

const reportWeekly: JobItem = {
  ...ordersProd,
  id: 4,
  name: "report 周备份",
  datasource_kind: "postgres",
  scope: "databases",
  databases: ["a", "b"],
  enabled: false,
  next_run_at: 0,
  last_run: oldSuccessRun,
  snapshot_count: 4,
};

const brandNew: JobItem = {
  ...ordersProd,
  id: 5,
  name: "new-job 首次运行前",
  last_run: null,
  snapshot_count: 0,
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

function renderPage() {
  render(
    <MemoryRouter>
      <JobsPage />
    </MemoryRouter>
  );
}

describe("任务列表页", () => {
  it("渲染任务行：名称/类型/范围/启用、数据源→存储与前缀、计划与下次、最近运行、快照数", async () => {
    respond(ok({ items: [ordersProd] }));
    renderPage();

    const rows = await screen.findAllByRole("row");
    const row = rows[1];
    expect(within(row).getByRole("link", { name: "orders-prod 全量备份" })).toHaveAttribute("href", "/jobs/1");
    expect(within(row).getByText("MySQL · 4 个库 · 已启用")).toBeInTheDocument();
    expect(within(row).getByText("db-01 · orders → 阿里云 OSS")).toBeInTheDocument();
    expect(within(row).getByText("阿里云 OSS:/prod/mysql/db-01")).toBeInTheDocument();
    expect(within(row).getByText("每天 02:00")).toBeInTheDocument();
    expect(within(row).getByText("成功")).toBeInTheDocument();
    expect(within(row).getByText("12 份快照")).toBeInTheDocument();
    expect(within(row).getByText(/耗时 4m12s/)).toBeInTheDocument();
    expect(within(row).getByText(/新增 186\.0 MB/)).toBeInTheDocument();
  });

  it("从未运行过的任务显示“还没有运行过”与 0 份快照", async () => {
    respond(ok({ items: [brandNew] }));
    renderPage();

    const row = (await screen.findAllByRole("row"))[1];
    expect(within(row).getByText("还没有运行过")).toBeInTheDocument();
    expect(within(row).getByText("0 份快照")).toBeInTheDocument();
  });

  it("暂停的任务显示“已暂停”，计划旁不显示下次执行时间", async () => {
    respond(ok({ items: [reportWeekly] }));
    renderPage();

    const row = (await screen.findAllByRole("row"))[1];
    expect(within(row).getByText("PostgreSQL · 2 个库 · 已暂停")).toBeInTheDocument();
    expect(within(row).getByText("已暂停")).toBeInTheDocument();
  });

  it("没有任务时显示空状态，引导新建任务", async () => {
    respond(ok({ items: [] }));
    renderPage();

    expect(await screen.findByText("还没有任务")).toBeInTheDocument();
    expect(screen.getAllByRole("link", { name: "新建任务" })[0]).toHaveAttribute("href", "/jobs/new");
  });

  it("加载失败时显示错误与重试", async () => {
    respond(fail(-1, "服务器开小差了"));
    renderPage();

    expect(await screen.findByText("无法加载任务列表：服务器开小差了")).toBeInTheDocument();
    respond(ok({ items: [ordersProd] }));
    await userEvent.click(screen.getByRole("button", { name: "重试" }));
    expect(await screen.findByRole("link", { name: "orders-prod 全量备份" })).toBeInTheDocument();
  });

  it("立即执行：请求 POST /jobs/:id/run，请求中禁用按钮，完成后状态更新为等待中并可取消", async () => {
    respond(ok({ items: [brandNew] }));
    renderPage();
    const row = (await screen.findAllByRole("row"))[1];

    let resolve!: (r: Response) => void;
    fetchMock.mockReturnValueOnce(new Promise<Response>((r) => (resolve = r)));
    const runButton = within(row).getByRole("button", { name: "立即执行 new-job 首次运行前" });
    await userEvent.click(runButton);
    expect(runButton).toBeDisabled();
    expect(call(1)).toMatchObject({ url: "/api/v1/jobs/5/run", method: "POST" });

    resolve(ok({ run: queuedRun }));
    expect(await within(row).findByText("等待中")).toBeInTheDocument();
    // 排队中：立即执行换成取消运行
    expect(within(row).queryByRole("button", { name: "立即执行 new-job 首次运行前" })).not.toBeInTheDocument();
    expect(within(row).getByRole("button", { name: "取消运行 new-job 首次运行前" })).toBeInTheDocument();
  });

  it("暂停期间也可以立即执行", async () => {
    respond(ok({ items: [reportWeekly] }));
    renderPage();
    const row = (await screen.findAllByRole("row"))[1];
    expect(within(row).getByRole("button", { name: /立即执行/ })).not.toBeDisabled();
  });

  it("排队中：立即执行不可用，可以取消排队中的运行", async () => {
    respond(ok({ items: [ordersDev] }));
    renderPage();
    const row = (await screen.findAllByRole("row"))[1];
    expect(within(row).queryByRole("button", { name: /立即执行/ })).not.toBeInTheDocument();

    respond(ok({ run: { ...queuedRun, status: "canceled" } }));
    await userEvent.click(within(row).getByRole("button", { name: "取消运行 orders-dev 备份" }));
    expect(call(1)).toMatchObject({ url: "/api/v1/jobs/3/runs/303/cancel", method: "POST" });
    expect(await within(row).findByText("已取消")).toBeInTheDocument();
  });

  it("运行中：立即执行换成取消运行，点击后请求取消接口并更新状态", async () => {
    respond(ok({ items: [pgAnalytics] }));
    renderPage();
    const row = (await screen.findAllByRole("row"))[1];
    expect(within(row).getByText(/已导出 1\.0 GB/)).toBeInTheDocument();
    expect(within(row).getByText(/已运行 2m03s/)).toBeInTheDocument();

    respond(ok({ run: { ...runningRun, status: "canceled", reason: "用户取消" } }));
    await userEvent.click(within(row).getByRole("button", { name: /取消运行/ }));
    expect(call(1)).toMatchObject({ url: "/api/v1/jobs/2/runs/202/cancel", method: "POST" });
    expect(await within(row).findByText("已取消")).toBeInTheDocument();
    expect(within(row).getByRole("button", { name: /立即执行/ })).toBeInTheDocument();
  });

  it("更多菜单：暂停与启用", async () => {
    respond(ok({ items: [ordersProd] }));
    renderPage();
    await screen.findAllByRole("row");

    respond(ok({ item: { ...ordersProd, enabled: false, next_run_at: 0 } }));
    await userEvent.click(screen.getByRole("button", { name: "orders-prod 全量备份 的更多操作" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "暂停" }));
    expect(call(1)).toMatchObject({ url: "/api/v1/jobs/1/pause", method: "POST" });
    expect(await screen.findByText("已暂停")).toBeInTheDocument();

    respond(ok({ item: { ...ordersProd, enabled: true } }));
    await userEvent.click(screen.getByRole("button", { name: "orders-prod 全量备份 的更多操作" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "启用" }));
    expect(call(2)).toMatchObject({ url: "/api/v1/jobs/1/enable", method: "POST" });
    expect(await screen.findByText("MySQL · 4 个库 · 已启用")).toBeInTheDocument();
  });

  it("运行中或排队中时删除菜单项不可用并给出提示", async () => {
    respond(ok({ items: [pgAnalytics] }));
    renderPage();
    await userEvent.click(await screen.findByRole("button", { name: "pg-analytics 每小时 的更多操作" }));
    const item = await screen.findByRole("menuitem", { name: /删除任务/ });
    expect(item).toHaveAttribute("aria-disabled", "true");
    expect(within(item).getByText("任务正在运行或排队，请先取消")).toBeInTheDocument();
  });

  it("删除任务：显示任务名称与默认不勾的复选框，不勾选时不删除快照", async () => {
    respond(ok({ items: [ordersProd] }));
    renderPage();
    await userEvent.click(await screen.findByRole("button", { name: "orders-prod 全量备份 的更多操作" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "删除任务" }));

    const dialog = await screen.findByRole("dialog");
    expect(
      within(dialog).getByText((_, el) => el?.textContent === "删除任务“orders-prod 全量备份”？")
    ).toBeInTheDocument();
    const checkbox = within(dialog).getByRole("checkbox", { name: "同时删除该任务的 12 份快照" });
    expect(checkbox).not.toBeChecked();

    respond(ok({ snapshots_deleted: 0, snapshots_failed: 0, snapshots_message: "" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "删除" }));
    expect(call(1)).toMatchObject({ url: "/api/v1/jobs/1", method: "DELETE" });
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(screen.queryByText("orders-prod 全量备份")).not.toBeInTheDocument();
  });

  it("勾选后连同快照一起删除；部分失败时提示可用 kopia 命令行处理", async () => {
    respond(ok({ items: [ordersProd] }));
    renderPage();
    await userEvent.click(await screen.findByRole("button", { name: "orders-prod 全量备份 的更多操作" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "删除任务" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("checkbox", { name: "同时删除该任务的 12 份快照" }));

    respond(
      ok({
        snapshots_deleted: 10,
        snapshots_failed: 2,
        snapshots_message: "有 2 份快照未能删除，可以用 kopia 命令行手动处理",
      })
    );
    await userEvent.click(within(dialog).getByRole("button", { name: "删除" }));
    expect(call(1)).toMatchObject({ url: "/api/v1/jobs/1?delete_snapshots=true", method: "DELETE" });
    expect(await screen.findByText("有 2 份快照未能删除，可以用 kopia 命令行手动处理")).toBeInTheDocument();
  });

  it("存储无法打开、快照未能删除时，任务照常删除并提示原因", async () => {
    respond(ok({ items: [ordersProd] }));
    renderPage();
    await userEvent.click(await screen.findByRole("button", { name: "orders-prod 全量备份 的更多操作" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "删除任务" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("checkbox", { name: "同时删除该任务的 12 份快照" }));

    const message = "任务已删除，但无法打开存储，它的快照未能删除，可以用 kopia 命令行手动处理";
    respond(ok({ snapshots_deleted: 0, snapshots_failed: 0, snapshots_message: message }));
    await userEvent.click(within(dialog).getByRole("button", { name: "删除" }));
    expect(await screen.findByText(message)).toBeInTheDocument();
  });

  it("数字、时间与快照数用等宽字体", async () => {
    respond(ok({ items: [ordersProd] }));
    renderPage();
    const row = (await screen.findAllByRole("row"))[1];
    expect(within(row).getByText("12 份快照")).toHaveClass("font-mono");
    expect(within(row).getByText(/^下次 /)).toHaveClass("font-mono");
    expect(within(row).getByText(/耗时 4m12s/)).toHaveClass("font-mono");
  });

  it("英文界面按单复数显示数量", async () => {
    await i18n.changeLanguage("en");
    try {
      respond(ok({ items: [{ ...ordersDev, databases: ["orders"], snapshot_count: 1, last_run: successRun }] }));
      renderPage();
      const row = (await screen.findAllByRole("row"))[1];
      expect(within(row).getByText("1 snapshot")).toBeInTheDocument();
      expect(within(row).getByText("MySQL · 1 database · Enabled")).toBeInTheDocument();
    } finally {
      await i18n.changeLanguage("zh-CN");
    }
  });

  it("切换界面语言后重新读取列表：最近一次运行的失败原因按新语言显示", async () => {
    const failed: Run = { ...successRun, status: "failed", snapshot_id: "", failed_step: "connect" };
    // 服务端按请求的 Accept-Language 给出原因
    fetchMock.mockImplementation((url: string, init?: RequestInit) => {
      const lang = (init?.headers as Record<string, string> | undefined)?.["Accept-Language"];
      const reason = lang === "en" ? "Failed to connect to the data source: timeout" : "连接数据源失败: timeout";
      return Promise.resolve(
        url === "/api/v1/jobs"
          ? ok({ items: [{ ...ordersProd, last_run: { ...failed, reason } }] })
          : fail(404, url, 404)
      );
    });
    try {
      renderPage();
      expect(await screen.findByText(/连接数据源失败: timeout/)).toBeInTheDocument();
      await act(async () => {
        await i18n.changeLanguage("en");
      });
      expect(await screen.findByText(/Failed to connect to the data source: timeout/)).toBeInTheDocument();
      expect(screen.queryByText(/连接数据源失败/)).not.toBeInTheDocument();
    } finally {
      await act(async () => {
        await i18n.changeLanguage("zh-CN");
      });
    }
  });

  it("没有运行中的任务时也低频刷新：计划触发的运行开始后无需手动刷新", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      respond(ok({ items: [ordersProd] }));
      renderPage();
      await screen.findAllByRole("row");

      respond(ok({ items: [{ ...ordersProd, last_run: { ...runningRun, job_id: 1 } }] }));
      await act(async () => {
        await vi.advanceTimersByTimeAsync(30000);
      });
      expect(call(1)).toMatchObject({ url: "/api/v1/jobs", method: "GET" });
      expect(await screen.findByText("运行中")).toBeInTheDocument();
    } finally {
      vi.useRealTimers();
    }
  });

  it("运行中或排队中时自动刷新列表，状态落定后降为低频刷新", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      respond(ok({ items: [pgAnalytics] }));
      renderPage();
      await screen.findAllByRole("row");

      respond(ok({ items: [{ ...pgAnalytics, last_run: { ...runningRun, status: "success" } }] }));
      await act(async () => {
        await vi.advanceTimersByTimeAsync(3000);
      });
      expect(call(1)).toMatchObject({ url: "/api/v1/jobs", method: "GET" });
      expect(await screen.findByText("成功")).toBeInTheDocument();

      await act(async () => {
        await vi.advanceTimersByTimeAsync(6000);
      });
      expect(fetchMock.mock.calls.length).toBe(2);
    } finally {
      vi.useRealTimers();
    }
  });

  it("暂停先于在途的列表刷新返回：较早发出的刷新结果不能把任务改回已启用", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      respond(ok({ items: [ordersProd] }));
      renderPage();
      await screen.findAllByRole("row");

      let resolvePoll!: (r: Response) => void;
      fetchMock.mockReturnValueOnce(new Promise<Response>((r) => (resolvePoll = r)));
      await act(async () => {
        await vi.advanceTimersByTimeAsync(30000);
      });
      expect(call(1)).toMatchObject({ url: "/api/v1/jobs", method: "GET" });

      respond(ok({ item: { ...ordersProd, enabled: false, next_run_at: 0 } }));
      await userEvent.click(screen.getByRole("button", { name: "orders-prod 全量备份 的更多操作" }));
      await userEvent.click(await screen.findByRole("menuitem", { name: "暂停" }));
      expect(await screen.findByText("MySQL · 4 个库 · 已暂停")).toBeInTheDocument();

      await act(async () => {
        resolvePoll(ok({ items: [ordersProd] }));
        await vi.advanceTimersByTimeAsync(0);
      });
      expect(screen.getByText("MySQL · 4 个库 · 已暂停")).toBeInTheDocument();
    } finally {
      vi.useRealTimers();
    }
  });

  it("一行的请求未返回时，该行其他操作也不可用，防止重复提交", async () => {
    respond(ok({ items: [brandNew] }));
    renderPage();
    const row = (await screen.findAllByRole("row"))[1];

    fetchMock.mockReturnValueOnce(new Promise<Response>(() => {}));
    await userEvent.click(within(row).getByRole("button", { name: "立即执行 new-job 首次运行前" }));
    await userEvent.click(within(row).getByRole("button", { name: "new-job 首次运行前 的更多操作" }));
    expect(await screen.findByRole("menuitem", { name: "暂停" })).toHaveAttribute("aria-disabled", "true");
  });

  it("立即执行被拒绝（已在运行或排队）时立刻刷新任务状态，换成取消运行", async () => {
    respond(ok({ items: [brandNew] }));
    renderPage();
    const row = (await screen.findAllByRole("row"))[1];

    respond(
      fail(10728, "任务已在运行或排队"),
      ok({ items: [{ ...brandNew, last_run: { ...runningRun, job_id: 5 } }] })
    );
    await userEvent.click(within(row).getByRole("button", { name: "立即执行 new-job 首次运行前" }));
    expect(await screen.findByText("任务已在运行或排队")).toBeInTheDocument();
    expect(await within(row).findByRole("button", { name: "取消运行 new-job 首次运行前" })).toBeInTheDocument();
    expect(call(2)).toMatchObject({ url: "/api/v1/jobs", method: "GET" });
  });
});
