import { describe, expect, it } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { SetupPage } from "./setup";
import { renderPage } from "@/test/render";
import { mockApi, ok } from "@/test/api-mock";

describe("首次设置页", () => {
  it("两次密码不一致时不提交", async () => {
    const m = mockApi({ "GET /auth/status": ok({ initialized: false }) });
    renderPage(<SetupPage />, { path: "/setup" });
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText("密码"), "correct-horse");
    await user.type(screen.getByLabelText("再输一次"), "correct-hors");
    await user.click(screen.getByRole("button", { name: "创建管理员" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("两次输入的密码不一致");
    expect(m.find("POST", "/auth/setup")).toHaveLength(0);
  });

  it("创建管理员后直接登录并进入概览", async () => {
    const m = mockApi({
      "GET /auth/status": ok({ initialized: false }),
      "POST /auth/setup": ok({}),
      "POST /auth/login": ok({ username: "root" }),
    });
    renderPage(<SetupPage />, { path: "/setup" });
    const user = userEvent.setup();
    const name = await screen.findByLabelText("用户名");
    await user.clear(name);
    await user.type(name, "root");
    await user.type(screen.getByLabelText("密码"), "correct-horse");
    await user.type(screen.getByLabelText("再输一次"), "correct-horse");
    await user.click(screen.getByRole("button", { name: "创建管理员" }));
    await waitFor(() => expect(screen.getByTestId("location").textContent).toBe("/"));
    expect(m.find("POST", "/auth/setup")[0].body).toEqual({ username: "root", password: "correct-horse" });
    expect(m.find("POST", "/auth/login")[0].body).toEqual({ username: "root", password: "correct-horse" });
  });

  it("已经有管理员时转到登录页", async () => {
    mockApi({ "GET /auth/status": ok({ initialized: true }) });
    renderPage(<SetupPage />, { path: "/setup" });
    await waitFor(() => expect(screen.getByTestId("location")).toHaveTextContent("/login"));
  });
});
