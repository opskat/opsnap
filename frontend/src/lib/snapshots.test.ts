import { describe, expect, it } from "vitest";
import {
  REASON_LABEL,
  addedPercent,
  fileSuffix,
  fmtSnapshotTime,
  pickedSummary,
  rootPath,
  withoutDescendants,
} from "./snapshots";
import type { SnapshotEntry, SnapshotItem } from "./api";

const entry = (over: Partial<SnapshotEntry>): SnapshotEntry => ({
  name: "a",
  path: "/a",
  type: "file",
  size: 10,
  mtime: 0,
  ...over,
});

describe("快照辅助函数", () => {
  it("保留原因的文字", () => {
    expect(REASON_LABEL).toEqual({ last: "最近", daily: "每天", weekly: "每周", monthly: "每月" });
  });

  it("新增量的相对条：最大的占满，未知为 0，有数据至少露出一点", () => {
    expect(addedPercent(500, 1000)).toBe(50);
    expect(addedPercent(1000, 1000)).toBe(100);
    expect(addedPercent(-1, 1000)).toBe(0);
    expect(addedPercent(0, 1000)).toBe(0);
    expect(addedPercent(1, 1_000_000)).toBe(2);
    expect(addedPercent(5, 0)).toBe(0);
  });

  it("快照时间：今天、昨天、更早", () => {
    const now = new Date(2026, 9, 15, 12, 0);
    const at = (d: Date) => Math.floor(d.getTime() / 1000);
    expect(fmtSnapshotTime(at(new Date(2026, 9, 15, 3, 5)), now)).toBe("今天 03:05");
    expect(fmtSnapshotTime(at(new Date(2026, 9, 14, 23, 0)), now)).toBe("昨天 23:00");
    expect(fmtSnapshotTime(at(new Date(2026, 9, 2, 3, 0)), now)).toBe("10月2日 03:00");
  });

  it("树的起点：只有一个路径时从它开始，否则从根开始", () => {
    const snap = (paths: string[]) => ({ paths }) as SnapshotItem;
    expect(rootPath(snap(["/data/mirror/3"]))).toBe("/data/mirror/3");
    expect(rootPath(snap(["/a", "/b"]))).toBe("/");
    expect(rootPath(snap([]))).toBe("/");
  });

  it("所选项：被其他所选目录包含的不重复计入", () => {
    const picked = [entry({ path: "/d", type: "dir" }), entry({ path: "/d/x" }), entry({ path: "/e", size: 5 })];
    expect(withoutDescendants(picked).map((e) => e.path)).toEqual(["/d", "/e"]);
  });

  it("所选项的汇总：只有文件时给出总大小，含目录时不给", () => {
    expect(pickedSummary([entry({ size: 10 }), entry({ path: "/b", size: 30 })])).toEqual({ count: 2, bytes: 40 });
    expect(pickedSummary([entry({ type: "dir", path: "/d" }), entry({ path: "/b", size: 30 })])).toEqual({
      count: 2,
      bytes: null,
    });
  });

  it("导出文件的下载后缀", () => {
    expect(fileSuffix("shop.sql.gz")).toBe(".sql.gz");
    expect(fileSuffix("app.dump")).toBe(".dump");
    expect(fileSuffix("noext")).toBe("");
  });
});
