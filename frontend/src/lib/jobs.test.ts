import { describe, expect, it } from "vitest";
import { ApiError } from "./api";
import { SOURCES, fieldErrorsOf, parseFieldErrors } from "./jobs";

describe("数据源", () => {
  it("每种数据源用自己的官方 logo", () => {
    const tiles = SOURCES.filter((s) => s.value !== "files").map((s) => s.tile);
    expect(new Set(tiles).size).toBe(tiles.length);
  });

  it("数据源只有 mysql、postgres、redis、mongodb、files", () => {
    expect(SOURCES.map((s) => s.value)).toEqual(["mysql", "postgres", "redis", "mongodb", "files"]);
  });
});

describe("parseFieldErrors", () => {
  it("每行 [json.path] 原因 拆成字段 → 原因", () => {
    const msg =
      "[name] 名称不能为空，且不超过 64 个字\n[schedule.time] 时刻须写成 HH:MM\n[advanced.timeout_sec] 超时须在 1 分钟到 7 天之间";
    expect(parseFieldErrors(msg)).toEqual({
      name: ["名称不能为空，且不超过 64 个字"],
      "schedule.time": ["时刻须写成 HH:MM"],
      "advanced.timeout_sec": ["超时须在 1 分钟到 7 天之间"],
    });
  });
  it("同一字段多条原因都保留，不合格式的行忽略", () => {
    expect(parseFieldErrors("[paths] 须是绝对路径\n随便一句\n[paths] 不能含 ..")).toEqual({
      paths: ["须是绝对路径", "不能含 .."],
    });
  });
});

describe("fieldErrorsOf", () => {
  it("只把 400 + 11401 当作字段错误", () => {
    expect(fieldErrorsOf(new ApiError(400, 11401, "[name] 不能为空"))).toEqual({ name: ["不能为空"] });
    expect(fieldErrorsOf(new ApiError(400, 11000, "[name] 不能为空"))).toBeNull();
    expect(fieldErrorsOf(new Error("x"))).toBeNull();
  });
});
