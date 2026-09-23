import { visitClassStrings } from "./class-strings.mjs";

// 字号只来自 src/styles/globals.css 定义的字号阶梯（text-2xs … text-3xl）。
const ARBITRARY_SIZE = /(?:^|[^\w-])((?:[\w-]+:)*text-\[(?:length:)?[\d.]+(?:px|rem|em)\])/;

export default {
  meta: {
    type: "problem",
    docs: { description: "Disallow arbitrary font sizes; use the type scale." },
    schema: [],
    messages: {
      arbitrary:
        "不要使用任意字号 `{{ cls }}`，改用字号阶梯 text-2xs / text-xs / text-sm / text-base / text-md / text-lg / " +
        "text-xl / text-2xl / text-3xl——见 docs/design.md#typography。",
    },
  },
  create(context) {
    return visitClassStrings((node, text) => {
      const hit = ARBITRARY_SIZE.exec(text);
      if (hit) context.report({ node, messageId: "arbitrary", data: { cls: hit[1] } });
    });
  },
};
