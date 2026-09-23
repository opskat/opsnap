import { readFileSync } from "node:fs";

import { defineConfig } from "@playwright/test";

import base from "./playwright.config";

// 真实环境验证（不提交用例）：只有这里读取 e2e/.env，环境变量优先于文件。流程见 docs/verification.md。
function loadEnv() {
  try {
    for (const line of readFileSync(new URL(".env", import.meta.url), "utf8").split("\n")) {
      const m = line.match(/^\s*([A-Z0-9_]+)\s*=\s*(.*)\s*$/);
      if (m && process.env[m[1]] === undefined) process.env[m[1]] = m[2];
    }
  } catch {
    // 没有 .env 时只使用环境变量
  }
}
loadEnv();

export default defineConfig({
  ...base,
  testDir: "./scratch",
  testIgnore: [],
  globalSetup: undefined,
  use: { ...base.use, baseURL: process.env.OPSNAP_BASE_URL },
});
