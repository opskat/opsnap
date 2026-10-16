import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError, GET_TIMEOUT_MS, api, loginPath, setUnauthorizedHandler } from "./api";
import { fail, mockApi, ok } from "@/test/api-mock";

describe("api 客户端", () => {
  afterEach(() => setUnauthorizedHandler(null));

  it("成功时解开 cago 的 {code:0,data} 并返回 data", async () => {
    mockApi({ "GET /auth/status": ok({ initialized: true }) });
    await expect(api.auth.status()).resolves.toEqual({ initialized: true });
  });

  it("以 JSON 发送请求体，带上同源 Cookie", async () => {
    const m = mockApi({ "POST /apikeys": ok({ id: 1, key: "osk_x" }) });
    await api.apikeys.create({ name: "ci", expires_in: "30d" });
    const [input, init] = m.fetch.mock.calls[0];
    expect(String(input)).toBe("/api/v1/apikeys");
    expect(init?.credentials).toBe("same-origin");
    expect(new Headers(init?.headers).get("Content-Type")).toBe("application/json");
    expect(m.calls[0].body).toEqual({ name: "ci", expires_in: "30d" });
  });

  it("读取接口超时时写明是哪个接口", async () => {
    vi.useFakeTimers();
    try {
      vi.stubGlobal(
        "fetch",
        vi.fn(
          (_input: RequestInfo | URL, init?: RequestInit) =>
            new Promise((_resolve, reject) => {
              init?.signal?.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")));
            })
        )
      );
      const pending = api.jobs.list("all").catch((e) => e);
      await vi.advanceTimersByTimeAsync(GET_TIMEOUT_MS);
      const err = await pending;
      expect(err).toBeInstanceOf(ApiError);
      expect(err.message).toContain("GET /api/v1/jobs");
      expect(err.message).toContain("超时");
    } finally {
      vi.useRealTimers();
      vi.unstubAllGlobals();
    }
  });

  it("撤销密钥用 DELETE /apikeys/:id", async () => {
    const m = mockApi({ "DELETE /apikeys/7": ok(null) });
    await api.apikeys.revoke(7);
    expect(m.find("DELETE", "/apikeys/7")).toHaveLength(1);
  });

  it("业务错误抛出带 code、msg 和状态码的 ApiError", async () => {
    mockApi({ "POST /auth/login": fail(400, 11003, "用户名或密码错误，还剩 3 次机会") });
    const err = await api.auth.login({ username: "admin", password: "x" }).catch((e) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ status: 400, code: 11003, message: "用户名或密码错误，还剩 3 次机会" });
  });

  it("401 时调用未登录处理（跳转登录）并抛出 ApiError", async () => {
    const onUnauthorized = vi.fn();
    setUnauthorizedHandler(onUnauthorized);
    mockApi({ "GET /settings": fail(401, 11000, "未登录或凭据无效，请重新登录或检查 API 密钥") });
    const err = await api.settings.get().catch((e) => e);
    expect(onUnauthorized).toHaveBeenCalledTimes(1);
    expect(err).toBeInstanceOf(ApiError);
    expect(err.status).toBe(401);
  });

  it("非 401 的错误不触发未登录处理", async () => {
    const onUnauthorized = vi.fn();
    setUnauthorizedHandler(onUnauthorized);
    mockApi({ "GET /settings": fail(500, -1, "系统错误") });
    await api.settings.get().catch(() => {});
    expect(onUnauthorized).not.toHaveBeenCalled();
  });

  it("响应不是 JSON 时说明哪个状态码", async () => {
    mockApi({ "GET /notify": { status: 502, body: "Bad Gateway" } });
    const err = await api.notify.get().catch((e) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err.status).toBe(502);
    expect(err.message).toContain("502");
  });

  it("网络不通时给出可读的错误", async () => {
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new TypeError("Failed to fetch")));
    const err = await api.auth.me().catch((e) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err.status).toBe(0);
    expect(err.message).toBe("无法连接 opsnap 服务，请检查网络后重试");
  });

  it("登录地址带上当前路径，已在登录页时不重复带", () => {
    expect(loginPath("/settings?tab=api")).toBe("/login?next=%2Fsettings%3Ftab%3Dapi");
    expect(loginPath("/")).toBe("/login");
    expect(loginPath("/login?next=%2Fjobs")).toBe("/login?next=%2Fjobs");
  });
});
