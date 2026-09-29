import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useParams } from "react-router";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

import i18n from "@/i18n";
import type { Run } from "@/lib/jobs";
import type { Overview, OverviewStorage, RecentRun } from "@/lib/overview";
import { OverviewPage } from "@/pages/OverviewPage";

const ok = (data: unknown) => new Response(JSON.stringify({ code: 0, msg: "success", data }), { status: 200 });
const fail = (code: number, msg: string, status = 500) => new Response(JSON.stringify({ code, msg }), { status });

// 固定在本地时间 2026-09-29 10:00，使“刚刚 / 当天 / 更早”“明天”等判断不随执行时刻变化
const NOW = new Date(2026, 8, 29, 10, 0, 0);
const now = () => Math.floor(NOW.getTime() / 1000);
const at = (month: number, day: number, hour: number, minute: number) =>
  Math.floor(new Date(2026, month - 1, day, hour, minute).getTime() / 1000);

const baseRun: Run = {
  id: 1,
  job_id: 1,
  status: "success",
  trigger: "schedule",
  retry_attempt: 0,
  retry_total: 0,
  scheduled_at: 0,
  created_at: at(9, 29, 2, 0),
  started_at: at(9, 29, 2, 0),
  finished_at: at(9, 29, 2, 4),
  duration_ms: 252000,
  exported_bytes: 12.4 * 1024 ** 3,
  uploaded_bytes: 1024 ** 3,
  snapshot_id: "k1",
  failed_step: "",
  reason: "",
};

const recent = (run: Partial<RecentRun>): RecentRun => ({
  ...baseRun,
  job_name: "orders-prod 全量备份",
  job_type: "backup",
  datasource_kind: "mysql",
  datasource_address: "mysql://db-01:3306",
  ...run,
});

const successRun = recent({ id: 11 });
const runningRun = recent({
  id: 12,
  job_id: 2,
  job_name: "analytics 全量备份",
  datasource_kind: "postgres",
  datasource_address: "postgres://pg-analytics-02:5432",
  status: "running",
  created_at: now() - 30,
  started_at: now() - 30,
  finished_at: 0,
  duration_ms: 185000,
  exported_bytes: 8.1 * 1024 ** 3,
});
const failedRun = recent({
  id: 13,
  job_id: 3,
  job_name: "billing 全量备份",
  datasource_address: "mysql://db-billing:3306",
  status: "failed",
  created_at: at(9, 28, 1, 30),
  started_at: at(9, 28, 1, 30),
  finished_at: at(9, 28, 1, 31),
  duration_ms: 12000,
  exported_bytes: 1024,
  reason: "连接数据源失败",
});
const queuedRun = recent({
  id: 14,
  job_id: 4,
  job_name: "crm 全量备份",
  status: "queued",
  created_at: now() - 5,
  started_at: 0,
  finished_at: 0,
  duration_ms: 0,
  exported_bytes: 0,
});
const skippedRun = recent({
  id: 15,
  status: "skipped",
  created_at: at(9, 29, 1, 0),
  started_at: 0,
  finished_at: 0,
  duration_ms: 0,
  exported_bytes: 0,
});
const canceledRun = recent({ id: 16, job_name: "legacy 全量备份", status: "canceled", duration_ms: 45000 });

const localStorageRow: OverviewStorage = {
  id: 1,
  name: "nas-local",
  kind: "local",
  location: "/mnt/backup",
  path: "/mnt/backup",
  status: "ok",
  readable: true,
  reason: "",
  packed_bytes: 142 * 1024 ** 3,
  original_bytes: 500 * 1024 ** 3,
  snapshots: 38,
  usage_recorded_at: now() - 60,
  disk: { used_bytes: 1.2 * 1024 ** 4, total_bytes: 2 * 1024 ** 4, free_bytes: 800 * 1024 ** 3 },
};
const s3Row: OverviewStorage = {
  ...localStorageRow,
  id: 2,
  name: "minio-dr",
  kind: "s3",
  location: "minio.lan:9000/opsnap-dr/prod",
  path: "",
  packed_bytes: 44 * 1024 ** 3,
  snapshots: 12,
  disk: null,
};
const brokenRow: OverviewStorage = {
  ...s3Row,
  id: 3,
  name: "oss-hz",
  location: "oss.example.com/oss-hz",
  status: "unreachable",
  readable: false,
  reason: "无法连接：目标位置不是 kopia 仓库",
  packed_bytes: 0,
  original_bytes: 0,
  snapshots: 0,
};

