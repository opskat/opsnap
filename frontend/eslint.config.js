import js from "@eslint/js";
import i18next from "eslint-plugin-i18next";
import prettier from "eslint-plugin-prettier/recommended";
import reactHooks from "eslint-plugin-react-hooks";
import reactRefresh from "eslint-plugin-react-refresh";
import reactX from "eslint-plugin-react-x";
import globals from "globals";
import tseslint from "typescript-eslint";

import opsnap from "./eslint-rules/index.mjs";

// 守护规则的说明见 docs/develop.md#enforced-rules；每条规则在 src/__tests__/eslint-harness.test.ts 中有守护测试。

export default tseslint.config(
  { ignores: ["dist", "node_modules"] },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  {
    languageOptions: { globals: globals.browser },
  },
  {
    // 构建脚本、配置文件与本项目 ESLint 规则运行在 Node 中
    files: ["scripts/**/*.mjs", "eslint-rules/**/*.mjs", "*.config.{js,ts}"],
    languageOptions: { globals: globals.node },
  },
  {
    plugins: {
      "react-hooks": reactHooks,
      "react-refresh": reactRefresh,
      "react-x": reactX,
      i18next,
      opsnap,
    },
    rules: {
      ...reactHooks.configs.recommended.rules,
      "react-refresh/only-export-components": ["error", { allowConstantExport: true }],
      "@typescript-eslint/no-unused-vars": ["error", { argsIgnorePattern: "^_", varsIgnorePattern: "^_" }],
      // React 19：不再使用 forwardRef、<Context.Provider>、useContext
      "react-x/no-forward-ref": "error",
      "react-x/no-context-provider": "error",
      "react-x/no-use-context": "error",
      "react-hooks/exhaustive-deps": "error",
    },
  },
  {
    files: ["src/**/*.{ts,tsx}"],
    rules: {
      // 设计系统：颜色、字号只用 token（docs/design.md#core-constraints）
      "opsnap/no-raw-color": "error",
      "opsnap/no-arbitrary-font-size": "error",
      "opsnap/no-dark-variant": "error",
      // 界面文案走 i18n：JSX 文本与可见属性中出现汉字即报错，英文标识与符号不受影响（docs/develop.md#internationalization）。
      // 插件会把字符串排除规则转换为不带 u 标志的 RegExp，所以用 Unicode 区间表示汉字
      "i18next/no-literal-string": [
        "error",
        {
          mode: "jsx-only",
          "jsx-attributes": { include: ["aria-label", "title", "placeholder", "alt"] },
          words: { exclude: ["^[^\\u3400-\\u9fff\\uf900-\\ufaff]*$"] },
        },
      ],
      // 接口请求只走 src/lib/api.ts 的 request()：统一 /api/v1 前缀、语言头和错误转换
      "no-restricted-globals": [
        "error",
        {
          name: "fetch",
          message: "用 src/lib/api.ts 的 request() 发请求，不直接调用 fetch（docs/develop.md#api-requests）。",
        },
      ],
    },
  },
  {
    // request() 是 fetch 的唯一封装点
    files: ["src/lib/api.ts"],
    rules: { "no-restricted-globals": "off" },
  },
  {
    // shadcn 生成的组件：同时导出组件与 variants 是其固定写法；其内置的 dark: 变体只引用 token，保留原样便于跟进上游
    files: ["src/components/ui/**/*.tsx"],
    rules: {
      "react-refresh/only-export-components": "off",
      "opsnap/no-dark-variant": "off",
    },
  },
  {
    // 测试需要写违规样例和中文断言；守护测试自身必须包含违规 fixture 字符串
    files: ["src/**/*.test.{ts,tsx}"],
    rules: {
      "opsnap/no-raw-color": "off",
      "opsnap/no-arbitrary-font-size": "off",
      "opsnap/no-dark-variant": "off",
      "i18next/no-literal-string": "off",
      "no-restricted-globals": "off",
    },
  },
  prettier
);
