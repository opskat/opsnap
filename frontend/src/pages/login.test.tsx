import { describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { LoginPage } from "./login";
import { renderPage } from "@/test/render";
import { fail, mockApi, ok } from "@/test/api-mock";
import { redirect } from "@/lib/redirect";

async function submit(username: string, password: string) {
  const user = userEvent.setup();
  await user.clear(screen.getByLabelText("用户名"));
  await user.type(screen.getByLabelText("用户名"), username);
  await user.type(screen.getByLabelText("密码"), password);
  await user.click(screen.getByRole("button", { name: "登录" }));
}

describe("登录页", () => {
  it("密码错误时写明还剩几次", async () => {
    const m = mockApi({ "POST /auth/login": fail(400, 11003, "用户名或密码错误，还剩 3 次机会") });
    renderPage(<LoginPage />, { path: "/login" });
    await submit("admin", "wrong-pass");
    expect(await screen.findByRole("alert")).toHaveTextContent("用户名或密码错误，还剩 3 次机会");
    expect(screen.getByLabelText("密码")).toHaveAttribute("aria-invalid", "true");
    expect(m.find("POST", "/auth/login")[0].body).toEqual({ username: "admin", password: "wrong-pass" });
    expect(screen.getByTestId("location")).toHaveTextContent("/login");
  });

  it("锁定时显示锁定提示", async () => {
    mockApi({ "POST /auth/login": fail(400, 11004, "登录失败次数过多，已锁定，请 10 分钟后再试") });
    renderPage(<LoginPage />, { path: "/login" });
    await submit("admin", "wrong-pass");
    expect(await screen.findByRole("alert")).toHaveTextContent("已锁定，请 10 分钟后再试");
  });

  it("成功后回到 next 指定的页面", async () => {
    mockApi({ "POST /auth/login": ok({ username: "admin" }) });
    renderPage(<LoginPage />, { path: "/login", at: "/login?next=%2Fsettings%3Ftab%3Dapi" });
    await submit("admin", "correct-horse");
    await waitFor(() => expect(screen.getByTestId("location")).toHaveTextContent("/settings?tab=api"));
  });

  it("next 不是站内路径时回到首页", async () => {
    mockApi({ "POST /auth/login": ok({ username: "admin" }) });
    renderPage(<LoginPage />, { path: "/login", at: "/login?next=https%3A%2F%2Fevil.example.com" });
    await submit("admin", "correct-horse");
    await waitFor(() => expect(screen.getByTestId("location").textContent).toBe("/"));
  });

  it("尚未创建管理员时转到首次设置", async () => {
    mockApi({ "POST /auth/login": fail(400, 11001, "尚未创建管理员，请先完成初始化") });
    renderPage(<LoginPage />, { path: "/login" });
    await submit("admin", "whatever1");
    await waitFor(() => expect(screen.getByTestId("location")).toHaveTextContent("/setup"));
  });
});

const SSO_STATUS = { initialized: true, sso_enabled: true, sso_label: "Gitea" };

describe("登录页 · 单点登录", () => {
  it("未启用单点登录时与原来一样，只有用户名密码", async () => {
    const m = mockApi({ "GET /auth/status": ok({ initialized: true, sso_enabled: false }) });
    renderPage(<LoginPage />, { path: "/login" });
    await waitFor(() => expect(m.find("GET", "/auth/status")).toHaveLength(1));
    expect(screen.getByRole("button", { name: "登录" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /用 .* 登录/ })).not.toBeInTheDocument();
  });

  it("已绑定时在表单下面显示「用 Gitea 登录」，点击后带上 next 跳到提供方", async () => {
    const to = vi.spyOn(redirect, "to").mockImplementation(() => {});
    const m = mockApi({
      "GET /auth/status": ok(SSO_STATUS),
      "POST /auth/oidc/login": ok({ url: "https://git.example.com/login/oauth/authorize?state=x" }),
    });
    renderPage(<LoginPage />, { path: "/login", at: "/login?next=%2Fruns" });
    const user = userEvent.setup();
    const btn = await screen.findByRole("button", { name: "用 Gitea 登录" });
    expect(screen.getByLabelText("密码")).toBeInTheDocument();
    await user.click(btn);
    await waitFor(() => expect(to).toHaveBeenCalledWith("https://git.example.com/login/oauth/authorize?state=x"));
    expect(m.find("POST", "/auth/oidc/login")[0].body).toEqual({ next: "/runs" });
    expect(screen.getByRole("button", { name: "正在跳转到 Gitea…" })).toBeDisabled();
    to.mockRestore();
  });

  it("开始登录失败时显示原因，按钮恢复可用", async () => {
    mockApi({
      "GET /auth/status": ok(SSO_STATUS),
      "POST /auth/oidc/login": fail(
        400,
        11812,
        "连不上 Gitea（https://git.example.com/）：connection refused。稍后再试。"
      ),
    });
    renderPage(<LoginPage />, { path: "/login" });
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "用 Gitea 登录" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("连不上 Gitea");
    expect(screen.getByRole("button", { name: "用 Gitea 登录" })).toBeEnabled();
  });

  it("回调失败跳回来时，读取并显示服务端整理过的原因", async () => {
    const m = mockApi({
      "GET /auth/status": ok(SSO_STATUS),
      "GET /auth/oidc/result": ok({ message: "Gitea 账号 bob 不是绑定的管理员账号，换成绑定的账号再试。" }),
    });
    renderPage(<LoginPage />, { path: "/login", at: "/login?sso_error=abc123" });
    expect(await screen.findByRole("alert")).toHaveTextContent("Gitea 账号 bob 不是绑定的管理员账号");
    expect(m.find("GET", "/auth/oidc/result")[0].query).toBe("id=abc123");
  });
});

describe("登录页 · 密码登录已关闭", () => {
  it("只显示主样式的「用 Gitea 登录」和恢复提示", async () => {
    mockApi({
      "GET /auth/status": ok({ initialized: true, sso_enabled: true, sso_label: "Gitea", password_login: false }),
    });
    renderPage(<LoginPage />, { path: "/login" });
    expect(await screen.findByRole("button", { name: "用 Gitea 登录" })).toBeInTheDocument();
    expect(screen.queryByLabelText("密码")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "登录" })).not.toBeInTheDocument();
    expect(screen.getByText("密码登录已关闭。Gitea 不可用时，见部署说明里的恢复方法。")).toBeInTheDocument();
  });

  it("旧页面仍提交密码时显示服务端的说明", async () => {
    mockApi({
      "GET /auth/status": ok({ initialized: true, sso_enabled: false }),
      "POST /auth/login": fail(403, 11821, "密码登录已关闭，请用 Gitea 登录"),
    });
    renderPage(<LoginPage />, { path: "/login" });
    await submit("admin", "correct-horse");
    expect(await screen.findByRole("alert")).toHaveTextContent("密码登录已关闭，请用 Gitea 登录");
  });
});
