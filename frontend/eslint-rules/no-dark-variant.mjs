import { visitClassStrings } from "./class-strings.mjs";

// 深浅主题的差异放在 token 取值里，业务代码不用 dark: 变体分叉样式。
const DARK = /(?:^|[^\w-])((?:[\w-]+:)*dark:[\w[\]#/.%-]+)/;

export default {
  meta: {
    type: "problem",
    docs: { description: "Disallow dark: variants in app code; put theme differences in tokens." },
    schema: [],
    messages: {
      dark:
        "不要在业务代码中使用 `{{ cls }}`：深浅主题的差异放在 src/styles/globals.css 的 token 取值里，" +
        "组件只写一套类名——见 docs/design.md#theme-and-tokens。",
    },
  },
  create(context) {
    return visitClassStrings((node, text) => {
      const hit = DARK.exec(text);
      if (hit) context.report({ node, messageId: "dark", data: { cls: hit[1] } });
    });
  },
};
