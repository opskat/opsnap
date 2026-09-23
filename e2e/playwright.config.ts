import { defineConfig, devices } from "@playwright/test";

import { SMOKE_PORT } from "./ports";

// 冒烟用例：提交进仓库、CI 可跑、完全隔离。真实环境验证见 playwright.scratch.config.ts 与 docs/verification.md。
export default defineConfig({
  testDir: "./tests",
  testIgnore: ["**/scratch/**"],
  workers: 1,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [["list"], ["html", { open: "never" }]] : "list",
  use: {
    baseURL: `http://127.0.0.1:${SMOKE_PORT}`,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
  // 启动隔离的被测实例；teardown 负责停止进程并删除临时数据
  globalSetup: "./global-setup.ts",
});
