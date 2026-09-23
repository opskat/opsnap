// 公共辅助：遍历所有可能携带 Tailwind 类名的字符串（普通字面量与模板字符串片段）。
export function visitClassStrings(check) {
  return {
    Literal(node) {
      if (typeof node.value === "string") check(node, node.value);
    },
    TemplateElement(node) {
      check(node, node.value.raw);
    },
  };
}
