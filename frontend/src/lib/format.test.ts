import { describe, expect, it } from "vitest";
import { fmtAgo, fmtBytes, fmtDate, fmtNext } from "./format";

const now = new Date(2026, 9, 15, 16, 16, 0);
const at = (d: Date) => Math.floor(d.getTime() / 1000);

describe("时间格式", () => {
  it("今年的日期不写年份，往年写年份", () => {
    expect(fmtDate(at(new Date(2026, 9, 2, 9, 0)), now)).toBe("10月2日");
    expect(fmtDate(at(new Date(2025, 5, 3, 9, 0)), now)).toBe("2025年6月3日");
  });

  it("最近使用按远近显示", () => {
    expect(fmtAgo(at(new Date(2026, 9, 15, 16, 15, 30)), now)).toBe("刚刚");
    expect(fmtAgo(at(new Date(2026, 9, 15, 16, 4, 0)), now)).toBe("12 分钟前");
    expect(fmtAgo(at(new Date(2026, 9, 15, 8, 0, 0)), now)).toBe("今天 08:00");
    expect(fmtAgo(at(new Date(2026, 7, 20, 8, 0, 0)), now)).toBe("8月20日");
  });
});

describe("fmtNext", () => {
  it("下次运行按远近显示", () => {
    expect(fmtNext(at(new Date(2026, 9, 15, 23, 0)), now)).toBe("今天 23:00");
    expect(fmtNext(at(new Date(2026, 9, 16, 3, 0)), now)).toBe("明天 03:00");
    expect(fmtNext(at(new Date(2026, 9, 20, 3, 5)), now)).toBe("10月20日 03:05");
  });
});

describe("fmtBytes", () => {
  it("按 1024 进位，三位数不带小数", () => {
    expect(fmtBytes(0)).toBe("0 B");
    expect(fmtBytes(1023)).toBe("1023 B");
    expect(fmtBytes(1536)).toBe("1.5 KB");
    expect(fmtBytes(820 * 1024 ** 3)).toBe("820 GB");
    expect(fmtBytes(1.4 * 1024 ** 4)).toBe("1.4 TB");
  });
});
