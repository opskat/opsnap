import js from "@eslint/js";
import i18next from "eslint-plugin-i18next";
import prettier from "eslint-plugin-prettier/recommended";
import reactHooks from "eslint-plugin-react-hooks";
import reactRefresh from "eslint-plugin-react-refresh";
import reactX from "eslint-plugin-react-x";
import globals from "globals";
import tseslint from "typescript-eslint";

// 守护规则的说明见 docs/develop.md「代码规范与守护规则」；每条规则在 src/__tests__/eslint-harness.test.ts 有对应测试。

// 设计 token：组件只用语义 token（text-muted-foreground / bg-success-soft …，定义在 src/styles/globals.css），
// 不写 Tailwind 调色板类名——一个颜色只在一处定义，深浅主题才都成立（docs/design.md）。
const paletteColors =
  "red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose|slate|gray|zinc|neutral|stone";
const paletteClassPattern = `(^|[^-\\w])(text|bg|border|ring|fill|stroke)-(${paletteColors})-[0-9]{2,3}([^-\\w]|$)`;
const paletteMessage =
  "不写调色板类名，用语义 token（text-muted-foreground / bg-success-soft …，定义在 src/styles/globals.css）——见 docs/design.md。";
const paletteRestrictions = [
  { selector: `Literal[value=/${paletteClassPattern}/]`, message: paletteMessage },
  { selector: `TemplateElement[value.raw=/${paletteClassPattern}/]`, message: paletteMessage },
];

export default tseslint.config(
  { ignores: ["dist", "node_modules"] },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  {
    languageOptions: { globals: globals.browser },
  },
  {
    // 构建脚本与配置文件运行在 Node 中
    files: ["scripts/**/*.mjs", "*.config.{js,ts}"],
    languageOptions: { globals: globals.node },
  },
  {
    plugins: {
      "react-hooks": reactHooks,
      "react-refresh": reactRefresh,
      "react-x": reactX,
      i18next,
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
      "no-restricted-syntax": ["error", ...paletteRestrictions],
      // 界面文案走 i18n：JSX 中出现汉字即报错（英文标识符、符号不受影响），见 docs/develop.md「国际化」
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
          message: "用 src/lib/api.ts 的 request() 发请求，不直接调用 fetch（docs/develop.md「接口请求」）。",
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
    // shadcn 生成的组件同时导出组件与 variants，属于其固定写法
    files: ["src/components/ui/**/*.tsx"],
    rules: { "react-refresh/only-export-components": "off" },
  },
  {
    // 测试需要写违规样例和中文断言；守护测试自身必须包含违规 fixture 字符串
    files: ["src/**/*.test.{ts,tsx}"],
    rules: {
      "no-restricted-syntax": "off",
      "i18next/no-literal-string": "off",
      "no-restricted-globals": "off",
    },
  },
  prettier
);
