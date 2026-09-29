import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { expect, test, type APIRequestContext, type Page } from "@playwright/test";

import { FAKE_PG_FAILING_DATABASE, FAKE_PG_PASSWORD, FAKE_PG_PORT, FAKE_PG_USER, SMOKE_PORT } from "../ports";

// 概览（docs/specs/2026-09-29-overview-docker.md「测试决策」的 Playwright 一行）：与 jobs.spec.ts 一样用
// bin/fakepg 作 PostgreSQL 数据源与假 pg_dump，本地目录作存储。数据源、存储与任务经接口准备（界面流程由
// jobs.spec.ts 覆盖），立即执行在任务详情页上点；运行前后各读一次概览，比较统计、最近运行、柱状图与存储行。
// 最后一个用例删除这里建的一切并确认概览回到运行前，afterAll 再兜底清理，不影响从空状态开始的 storage.spec.ts。
test.describe.configure({ mode: "serial" });

const DATASOURCE_NAME = "e2e 概览 PG";
const STORAGE_NAME = "e2e 概览存储";
const JOB_OK = "e2e 概览 成功";
const JOB_FAIL = "e2e 概览 失败";
const ORIGIN = `http://127.0.0.1:${SMOKE_PORT}`;

let base: string;
let dsId: number;
let storageId: number;
let okId: number;
let failId: number;

/** 运行前从概览读到的数字 */
let before: { runs: number; failed: number; failed24h: number; protected: number; today: [number, number] };

async function call(api: APIRequestContext, method: "GET" | "POST" | "DELETE", path: string, data?: unknown) {
  const res = await api.fetch(`/api/v1${path}`, { method, data });
  expect(res.ok(), `${method} ${path}`).toBeTruthy();
  const body = await res.json();
  expect(body.code, `${method} ${path}: ${body.msg}`).toBe(0);
  return body.data;
}

function jobBody(name: string, database: string, prefix: string) {
  return {
    name,
    type: "backup",
    datasource_id: dsId,
    storage_id: storageId,
    prefix,
    scope: "databases",
    databases: [database],
    method: "full",
    options: { routines: true, triggers: true, events: true, users: false, globals: false },
    exclude_tables: [],
    compression: "zstd",
    schedule: { kind: "daily", minute: 0, hour: 2, weekdays: [], cron: "", timezone: "UTC" },
    retention: { days: 7, weeks: 4, months: 6 },
    failure: { retries: 0, retry_interval: 5, timeout: 120 },
    run_now: false,
  };
}

const stat = (page: Page, name: string) => page.getByRole("group", { name });
const recentRegion = (page: Page) => page.getByRole("region", { name: "最近运行" });
const storageRow = (page: Page) =>
  page.getByRole("region", { name: "存储目标" }).getByRole("listitem").filter({ hasText: STORAGE_NAME });
const todayBar = (page: Page) => page.getByRole("region", { name: "14 天运行" }).getByRole("img").last();
const recentRows = (page: Page) =>
  recentRegion(page)
    .getByRole("row")
    .filter({ has: page.getByRole("cell") });

/** 等概览加载完，再从中读出当前的数字（“最近 24 小时没有运行”时成功、失败都为 0） */
async function readNumbers(page: Page) {
  await expect(stat(page, "24h 成功率")).toHaveAttribute("aria-busy", "false");
  const rate = (await stat(page, "24h 成功率").textContent()) ?? "";
  const runs = Number(/(\d+) 次运行/.exec(rate)?.[1] ?? 0);
  const failed = Number(/(\d+) 次失败/.exec(rate)?.[1] ?? 0);
  const failedButton =
    (await recentRegion(page)
      .getByRole("button", { name: /^失败 \d+$/ })
      .textContent()) ?? "";
  const failed24h = Number(/\d+/.exec(failedButton)?.[0]);
  const protectedText = (await stat(page, "受保护数据源").locator("p").nth(1).textContent()) ?? "";
  const bar = (await todayBar(page).getAttribute("aria-label")) ?? "";
  const today: [number, number] = [Number(/成功 (\d+)/.exec(bar)?.[1]), Number(/失败 (\d+)/.exec(bar)?.[1])];
  return { runs, failed, failed24h, protected: Number(protectedText), today };
}

