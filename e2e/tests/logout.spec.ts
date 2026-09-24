import { expect, test } from "@playwright/test";

import { ADMIN_NEW_PASSWORD, ADMIN_PASSWORD, ADMIN_USERNAME } from "./credentials";

// 本文件的用例会让会话失效、触发登录限速，因此放在 logout 项目中最后执行，且按顺序运行
test.describe.configure({ mode: "serial" });

test.describe("修改密码、退出、登录与登录保护", () => {
  test("修改密码后其他会话失效，当前浏览器保持登录，旧密码不再可用", async ({ page, playwright, baseURL }) => {
    const other = await playwright.request.newContext({ baseURL, storageState: { cookies: [], origins: [] } });
    const login = await other.post("/api/v1/auth/login", {
      data: { username: ADMIN_USERNAME, password: ADMIN_PASSWORD },
    });
    expect(login.status()).toBe(200);

    await page.goto("/settings");
    await expect(page.getByRole("region", { name: "账号" })).toContainText(ADMIN_USERNAME);
    await page.getByRole("button", { name: "修改密码" }).click();
    const dialog = page.getByRole("dialog", { name: "修改密码" });
    await dialog.getByLabel("当前密码").fill("not-the-password");
    await dialog.getByLabel("新密码", { exact: true }).fill(ADMIN_NEW_PASSWORD);
    await dialog.getByLabel("确认新密码").fill(ADMIN_NEW_PASSWORD);
    await dialog.getByRole("button", { name: "保存" }).click();
    await expect(dialog.getByText("当前密码不正确")).toBeVisible();

    await dialog.getByLabel("当前密码").fill(ADMIN_PASSWORD);
    await dialog.getByRole("button", { name: "保存" }).click();
    await expect(page.getByRole("status")).toHaveText("密码已修改，其他浏览器的登录已退出");

    expect((await other.get("/api/v1/auth/me")).status()).toBe(401);
    expect((await page.request.get("/api/v1/auth/me")).status()).toBe(200);
    await other.dispose();
  });

  test("退出后回到登录页，原会话立即失效", async ({ page }) => {
    await page.goto("/");
    await page.getByRole("button", { name: "退出登录" }).click();
    await expect(page).toHaveURL(/\/login$/);
    expect((await page.request.get("/api/v1/auth/me")).status()).toBe(401);
    await page.goto("/jobs");
    await expect(page).toHaveURL(/\/login\?next=%2Fjobs$/);
  });

  test("密码错误时提示，正确后回到原本要访问的页面", async ({ browser }) => {
    const context = await browser.newContext({ storageState: { cookies: [], origins: [] } });
    const page = await context.newPage();
    await page.goto("/storage");
    await expect(page).toHaveURL(/\/login\?next=%2Fstorage$/);
    await expect(page.getByText("opsnap admin reset-password")).toBeVisible();

    await page.getByLabel("用户名").fill(ADMIN_USERNAME);
    await page.getByLabel("密码", { exact: true }).fill("wrong-password-123");
    await page.getByRole("button", { name: "登录" }).click();
    await expect(page.getByRole("alert")).toHaveText("用户名或密码错误");

    await page.getByLabel("密码", { exact: true }).fill(ADMIN_NEW_PASSWORD);
    await page.getByRole("button", { name: "登录" }).click();
    await expect(page).toHaveURL(/\/storage$/);
    await expect(page.getByTestId("current-user")).toHaveText(ADMIN_USERNAME);
    await context.close();
  });

  test("同一 IP 连续失败 5 次后被锁定，正确密码也被拒绝", async ({ request }) => {
    for (let i = 0; i < 5; i++) {
      const res = await request.post("/api/v1/auth/login", {
        data: { username: ADMIN_USERNAME, password: `wrong-password-${i}` },
      });
      expect(res.status()).toBe(400);
    }
    const locked = await request.post("/api/v1/auth/login", {
      data: { username: ADMIN_USERNAME, password: ADMIN_NEW_PASSWORD },
    });
    expect(locked.status()).toBe(429);
    expect((await locked.json()).msg).toContain("15 分钟");
  });
});