/** 最近 14 天，从 09-16 到今天 09-29 */
const days = (counts: [number, number][] = []) =>
  Array.from({ length: 14 }, (_, i) => ({
    date: `2026-09-${String(16 + i).padStart(2, "0")}`,
    success: counts[i]?.[0] ?? 0,
    failed: counts[i]?.[1] ?? 0,
  }));

const full: Overview = {
  protected: {
    count: 5,
    by_kind: [
      { kind: "mysql", count: 3 },
      { kind: "postgres", count: 2 },
    ],
  },
  success_24h: { runs: 25, success: 24, failed: 1, success_rate: 0.96 },
  next_run: { at: now() + 18 * 60, job_id: 1, job_name: "orders-prod 全量备份" },
  recent: {
    items: [runningRun, queuedRun, successRun, skippedRun, canceledRun, failedRun],
    failed: [failedRun],
    failed_24h: 1,
  },
  timezone: "Asia/Shanghai",
  daily: days([[3, 0], [4, 1], ...Array.from({ length: 11 }, (): [number, number] => [2, 0]), [5, 2]]),
  counts: { datasources: 5, storages: 3, jobs: 6 },
  storage_usage: { packed_bytes: 186 * 1024 ** 3, original_bytes: 641 * 1024 ** 3, savings: 0.71, unreadable: 0 },
  storages: [localStorageRow, s3Row],
};

let fetchMock: ReturnType<typeof vi.fn>;
function respond(...responses: Response[]) {
  for (const r of responses) fetchMock.mockResolvedValueOnce(r);
}
function url(i: number) {
  return (fetchMock.mock.calls[i] as [string])[0];
}

let hidden = false;
function setHidden(value: boolean) {
  hidden = value;
  document.dispatchEvent(new Event("visibilitychange"));
}

beforeAll(() => {
  i18n.changeLanguage("zh-CN");
  Object.defineProperty(document, "hidden", { configurable: true, get: () => hidden });
});
beforeEach(() => {
  vi.useFakeTimers({ shouldAdvanceTime: true, now: NOW });
  hidden = false;
  fetchMock = vi.fn();
  vi.stubGlobal("fetch", fetchMock);
});
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

function JobProbe() {
  return <p>job-page-{useParams().id}</p>;
}

function renderPage() {
  render(
    <MemoryRouter>
      <Routes>
        <Route path="/" element={<OverviewPage />} />
        <Route path="/jobs/:id" element={<JobProbe />} />
      </Routes>
    </MemoryRouter>
  );
}

const stat = (name: string) => screen.getByRole("group", { name });
const recentRegion = () => screen.getByRole("region", { name: "最近运行" });
const storageRegion = () => screen.getByRole("region", { name: "存储目标" });
const chartRegion = () => screen.getByRole("region", { name: "14 天运行" });
const bodyRows = () => within(recentRegion()).getAllByRole("row").slice(1);

async function tick(ms: number) {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
}

describe("概览页 · 加载", () => {
  it("按浏览器时区请求概览；返回前统计、最近运行与柱状图显示占位而不是空白", async () => {
    fetchMock.mockReturnValueOnce(new Promise(() => {}));
    renderPage();

    const tz = Intl.DateTimeFormat().resolvedOptions().timeZone;
    expect(url(0)).toBe(`/api/v1/overview?tz=${encodeURIComponent(tz)}`);
    expect(stat("受保护数据源")).toHaveAttribute("aria-busy", "true");
    expect(stat("24h 成功率")).toHaveAttribute("aria-busy", "true");
    expect(recentRegion()).toHaveAttribute("aria-busy", "true");
    expect(chartRegion()).toHaveAttribute("aria-busy", "true");
    expect(storageRegion()).toHaveAttribute("aria-busy", "true");
    expect(screen.queryByText("还没有运行记录")).not.toBeInTheDocument();
    expect(screen.queryByText("还没有存储")).not.toBeInTheDocument();
  });
});

