import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { expect, test, type Page } from "@playwright/test";

import { ADMIN_PASSWORD } from "./credentials";

// 存储的本地目录流程（docs/specs/2026-09-24-storage.md）：被测实例与用例在同一台机器上，
// 用例在临时目录里准备位置，并直接检查磁盘上的 kopia 仓库文件作为独立判据。S3 由运行时验证覆盖。
test.describe.configure({ mode: "serial" });

const isRepo = (dir: string) => existsSync(join(dir, "kopia.repository.f"));

let base: string;
let repoDir: string;
let key: string;

test.beforeAll(() => {
  base = mkdtempSync(join(tmpdir(), "opsnap-e2e-storage-"));
  repoDir = join(base, "repo-a");
});
test.afterAll(() => rmSync(base, { recursive: true, force: true }));

async function openCreate(page: Page) {
  await page.goto("/storage");
  await page.getByRole("button", { name: "新建存储" }).first().click();
  return page.getByRole("dialog", { name: "新建存储" });
}

function row(page: Page, name: string) {
  return page.getByRole("row").filter({ hasText: name });
}

test.describe("存储 · 本地目录", () => {
  test("没有存储时显示空状态", async ({ page }) => {
    await page.goto("/storage");
    await expect(page.getByText("还没有存储")).toBeVisible();
  });

  test("通过选择目录新建文件夹，设置系统生成的密钥并下载密钥文件", async ({ page }) => {
    const dialog = await openCreate(page);
    await dialog.getByLabel("名称").fill("e2e 本地");
    await dialog.getByLabel("目录路径").fill(base);
    await dialog.getByRole("button", { name: "浏览…" }).click();

    const picker = page.getByRole("dialog", { name: "选择目录" });
    await expect(picker.getByLabel("当前选择")).toHaveText(base);
    await picker.getByLabel("新文件夹名称").fill("repo-a");
    await picker.getByRole("button", { name: "新建文件夹" }).click();
    await expect(picker.getByLabel("当前选择")).toHaveText(repoDir);
    await picker.getByRole("button", { name: "上一级" }).click();
    await expect(picker.getByRole("button", { name: /repo-a\s*空目录/ })).toBeVisible();
    await picker.getByRole("button", { name: /repo-a/ }).click();
    await picker.getByRole("button", { name: "选择此目录" }).click();
    await expect(dialog.getByLabel("目录路径")).toHaveValue(repoDir);

    await dialog.getByRole("button", { name: "测试连接" }).click();
    await expect(dialog.getByRole("status")).toContainText("目标位置为空");
    await dialog.getByRole("button", { name: /下一步：设置加密密钥/ }).click();

    const setKey = page.getByRole("dialog", { name: "设置加密密钥" });
    const code = setKey.getByLabel("密钥", { exact: true });
    await expect(code).toHaveText(/^[A-Za-z0-9]{4}(-[A-Za-z0-9]{4}){5}$/);
    key = (await code.textContent())!.trim();

    const downloading = page.waitForEvent("download");
    await setKey.getByRole("button", { name: "下载密钥文件" }).click();
    const download = await downloading;
    expect(download.suggestedFilename()).toBe("opsnap-e2e-本地-key.txt");
    const content = readFileSync((await download.path())!, "utf8");
    expect(content).toContain(`Key: ${key}`);
    expect(content).toContain(`kopia repository connect filesystem --path ${repoDir}`);

    await expect(setKey.getByRole("button", { name: "启用加密" })).toBeDisabled();
    expect(isRepo(repoDir), "确认前不建库").toBe(false);
    await setKey.getByRole("checkbox", { name: /我已保存这把密钥/ }).check();
    await setKey.getByRole("button", { name: "启用加密" }).click();

    await expect(row(page, "e2e 本地")).toContainText("正常");
    await expect(row(page, "e2e 本地")).toContainText(repoDir);
    expect(isRepo(repoDir)).toBe(true);
  });

  test("不为空也不是仓库的目录被拒绝，目录保持原样", async ({ page }) => {
    const dir = mkdtempSync(join(base, "full-"));
    writeFileSync(join(dir, "notes.txt"), "x");
    const dialog = await openCreate(page);
    await dialog.getByLabel("名称").fill("e2e 非空");
    await dialog.getByLabel("目录路径").fill(dir);
    await dialog.getByRole("button", { name: "下一步" }).click();
    await expect(dialog.getByRole("alert")).toContainText("目标位置不为空，且不是 kopia 仓库");
    expect(isRepo(dir)).toBe(false);
  });

  test("查看密钥需要再次验证登录密码", async ({ page }) => {
    await page.goto("/storage");
    await page.getByRole("button", { name: "e2e 本地 的更多操作" }).click();
    await page.getByRole("menuitem", { name: "查看密钥" }).click();
    const dialog = page.getByRole("dialog", { name: "查看「e2e 本地」的密钥" });
    await dialog.getByLabel("当前登录密码").fill("wrong-password");
    await dialog.getByRole("button", { name: "验证并查看" }).click();
    await expect(dialog.getByText("登录密码不正确")).toBeVisible();

    await dialog.getByLabel("当前登录密码").fill(ADMIN_PASSWORD);
    await dialog.getByRole("button", { name: "验证并查看" }).click();
    const shown = page.getByRole("dialog", { name: "「e2e 本地」的仓库密钥" });
    await expect(shown.getByLabel("密钥", { exact: true })).toHaveText(key);
    await shown.getByRole("button", { name: "完成" }).click();
    await expect(page.getByText(key)).toHaveCount(0);
  });

  test("更改位置到空目录：确认后用当前密钥在新位置建库，原仓库保持不动", async ({ page }) => {
    const moved = join(base, "repo-b");
    await page.goto("/storage");
    await page.getByRole("button", { name: "编辑 e2e 本地" }).click();
    const dialog = page.getByRole("dialog", { name: "编辑存储" });
    await dialog.getByLabel("目录路径").fill(moved);
    await dialog.getByRole("button", { name: "保存" }).click();
    const confirm = page.getByRole("dialog", { name: "更改存储位置？" });
    await expect(confirm).toContainText(`位置从 ${repoDir} 改为 ${moved}`);
    await confirm.getByRole("button", { name: "确认并继续" }).click();
    await expect(row(page, "e2e 本地")).toContainText(moved);
    expect(isRepo(moved)).toBe(true);
    expect(isRepo(repoDir)).toBe(true);
  });

  test("删除只删记录；重新添加同一位置需要密钥，错误密钥累计失败次数", async ({ page }) => {
    const moved = join(base, "repo-b");
    await page.goto("/storage");
    await page.getByRole("button", { name: "e2e 本地 的更多操作" }).click();
    await page.getByRole("menuitem", { name: "删除存储" }).click();
    const confirm = page.getByRole("dialog", { name: "删除存储「e2e 本地」？" });
    await expect(confirm).toContainText("存储里的备份数据不会被删除");
    await confirm.getByRole("button", { name: "删除存储" }).click();
    await expect(page.getByText("还没有存储")).toBeVisible();
    expect(isRepo(moved), "数据保留").toBe(true);

    const dialog = await openCreate(page);
    await dialog.getByLabel("名称").fill("e2e 重新添加");
    await dialog.getByLabel("目录路径").fill(moved);
    await dialog.getByRole("button", { name: "下一步" }).click();
    const unlock = page.getByRole("dialog", { name: "解锁已有仓库" });
    await expect(unlock).toContainText("创建于");
    await unlock.getByLabel("密钥", { exact: true }).fill("not-the-right-key");
    await unlock.getByRole("button", { name: "解锁并继续" }).click();
    await expect(unlock.getByText("密钥不正确，无法解开这个仓库。已连续 1 次失败。")).toBeVisible();
    await unlock.getByRole("button", { name: "解锁并继续" }).click();
    await expect(unlock.getByText("已连续 2 次失败。", { exact: false })).toBeVisible();

    await unlock.getByLabel("密钥", { exact: true }).fill(key);
    await unlock.getByRole("button", { name: "解锁并继续" }).click();
    await expect(unlock.getByText("已解锁，仓库中有 0 个快照。")).toBeVisible();
    await unlock.getByRole("button", { name: "完成" }).click();
    await expect(row(page, "e2e 重新添加")).toContainText("正常");
  });

  test("英文界面与深色主题下的存储页", async ({ page }) => {
    await page.goto("/storage");
    await page.getByRole("button", { name: "EN" }).click();
    await page.getByRole("button", { name: /深色|Dark/ }).click();
    await expect(page.locator("html")).toHaveClass(/dark/);
    await expect(page.getByRole("heading", { level: 1 })).toHaveText("Storage");
    await expect(row(page, "e2e 重新添加")).toContainText("OK");
    await page.getByRole("button", { name: "Test connection to e2e 重新添加" }).click();
    await expect(row(page, "e2e 重新添加")).toContainText(/Tested (Just now|\d+ min ago)/);
    await page.getByRole("button", { name: "New storage" }).first().click();
    await expect(page.getByRole("dialog", { name: "New storage" })).toContainText("Directory path");
  });
});
