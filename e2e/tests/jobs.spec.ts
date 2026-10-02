import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { expect, test, type APIRequestContext, type Page } from "@playwright/test";

import { FAKE_PG_FAILING_DATABASE, FAKE_PG_PASSWORD, FAKE_PG_PORT, FAKE_PG_USER, SMOKE_PORT } from "../ports";

// 备份任务（docs/specs/2026-09-27-backup-jobs.md「测试决策」的 Playwright 一行）：CI 中没有数据库，
// 所以 PostgreSQL 数据源连的是假服务端 bin/fakepg，导出工具是 global-setup 放在被测实例 PATH 最前面的
// 假 pg_dump / pg_dumpall（同一个 fakepg 程序，经 OpsNap 的本机转发端口真的连上假服务端）；存储是
// 临时目录中的本地 kopia 仓库。独立判据：存储“测试连接”接口直接从 kopia 仓库数出的快照份数。
// 用完后删除这里建的数据源与存储，不影响按文件名排在后面、从空状态开始的 storage.spec.ts。
test.describe.configure({ mode: "serial" });

const DATASOURCE_NAME = "e2e PG";
const STORAGE_NAME = "e2e 任务存储";
const JOB_OK = "e2e 备份 app";
const JOB_OK_EDITED = "e2e 备份 app（已编辑）";
const JOB_FAIL = "e2e 失败 broken";
const JOB_KEEP = "e2e 保留快照";
/** 数据源名称生成的默认路径前缀（frontend/src/lib/jobs.ts defaultPrefix） */
const DEFAULT_PREFIX = "postgres/e2e-PG";
const ORIGIN = `http://127.0.0.1:${SMOKE_PORT}`;

let base: string;
let okId: number;
let failId: number;

test.beforeAll(() => {
  base = mkdtempSync(join(tmpdir(), "opsnap-e2e-jobs-"));
});

// 尽力清理：删除剩下的任务（连同快照）、存储与数据源记录，再删除临时目录
test.afterAll(async ({ playwright }, testInfo) => {
  const api = await playwright.request.newContext({
    baseURL: ORIGIN,
    storageState: testInfo.project.use.storageState as string,
    extraHTTPHeaders: { Origin: ORIGIN },
  });
  try {
    const jobs = await listItems(api, "/api/v1/jobs");
    for (const j of jobs.filter((j) => j.name.startsWith("e2e "))) {
      await api.delete(`/api/v1/jobs/${j.id}?delete_snapshots=true`);
    }
    const storage = (await listItems(api, "/api/v1/storages")).find((s) => s.name === STORAGE_NAME);
    if (storage) await api.delete(`/api/v1/storages/${storage.id}`);
    const ds = (await listItems(api, "/api/v1/datasources")).find((d) => d.name === DATASOURCE_NAME);
    if (ds) await api.delete(`/api/v1/datasources/${ds.id}`);
  } finally {
    await api.dispose();
    rmSync(base, { recursive: true, force: true });
  }
});

async function listItems(api: APIRequestContext, path: string): Promise<{ id: number; name: string }[]> {
  const res = await api.get(path);
  if (!res.ok()) return [];
  return (await res.json()).data?.items ?? [];
}

/** 独立判据：存储“测试连接”用 kopia 打开仓库，返回其中的快照份数（所有任务合计） */
async function repositorySnapshots(page: Page): Promise<number> {
  const storages = await (await page.request.get("/api/v1/storages")).json();
  const storage = storages.data.items.find((s: { name: string }) => s.name === STORAGE_NAME);
  const res = await page.request.post(`/api/v1/storages/${storage.id}/test`, { headers: { Origin: ORIGIN } });
  expect(res.ok()).toBeTruthy();
  const body = await res.json();
  expect(body.code).toBe(0);
  return body.data.snapshots;
}

function row(page: Page, name: string) {
  return page.getByRole("row").filter({ hasText: name });
}

async function next(page: Page) {
  await page.getByRole("button", { name: "下一步", exact: true }).click();
}

