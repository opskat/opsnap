import { defineConfig, devices } from "@playwright/test";

import { SMOKE_PORT } from "./ports";

export const ADMIN_STATE = ".auth/admin.json";

// 冒烟用例：提交进仓库、CI 可跑、完全隔离。真实环境验证见 playwright.scratch.config.ts 与 docs/verification.md。
// 项目按依赖顺序执行：setup（界面上完成首次设置并保存登录态）→ chromium（其余用例）→ logout（会让会话失效，最后执行）
export default defineConfig({
  testDir: "./tests",
  testIgnore: ["**/scratch/**"],
  workers: 1,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [["list"], ["html", { open: "never" }]] : "list",
  use: {
    baseURL: `http://127.0.0.1:${SMOKE_PORT}`,
    // 用例按中文界面断言；切换语言的用例自行切换
    locale: "zh-CN",
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  projects: [
    { name: "setup", testMatch: /setup\.spec\.ts/, use: { ...devices["Desktop Chrome"] } },
    {
      name: "chromium",
      testIgnore: [/setup\.spec\.ts/, /logout\.spec\.ts/],
      dependencies: ["setup"],
      use: { ...devices["Desktop Chrome"], storageState: ADMIN_STATE },
    },
    {
      name: "logout",
      testMatch: /logout\.spec\.ts/,
      dependencies: ["chromium"],
      use: { ...devices["Desktop Chrome"], storageState: ADMIN_STATE },
    },
  ],
  // 启动隔离的被测实例；teardown 负责停止进程并删除临时数据
  globalSetup: "./global-setup.ts",
});
