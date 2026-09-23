import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, describe, expect, it } from "vitest";

import { checkI18n } from "./check-i18n.mjs";

let root;

function project(locales, files) {
  root = mkdtempSync(join(tmpdir(), "i18n-"));
  mkdirSync(join(root, "src/i18n/locales"), { recursive: true });
  for (const [name, data] of Object.entries(locales)) {
    writeFileSync(join(root, "src/i18n/locales", name), JSON.stringify(data));
  }
  for (const [name, text] of Object.entries(files)) writeFileSync(join(root, "src", name), text);
  return root;
}

afterEach(() => rmSync(root, { recursive: true, force: true }));

describe("check-i18n", () => {
  it("键一致且引用都存在时通过", () => {
    const dir = project({ "en.json": { a: { b: "B" } }, "zh-CN.json": { a: { b: "乙" } } }, { "x.tsx": 't("a.b")' });
    expect(checkI18n(dir)).toEqual([]);
  });

  it("报告某个语言文件缺少的键", () => {
    const dir = project({ "en.json": { a: "A", b: "B" }, "zh-CN.json": { a: "甲" } }, {});
    expect(checkI18n(dir)).toEqual(["zh-CN.json 缺少键 b"]);
  });

  it("报告源码引用了不存在的键", () => {
    const dir = project({ "en.json": { a: "A" }, "zh-CN.json": { a: "甲" } }, { "x.tsx": 't("nope.key")' });
    expect(checkI18n(dir)).toHaveLength(2);
  });

  it("测试文件中的引用不参与检查", () => {
    const dir = project({ "en.json": { a: "A" }, "zh-CN.json": { a: "甲" } }, { "x.test.tsx": 't("nope")' });
    expect(checkI18n(dir)).toEqual([]);
  });
});
