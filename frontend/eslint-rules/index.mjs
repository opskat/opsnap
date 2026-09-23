// 本项目的 ESLint 插件：设计系统守护规则（docs/design.md#core-constraints）。
// 每条规则在 src/__tests__/eslint-harness.test.ts 中有守护测试。
import noArbitraryFontSize from "./no-arbitrary-font-size.mjs";
import noDarkVariant from "./no-dark-variant.mjs";
import noRawColor from "./no-raw-color.mjs";

export default {
  meta: { name: "opsnap" },
  rules: {
    "no-raw-color": noRawColor,
    "no-arbitrary-font-size": noArbitraryFontSize,
    "no-dark-variant": noDarkVariant,
  },
};
