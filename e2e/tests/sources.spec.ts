import { expect, test, type APIRequestContext, type Page } from "@playwright/test";

import {
  FAKE_SSH_JUMP_CONTROL_PORT,
  FAKE_SSH_JUMP_PORT,
  FAKE_SSH_PASSWORD,
  FAKE_SSH_TARGET_PORT,
  FAKE_SSH_USER,
} from "../ports";

// 数据源与网络通道（docs/specs/2026-09-25-datasources.md）：用两个假 SSH 服务端（bin/fakessh，
// e2e/global-setup.ts 启动）代替真实主机——一个作跳板（网络通道），一个作服务器文件数据源的目标，
// 经跳板转发到达。MySQL / PostgreSQL 数据源不进 CI（没有真实数据库），由运行时验证覆盖
// （docs/verification.md#test-environment）。
test.describe.configure({ mode: "serial" });

const CHANNEL_NAME = "e2e 跳板";
const DATASOURCE_NAME = "e2e 服务器文件";
const FINGERPRINT = /SHA256:[A-Za-z0-9+/]+/;

function row(page: Page, name: string) {
  return page.getByRole("row").filter({ hasText: name });
}

async function channelsTab(page: Page) {
  await page.goto("/sources");
  await page.getByRole("tab", { name: /网络通道/ }).click();
}

async function dataSourcesTab(page: Page) {
  await page.goto("/sources");
  await page.getByRole("tab", { name: /^数据源/ }).click();
}

/** 提交后如果（等一等）弹出「确认主机密钥」，核对指纹格式后信任并继续；这一跳的密钥已确认过则不会弹出 */
async function trustHostKeyIfPrompted(page: Page) {
  const dialog = page.getByRole("dialog", { name: "确认主机密钥" });
  const appeared = await dialog
    .waitFor({ state: "visible", timeout: 5_000 })
    .then(() => true)
    .catch(() => false);
  if (!appeared) return;
  await expect(dialog.getByText(FINGERPRINT)).toBeVisible();
  await dialog.getByRole("button", { name: "信任并继续" }).click();
}

async function rotateJumpHostKey(request: APIRequestContext) {
  const res = await request.post(`http://127.0.0.1:${FAKE_SSH_JUMP_CONTROL_PORT}/rotate-host-key`);
  expect(res.status()).toBe(204);
}

