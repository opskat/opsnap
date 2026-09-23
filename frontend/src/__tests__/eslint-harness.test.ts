import path from "node:path";

import { ESLint } from "eslint";
import { beforeAll, describe, expect, it } from "vitest";

// 按项目目录加载真实的 eslint.config.js 跑样例：同时验证规则逻辑正确、确实接入了配置、作用范围符合预期。
const frontendRoot = path.resolve(__dirname, "../..");
const eslint = new ESLint({ cwd: frontendRoot });

async function ruleIdsAt(code: string, filename: string) {
  const [result] = await eslint.lintText(code, { filePath: path.join(frontendRoot, filename) });
  const fatal = result.messages.find((m) => m.fatal);
  if (fatal) throw new Error(`样例解析失败（${filename}）：${fatal.message}`);
  return result.messages.map((m) => m.ruleId);
}

describe("守护规则", () => {
  beforeAll(() => ruleIdsAt("export const x = 1;\n", "src/pages/Warmup.tsx"));

  describe("禁止调色板颜色类名", () => {
    const RULE = "no-restricted-syntax";
    it.each([
      ['export const JobList = () => <div className="bg-red-500" />;\n', "调色板类名"],
      ["export const c = (x: string) => `text-gray-400 ${x}`;\n", "模板字符串"],
    ])("报告 %s（%s）", async (code) => {
      expect(await ruleIdsAt(code, "src/pages/Foo.tsx")).toContain(RULE);
    });
    it.each([
      'export const JobList = () => <div className="bg-background text-muted-foreground" />;\n',
      'export const JobList = () => <div className="bg-success-soft text-success" />;\n',
    ])("放行语义 token：%s", async (code) => {
      expect(await ruleIdsAt(code, "src/pages/Foo.tsx")).not.toContain(RULE);
    });
  });

  describe("JSX 中禁止写死中文", () => {
    const RULE = "i18next/no-literal-string";
    it.each([
      ["export const JobList = () => <p>新建任务</p>;\n", "JSX 文本"],
      ['export const JobList = () => <input placeholder="搜索" />;\n', "可见属性"],
    ])("报告 %s（%s）", async (code) => {
      expect(await ruleIdsAt(code, "src/pages/Foo.tsx")).toContain(RULE);
    });
    it.each([
      'export const JobList = () => <p className="font-mono">OpsNap</p>;\n',
      'export const JobList = ({ t }: { t: (k: string) => string }) => <p>{t("nav.jobs")}</p>;\n',
    ])("放行英文标识与 t()：%s", async (code) => {
      expect(await ruleIdsAt(code, "src/pages/Foo.tsx")).not.toContain(RULE);
    });
    it("测试文件不受限制", async () => {
      expect(
        await ruleIdsAt("export const JobList = () => <p>新建任务</p>;\n", "src/pages/Foo.test.tsx")
      ).not.toContain(RULE);
    });
  });

  describe("接口请求只走 request()", () => {
    const RULE = "no-restricted-globals";
    it("业务代码直接调用 fetch 会被报告", async () => {
      expect(await ruleIdsAt('export const f = () => fetch("/api/v1/x");\n', "src/pages/Foo.tsx")).toContain(RULE);
    });
    it("src/lib/api.ts 作为唯一封装点被放行", async () => {
      expect(await ruleIdsAt('export const f = () => fetch("/api/v1/x");\n', "src/lib/api.ts")).not.toContain(RULE);
    });
  });
});
