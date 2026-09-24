import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, Outlet, RouterProvider } from "react-router";
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";

import { AuthGate } from "@/components/auth/AuthGate";
import { useSession } from "@/lib/session";
import i18n from "@/i18n";

const ok = (data: unknown) => new Response(JSON.stringify({ code: 0, msg: "success", data }), { status: 200 });
const unauthorized = () => new Response(JSON.stringify({ code: 10001, msg: "未登录" }), { status: 401 });

function Whoami() {
  const { user, logout } = useSession();
  return (
    <div>
      <p>user:{user.username}</p>
      <button onClick={() => void logout()}>logout</button>
    </div>
  );
}

function renderAt(path: string, ...responses: Response[]) {
  const fetchMock = vi.fn();
  for (const r of responses) fetchMock.mockResolvedValueOnce(r);
  vi.stubGlobal("fetch", fetchMock);
  const router = createMemoryRouter(
    [
      {
        element: <AuthGate />,
        children: [{ element: <Outlet />, children: [{ path: "/jobs", element: <Whoami /> }] }],
      },
      { path: "/setup", element: <p>setup-page</p> },
      { path: "/login", element: <p>login-page</p> },
    ],
    { initialEntries: [path] }
  );
  render(<RouterProvider router={router} />);
  return { fetchMock, router };
}

beforeAll(() => i18n.changeLanguage("zh-CN"));
afterEach(() => vi.unstubAllGlobals());

describe("登录门禁", () => {
  it("尚未初始化时跳转到首次设置页", async () => {
    renderAt("/jobs", ok({ initialized: false }));
    expect(await screen.findByText("setup-page")).toBeInTheDocument();
  });

  it("未登录时跳转到登录页，并记住原本要访问的页面", async () => {
    const { router } = renderAt("/jobs?x=1", ok({ initialized: true }), unauthorized());
    expect(await screen.findByText("login-page")).toBeInTheDocument();
    expect(router.state.location.search).toBe("?next=%2Fjobs%3Fx%3D1");
  });

  it("已登录时显示页面并提供当前用户", async () => {
    renderAt("/jobs", ok({ initialized: true }), ok({ username: "admin" }));
    expect(await screen.findByText("user:admin")).toBeInTheDocument();
  });

  it("退出登录后回到登录页", async () => {
    const { fetchMock } = renderAt("/jobs", ok({ initialized: true }), ok({ username: "admin" }), ok({}));
    await userEvent.click(await screen.findByRole("button", { name: "logout" }));
    expect(await screen.findByText("login-page")).toBeInTheDocument();
    expect(fetchMock.mock.calls[2][0]).toBe("/api/v1/auth/logout");
  });

  it("使用中会话失效（接口返回 401）时跳转到登录页", async () => {
    renderAt("/jobs", ok({ initialized: true }), ok({ username: "admin" }), unauthorized());
    await userEvent.click(await screen.findByRole("button", { name: "logout" }));
    expect(await screen.findByText("login-page")).toBeInTheDocument();
  });

  it("状态接口失败时显示错误并可重试，而不是空白页", async () => {
    renderAt(
      "/jobs",
      new Response("oops", { status: 502, statusText: "Bad Gateway" }),
      ok({ initialized: true }),
      ok({ username: "admin" })
    );
    await userEvent.click(await screen.findByRole("button", { name: "重试" }));
    expect(await screen.findByText("user:admin")).toBeInTheDocument();
  });
});
