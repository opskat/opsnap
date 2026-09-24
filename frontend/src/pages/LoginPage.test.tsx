import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";

import i18n from "@/i18n";
import { ThemeProvider } from "@/lib/theme";
import { LoginPage } from "@/pages/LoginPage";

const ok = (data: unknown) => new Response(JSON.stringify({ code: 0, msg: "success", data }), { status: 200 });
const fail = (code: number, msg: string, status = 400) => new Response(JSON.stringify({ code, msg }), { status });

function renderLogin(entry: string, ...responses: Response[]) {
  const fetchMock = vi.fn();
  for (const r of responses) fetchMock.mockResolvedValueOnce(r);
  vi.stubGlobal("fetch", fetchMock);
  const router = createMemoryRouter(
    [
      { path: "/login", element: <LoginPage /> },
      { path: "/setup", element: <p>setup-page</p> },
      { path: "/", element: <p>home-page</p> },
      { path: "/jobs", element: <p>jobs-page</p> },
    ],
    { initialEntries: [entry] }
  );
  render(
    <ThemeProvider>
      <RouterProvider router={router} />
    </ThemeProvider>
  );
  return fetchMock;
}

async function submit(username: string, password: string) {
  await userEvent.type(await screen.findByLabelText("用户名"), username);
  await userEvent.type(screen.getByLabelText("密码"), password);
  await userEvent.click(screen.getByRole("button", { name: "登录" }));
}

beforeAll(() => i18n.changeLanguage("zh-CN"));
afterEach(() => vi.unstubAllGlobals());

describe("登录页", () => {
  it("尚未初始化时跳转到首次设置页", async () => {
    renderLogin("/login", ok({ initialized: false }));
    expect(await screen.findByText("setup-page")).toBeInTheDocument();
  });

  it("显示忘记密码时的命令行重置提示", async () => {
    renderLogin("/login", ok({ initialized: true }));
    expect(await screen.findByText(/opsnap admin reset-password/)).toBeInTheDocument();
  });

  it("登录成功后回到原本要访问的页面", async () => {
    const fetchMock = renderLogin("/login?next=%2Fjobs", ok({ initialized: true }), ok({ username: "admin" }));
    await submit("admin", "correct-horse-battery");
    expect(await screen.findByText("jobs-page")).toBeInTheDocument();
    const [url, init] = fetchMock.mock.calls[1] as [string, RequestInit];
    expect(url).toBe("/api/v1/auth/login");
    expect(JSON.parse(init.body as string)).toEqual({ username: "admin", password: "correct-horse-battery" });
  });

  it("next 指向站外地址时忽略，回到概览", async () => {
    renderLogin("/login?next=%2F%2Fevil.example", ok({ initialized: true }), ok({ username: "admin" }));
    await submit("admin", "correct-horse-battery");
    expect(await screen.findByText("home-page")).toBeInTheDocument();
  });

  it("用户名或密码错误时在表单顶部提示，并保留在登录页", async () => {
    renderLogin("/login", ok({ initialized: true }), fail(10104, "用户名或密码错误"));
    await submit("admin", "wrong");
    expect(await screen.findByRole("alert")).toHaveTextContent("用户名或密码错误");
  });

  it("配置并绑定 OIDC 后显示“使用 <名称> 登录”，链接带上 next", async () => {
    renderLogin("/login?next=%2Fjobs", ok({ initialized: true, oidc_login: { display_name: "Keycloak" } }));
    const link = await screen.findByRole("link", { name: "使用 Keycloak 登录" });
    expect(link).toHaveAttribute("href", "/api/v1/auth/oidc/login?next=%2Fjobs");
  });

  it.each([["/\\evil.com"], ["/\t/evil.com"], ["//evil.com"], ["https://evil.com"]])(
    "站外 next（%j）不带进 OIDC 登录链接",
    async (next) => {
      renderLogin(
        `/login?next=${encodeURIComponent(next)}`,
        ok({ initialized: true, oidc_login: { display_name: "Keycloak" } })
      );
      const link = await screen.findByRole("link", { name: "使用 Keycloak 登录" });
      expect(link).toHaveAttribute("href", "/api/v1/auth/oidc/login?next=%2F");
    }
  );

  it("密码登录关闭时只显示 OIDC 登录", async () => {
    renderLogin("/login", ok({ initialized: true, password_login: false, oidc_login: { display_name: "Keycloak" } }));
    expect(await screen.findByRole("link", { name: "使用 Keycloak 登录" })).toBeInTheDocument();
    expect(screen.queryByLabelText("密码")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "登录" })).not.toBeInTheDocument();
  });

  it("未配置 OIDC 时不显示 OIDC 登录", async () => {
    renderLogin("/login", ok({ initialized: true, oidc_login: null }));
    await screen.findByRole("button", { name: "登录" });
    expect(screen.queryByRole("link", { name: /使用 .* 登录/ })).not.toBeInTheDocument();
  });

  it.each([
    ["not_bound", "", "此 OIDC 账号未绑定到 OpsNap"],
    ["idp", "User canceled", "OIDC 登录已取消或失败：User canceled"],
    ["invalid", "", "OIDC 登录校验失败，请重试"],
    ["unreachable", "", "无法连接 OIDC 提供方"],
  ])("OIDC 回调失败（%s）时显示对应错误", async (kind, desc, text) => {
    const q = new URLSearchParams({ oidc_error: kind, ...(desc ? { oidc_error_description: desc } : {}) });
    renderLogin(`/login?${q}`, ok({ initialized: true, oidc_login: { display_name: "Keycloak" } }));
    expect(await screen.findByRole("alert")).toHaveTextContent(text);
  });

  it("尝试次数过多时显示剩余等待时间", async () => {
    renderLogin("/login", ok({ initialized: true }), fail(10105, "尝试次数过多，请在 15 分钟后重试", 429));
    await submit("admin", "wrong");
    expect(await screen.findByRole("alert")).toHaveTextContent("15 分钟后重试");
  });
});
