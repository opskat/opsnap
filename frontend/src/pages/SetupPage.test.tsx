import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";

import i18n from "@/i18n";
import { ThemeProvider } from "@/lib/theme";
import { SetupPage } from "@/pages/SetupPage";

const ok = (data: unknown) => new Response(JSON.stringify({ code: 0, msg: "success", data }), { status: 200 });
const fail = (code: number, msg: string, status = 400) => new Response(JSON.stringify({ code, msg }), { status });

function renderSetup(...responses: Response[]) {
  const fetchMock = vi.fn();
  for (const r of responses) fetchMock.mockResolvedValueOnce(r);
  vi.stubGlobal("fetch", fetchMock);
  const router = createMemoryRouter(
    [
      { path: "/setup", element: <SetupPage /> },
      { path: "/login", element: <p>login-page</p> },
      { path: "/", element: <p>home-page</p> },
    ],
    { initialEntries: ["/setup"] }
  );
  render(
    <ThemeProvider>
      <RouterProvider router={router} />
    </ThemeProvider>
  );
  return fetchMock;
}

async function fill(code: string, username: string, password: string, confirm = password) {
  await userEvent.type(await screen.findByLabelText("设置码"), code);
  await userEvent.type(screen.getByLabelText("用户名"), username);
  await userEvent.type(screen.getByLabelText("密码"), password);
  await userEvent.type(screen.getByLabelText("确认密码"), confirm);
  await userEvent.click(screen.getByRole("button", { name: "创建管理员并登录" }));
}

beforeAll(() => i18n.changeLanguage("zh-CN"));
afterEach(() => vi.unstubAllGlobals());

describe("首次设置页", () => {
  it("提示去启动日志查找设置码", async () => {
    renderSetup(ok({ initialized: false }));
    expect(await screen.findByText(/启动日志/)).toBeInTheDocument();
  });

  it("已初始化时跳转到登录页", async () => {
    renderSetup(ok({ initialized: true }));
    expect(await screen.findByText("login-page")).toBeInTheDocument();
  });

  it("本地校验不通过时不提交，并在字段旁显示错误", async () => {
    const fetchMock = renderSetup(ok({ initialized: false }));
    await fill("K7QF-2MXP-9DVA", "Admin", "short", "different");
    expect(screen.getByText(/小写字母、数字/)).toBeInTheDocument();
    expect(screen.getByText("至少 12 个字符")).toBeInTheDocument();
    expect(screen.getByText("两次输入的密码不一致")).toBeInTheDocument();
    expect(screen.getByLabelText("用户名")).toHaveAttribute("aria-invalid", "true");
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("设置码错误时显示在设置码字段旁", async () => {
    renderSetup(ok({ initialized: false }), fail(10101, "设置码不正确"));
    await fill("AAAA-BBBB-CCCC", "admin", "correct-horse-battery");
    expect(await screen.findByText("设置码不正确")).toBeInTheDocument();
    expect(screen.getByLabelText("设置码")).toHaveAttribute("aria-invalid", "true");
  });

  it("成功后提交设置信息并进入控制台", async () => {
    const fetchMock = renderSetup(ok({ initialized: false }), ok({ username: "admin" }));
    await fill("K7QF-2MXP-9DVA", "admin", "correct-horse-battery");
    expect(await screen.findByText("home-page")).toBeInTheDocument();
    const [url, init] = fetchMock.mock.calls[1] as [string, RequestInit];
    expect(url).toBe("/api/v1/auth/setup");
    expect(JSON.parse(init.body as string)).toEqual({
      setup_code: "K7QF-2MXP-9DVA",
      username: "admin",
      password: "correct-horse-battery",
    });
  });

  it("提交时被别人抢先初始化，跳转到登录页", async () => {
    renderSetup(ok({ initialized: false }), fail(10100, "已初始化", 409));
    await fill("K7QF-2MXP-9DVA", "admin", "correct-horse-battery");
    expect(await screen.findByText("login-page")).toBeInTheDocument();
  });
});
