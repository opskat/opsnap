import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

import i18n from "@/i18n";
import type { JobItem, JobStats, Run } from "@/lib/jobs";
import type { Storage } from "@/lib/storage";
import { JobDetailPage } from "@/pages/JobDetailPage";

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

const failedRun: Run = {
  id: 100,
  job_id: 1,
  status: "failed",
  trigger: "retry",
  retry_attempt: 1,
  retry_total: 2,
  scheduled_at: 0,
  created_at: now() - 90000,
  started_at: now() - 90000,
  finished_at: now() - 89900,
  duration_ms: 45000,
  exported_bytes: 10 * 1024 * 1024,
  uploaded_bytes: 0,
  snapshot_id: "",
  failed_step: "verify",
  reason: "坏归档头",
};

const runningRun: Run = {
  id: 202,
  job_id: 1,
  status: "running",
  trigger: "manual",
  retry_attempt: 0,
  retry_total: 0,
  scheduled_at: 0,
  created_at: now() - 60,
  started_at: now() - 60,
  finished_at: 0,
  duration_ms: 60000,
  exported_bytes: 1024 ** 2,
  uploaded_bytes: 0,
  snapshot_id: "",
  failed_step: "",
  reason: "",
};

const queuedRun: Run = { ...runningRun, id: 303, status: "queued", started_at: 0, duration_ms: 0 };

const baseJob: JobItem = {
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
  databases: ["orders", "payments"],
  method: "full",
  options: { routines: true, triggers: true, events: false, users: false, globals: false },
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

const storageItem: Storage = {
  id: 20,
  name: "阿里云 OSS",
  kind: "s3",
  path: "",
  endpoint: "oss-cn.example.com",
  region: "cn-hangzhou",
  bucket: "opsnap",
  prefix: "",
  access_key: "AK",
  has_secret_key: true,
  use_tls: true,
  skip_verify: false,
  location: "s3://opsnap/",
  fingerprint: "AB:CD:EF:00",
  encryption: "AES256-GCM-HMAC-SHA256",
  status: "ok",
  status_message: "",
  checked_at: now(),
  created_at: now() - 1000000,
};

const stats: JobStats = {
  snapshot_count: 12,
  earliest_snapshot_at: now() - 30 * 86400,
  packed_bytes: 12 * 1024 * 1024,
  export_bytes: 40 * 1024 * 1024,
  savings: 0.7,
  storage_error: "",
  last_success: successRun,
  recent: { runs: 20, success: 18, failed: 2, success_rate: 0.9 },
};

const previewResponse = {
  next_runs: ["2026-09-29T02:00:00+08:00", "2026-09-30T02:00:00+08:00", "2026-10-01T02:00:00+08:00"],
  max_snapshots: 18,
};

const runsPage1 = { items: [successRun, failedRun], total: 45 };

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
});
beforeEach(() => {
  fetchMock = vi.fn();
  vi.stubGlobal("fetch", fetchMock);
});
afterEach(() => vi.unstubAllGlobals());

/** 依次响应任务详情页首次加载的五个请求：任务、统计、存储列表、计划预览、运行记录第一页 */
function respondInitialLoad(job: JobItem = baseJob, s: JobStats = stats) {
  respond(ok({ item: job }), ok(s), ok({ items: [storageItem] }), ok(previewResponse), ok(runsPage1));
}

function renderPage(id = "1") {
  render(
    <MemoryRouter initialEntries={[`/jobs/${id}`]}>
      <Routes>
        <Route path="/jobs/:id" element={<JobDetailPage />} />
      </Routes>
    </MemoryRouter>
  );
}