test.describe("数据源 · 网络通道与服务器文件", () => {
  test("新建 SSH 通道并确认主机密钥", async ({ page }) => {
    await channelsTab(page);
    // 空状态下页头与空状态区各有一个「新建通道」按钮
    await page.getByRole("button", { name: "新建通道" }).first().click();
    const dialog = page.getByRole("dialog", { name: "新建网络通道" });
    await dialog.getByLabel("名称").fill(CHANNEL_NAME);
    await dialog.getByLabel("主机").fill("127.0.0.1");
    await dialog.getByLabel("端口").fill(String(FAKE_SSH_JUMP_PORT));
    await dialog.getByLabel("用户名").fill(FAKE_SSH_USER);
    await dialog.getByLabel("密码", { exact: true }).fill(FAKE_SSH_PASSWORD);

    await dialog.getByRole("button", { name: "保存" }).click();
    const confirm = page.getByRole("dialog", { name: "确认主机密钥" });
    await expect(confirm).toContainText("首次连接");
    await expect(confirm.getByText(FINGERPRINT)).toBeVisible();
    await confirm.getByRole("button", { name: "信任并继续" }).click();

    await expect(dialog).toHaveCount(0);
    await expect(row(page, CHANNEL_NAME)).toContainText("正常");
    await expect(row(page, CHANNEL_NAME)).toContainText("SSH 跳板");
  });

  test("经通道新建服务器文件数据源，确认目标主机密钥后查看探测结果", async ({ page }) => {
    await dataSourcesTab(page);
    // 空状态下页头与空状态区各有一个「新建数据源」按钮
    await page.getByRole("button", { name: "新建数据源" }).first().click();
    const dialog = page.getByRole("dialog", { name: "新建数据源" });
    await dialog.getByRole("radio", { name: "服务器文件" }).click();
    await dialog.getByLabel("名称").fill(DATASOURCE_NAME);
    await dialog.getByLabel("主机").fill("127.0.0.1");
    await dialog.getByLabel("端口").fill(String(FAKE_SSH_TARGET_PORT));
    await dialog.getByLabel("用户名").fill(FAKE_SSH_USER);
    await dialog.getByLabel("密码", { exact: true }).fill(FAKE_SSH_PASSWORD);
    await dialog.getByRole("combobox", { name: "网络通道" }).click();
    await page.getByRole("option", { name: CHANNEL_NAME }).click();
    await expect(dialog).toContainText(`链路：OpsNap → ${CHANNEL_NAME} (SSH)`);

    await dialog.getByRole("button", { name: "保存" }).click();
    await trustHostKeyIfPrompted(page);

    await expect(dialog).toHaveCount(0);
    await expect(row(page, DATASOURCE_NAME)).toContainText("服务器文件");
    await expect(row(page, DATASOURCE_NAME)).toContainText("正常");
    // 探测在保存后于后台进行；列表只在加载时取一次数据，不会自己刷新，所以重新进入分页来看结果。
    // 针对假 SSH 服务端应很快完成：3 项通过（SSH 可达、CPU 架构、临时目录可执行），免密 sudo 得到一条提醒
    await expect(async () => {
      await dataSourcesTab(page);
      await expect(row(page, DATASOURCE_NAME)).toContainText("3 通过 · 1 提醒");
    }).toPass({ timeout: 15_000 });
  });

  test("详情页：连接链路含指纹，能力探测列出四项与修复方法", async ({ page }) => {
    await dataSourcesTab(page);
    await row(page, DATASOURCE_NAME).getByRole("link", { name: DATASOURCE_NAME }).click();
    await expect(page.getByRole("heading", { level: 1 })).toHaveText(DATASOURCE_NAME);

    const connection = page.getByRole("region", { name: "连接" });
    await expect(connection).toContainText("服务器文件");
    const chain = connection.getByRole("list", { name: "网络链路" });
    await expect(chain).toContainText(CHANNEL_NAME);
    await expect(chain.getByText(FINGERPRINT).first()).toBeVisible();

    const probe = page.getByRole("region", { name: "能力探测" });
    await expect(probe).toContainText("SSH 可达");
    await expect(probe).toContainText("CPU 架构");
    await expect(probe).toContainText("临时目录可执行");
    await expect(probe).toContainText("免密 sudo");
    // 免密 sudo 未通过（假服务端总是拒绝），修复方法里带着 sudoers 提示
    await expect(probe).toContainText("sudoers");
  });

  test("跳板主机密钥变化后：通道与数据源都标记变化，重新确认通道后一起恢复正常", async ({ page, request }) => {
    await rotateJumpHostKey(request);

    // 测试数据源时途经跳板，发现密钥已变化：数据源标记 host_key_changed，并指出是这个通道
    await dataSourcesTab(page);
    await page.getByRole("button", { name: `测试连接 ${DATASOURCE_NAME}` }).click();
    await expect(row(page, DATASOURCE_NAME)).toContainText("主机密钥已变化");

    // 途经它的连接检测到密钥变化时，通道自己也会被记为已变化；重新进入通道分页看最新状态
    await channelsTab(page);
    await expect(row(page, CHANNEL_NAME)).toContainText("主机密钥已变化");
    await page.getByRole("button", { name: `重新确认 ${CHANNEL_NAME} 的主机密钥` }).click();
    const changed = page.getByRole("dialog", { name: "主机密钥已变化" });
    await expect(changed).toContainText(CHANNEL_NAME);
    await expect(changed.getByText("保存的密钥")).toBeVisible();
    // 保存的密钥与现在出示的密钥两个指纹都要有值（不是空的）
    await expect(changed.getByText(FINGERPRINT)).toHaveCount(2);
    await changed.getByRole("button", { name: "信任新密钥" }).click();
    await expect(changed).toHaveCount(0);
    await expect(row(page, CHANNEL_NAME)).toContainText("正常");

    // 信任通道新密钥的请求内会同步重测经过它的数据源
    await dataSourcesTab(page);
    await expect(row(page, DATASOURCE_NAME)).toContainText("正常");
  });

  test("仍被数据源引用的通道不能删除", async ({ page }) => {
    await channelsTab(page);
    await page.getByRole("button", { name: `${CHANNEL_NAME} 的更多操作` }).click();
    const deleteItem = page.getByRole("menuitem").filter({ hasText: "删除通道" });
    await expect(deleteItem).toContainText(`仍被 ${DATASOURCE_NAME} 使用，不能删除`);
    await expect(deleteItem).toHaveAttribute("data-disabled");
    await page.keyboard.press("Escape");
  });

  test("英文界面与深色主题下的数据源页", async ({ page }) => {
    await dataSourcesTab(page);
    await page.getByRole("button", { name: "EN" }).click();
    await page.getByRole("button", { name: /深色|Dark/ }).click();
    await expect(page.locator("html")).toHaveClass(/dark/);
    await expect(page.getByRole("heading", { level: 1 })).toHaveText("Sources");
    await expect(page.getByRole("tab", { name: /Network Channels/ })).toBeVisible();
    await expect(row(page, DATASOURCE_NAME)).toContainText("OK");
    await page.getByRole("button", { name: "New Data Source" }).first().click();
    await expect(page.getByRole("dialog", { name: "New Data Source" })).toContainText("Network channel");
  });
});