describe("概览页 · 四个统计", () => {
  it("有数据时显示数量、成功率、存储占用与下一次运行", async () => {
    respond(ok(full));
    renderPage();

    await screen.findByText("MySQL 3 · PostgreSQL 2");
    expect(stat("受保护数据源")).toHaveTextContent("5");
    expect(stat("24h 成功率")).toHaveTextContent("96.0%");
    expect(stat("24h 成功率")).toHaveTextContent("25 次运行 · 1 次失败");
    expect(stat("存储占用")).toHaveTextContent("186.0 GB");
    expect(stat("存储占用")).toHaveTextContent("去重压缩后 · 节省 71%");
    expect(stat("下一次运行")).toHaveTextContent("10:18");
    expect(stat("下一次运行")).toHaveTextContent("orders-prod 全量备份 · 18 分钟后");
  });

  it("没有可计入的数据时按空值说明", async () => {
    respond(
      ok({
        ...full,
        protected: { count: 0, by_kind: [] },
        success_24h: { runs: 0, success: 0, failed: 0, success_rate: 0 },
        next_run: null,
        storage_usage: { packed_bytes: 0, original_bytes: 0, savings: 0, unreadable: 0 },
        storages: [],
      })
    );
    renderPage();

    expect(await screen.findByText("还没有被任务备份的数据源")).toBeInTheDocument();
    expect(stat("受保护数据源")).toHaveTextContent("0");
    expect(stat("24h 成功率")).toHaveTextContent("—");
    expect(stat("24h 成功率")).toHaveTextContent("最近 24 小时没有运行");
    expect(stat("存储占用")).toHaveTextContent("0 B");
    expect(stat("存储占用")).toHaveTextContent("还没有存储");
    expect(stat("下一次运行")).toHaveTextContent("—");
    expect(stat("下一次运行")).toHaveTextContent("还没有启用的任务");
  });

  it("有存储读不到用量时说明改为无法读取的个数", async () => {
    respond(
      ok({
        ...full,
        storage_usage: { ...full.storage_usage, unreadable: 1 },
        storages: [localStorageRow, s3Row, brokenRow],
      })
    );
    renderPage();

    await screen.findByText("MySQL 3 · PostgreSQL 2");
    expect(stat("存储占用")).toHaveTextContent("有 1 个存储无法读取");
    expect(stat("存储占用")).not.toHaveTextContent("节省");
  });

  it("下一次运行不是今天时带上日期：明天；当天超过一小时按小时", async () => {
    respond(ok({ ...full, next_run: { at: at(9, 30, 2, 0), job_id: 1, job_name: "orders-prod 全量备份" } }));
    renderPage();
    await screen.findByText("MySQL 3 · PostgreSQL 2");
    expect(stat("下一次运行")).toHaveTextContent("09-30 02:00");
    expect(stat("下一次运行")).toHaveTextContent("orders-prod 全量备份 · 明天");
  });

  it("下一次运行在今天稍后：显示时:分与 N 小时后", async () => {
    respond(ok({ ...full, next_run: { at: at(9, 29, 15, 30), job_id: 1, job_name: "orders-prod 全量备份" } }));
    renderPage();
    await screen.findByText("MySQL 3 · PostgreSQL 2");
    expect(stat("下一次运行")).toHaveTextContent("15:30");
    expect(stat("下一次运行")).not.toHaveTextContent("09-29");
    expect(stat("下一次运行")).toHaveTextContent("orders-prod 全量备份 · 5 小时后");
  });
});

