// 守卫测试：经由真实的 eslint.config.js 断言门禁两个方向都成立——
// 违规代码必须报出，合规代码不能报出。规则说明见 docs/develop.md「前端检查」。
import { ESLint } from "eslint";
import { describe, expect, it } from "vitest";

// vitest 在 frontend/ 下运行，ESLint 以当前目录读取 eslint.config.js
const eslint = new ESLint();

async function ruleIds(code: string, file = "src/guard-sample.tsx") {
  const [result] = await eslint.lintText(code, { filePath: file });
  return result.messages.map((m) => m.ruleId);
}

// 冷启动 ESLint（加载配置与 TypeScript 解析器）在机器繁忙时会超过默认的 5 秒
const TIMEOUT_MS = 30_000;

describe("ESLint 门禁", { timeout: TIMEOUT_MS }, () => {
  it("报出 effect 依赖遗漏", async () => {
    const ids = await ruleIds(
      `import * as React from "react";\n\nexport function A({ id }: { id: number }) {\n  React.useEffect(() => {\n    console.log(id);\n  }, []);\n  return null;\n}\n`
    );
    expect(ids).toContain("react-hooks/exhaustive-deps");
  });

  it("报出未使用的变量", async () => {
    const ids = await ruleIds(`export function B() {\n  const unused = 1;\n  return null;\n}\n`);
    expect(ids).toContain("@typescript-eslint/no-unused-vars");
  });

  it("报出不符合 Prettier 的格式", async () => {
    const ids = await ruleIds(`export const c = { a:1 }\n`);
    expect(ids).toContain("prettier/prettier");
  });

  it("合规代码不报", async () => {
    const ids = await ruleIds(
      `import * as React from "react";\n\nexport function D({ id }: { id: number }) {\n  React.useEffect(() => {\n    console.log(id);\n  }, [id]);\n  return null;\n}\n`
    );
    expect(ids).toEqual([]);
  });
});
