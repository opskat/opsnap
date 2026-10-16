import { afterEach, beforeEach, vi } from "vitest";

/**
 * 测试统一的「现在」：2026-10-15 16:16 本地时间。白天、远离午夜，
 * 避免「今天/昨天」「14 天运行带」这类标签在真实时钟跨过零点时翻转。
 */
export const PINNED_NOW = new Date(2026, 9, 15, 16, 16, 0);
/** 固定时刻的 Unix 秒，用于构造相对时间的 fixture */
export const PINNED_NOW_SEC = Math.floor(PINNED_NOW.getTime() / 1000);

/**
 * 在当前测试文件内把 Date 固定在 PINNED_NOW。只替换 Date，不替换定时器，
 * testing-library 的 findBy*、waitFor 依旧按真实定时器运行。
 */
export function pinClock() {
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ["Date"], now: PINNED_NOW });
  });
  afterEach(() => {
    vi.useRealTimers();
  });
}