describe("概览页 · 最近运行", () => {
  it("每行显示引擎缩写、任务与地址、类型、状态、耗时、大小与时间", async () => {
    respond(ok(full));
    renderPage();
    await screen.findByText("MySQL 3 · PostgreSQL 2");

    const [running, queued, success, skipped, canceled, failed] = bodyRows();
    expect(running).toHaveTextContent("PG");
    expect(running).toHaveTextContent("analytics 全量备份");
    expect(running).toHaveTextContent("postgres://pg-analytics-02:5432");
    expect(running).toHaveTextContent("备份");
    expect(running).toHaveTextContent("运行中");
    // 运行中：已运行时长与已导出量
    expect(running).toHaveTextContent("3m05s");
    expect(running).toHaveTextContent("8.1 GB");
    expect(running).toHaveTextContent("刚刚");

    expect(queued).toHaveTextContent("等待中");
    expect(within(queued).getAllByText("—")).toHaveLength(2);
    expect(queued).toHaveTextContent("刚刚");

    expect(success).toHaveTextContent("MY");
    expect(success).toHaveTextContent("成功");
    expect(success).toHaveTextContent("4m12s");
    expect(success).toHaveTextContent("12.4 GB");
    expect(success).toHaveTextContent("02:00");
    expect(success).not.toHaveTextContent("09-29");

    expect(skipped).toHaveTextContent("已跳过");
    expect(within(skipped).getAllByText("—")).toHaveLength(2);
    expect(canceled).toHaveTextContent("已取消");
    expect(canceled).toHaveTextContent("45s");
    expect(within(canceled).getAllByText("—")).toHaveLength(1);

    // 失败：有耗时，大小为“—”；不是今天的带日期
    expect(failed).toHaveTextContent("失败");
    expect(failed).toHaveTextContent("12s");
    expect(within(failed).getAllByText("—")).toHaveLength(1);
    expect(failed).toHaveTextContent("09-28 01:30");
  });

  it("点击一行进入该任务的详情页", async () => {
    respond(ok(full));
    renderPage();
    await screen.findByText("MySQL 3 · PostgreSQL 2");

    await userEvent.click(within(bodyRows()[5]).getByText("mysql://db-billing:3306"));
    expect(await screen.findByText("job-page-3")).toBeInTheDocument();
  });

  it("“失败 N”按最近 24 小时计数，选中后只显示失败的运行；备份筛选显示备份运行", async () => {
    respond(ok({ ...full, recent: { ...full.recent, failed_24h: 3 } }));
    renderPage();
    await screen.findByText("MySQL 3 · PostgreSQL 2");
    const filters = within(recentRegion()).getByRole("group", { name: "筛选最近运行" });

    await userEvent.click(within(filters).getByRole("button", { name: "失败 3" }));
    expect(within(filters).getByRole("button", { name: "失败 3" })).toHaveAttribute("aria-pressed", "true");
    expect(bodyRows()).toHaveLength(1);
    expect(bodyRows()[0]).toHaveTextContent("billing 全量备份");

    await userEvent.click(within(filters).getByRole("button", { name: "备份" }));
    expect(bodyRows()).toHaveLength(6);

    await userEvent.click(within(filters).getByRole("button", { name: "全部" }));
    expect(bodyRows()).toHaveLength(6);
  });

  it("“同步”筛选禁用：可聚焦并读出“后续版本支持”，点击不改变筛选", async () => {
    respond(ok(full));
    renderPage();
    await screen.findByText("MySQL 3 · PostgreSQL 2");
    const filters = within(recentRegion()).getByRole("group", { name: "筛选最近运行" });
    const sync = within(filters).getByRole("button", { name: "同步" });

    expect(sync).toHaveAttribute("aria-disabled", "true");
    expect(sync).toHaveAccessibleDescription("后续版本支持");
    sync.focus();
    expect(sync).toHaveFocus();
    await userEvent.click(sync);
    expect(sync).toHaveAttribute("aria-pressed", "false");
    expect(within(filters).getByRole("button", { name: "全部" })).toHaveAttribute("aria-pressed", "true");
    expect(bodyRows()).toHaveLength(6);
  });

  it("有任务但还没有运行时显示“还没有运行记录”", async () => {
    respond(ok({ ...full, recent: { items: [], failed: [], failed_24h: 0 } }));
    renderPage();
    expect(
      await within(await screen.findByRole("region", { name: "最近运行" })).findByText("还没有运行记录")
    ).toBeVisible();
  });
});

