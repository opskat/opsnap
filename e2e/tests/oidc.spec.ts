import { expect, test, type Browser } from "@playwright/test";

import { FAKE_IDP_CLIENT_ID, FAKE_IDP_CLIENT_SECRET, FAKE_IDP_ISSUER } from "../ports";

async function anonymousPage(browser: Browser) {
  const context = await browser.newContext({ storageState: { cookies: [], origins: [] } });
  return { context, page: await context.newPage() };
}

async function nextIdPBehavior(behavior: object) {
  const res = await fetch(`${FAKE_IDP_ISSUER}/control/next`, { method: "POST", body: JSON.stringify(behavior) });
  expect(res.status).toBe(204);
}

test.describe.configure({ mode: "serial" });

test.describe("OIDC", () => {
  test("配置提供方：Issuer 连不上时说明原因；正确配置后 Secret 不回显", async ({ page }) => {
    await page.goto("/settings");
    const card = page.getByRole("region", { name: "登录方式" });
    await card.getByRole("button", { name: "配置 OIDC" }).click();
    const dialog = page.getByRole("dialog", { name: "配置 OIDC" });
    await dialog.getByLabel("显示名称").fill("FakeIdP");
    await dialog.getByLabel("Issuer").fill("http://127.0.0.1:1");
    await dialog.getByLabel("Client ID").fill(FAKE_IDP_CLIENT_ID);
    await dialog.getByLabel("Client Secret").fill(FAKE_IDP_CLIENT_SECRET);
    await dialog.getByRole("button", { name: "保存" }).click();
    await expect(dialog.getByRole("alert")).toContainText("无法连接 http://127.0.0.1:1");

    await dialog.getByLabel("Issuer").fill(FAKE_IDP_ISSUER);
    await dialog.getByRole("button", { name: "保存" }).click();
    await expect(card).toContainText("已保存（加密存储）");
    await expect(card).toContainText("未绑定");
    await expect(card).toContainText("/api/v1/auth/oidc/callback");
    await expect(page.getByText(FAKE_IDP_CLIENT_SECRET)).toHaveCount(0);
  });

  test("绑定身份后，登录页出现 OIDC 登录并回到原页面", async ({ page, browser }) => {
    await page.goto("/settings");
    await page.getByRole("region", { name: "登录方式" }).getByRole("link", { name: "绑定" }).click();
    await expect(page).toHaveURL(/\/settings\?oidc=bound$/);
    await expect(page.getByRole("status")).toContainText("已绑定 OIDC 身份");
    await expect(page.getByRole("region", { name: "登录方式" })).toContainText("ops@example.com");

    const { context, page: anon } = await anonymousPage(browser);
    await anon.goto("/jobs");
    await expect(anon).toHaveURL(/\/login\?next=%2Fjobs$/);
    await anon.getByRole("link", { name: "使用 FakeIdP 登录" }).click();
    await expect(anon).toHaveURL(/\/jobs$/);
    await expect(anon.getByTestId("current-user")).toHaveText("admin");
    await context.close();
  });

  test("未绑定的身份与在 IdP 取消时回到登录页并显示原因", async ({ browser }) => {
    const { context, page } = await anonymousPage(browser);
    await nextIdPBehavior({ Subject: "someone-else", Email: "x@example.com" });
    await page.goto("/login");
    await page.getByRole("link", { name: "使用 FakeIdP 登录" }).click();
    await expect(page.getByRole("alert")).toHaveText("此 OIDC 账号未绑定到 OpsNap");

    await nextIdPBehavior({ Error: "access_denied", ErrorDescription: "User canceled" });
    await page.getByRole("link", { name: "使用 FakeIdP 登录" }).click();
    await expect(page.getByRole("alert")).toHaveText("OIDC 登录已取消或失败：User canceled");
    expect((await page.request.get("/api/v1/auth/me")).status()).toBe(401);
    await context.close();
  });

  test("用 OIDC 登录过之后可以关闭密码登录：登录页只剩 OIDC，密码登录接口被拒绝；再开启恢复", async ({
    page,
    browser,
  }) => {
    await page.goto("/settings");
    const card = page.getByRole("region", { name: "登录方式" });
    const sw = card.getByRole("switch", { name: "密码登录" });
    await expect(sw).toBeEnabled();
    await sw.click();
    await page.getByRole("dialog", { name: "关闭密码登录？" }).getByRole("button", { name: "关闭密码登录" }).click();
    await expect(sw).toHaveAttribute("aria-checked", "false");

    const { context, page: anon } = await anonymousPage(browser);
    await anon.goto("/login");
    await expect(anon.getByRole("link", { name: "使用 FakeIdP 登录" })).toBeVisible();
    await expect(anon.getByLabel("密码", { exact: true })).toHaveCount(0);
    const res = await anon.request.post("/api/v1/auth/login", { data: { username: "admin", password: "x" } });
    expect(res.status()).toBe(403);
    expect((await res.json()).msg).toBe("密码登录已关闭");
    await context.close();

    await sw.click();
    await expect(sw).toHaveAttribute("aria-checked", "true");
  });
});
