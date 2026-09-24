import path from "node:path";

import { ESLint } from "eslint";
import { beforeAll, describe, expect, it } from "vitest";

// 按项目目录加载真实的 eslint.config.js 跑样例：同时验证规则逻辑正确、确实接入了配置、作用范围符合预期。
// 三个方向都要覆盖：违规被报告、合规写法不被误报、豁免路径不被检查。
const frontendRoot = path.resolve(__dirname, "../..");
const eslint = new ESLint({ cwd: frontendRoot });

async function ruleIdsAt(code: string, filename: string) {
  const [result] = await eslint.lintText(code, { filePath: path.join(frontendRoot, filename) });
  const fatal = result.messages.find((m) => m.fatal);
  if (fatal) throw new Error(`样例解析失败（${filename}）：${fatal.message}`);
  return result.messages.map((m) => m.ruleId);
}

const jsx = (className: string) => `export const JobList = () => <div className="${className}" />;\n`;
const PAGE = "src/pages/Foo.tsx";

describe("守护规则", () => {
  // 首次加载配置与类型信息较慢，机器负载高时会超过默认的 10 秒 hook 超时
  beforeAll(() => ruleIdsAt("export const x = 1;\n", PAGE), 60_000);

  describe("opsnap/no-raw-color", () => {
    const RULE = "opsnap/no-raw-color";
    it.each([
      [jsx("bg-red-500"), "调色板类名"],
      [jsx("text-white"), "white/black 不带色号"],
      [jsx("bg-black/50"), "带透明度"],
      [jsx("hover:border-gray-200"), "带变体前缀"],
      [jsx("bg-[#0b0d0e]"), "任意十六进制色值"],
      [jsx("text-[rgb(0,0,0)]"), "任意 rgb 色值"],
      ["export const c = (x: string) => `text-gray-400 ${x}`;\n", "模板字符串"],
      ['export const JobList = () => <div style={{ color: "#fff" }} />;\n', "style 中写死颜色"],
    ])("报告 %s（%s）", async (code) => {
      expect(await ruleIdsAt(code, PAGE)).toContain(RULE);
    });
    it.each([
      jsx("bg-background text-muted-foreground"),
      jsx("bg-primary/90 text-primary-foreground hover:bg-primary"),
      jsx("bg-success-soft text-success border-border"),
      jsx("text-sm font-mono"),
      'export const JobList = () => <div style={{ color: "var(--success)" }} />;\n',
    ])("放行语义 token：%s", async (code) => {
      expect(await ruleIdsAt(code, PAGE)).not.toContain(RULE);
    });
    it("测试文件不受限制", async () => {
      expect(await ruleIdsAt(jsx("bg-red-500"), "src/pages/Foo.test.tsx")).not.toContain(RULE);
    });
  });

  describe("opsnap/no-arbitrary-font-size", () => {
    const RULE = "opsnap/no-arbitrary-font-size";
    it.each([jsx("text-[13px]"), jsx("md:text-[0.8rem]"), jsx("text-[length:14px]")])("报告 %s", async (code) => {
      expect(await ruleIdsAt(code, PAGE)).toContain(RULE);
    });
    it.each([jsx("text-sm"), jsx("text-2xs text-3xl"), jsx("text-muted-foreground")])(
      "放行字号阶梯 %s",
      async (code) => {
        expect(await ruleIdsAt(code, PAGE)).not.toContain(RULE);
      }
    );
  });

  describe("opsnap/no-dark-variant", () => {
    const RULE = "opsnap/no-dark-variant";
    it("业务代码中的 dark: 变体会被报告", async () => {
      expect(await ruleIdsAt(jsx("bg-card dark:bg-muted"), PAGE)).toContain(RULE);
    });
    it("不含 dark: 的类名不被误报", async () => {
      expect(await ruleIdsAt(jsx("bg-card darken-none"), PAGE)).not.toContain(RULE);
    });
    it("shadcn 生成的组件被豁免", async () => {
      expect(await ruleIdsAt(jsx("bg-card dark:bg-input/30"), "src/components/ui/button.tsx")).not.toContain(RULE);
    });
  });

  describe("i18next/no-literal-string（JSX 中禁止写死中文）", () => {
    const RULE = "i18next/no-literal-string";
    it.each([
      ["export const JobList = () => <p>新建任务</p>;\n", "JSX 文本"],
      ['export const JobList = () => <input placeholder="搜索" />;\n', "可见属性"],
    ])("报告 %s（%s）", async (code) => {
      expect(await ruleIdsAt(code, PAGE)).toContain(RULE);
    });
    it.each([
      'export const JobList = () => <p className="font-mono">OpsNap</p>;\n',
      'export const JobList = ({ t }: { t: (k: string) => string }) => <p>{t("nav.jobs")}</p>;\n',
    ])("放行英文标识与 t()：%s", async (code) => {
      expect(await ruleIdsAt(code, PAGE)).not.toContain(RULE);
    });
    it("测试文件不受限制", async () => {
      expect(
        await ruleIdsAt("export const JobList = () => <p>新建任务</p>;\n", "src/pages/Foo.test.tsx")
      ).not.toContain(RULE);
    });
  });

  describe("no-restricted-globals（接口请求只走 request()）", () => {
    const RULE = "no-restricted-globals";
    const code = 'export const f = () => fetch("/api/v1/x");\n';
    it("业务代码直接调用 fetch 会被报告", async () => {
      expect(await ruleIdsAt(code, PAGE)).toContain(RULE);
    });
    it("src/lib/api.ts 作为唯一封装点被放行", async () => {
      expect(await ruleIdsAt(code, "src/lib/api.ts")).not.toContain(RULE);
    });
  });
});
