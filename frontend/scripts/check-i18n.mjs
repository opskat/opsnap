// 检查 i18n 键的完整性：各语言文件的键集合一致；源码中字面量 t("a.b") 引用的键在每个语言文件中都存在。
// 动态键（t(item.label)、模板字符串）无法静态检查，由类型与评审保证。接入 pnpm lint。
import { readdirSync, readFileSync } from "node:fs";
import { join, relative } from "node:path";
import { fileURLToPath } from "node:url";

function flatten(obj, prefix = "") {
  return Object.entries(obj).flatMap(([k, v]) =>
    v && typeof v === "object" ? flatten(v, `${prefix}${k}.`) : [`${prefix}${k}`]
  );
}

function walk(dir) {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = join(dir, e.name);
    if (e.isDirectory()) return walk(p);
    return /\.(ts|tsx)$/.test(e.name) && !/\.test\.tsx?$/.test(e.name) ? [p] : [];
  });
}

const T_CALL = /\bt\(\s*["']([\w.-]+)["']/g;

/** 返回问题列表；为空表示通过 */
export function checkI18n(root) {
  const localeDir = join(root, "src/i18n/locales");
  const locales = Object.fromEntries(
    readdirSync(localeDir)
      .filter((f) => f.endsWith(".json"))
      .map((f) => [f, new Set(flatten(JSON.parse(readFileSync(join(localeDir, f), "utf8"))))])
  );
  const problems = [];
  const all = new Set(Object.values(locales).flatMap((s) => [...s]));
  for (const [file, keys] of Object.entries(locales)) {
    for (const key of all) if (!keys.has(key)) problems.push(`${file} 缺少键 ${key}`);
  }
  for (const file of walk(join(root, "src"))) {
    const text = readFileSync(file, "utf8");
    for (const m of text.matchAll(T_CALL)) {
      for (const [locale, keys] of Object.entries(locales)) {
        if (!keys.has(m[1])) problems.push(`${relative(root, file)} 引用了 ${locale} 中不存在的键 ${m[1]}`);
      }
    }
  }
  return problems;
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const problems = checkI18n(fileURLToPath(new URL("..", import.meta.url)));
  if (problems.length) {
    console.error(`i18n 键检查未通过（docs/develop.md#internationalization）：\n  ${problems.join("\n  ")}`);
    process.exit(1);
  }
  console.log("i18n 键检查通过");
}
