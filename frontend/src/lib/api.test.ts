import { afterEach, describe, expect, it, vi } from "vitest";

import { ApiError, request } from "@/lib/api";

function mockFetch(status: number, body: unknown) {
  const fn = vi
    .fn()
    .mockResolvedValue(new Response(typeof body === "string" ? body : JSON.stringify(body), { status }));
  vi.stubGlobal("fetch", fn);
  return fn;
}

afterEach(() => vi.unstubAllGlobals());

describe("request", () => {
  it("拼接 /api/v1 前缀并返回 data", async () => {
    const fn = mockFetch(200, { code: 0, msg: "success", data: { version: "1.0.0" } });
    await expect(request("/system/health")).resolves.toEqual({ version: "1.0.0" });
    expect(fn).toHaveBeenCalledWith("/api/v1/system/health", expect.anything());
  });

  it("业务错误码转换为 ApiError", async () => {
    mockFetch(400, { code: 10001, msg: "参数错误", data: null });
    await expect(request("/x")).rejects.toMatchObject({
      name: "ApiError",
      code: 10001,
      status: 400,
      message: "参数错误",
    });
  });

  it("非 JSON 响应也转换为 ApiError", async () => {
    mockFetch(502, "<html>bad gateway</html>");
    const err = await request("/x").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).status).toBe(502);
  });
});