/** 走完新建向导五步，返回新任务的 id（创建后跳转到详情页） */
async function createJob(
  page: Page,
  opts: { name: string; database: string; prefix?: string; retries?: number; runNow: boolean }
) {
  await page.goto("/jobs/new");
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("新建任务");

  // 第 1 步：备份 + 数据源（假服务端上的 PostgreSQL 16）
  await expect(page.getByRole("radio", { name: /备份/ }).first()).toHaveAttribute("aria-checked", "true");
  const source = page
    .getByRole("radiogroup", { name: "数据源" })
    .getByRole("radio", { name: new RegExp(DATASOURCE_NAME) });
  await expect(source).toContainText("正常");
  await source.click();
  await next(page);

  // 第 2 步：指定数据库；库列表来自假服务端，全量工具 pg_dump 可用
  await expect(page.getByText("第 2 步，共 5 步", { exact: false })).toBeVisible();
  await page.getByRole("radiogroup", { name: "备份范围" }).getByRole("radio", { name: "指定数据库" }).click();
  await expect(page.getByRole("checkbox", { name: "postgres", exact: true })).toBeVisible();
  await page.getByRole("checkbox", { name: opts.database, exact: true }).check();
  await expect(page.getByRole("radiogroup", { name: "备份方式" }).getByRole("radio", { name: /仅全量/ })).toBeEnabled();
  await next(page);

  // 第 3 步：本地目录存储与路径前缀
  await page
    .getByRole("radiogroup", { name: "存储" })
    .getByRole("radio", { name: new RegExp(STORAGE_NAME) })
    .click();
  if (opts.prefix) await page.getByLabel("路径前缀").fill(opts.prefix);
  await expect(page.getByText(`${STORAGE_NAME}:/${opts.prefix ?? DEFAULT_PREFIX}`)).toBeVisible();
  await next(page);

  // 第 4 步：默认每天 02:00，服务端预览接下来三次
  await expect(page.getByText("接下来三次")).toBeVisible();
  if (opts.retries !== undefined) await page.getByLabel("失败重试次数").fill(String(opts.retries));
  await next(page);

  // 第 5 步：确认
  await page.getByLabel("任务名称").fill(opts.name);
  await page
    .getByRole("radiogroup", { name: "创建后" })
    .getByRole("radio", { name: opts.runNow ? "立即执行一次" : /等下一次计划/ })
    .click();
  await page.getByRole("button", { name: "创建任务" }).click();

  await expect(page).toHaveURL(/\/jobs\/\d+$/);
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(opts.name);
  return Number(new URL(page.url()).pathname.split("/").pop());
}

/** 详情页运行记录中最新的一条（倒序，第一行是表头） */
function latestRun(page: Page) {
  return page
    .getByRole("row")
    .filter({ has: page.getByRole("cell") })
    .first();
}

