import { visitClassStrings } from "./class-strings.mjs";

// 颜色只来自 src/styles/globals.css 的语义 token：只有 token 能在深浅主题间正确切换。
const PALETTES = [
  "white",
  "black",
  "slate",
  "gray",
  "zinc",
  "neutral",
  "stone",
  "red",
  "orange",
  "amber",
  "yellow",
  "lime",
  "green",
  "emerald",
  "teal",
  "cyan",
  "sky",
  "blue",
  "indigo",
  "violet",
  "purple",
  "fuchsia",
  "pink",
  "rose",
].join("|");
const PREFIXES = [
  "bg",
  "text",
  "border",
  "border-[trblxy]",
  "ring",
  "ring-offset",
  "fill",
  "stroke",
  "divide",
  "from",
  "via",
  "to",
  "outline",
  "decoration",
  "placeholder",
  "caret",
  "accent",
  "shadow",
].join("|");

// 例：bg-red-500、dark:hover:text-white、bg-black/50
const PALETTE = new RegExp(`(?:^|[^\\w-])((?:[\\w-]+:)*(?:${PREFIXES})-(?:${PALETTES})(?:-\\d{2,3})?)(?=$|[^\\w-])`);
// 例：bg-[#fff]、text-[rgb(0,0,0)]、border-[hsl(...)]
const ARBITRARY = new RegExp(
  `(?:^|[^\\w-])((?:[\\w-]+:)*(?:${PREFIXES})-\\[(?:#[0-9a-fA-F]{3,8}|(?:rgb|rgba|hsl|hsla|oklch|oklab)\\()[^\\]]*\\])`
);
const STYLE_COLOR_KEYS = new Set([
  "color",
  "background",
  "backgroundColor",
  "borderColor",
  "outlineColor",
  "fill",
  "stroke",
  "caretColor",
]);
const COLOR_VALUE = /^\s*(#[0-9a-fA-F]{3,8}|(rgb|rgba|hsl|hsla|oklch|oklab)\(|(white|black)\s*$)/;

export default {
  meta: {
    type: "problem",
    docs: { description: "Disallow raw colours; use semantic design tokens." },
    schema: [],
    messages: {
      rawClass:
        "不要使用原始颜色 `{{ cls }}`，改用语义 token（bg-background / text-muted-foreground / bg-success-soft …，" +
        "定义在 src/styles/globals.css）——见 docs/design.md#core-constraints。",
      rawStyle:
        "不要在 style 中写死颜色 `{{ value }}`，改用语义 token 类名或 var(--token)。见 docs/design.md#core-constraints。",
    },
  },
  create(context) {
    return {
      ...visitClassStrings((node, text) => {
        const hit = PALETTE.exec(text) ?? ARBITRARY.exec(text);
        if (hit) context.report({ node, messageId: "rawClass", data: { cls: hit[1] } });
      }),
      Property(node) {
        const key = node.key.type === "Identifier" ? node.key.name : node.key.value;
        if (!STYLE_COLOR_KEYS.has(key)) return;
        if (
          node.value.type === "Literal" &&
          typeof node.value.value === "string" &&
          COLOR_VALUE.test(node.value.value)
        ) {
          context.report({ node: node.value, messageId: "rawStyle", data: { value: node.value.value } });
        }
      },
    };
  },
};
