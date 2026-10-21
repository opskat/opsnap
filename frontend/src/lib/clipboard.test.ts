// 写剪贴板的两种通路：安全上下文用异步 Clipboard API，http 访问退回 execCommand（临时节点）。
import { afterEach, describe, expect, it, vi } from "vitest";
import { copyText } from "./clipboard";

/** 把 navigator.clipboard 变成不存在的（模拟 http 访问的非安全上下文） */
function withoutClipboardApi(): () => void {
  const own = Object.getOwnPropertyDescriptor(navigator, "clipboard");
  Object.defineProperty(navigator, "clipboard", { value: undefined, configurable: true });
  return () => {
    if (own) Object.defineProperty(navigator, "clipboard", own);
    else delete (navigator as unknown as Record<string, unknown>).clipboard;
  };
}

/** document.execCommand 的替身，记下被选中节点的内容 */
function stubExecCommand(result: boolean) {
  let copied = "";
  const exec = vi.fn((cmd: string) => {
    const area = document.querySelector("textarea");
    if (area) copied = area.value;
    return cmd === "copy" && result;
  });
  Object.defineProperty(document, "execCommand", { value: exec, configurable: true, writable: true });
  return {
    exec,
    get copied() {
      return copied;
    },
  };
}

afterEach(() => {
  delete (document as unknown as Record<string, unknown>).execCommand;
});

describe("写剪贴板", () => {
  it("有 Clipboard API 时直接写，不碰旧接口", async () => {
    const write = vi.spyOn(navigator.clipboard, "writeText").mockResolvedValue(undefined);
    const legacy = stubExecCommand(true);

    await expect(copyText("SECRET-PW-1")).resolves.toBe(true);

    expect(write).toHaveBeenCalledWith("SECRET-PW-1");
    expect(legacy.exec).not.toHaveBeenCalled();
  });

  it("http 访问时没有 Clipboard API，退回 execCommand 复制", async () => {
    const restore = withoutClipboardApi();
    try {
      const legacy = stubExecCommand(true);

      await expect(copyText("SECRET-PW-1")).resolves.toBe(true);

      expect(legacy.exec).toHaveBeenCalledWith("copy");
      expect(legacy.copied).toBe("SECRET-PW-1");
    } finally {
      restore();
    }
  });

  it("Clipboard API 被拒时也退回 execCommand", async () => {
    vi.spyOn(navigator.clipboard, "writeText").mockRejectedValue(new Error("denied"));
    const legacy = stubExecCommand(true);

    await expect(copyText("SECRET-PW-1")).resolves.toBe(true);

    expect(legacy.copied).toBe("SECRET-PW-1");
  });

  it("两条通路都失败时返回 false，并清掉临时节点", async () => {
    const restore = withoutClipboardApi();
    try {
      const legacy = stubExecCommand(false);

      await expect(copyText("SECRET-PW-1")).resolves.toBe(false);

      expect(legacy.exec).toHaveBeenCalledTimes(1);
      expect(document.querySelector("textarea")).toBeNull();
    } finally {
      restore();
    }
  });
});
