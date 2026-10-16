import { vi } from "vitest";

/** 一次被记录的请求 */
export interface Call {
  method: string;
  path: string;
  /** 查询串，不含问号，例如 filter=attention&q=db */
  query: string;
  body: unknown;
}

export interface MockResponse {
  status: number;
  body: unknown;
}

type Route = MockResponse | ((call: Call) => MockResponse | Promise<MockResponse>);

/** cago 成功响应 */
export function ok(data: unknown = null): MockResponse {
  return { status: 200, body: { code: 0, msg: "success", data } };
}

/** cago 错误响应 {code,msg} */
export function fail(status: number, code: number, msg: string): MockResponse {
  return { status, body: { code, msg } };
}

/**
 * 用路由表替换全局 fetch。键形如 "GET /settings"（不含 /api/v1 前缀），
 * 值可以是固定响应，也可以是按调用次数变化的函数。未登记的请求返回 404。
 */
export function mockApi(routes: Record<string, Route>) {
  const calls: Call[] = [];
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    const method = (init?.method ?? "GET").toUpperCase();
    const path = url.pathname.replace(/^\/api\/v1/, "");
    const body = typeof init?.body === "string" ? JSON.parse(init.body) : undefined;
    const call = { method, path, query: url.search.replace(/^\?/, ""), body };
    calls.push(call);
    const route = routes[`${method} ${path}`];
    const res =
      route === undefined ? fail(404, 10000, "接口不存在") : typeof route === "function" ? await route(call) : route;
    return new Response(typeof res.body === "string" ? res.body : JSON.stringify(res.body), {
      status: res.status,
      headers: { "Content-Type": typeof res.body === "string" ? "text/plain" : "application/json" },
    });
  });
  vi.stubGlobal("fetch", fetchMock);
  return {
    calls,
    fetch: fetchMock,
    /** 指定方法和路径的调用 */
    find: (method: string, path: string) => calls.filter((c) => c.method === method && c.path === path),
  };
}