describe("任务详情页", () => {
  it("加载后依次请求任务、统计、存储、计划预览与运行记录", async () => {
    respondInitialLoad();
    renderPage();

    expect(await screen.findByText("orders-prod 全量备份")).toBeInTheDocument();
    expect(call(0)).toMatchObject({ url: "/api/v1/jobs/1", method: "GET" });
    expect(call(1)).toMatchObject({ url: "/api/v1/jobs/1/stats", method: "GET" });
    expect(call(2)).toMatchObject({ url: "/api/v1/storages", method: "GET" });
    expect(call(3)).toMatchObject({
      url: "/api/v1/jobs/schedule-preview",
      method: "POST",
      body: { schedule: baseJob.schedule, retention: baseJob.retention },
    });
    expect(call(4)).toMatchObject({ url: "/api/v1/jobs/1/runs?page=1", method: "GET" });
  });

  it("头部：名称、启用状态、数据源→存储、计划与下次执行、返回入口", async () => {
    respondInitialLoad();
    renderPage();

    expect(await screen.findByText("orders-prod 全量备份")).toBeInTheDocument();
    const header = screen.getByRole("banner");
    expect(within(header).getByText("已启用")).toBeInTheDocument();
    expect(within(header).getByText(/db-01 · orders → 阿里云 OSS/)).toBeInTheDocument();
    expect(within(header).getByText(/每天 02:00/)).toBeInTheDocument();
    expect(within(header).getByText(/下次/)).toBeInTheDocument();
    expect(within(header).getByRole("link", { name: /返回「任务」/ })).toHaveAttribute("href", "/jobs");
    expect(within(header).getByRole("link", { name: "编辑" })).toHaveAttribute("href", "/jobs/1/edit");
  });

  it("配置卡片：数据源、范围、一并备份、方式、存储、仓库内位置、压缩、加密指纹、计划、保留、失败处理", async () => {
    respondInitialLoad();
    renderPage();

    const region = await screen.findByRole("region", { name: "配置" });
    // 存储列表与计划预览是各自独立的异步请求，等它们落地后再断言其余内容
    await within(region).findByText("已加密");
    await within(region).findByText("2026-09-29 02:00");
    expect(within(region).getByText("db-01 · orders（MySQL）")).toBeInTheDocument();
    expect(within(region).getByText("指定数据库（2 个）")).toBeInTheDocument();
    expect(within(region).getByText("orders、payments")).toBeInTheDocument();
    expect(within(region).getByText("存储过程与函数、触发器")).toBeInTheDocument();
    expect(within(region).getByText("仅全量")).toBeInTheDocument();
    expect(within(region).getByText("阿里云 OSS:/prod/mysql/db-01")).toBeInTheDocument();
    expect(within(region).getByText("zstd")).toBeInTheDocument();
    expect(within(region).getByText("已加密")).toBeInTheDocument();
    expect(within(region).getByText("仓库密钥指纹 AB:CD:EF:00")).toBeInTheDocument();
    expect(within(region).getByText("2026-09-29 02:00")).toBeInTheDocument();
    expect(within(region).getByText("近 7 天全部 · 之后每周 4 份 · 每月 6 份")).toBeInTheDocument();
    expect(within(region).getByText("重试 2 次 · 间隔 5 分钟 · 超时 120 分钟")).toBeInTheDocument();
  });

  it("统计卡片：快照数与最早时间、仓库占用与节省、最近成功、近 30 次成功率", async () => {
    respondInitialLoad();
    renderPage();

    const region = await screen.findByRole("region", { name: "统计" });
    // 统计是独立的异步请求，等它落地后再断言其余内容
    await within(region).findByText(/12 份快照/);
    expect(within(region).getByText(/12 份快照/)).toBeInTheDocument();
    expect(within(region).getByText(/最早/)).toBeInTheDocument();
    expect(within(region).getByText(/12\.0 MB/)).toBeInTheDocument();
    expect(within(region).getByText(/导出总量 40\.0 MB/)).toBeInTheDocument();
    expect(within(region).getByText(/节省 70%/)).toBeInTheDocument();
    expect(within(region).getByText("最近一次成功")).toBeInTheDocument();
    expect(within(region).getByText(/新增 186\.0 MB/)).toBeInTheDocument();
    expect(within(region).getByText(/18 成功 · 2 失败 · 成功率 90%/)).toBeInTheDocument();
  });

  it("统计接口报告仓库不可读时显示非阻塞提示", async () => {
    respond(
      ok({ item: baseJob }),
      ok({
        ...stats,
        snapshot_count: 0,
        earliest_snapshot_at: 0,
        packed_bytes: 0,
        storage_error: "仓库不可读：连接超时",
      }),
      ok({ items: [storageItem] }),
      ok(previewResponse),
      ok(runsPage1)
    );
    renderPage();

    expect(await screen.findByText("仓库不可读：连接超时")).toBeInTheDocument();
    expect(screen.getByText("还没有快照")).toBeInTheDocument();
  });

  it("运行记录表：列与行内容，分页信息", async () => {
    respondInitialLoad();
    renderPage();

    const rows = await screen.findAllByRole("row");
    expect(within(rows[0]).getByRole("columnheader", { name: "状态" })).toBeInTheDocument();
    const successRow = rows.find((r) => within(r).queryByText("成功"));
    expect(successRow).toBeDefined();
    expect(within(successRow!).getByText(/耗时 4m12s|4m12s/)).toBeTruthy();
    expect(within(successRow!).getByText("200.0 MB / 186.0 MB")).toBeInTheDocument();
    expect(within(successRow!).getByText("k1a2b3")).toBeInTheDocument();

    expect(screen.getByText("第 1 / 3 页")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "上一页" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "下一页" })).not.toBeDisabled();
  });

  it("翻页请求下一页运行记录", async () => {
    respondInitialLoad();
    renderPage();
    await screen.findAllByRole("row");

    respond(ok({ items: [], total: 45 }));
    await userEvent.click(screen.getByRole("button", { name: "下一页" }));
    expect(call(5)).toMatchObject({ url: "/api/v1/jobs/1/runs?page=2", method: "GET" });
  });

  it("点击失败的运行行：展开失败步骤与原因，懒加载执行日志，含省略标记", async () => {
    respondInitialLoad();
    renderPage();
    const rows = await screen.findAllByRole("row");
    const failedRow = rows.find((r) => within(r).queryByText("失败"))!;

    respond(
      ok({
        lines: [
          { time: Date.UTC(2026, 8, 28, 1, 30, 0), step: "connect", message: "建立链路" },
          { time: Date.UTC(2026, 8, 28, 1, 30, 1), step: "", message: "", omitted: 5 },
        ],
      })
    );
    await userEvent.click(failedRow);

    expect(await screen.findByText("失败在「校验」：坏归档头")).toBeInTheDocument();
    expect(call(5)).toMatchObject({ url: "/api/v1/jobs/1/runs/100/log", method: "GET" });
    expect(await screen.findByText(/建立链路/)).toBeInTheDocument();
    expect(screen.getByText("省略了 5 行")).toBeInTheDocument();
  });

  it("立即执行：请求 POST /jobs/:id/run，运行中变为取消运行", async () => {
    respondInitialLoad({ ...baseJob, last_run: undefined });
    renderPage();
    await screen.findByText("orders-prod 全量备份");

    const runButton = screen.getByRole("button", { name: "立即执行" });
    respond(ok({ run: runningRun }));
    await userEvent.click(runButton);
    expect(call(5)).toMatchObject({ url: "/api/v1/jobs/1/run", method: "POST" });
    expect(await screen.findByRole("button", { name: "取消运行" })).toBeInTheDocument();
  });

  it("排队中时立即执行按钮不可用", async () => {
    respondInitialLoad({ ...baseJob, last_run: queuedRun });
    renderPage();

    const runButton = await screen.findByRole("button", { name: "立即执行" });
    expect(runButton).toBeDisabled();
  });

  it("运行中：立即执行换成取消运行，点击后请求取消接口并更新状态", async () => {
    respondInitialLoad({ ...baseJob, last_run: runningRun });
    renderPage();
    const cancelButton = await screen.findByRole("button", { name: "取消运行" });

    respond(
      ok({ run: { ...runningRun, status: "canceled", reason: "用户取消" } }),
      ok({ items: [{ ...runningRun, status: "canceled", reason: "用户取消" }], total: 1 })
    );
    await userEvent.click(cancelButton);
    expect(call(5)).toMatchObject({ url: "/api/v1/jobs/1/runs/202/cancel", method: "POST" });
    expect(await screen.findByRole("button", { name: "立即执行" })).toBeInTheDocument();
    expect(call(6)).toMatchObject({ url: "/api/v1/jobs/1/runs?page=1", method: "GET" });
  });

  it("暂停与启用", async () => {
    respondInitialLoad();
    renderPage();
    await screen.findByText("orders-prod 全量备份");

    respond(ok({ item: { ...baseJob, enabled: false, next_run_at: 0 } }));
    await userEvent.click(screen.getByRole("button", { name: "暂停" }));
    expect(call(5)).toMatchObject({ url: "/api/v1/jobs/1/pause", method: "POST" });
    expect(await screen.findByText("已暂停")).toBeInTheDocument();
  });

  it("运行中或排队中时自动刷新，状态落定后停止轮询", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      respondInitialLoad({ ...baseJob, last_run: runningRun });
      renderPage();
      await screen.findByRole("button", { name: "取消运行" });

      respond(ok({ item: { ...baseJob, last_run: { ...runningRun, status: "success" } } }), ok(runsPage1));
      await act(async () => {
        await vi.advanceTimersByTimeAsync(3000);
      });
      expect(await screen.findByRole("button", { name: "立即执行" })).toBeInTheDocument();
      const callsAfterFirstTick = fetchMock.mock.calls.length;

      await act(async () => {
        await vi.advanceTimersByTimeAsync(6000);
      });
      expect(fetchMock.mock.calls.length).toBe(callsAfterFirstTick);
    } finally {
      vi.useRealTimers();
    }
  });

  it("任务不存在：显示提示与返回列表入口", async () => {
    respond(fail(10700, "任务不存在", 404));
    renderPage("999");

    expect(await screen.findByText("任务不存在")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "返回列表" })).toHaveAttribute("href", "/jobs");
  });

  it("加载失败时显示错误与重试，不出现空白页", async () => {
    respond(fail(-1, "服务器开小差了"));
    renderPage();

    expect(await screen.findByText("无法加载任务：服务器开小差了")).toBeInTheDocument();
    respondInitialLoad();
    await userEvent.click(screen.getByRole("button", { name: "重试" }));
    expect(await screen.findByText("orders-prod 全量备份")).toBeInTheDocument();
  });
});
