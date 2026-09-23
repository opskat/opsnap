import { expect, test } from "@playwright/test";

test.describe("启动与身份", () => {
  test("健康检查返回 OpsNap 版本与可用的元数据库", async ({ request }) => {
    const res = await request.get("/api/v1/system/health");
    expect(res.ok()).toBeTruthy();
    const body = await res.json();
    expect(body.code).toBe(0);
    expect(body.data.database).toBe("ok");
    expect(body.data.version).toBeTruthy();
  });

  test("未知接口返回 404，不被当成页面", async ({ request }) => {
    const res = await request.get("/api/v1/does-not-exist");
    expect(res.status()).toBe(404);
  });

  test("首页由内嵌前端提供，并显示系统状态", async ({ page, request }) => {
    const health = await (await request.get("/api/v1/system/health")).json();
    await page.goto("/");
    await expect(page).toHaveTitle("OpsNap");
    // 独立校验：页面显示的版本号必须与接口返回一致
    await expect(page.getByTestId("health-version")).toHaveText(health.data.version);
  });
});

test.describe("主导航", () => {
  test("前端路由可直接访问并刷新", async ({ page }) => {
    await page.goto("/jobs");
    await expect(page.getByRole("link", { name: /任务|Jobs/ })).toHaveAttribute("aria-current", "page");
    await page.reload();
    await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
  });
});

test.describe("主题与语言", () => {
  test("切换深色主题后刷新仍保持", async ({ page }) => {
    await page.goto("/");
    await page.getByRole("button", { name: /深色|Dark/ }).click();
    await expect(page.locator("html")).toHaveClass(/dark/);
    await page.reload();
    await expect(page.locator("html")).toHaveClass(/dark/);
  });

  test("切换到英文后界面文案随之变化", async ({ page }) => {
    await page.goto("/");
    await page.getByRole("button", { name: "EN" }).click();
    await expect(page.getByRole("heading", { level: 1 })).toHaveText("Overview");
    await expect(page.locator("html")).toHaveAttribute("lang", "en");
    await page.getByRole("button", { name: "中文" }).click();
    await expect(page.getByRole("heading", { level: 1 })).toHaveText("概览");
  });
});