describe("概览页 · 14 天运行", () => {
  it("每天一根柱，悬停显示日期、成功与失败次数；横轴标注第一天与今天", async () => {
    respond(ok(full));
    renderPage();
    await screen.findByText("MySQL 3 · PostgreSQL 2");

    const bars = within(chartRegion()).getAllByRole("img");
    expect(bars).toHaveLength(14);
    expect(chartRegion()).toHaveTextContent("09/16");
    expect(chartRegion()).toHaveTextContent("今天");

    await userEvent.hover(bars[1]);
    const tip = await screen.findByRole("tooltip");
    expect(tip).toHaveTextContent("2026-09-17");
    expect(tip).toHaveTextContent("成功 4");
    expect(tip).toHaveTextContent("失败 1");
    expect(screen.queryByText("最近 14 天没有运行")).not.toBeInTheDocument();
  });

  it("14 天都没有运行时注明", async () => {
    respond(ok({ ...full, daily: days() }));
    renderPage();
    expect(
      await within(await screen.findByRole("region", { name: "14 天运行" })).findByText("最近 14 天没有运行")
    ).toBeVisible();
  });
});

describe("概览页 · 存储目标", () => {
  it("本地目录显示磁盘进度条、路径与剩余；S3 显示位置不画进度条；读不到的显示“—”与原因", async () => {
    respond(ok({ ...full, storages: [localStorageRow, s3Row, brokenRow] }));
    renderPage();
    await screen.findByText("MySQL 3 · PostgreSQL 2");

    const [local, s3, broken] = within(storageRegion()).getAllByRole("listitem");
    expect(local).toHaveTextContent("nas-local");
    expect(local).toHaveTextContent("本地目录");
    expect(local).toHaveTextContent("142.0 GB · 38 份快照");
    expect(local).toHaveTextContent("/mnt/backup");
    expect(local).toHaveTextContent("剩余 800.0 GB");
    expect(within(local).getByRole("progressbar")).toHaveAttribute("aria-valuenow", "60");

    expect(s3).toHaveTextContent("minio-dr");
    expect(s3).toHaveTextContent("44.0 GB · 12 份快照");
    expect(s3).toHaveTextContent("minio.lan:9000/opsnap-dr/prod");
    expect(within(s3).queryByRole("progressbar")).not.toBeInTheDocument();

    expect(broken).toHaveTextContent("oss-hz");
    expect(broken).toHaveTextContent("—");
    expect(broken).not.toHaveTextContent("份快照");
    expect(within(broken).getByText("无法连接：目标位置不是 kopia 仓库")).toHaveClass("text-destructive");
  });

  it("没有存储时显示“还没有存储”与添加存储链接", async () => {
    respond(ok({ ...full, storages: [] }));
    renderPage();
    await screen.findByText("MySQL 3 · PostgreSQL 2");
    expect(within(storageRegion()).getByText("还没有存储")).toBeInTheDocument();
    expect(within(storageRegion()).getByRole("link", { name: "添加存储" })).toHaveAttribute("href", "/storage");
  });
});

describe("概览页 · 页头", () => {
  it("搜索框与“恢复数据”禁用但可聚焦，读出“后续版本支持”；新建任务进入向导", async () => {
    respond(ok(full));
    renderPage();
    await screen.findByText("MySQL 3 · PostgreSQL 2");

    const search = screen.getByRole("textbox", { name: "搜索 · 后续版本支持" });
    expect(search).toHaveAttribute("aria-disabled", "true");
    expect(search).toHaveAttribute("readonly");
    const restore = screen.getByRole("button", { name: "恢复数据" });
    expect(restore).toHaveAttribute("aria-disabled", "true");
    expect(restore).toHaveAccessibleDescription("后续版本支持");

    await userEvent.tab();
    expect(search).toHaveFocus();
    await userEvent.tab();
    expect(restore).toHaveFocus();
    expect(await screen.findByRole("tooltip")).toHaveTextContent("后续版本支持");

    expect(screen.getByRole("link", { name: "新建任务" })).toHaveAttribute("href", "/jobs/new");
  });
});