test.beforeAll(async ({ playwright }, testInfo) => {
  base = mkdtempSync(join(tmpdir(), "opsnap-e2e-overview-"));
  const api = await playwright.request.newContext({
    baseURL: ORIGIN,
    storageState: testInfo.project.use.storageState as string,
  });
  try {
    const ds = await call(api, "POST", "/datasources", {
      data_source: {
        name: DATASOURCE_NAME,
        kind: "postgres",
        host: "127.0.0.1",
        port: FAKE_PG_PORT,
        username: FAKE_PG_USER,
        auth_method: "password",
        password: FAKE_PG_PASSWORD,
        private_key: "",
        passphrase: "",
        database: "",
        tls_mode: "prefer",
        tls_ca: "",
        tls_client_cert: "",
        tls_client_key: "",
        channel_id: 0,
        host_key: "",
      },
    });
    dsId = ds.item.id;
    // 后台探测完成（假 pg_dump 可用）后再建任务，与向导里的前提一致
    await expect(async () => {
      const item = (await call(api, "GET", `/datasources/${dsId}`)).item;
      expect(item.probe?.state).toBe("done");
    }).toPass({ timeout: 15_000 });

    const { key } = await call(api, "POST", "/storages/key", { key: "" });
    const location = {
      kind: "local",
      path: base,
      endpoint: "",
      region: "",
      bucket: "",
      prefix: "",
      access_key: "",
      secret_key: "",
      use_tls: true,
      skip_verify: false,
    };
    storageId = (await call(api, "POST", "/storages", { name: STORAGE_NAME, location, key, confirm_saved: true })).item
      .id;
    // 测试连接记录一次仓库用量，存储行显示 0 份快照
    await call(api, "POST", `/storages/${storageId}/test`);

    okId = (await call(api, "POST", "/jobs", jobBody(JOB_OK, "app", "postgres/e2e-overview"))).item.id;
    failId = (
      await call(api, "POST", "/jobs", jobBody(JOB_FAIL, FAKE_PG_FAILING_DATABASE, "postgres/e2e-overview-broken"))
    ).item.id;
  } finally {
    await api.dispose();
  }
});

// 兜底清理：最后一个用例失败或没跑到时，删除剩下的任务（连同快照）、存储与数据源，再删除临时目录
test.afterAll(async ({ playwright }, testInfo) => {
  const api = await playwright.request.newContext({
    baseURL: ORIGIN,
    storageState: testInfo.project.use.storageState as string,
  });
  try {
    for (const id of [okId, failId]) if (id) await api.delete(`/api/v1/jobs/${id}?delete_snapshots=true`);
    if (storageId) await api.delete(`/api/v1/storages/${storageId}`);
    if (dsId) await api.delete(`/api/v1/datasources/${dsId}`);
  } finally {
    await api.dispose();
    rmSync(base, { recursive: true, force: true });
  }
});

