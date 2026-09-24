import { expect, request as playwrightRequest, test } from "@playwright/test";

import { ADMIN_PASSWORD, ADMIN_USERNAME } from "./credentials";

// 本项目默认带管理员登录态；未登录场景必须显式清空
const anonymous = { cookies: [], origins: [] };

test.describe("认证边界", () => {
  test("未登录时受保护接口返回 401，公开接口可访问", async ({ baseURL }) => {
    const anon = await playwrightRequest.newContext({ baseURL, storageState: anonymous });
    expect((await anon.get("/api/v1/auth/me")).status()).toBe(401);
    expect((await anon.get("/api/v1/system/health")).status()).toBe(200);
    const status = await (await anon.get("/api/v1/auth/status")).json();
    expect(status.data.initialized).toBe(true);
    await anon.dispose();
  });

  test("未登录访问页面跳转到登录页，并记住原地址", async ({ browser }) => {
    const context = await browser.newContext({ storageState: anonymous });
    const page = await context.newPage();
    await page.goto("/storage?tab=s3");
    await expect(page).toHaveURL(/\/login\?next=%2Fstorage%3Ftab%3Ds3$/);
    await context.close();
  });

  test("已初始化后再次首次设置返回 409", async ({ baseURL }) => {
    const anon = await playwrightRequest.newContext({ baseURL, storageState: anonymous });
    const res = await anon.post("/api/v1/auth/setup", {
      data: { setup_code: "AAAA-BBBB-CCCC", username: ADMIN_USERNAME, password: ADMIN_PASSWORD },
    });
    expect(res.status()).toBe(409);
    await anon.dispose();
  });

  test("跨站的写请求被拒绝（403）", async ({ request }) => {
    const res = await request.post("/api/v1/auth/logout", { headers: { Origin: "https://evil.example" } });
    expect(res.status()).toBe(403);
    expect((await request.get("/api/v1/auth/me")).status()).toBe(200);
  });

  test("登录页与设置页在英文与深色主题下可用", async ({ browser, page }) => {
    const context = await browser.newContext({ storageState: anonymous });
    const login = await context.newPage();
    await login.goto("/login");
    await login.getByRole("button", { name: "深色" }).click();
    await login.getByRole("button", { name: "EN" }).click();
    await expect(login.locator("html")).toHaveClass(/dark/);
    await expect(login.locator("html")).toHaveAttribute("lang", "en");
    await expect(login.getByRole("heading", { level: 1 })).toHaveText("Sign in");
    await expect(login.getByLabel("Username")).toBeVisible();
    await expect(login.getByText("opsnap admin reset-password")).toBeVisible();
    await context.close();

    await page.goto("/settings");
    await page.getByRole("button", { name: "深色" }).click();
    await page.getByRole("button", { name: "EN" }).click();
    await expect(page.locator("html")).toHaveClass(/dark/);
    for (const name of ["Account", "Sign-in methods", "API tokens"]) {
      await expect(page.getByRole("region", { name })).toBeVisible();
    }
  });
});
