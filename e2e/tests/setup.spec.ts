import { expect, test } from "@playwright/test";

import { ADMIN_STATE } from "../playwright.config";
import { ADMIN_PASSWORD, ADMIN_USERNAME } from "./credentials";

test.describe("首次设置", () => {
  test("未初始化时访问控制台跳转到首次设置页；设置码错误被拒绝；正确后进入控制台", async ({ page, browser }) => {
    const setupCode = process.env.OPSNAP_SETUP_CODE;
    expect(setupCode, "global-setup 应从启动日志中读到设置码").toBeTruthy();

    // 首次设置页在英文与深色主题下可用（单独的浏览器上下文，不影响下面保存的登录态）
    const other = await browser.newContext();
    const preview = await other.newPage();
    await preview.goto("/setup");
    await preview.getByRole("button", { name: "深色" }).click();
    await preview.getByRole("button", { name: "EN" }).click();
    await expect(preview.locator("html")).toHaveClass(/dark/);
    await expect(preview.locator("html")).toHaveAttribute("lang", "en");
    await expect(preview.getByRole("heading", { level: 1 })).toHaveText("Set up OpsNap");
    await expect(preview.getByLabel("Username")).toBeVisible();
    await expect(preview.getByRole("button", { name: "Create administrator and sign in" })).toBeVisible();
    await other.close();

    await page.goto("/jobs");
    await expect(page).toHaveURL(/\/setup$/);
    await expect(page.getByRole("heading", { level: 1 })).toHaveText("初始化 OpsNap");

    await page.getByLabel("设置码").fill("AAAA-BBBB-CCCC");
    await page.getByLabel("用户名").fill(ADMIN_USERNAME);
    await page.getByLabel("密码", { exact: true }).fill(ADMIN_PASSWORD);
    await page.getByLabel("确认密码").fill(ADMIN_PASSWORD);
    await page.getByRole("button", { name: "创建管理员并登录" }).click();
    await expect(page.getByText("设置码不正确")).toBeVisible();

    await page.getByLabel("设置码").fill(setupCode!);
    await page.getByRole("button", { name: "创建管理员并登录" }).click();
    await expect(page).toHaveURL(/\/$/);
    await expect(page.getByTestId("current-user")).toHaveText(ADMIN_USERNAME);

    const cookies = await page.context().cookies();
    const session = cookies.find((c) => c.name === "opsnap_session");
    expect(session?.httpOnly).toBe(true);
    expect(session?.sameSite).toBe("Lax");

    await page.context().storageState({ path: ADMIN_STATE });
  });
});