test.describe("备份任务 · PostgreSQL 全量到本地目录", () => {
  test("新建连到假服务端的 PostgreSQL 数据源与本地目录存储", async ({ page }) => {
    await page.goto("/sources");
    await page.getByRole("tab", { name: /^数据源/ }).click();
    await page.getByRole("button", { name: "新建数据源" }).first().click();
    const dialog = page.getByRole("dialog", { name: "新建数据源" });
    await dialog.getByRole("radio", { name: "PostgreSQL" }).click();
    await dialog.getByLabel("名称").fill(DATASOURCE_NAME);
    await dialog.getByLabel("主机").fill("127.0.0.1");
    await dialog.getByLabel("端口").fill(String(FAKE_PG_PORT));
    await dialog.getByLabel("用户名").fill(FAKE_PG_USER);
    await dialog.getByLabel("密码", { exact: true }).fill(FAKE_PG_PASSWORD);
    await dialog.getByRole("button", { name: "保存" }).click();
    await expect(dialog).toHaveCount(0);
    await expect(row(page, DATASOURCE_NAME)).toContainText("PostgreSQL");
    await expect(row(page, DATASOURCE_NAME)).toContainText("正常");
    // 探测在后台进行（列表不会自己刷新）：6 项全部通过，其中包括 PATH 上的假 pg_dump 大版本不低于服务端
    await expect(async () => {
      await page.goto("/sources");
      await page.getByRole("tab", { name: /^数据源/ }).click();
      await expect(row(page, DATASOURCE_NAME)).toContainText("6 项全部通过");
    }).toPass({ timeout: 15_000 });

    await page.goto("/storage");
    await page.getByRole("button", { name: "新建存储" }).first().click();
    const create = page.getByRole("dialog", { name: "新建存储" });
    await create.getByLabel("名称").fill(STORAGE_NAME);
    await create.getByLabel("目录路径").fill(base);
    await create.getByRole("button", { name: /下一步/ }).click();
    const setKey = page.getByRole("dialog", { name: "设置加密密钥" });
    await setKey.getByRole("checkbox", { name: /我已保存这把密钥/ }).check();
    await setKey.getByRole("button", { name: "启用加密" }).click();
    await expect(row(page, STORAGE_NAME)).toContainText("正常");
    expect(await repositorySnapshots(page)).toBe(0);
  });

  test("向导五步新建任务，立即执行成功并出现一份快照", async ({ page }) => {
    okId = await createJob(page, { name: JOB_OK, database: "app", runNow: false });
    // 选了“等下一次计划”：还没有运行
    await expect(page.getByText("还没有运行记录")).toBeVisible();
    const config = page.getByRole("region", { name: "配置" });
    await expect(config).toContainText(`${DATASOURCE_NAME}（PostgreSQL）`);
    await expect(config).toContainText(DEFAULT_PREFIX);

    await page.getByRole("button", { name: "立即执行", exact: true }).click();
    // 运行中或排队中时详情页每 3 秒自动刷新
    await expect(latestRun(page)).toContainText("成功", { timeout: 20_000 });
    await expect(latestRun(page)).toContainText("手动");
    await expect(latestRun(page)).toContainText(/[0-9a-f]{16,}/);

    await latestRun(page).click();
    await expect(page.getByText(/快照 \S+ 已写入并读回校验：2 个文件/)).toBeVisible();

    await page.reload();
    await expect(page.getByRole("region", { name: "统计" })).toContainText("1 份快照");
    expect(await repositorySnapshots(page)).toBe(1);

    await page.goto("/jobs");
    await expect(row(page, JOB_OK)).toContainText("成功");
    await expect(row(page, JOB_OK)).toContainText("1 份快照");
    await expect(row(page, JOB_OK)).toContainText(`${DATASOURCE_NAME} → ${STORAGE_NAME}`);
  });

  test("导出失败的运行：详情页展开后看到失败步骤、原因与日志，且没有快照", async ({ page }) => {
    failId = await createJob(page, {
      name: JOB_FAIL,
      database: FAKE_PG_FAILING_DATABASE,
      prefix: "postgres/e2e-broken",
      retries: 0,
      runNow: true,
    });
    await expect(latestRun(page)).toContainText("失败", { timeout: 20_000 });
    await expect(latestRun(page)).toHaveAttribute("aria-expanded", "false");
    await latestRun(page).click();
    await expect(latestRun(page)).toHaveAttribute("aria-expanded", "true");
    await expect(page.getByText("失败在「导出并写入仓库」", { exact: false })).toBeVisible();
    await expect(page.getByText("执行日志")).toBeVisible();
    // 日志来自 pg_dump 的错误输出（假工具模拟服务端读数据块失败），不含数据源密码
    await expect(page.getByText(/could not read block/).first()).toBeVisible();
    await expect(page.getByText(/pg_dump 失败（退出码 1）/).first()).toBeVisible();
    await expect(page.getByText(FAKE_PG_PASSWORD)).toHaveCount(0);

    await page.reload();
    await expect(page.getByRole("region", { name: "统计" })).toContainText("还没有快照");
    expect(await repositorySnapshots(page), "失败的运行不形成快照").toBe(1);

    await page.goto("/jobs");
    await expect(row(page, JOB_FAIL)).toContainText("失败");
  });

  test("暂停后计划不再显示下次执行，启用后恢复；刷新后状态保持", async ({ page }) => {
    await page.goto("/jobs");
    await expect(row(page, JOB_OK).getByRole("link", { name: JOB_OK, exact: true })).toHaveAttribute(
      "href",
      `/jobs/${okId}`
    );
    await page.getByRole("button", { name: `${JOB_OK} 的更多操作` }).click();
    await page.getByRole("menuitem", { name: "查看详情" }).click();
    await expect(page.getByRole("heading", { level: 1 })).toHaveText(JOB_OK);
    const header = page.getByRole("heading", { level: 1 }).locator("..");
    await expect(header).toContainText("已启用");

    await page.getByRole("button", { name: "暂停", exact: true }).click();
    await expect(header).toContainText("已暂停");
    await expect(page.getByRole("button", { name: "启用", exact: true })).toBeVisible();
    await page.reload();
    await expect(header).toContainText("已暂停");
    await page.goto("/jobs");
    await expect(row(page, JOB_OK)).toContainText("已暂停");
    await expect(row(page, JOB_OK)).not.toContainText("下次");

    // 列表的更多菜单里启用
    await page.getByRole("button", { name: `${JOB_OK} 的更多操作` }).click();
    await page.getByRole("menuitem", { name: "启用" }).click();
    await expect(row(page, JOB_OK)).toContainText("下次");
    await page.reload();
    await expect(row(page, JOB_OK)).toContainText("下次");
    await expect(row(page, JOB_OK)).not.toContainText("已暂停");
  });

  test("编辑任务并保存：数据源、存储与路径前缀保持不变", async ({ page }) => {
    const id = okId;
    await page.goto(`/jobs/${id}`);
    await page.getByRole("link", { name: "编辑", exact: true }).click();
    await expect(page).toHaveURL(`/jobs/${id}/edit`);
    await expect(page.getByRole("heading", { level: 1 })).toHaveText("编辑任务");

    // 第 1、3 步中不可修改的项保持选中且锁定
    await expect(
      page.getByRole("radiogroup", { name: "数据源" }).getByRole("radio", { name: new RegExp(DATASOURCE_NAME) })
    ).toHaveAttribute("aria-checked", "true");
    await next(page);
    await expect(page.getByRole("checkbox", { name: "app", exact: true })).toBeChecked();
    await next(page);
    await expect(page.getByLabel("路径前缀")).toHaveValue(DEFAULT_PREFIX);
    await expect(page.getByLabel("路径前缀")).toHaveAttribute("readonly");
    await next(page);
    await page.getByLabel("保留最近 N 天内的全部快照").fill("14");
    await next(page);
    await page.getByLabel("任务名称").fill(JOB_OK_EDITED);

    const saving = page.waitForResponse(
      (r) => r.request().method() === "PUT" && r.url().endsWith(`/api/v1/jobs/${id}`)
    );
    await page.getByRole("button", { name: "保存", exact: true }).click();
    const res = await saving;
    // 编辑时不回传不可修改的字段（0 / 空串表示不修改），后端接受
    expect(res.request().postDataJSON()).toMatchObject({ datasource_id: 0, storage_id: 0, prefix: "" });
    expect(res.status()).toBe(200);
    expect((await res.json()).code).toBe(0);

    await expect(page).toHaveURL(`/jobs/${id}`);
    await expect(page.getByRole("heading", { level: 1 })).toHaveText(JOB_OK_EDITED);
    const config = page.getByRole("region", { name: "配置" });
    await expect(config).toContainText(`${DATASOURCE_NAME}（PostgreSQL）`);
    await expect(config).toContainText(STORAGE_NAME);
    await expect(config).toContainText(DEFAULT_PREFIX);
    await expect(config).toContainText("近 14 天全部");
    // 独立判据：重新从接口读取
    const saved = (await (await page.request.get(`/api/v1/jobs/${id}`)).json()).data.item;
    expect(saved).toMatchObject({ name: JOB_OK_EDITED, prefix: DEFAULT_PREFIX });
    expect(saved.datasource_name).toBe(DATASOURCE_NAME);
    expect(saved.storage_name).toBe(STORAGE_NAME);
    expect(saved.retention.days).toBe(14);
  });

  test("英文界面与深色主题下的任务列表与详情", async ({ page }, testInfo) => {
    await page.goto("/jobs");
    await page.getByRole("button", { name: "EN", exact: true }).click();
    await page.getByRole("button", { name: /深色|Dark/ }).click();
    await expect(page.locator("html")).toHaveClass(/dark/);
    await expect(page.getByRole("heading", { level: 1 })).toHaveText("Jobs");
    await expect(page.getByRole("columnheader", { name: "Last run" })).toBeVisible();
    await expect(row(page, JOB_OK_EDITED)).toContainText("Success");
    await expect(row(page, JOB_OK_EDITED)).toContainText("Enabled");
    await expect(row(page, JOB_FAIL)).toContainText("Failed");
    await testInfo.attach("jobs-en-dark", {
      body: await page.screenshot({ fullPage: true }),
      contentType: "image/png",
    });

    await page.goto(`/jobs/${failId}`);
    await expect(page.getByRole("region", { name: "Configuration" })).toBeVisible();
    await expect(page.getByRole("region", { name: "Statistics" })).toContainText("No snapshots yet");
    await expect(page.getByRole("button", { name: "Run now", exact: true })).toBeVisible();
    await latestRun(page).click();
    await expect(page.getByText('Failed at "Export and write to repository"', { exact: false })).toBeVisible();
    await expect(page.getByText("Execution log")).toBeVisible();
    await testInfo.attach("job-detail-en-dark", {
      body: await page.screenshot({ fullPage: true }),
      contentType: "image/png",
    });
  });

  test("删除任务：勾选时一并删除本任务的快照，不勾选时快照留在仓库", async ({ page }) => {
    // 再建一个同样能成功的任务，使仓库中有两个任务各自的快照
    const keepId = await createJob(page, {
      name: JOB_KEEP,
      database: "app",
      prefix: "postgres/e2e-keep",
      runNow: true,
    });
    await expect(latestRun(page)).toContainText("成功", { timeout: 20_000 });
    expect(await repositorySnapshots(page)).toBe(2);

    const remove = async (name: string, count: number, withSnapshots: boolean) => {
      await page.goto("/jobs");
      await page.getByRole("button", { name: `${name} 的更多操作` }).click();
      await page.getByRole("menuitem", { name: "删除任务" }).click();
      const dialog = page.getByRole("dialog", { name: `删除任务“${name}”？` });
      await expect(dialog).toContainText("任务和它的运行记录一并删除");
      const checkbox = dialog.getByRole("checkbox", { name: `同时删除该任务的 ${count} 份快照` });
      await expect(checkbox).not.toBeChecked();
      if (withSnapshots) await checkbox.check();
      const deleting = page.waitForResponse((r) => r.request().method() === "DELETE");
      await dialog.getByRole("button", { name: "删除", exact: true }).click();
      const res = await deleting;
      expect(res.ok()).toBeTruthy();
      expect(res.url().includes("delete_snapshots=true")).toBe(withSnapshots);
      await expect(dialog).toHaveCount(0);
      await expect(row(page, name)).toHaveCount(0);
      return (await res.json()).data;
    };

    // 勾选：只删除本任务的 1 份快照，另一个任务的快照不受影响
    expect(await remove(JOB_OK_EDITED, 1, true)).toMatchObject({ snapshots_deleted: 1 });
    expect(await repositorySnapshots(page)).toBe(1);

    // 不勾选：任务没了，快照仍在仓库中
    expect(await remove(JOB_KEEP, 1, false)).toMatchObject({ snapshots_deleted: 0 });
    expect(await repositorySnapshots(page)).toBe(1);

    await remove(JOB_FAIL, 0, false);
    await expect(page.getByText("还没有任务")).toBeVisible();
    await page.goto(`/jobs/${keepId}`);
    await expect(page.getByText("任务不存在")).toBeVisible();
    await expect(page.getByRole("link", { name: "返回列表" })).toBeVisible();
  });
});
