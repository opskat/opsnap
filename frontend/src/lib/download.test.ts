import { afterEach, describe, expect, it, vi } from "vitest";
import { downloadText, downloadUrl } from "./download";

/** 记录被点击的链接 */
function spyClick() {
  const clicked: HTMLAnchorElement[] = [];
  vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (this: HTMLAnchorElement) {
    clicked.push(this);
  });
  return clicked;
}

afterEach(() => {
  vi.restoreAllMocks();
  vi.useRealTimers();
});

describe("downloadUrl", () => {
  it("链接带 download 属性：出错时返回的 JSON 只会成为失败的下载，不会把页面导航走", () => {
    const clicked = spyClick();
    downloadUrl("/api/v1/jobs/1/snapshots/abc/download?path=%2Fa");
    expect(clicked).toHaveLength(1);
    expect(clicked[0].hasAttribute("download")).toBe(true);
    expect(clicked[0].getAttribute("href")).toBe("/api/v1/jobs/1/snapshots/abc/download?path=%2Fa");
    expect(document.body.contains(clicked[0])).toBe(false);
  });
});

describe("downloadText", () => {
  it("点击后等一拍再回收临时地址，避免浏览器还没开始下载就失效", () => {
    vi.useFakeTimers();
    const clicked = spyClick();
    vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:opsnap/1");
    const revoke = vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => undefined);
    downloadText("恢复说明.txt", "内容");
    expect(clicked).toHaveLength(1);
    expect(clicked[0].download).toBe("恢复说明.txt");
    expect(revoke).not.toHaveBeenCalled();
    vi.runAllTimers();
    expect(revoke).toHaveBeenCalledWith("blob:opsnap/1");
  });
});
