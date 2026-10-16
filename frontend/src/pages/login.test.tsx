import { describe, expect, it } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { LoginPage } from "./login";
import { renderPage } from "@/test/render";
import { fail, mockApi, ok } from "@/test/api-mock";

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
