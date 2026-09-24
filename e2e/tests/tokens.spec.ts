import { expect, request as playwrightRequest, test } from "@playwright/test";

test.describe("API 令牌", () => {
  test("生成后只显示一次；可调用业务接口但不能管理令牌；吊销后立即 401", async ({ page, baseURL }) => {
    await page.goto("/settings");
    await page.getByRole("button", { name: "生成令牌" }).click();
    const dialog = page.getByRole("dialog", { name: "生成 API 令牌" });
    await expect(dialog.getByRole("radio", { name: "90 天" })).toHaveAttribute("aria-checked", "true");
    await dialog.getByLabel("名称").fill("e2e-ci");
    await dialog.getByRole("button", { name: "生成" }).click();

    const created = page.getByRole("dialog", { name: "令牌已生成" });
    const token = (await created.locator("code").textContent())!.trim();
    expect(token).toMatch(/^onp_/);
    await created.getByRole("button", { name: "我已复制，关闭" }).click();
    const row = page.getByRole("row").filter({ hasText: "e2e-ci" });
    await expect(row).toContainText(token.slice(0, 8) + "…");
    await expect(page.getByText(token)).toHaveCount(0);

    const api = await playwrightRequest.newContext({
      baseURL,
      storageState: { cookies: [], origins: [] },
      extraHTTPHeaders: { Authorization: `Bearer ${token}` },
    });
    const me = await api.get("/api/v1/auth/me");
    expect(me.status()).toBe(200);
    expect((await me.json()).data.session).toBeUndefined();
    const manage = await api.get("/api/v1/tokens");
    expect(manage.status()).toBe(403);

    await page.reload();
    await expect(row).toContainText(/分钟前|刚刚/);

    await row.getByRole("button", { name: "吊销 e2e-ci" }).click();
    await page.getByRole("dialog", { name: "吊销令牌" }).getByRole("button", { name: "吊销" }).click();
    await expect(row).toContainText("已吊销");
    const after = await api.get("/api/v1/auth/me");
    expect(after.status()).toBe(401);
    expect((await after.json()).msg).toBe("令牌已吊销");
    await api.dispose();
  });
});