describe("概览页 · 失败与刷新", () => {
  it("首次加载失败显示错误原因与重试，不出现空白页；重试成功后显示概览", async () => {
    respond(fail(-1, "服务器开小差了"));
    renderPage();

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("概览加载失败");
    expect(alert).toHaveTextContent("服务器开小差了");
    respond(ok(full));
    await userEvent.click(within(alert).getByRole("button", { name: "重试" }));
    expect(await screen.findByText("MySQL 3 · PostgreSQL 2")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("每 30 秒刷新；刷新失败时保留数据并在上方提示，之后照常刷新并恢复", async () => {
    respond(ok(full));
    renderPage();
    await screen.findByText("MySQL 3 · PostgreSQL 2");

    respond(fail(-1, "网络中断"));
    await tick(30000);
    expect(fetchMock).toHaveBeenCalledTimes(2);
    const banner = await screen.findByRole("alert");
    expect(banner).toHaveTextContent("网络中断");
    expect(within(banner).getByRole("button", { name: "重试" })).toBeInTheDocument();
    expect(stat("24h 成功率")).toHaveTextContent("96.0%");

    respond(ok({ ...full, success_24h: { runs: 26, success: 25, failed: 1, success_rate: 25 / 26 } }));
    await tick(30000);
    expect(await screen.findByText("26 次运行 · 1 次失败")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("刷新失败的提示里可以立即重试", async () => {
    respond(ok(full));
    renderPage();
    await screen.findByText("MySQL 3 · PostgreSQL 2");
    respond(fail(-1, "网络中断"));
    await tick(30000);
    const banner = await screen.findByRole("alert");

    respond(ok({ ...full, success_24h: { runs: 26, success: 25, failed: 1, success_rate: 25 / 26 } }));
    await userEvent.click(within(banner).getByRole("button", { name: "重试" }));
    expect(await screen.findByText("26 次运行 · 1 次失败")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("标签页隐藏时暂停刷新，回到页面时立即刷新一次", async () => {
    respond(ok(full));
    renderPage();
    await screen.findByText("MySQL 3 · PostgreSQL 2");

    act(() => setHidden(true));
    await tick(95000);
    expect(fetchMock).toHaveBeenCalledTimes(1);

    respond(ok({ ...full, success_24h: { runs: 26, success: 25, failed: 1, success_rate: 25 / 26 } }));
    act(() => setHidden(false));
    expect(await screen.findByText("26 次运行 · 1 次失败")).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledTimes(2);

    respond(ok(full));
    await tick(30000);
    expect(fetchMock).toHaveBeenCalledTimes(3);
  });

  it("切换语言后按新语言重新读取，之前发出的请求结果被丢弃", async () => {
    respond(ok({ ...full, storages: [brokenRow] }));
    renderPage();
    await screen.findByText("无法连接：目标位置不是 kopia 仓库");

    let resolveStale!: (r: Response) => void;
    fetchMock.mockReturnValueOnce(new Promise<Response>((r) => (resolveStale = r)));
    await tick(30000);
    respond(ok({ ...full, storages: [{ ...brokenRow, reason: "Unreachable: not a kopia repository" }] }));
    try {
      await act(async () => {
        await i18n.changeLanguage("en");
      });
      expect(await screen.findByText("Unreachable: not a kopia repository")).toBeInTheDocument();
      expect(screen.getByRole("region", { name: "Storage targets" })).toBeInTheDocument();
      await act(async () => {
        resolveStale(ok({ ...full, storages: [brokenRow] }));
        await vi.advanceTimersByTimeAsync(0);
      });
      expect(screen.getByText("Unreachable: not a kopia repository")).toBeInTheDocument();
    } finally {
      await act(async () => {
        await i18n.changeLanguage("zh-CN");
      });
    }
  });
});