test.describe("概览 · 立即执行后数字随之变化", () => {
  test("运行前：存储行显示 0 份快照与本地磁盘，记下当前的统计", async ({ page }) => {
    await page.goto("/");
    await expect(page.getByRole("heading", { level: 1 })).toHaveText("概览");
    await expect(storageRow(page)).toContainText("本地目录");
    await expect(storageRow(page)).toContainText("0 份快照");
    await expect(storageRow(page)).toContainText(base);
    await expect(storageRow(page).getByRole("progressbar")).toBeVisible();
    await expect(stat(page, "下一次运行")).toContainText("e2e 概览");
    await expect(recentRegion(page)).not.toContainText(JOB_OK);
    before = await readNumbers(page);
  });

  test("立即执行一个成功、一个失败的任务后，统计、最近运行、柱状图与存储行随之变化", async ({ page }) => {
    await page.goto(`/jobs/${okId}`);
    await page.getByRole("button", { name: "立即执行", exact: true }).click();
    await expect(
      page
        .getByRole("row")
        .filter({ has: page.getByRole("cell") })
        .first()
    ).toContainText("成功", {
      timeout: 20_000,
    });
    await page.goto(`/jobs/${failId}`);
    await page.getByRole("button", { name: "立即执行", exact: true }).click();
    await expect(
      page
        .getByRole("row")
        .filter({ has: page.getByRole("cell") })
        .first()
    ).toContainText("失败", {
      timeout: 20_000,
    });

    await page.goto("/");
    await expect(stat(page, "24h 成功率")).toContainText(`${before.runs + 2} 次运行 · ${before.failed + 1} 次失败`);
    const after = await readNumbers(page);
    expect(after.protected, "成功过一次的任务所引用的数据源变为受保护").toBe(before.protected + 1);
    expect(after.failed24h).toBe(before.failed24h + 1);
    expect(after.today).toEqual([before.today[0] + 1, before.today[1] + 1]);

    // 最近运行：最新的两条在最前，带引擎缩写与数据源地址
    const [first, second] = [recentRows(page).nth(0), recentRows(page).nth(1)];
    await expect(first).toContainText(JOB_FAIL);
    await expect(first).toContainText("失败");
    await expect(first).toContainText("刚刚");
    await expect(second).toContainText(JOB_OK);
    await expect(second).toContainText("成功");
    await expect(second).toContainText("PG");
    await expect(second).toContainText(`postgres://127.0.0.1:${FAKE_PG_PORT}`);

    // “失败 N” 只显示失败的运行
    await recentRegion(page)
      .getByRole("button", { name: `失败 ${after.failed24h}` })
      .click();
    await expect(recentRows(page).first()).toContainText(JOB_FAIL);
    await expect(recentRegion(page)).not.toContainText(JOB_OK);

    // 柱状图：悬停今天的柱显示当天的次数
    await todayBar(page).hover();
    await expect(page.getByRole("tooltip")).toContainText(`成功 ${after.today[0]}`);

    // 存储行：这次运行之后记录的仓库用量
    await expect(storageRow(page)).toContainText("1 份快照");

    // 点击一行进入该任务的详情页
    await recentRegion(page).getByRole("button", { name: "全部" }).click();
    await recentRows(page).filter({ hasText: JOB_OK }).first().click();
    await expect(page).toHaveURL(`/jobs/${okId}`);
  });

  test("英文界面与深色主题；侧栏底部显示服务状态", async ({ page }, testInfo) => {
    await page.goto("/");
    await page.getByRole("button", { name: "EN" }).click();
    await page.getByRole("button", { name: /深色|Dark/ }).click();
    await expect(page.locator("html")).toHaveClass(/dark/);
    await expect(page.getByRole("heading", { level: 1 })).toHaveText("Overview");
    await expect(page.getByRole("region", { name: "Recent runs" })).toContainText(JOB_OK);
    await expect(page.getByRole("region", { name: "Storage targets" })).toContainText("1 snapshot");
    await expect(page.getByText("Service running")).toBeVisible();
    await expect(page.getByRole("button", { name: "Restore data" })).toHaveAccessibleDescription(
      "Coming in a later version"
    );
    await testInfo.attach("overview-en-dark", {
      body: await page.screenshot({ fullPage: true }),
      contentType: "image/png",
    });
  });

  test("删除这里建的任务、存储与数据源后，概览回到运行前", async ({ page }) => {
    await call(page.request, "DELETE", `/jobs/${okId}?delete_snapshots=true`);
    await call(page.request, "DELETE", `/jobs/${failId}?delete_snapshots=true`);
    okId = failId = 0;
    await call(page.request, "DELETE", `/storages/${storageId}`);
    storageId = 0;
    await call(page.request, "DELETE", `/datasources/${dsId}`);
    dsId = 0;

    await page.goto("/");
    await expect(page.getByRole("region", { name: "存储目标" })).not.toContainText(STORAGE_NAME);
    await expect(recentRegion(page)).not.toContainText("e2e 概览");
    await expect(stat(page, "下一次运行")).not.toContainText("e2e 概览");
    const now = await readNumbers(page);
    expect(now).toEqual(before);
  });
});
