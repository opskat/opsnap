import { describe, expect, it } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { AppProviders } from "@/app/providers";
import { AppRoutes } from "@/App";
import { LocationProbe } from "@/test/render";
import { fail, mockApi, ok } from "@/test/api-mock";

const AUTHED = {
  "GET /auth/status": ok({ initialized: true }),
  "GET /auth/me": ok({ kind: "admin", username: "admin" }),
};

function renderApp(at: string) {
  return render(
    <MemoryRouter initialEntries={[at]}>
      <AppProviders>
        <AppRoutes />
        <LocationProbe />
      </AppProviders>
    </MemoryRouter>
  );
}

describe("登录守卫", () => {
  it("未登录时转到登录页并记住原地址", async () => {
    mockApi({ "GET /auth/status": ok({ initialized: true }), "GET /auth/me": fail(401, 11000, "未登录或凭据无效") });
    renderApp("/settings?tab=api");
    await waitFor(() =>
      expect(screen.getByTestId("location")).toHaveTextContent("/login?next=%2Fsettings%3Ftab%3Dapi")
    );
    expect(await screen.findByRole("heading", { name: "登录 opsnap" })).toBeInTheDocument();
  });

  it("还没有管理员时转到首次设置", async () => {
    mockApi({ "GET /auth/status": ok({ initialized: false }) });
    renderApp("/");
    await waitFor(() => expect(screen.getByTestId("location")).toHaveTextContent("/setup"));
  });
});

describe("面板外壳", () => {
  it("侧边栏分三组导航，首页是概览", async () => {
    mockApi(AUTHED);
    renderApp("/");
    const nav = (await screen.findAllByRole("navigation", { name: "主导航" }))[0];
    const links = within(nav)
      .getAllByRole("link")
      .map((a) => a.textContent);
    expect(links).toEqual(["概览", "备份任务", "快照", "运行记录", "服务器", "存储位置", "通知", "设置"]);
    expect(within(nav).getByText("资源")).toBeInTheDocument();
    expect(within(nav).getByText("系统")).toBeInTheDocument();
    expect(within(nav).getByRole("link", { name: "概览" })).toHaveAttribute("aria-current", "page");
    expect(await screen.findByRole("heading", { name: "暂时读不到任务状态" })).toBeInTheDocument();
  });

  it("深色模式切换后保存在本地", async () => {
    mockApi(AUTHED);
    renderApp("/");
    const user = userEvent.setup();
    await user.click((await screen.findAllByRole("button", { name: "切换到深色" }))[0]);
    expect(document.documentElement).toHaveClass("dark");
    expect(window.localStorage.getItem("opsnap-theme")).toBe("dark");
    await user.click(screen.getAllByRole("button", { name: "切换到浅色" })[0]);
    expect(document.documentElement).not.toHaveClass("dark");
    expect(window.localStorage.getItem("opsnap-theme")).toBe("light");
  });

  it("窄屏时可以从抽屉打开导航，点选后关闭", async () => {
    mockApi(AUTHED);
    renderApp("/");
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "打开导航" }));
    const drawer = await screen.findByRole("dialog", { name: "导航" });
    await user.click(within(drawer).getByRole("link", { name: "通知" }));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "导航" })).not.toBeInTheDocument());
    expect(screen.getByTestId("location")).toHaveTextContent("/notify");
  });

  it("账号菜单可以退出登录", async () => {
    const m = mockApi({ ...AUTHED, "POST /auth/logout": ok({}) });
    renderApp("/");
    const user = userEvent.setup();
    await user.click((await screen.findAllByRole("button", { name: "账号菜单" }))[0]);
    await user.click(await screen.findByRole("menuitem", { name: "退出登录" }));
    await waitFor(() => expect(screen.getByTestId("location")).toHaveTextContent("/login"));
    expect(m.find("POST", "/auth/logout")).toHaveLength(1);
  });

  it("会话已失效时退出登录直接回到登录页，不报错", async () => {
    mockApi({ ...AUTHED, "POST /auth/logout": fail(401, 11000, "未登录或凭据无效") });
    renderApp("/");
    const user = userEvent.setup();
    await user.click((await screen.findAllByRole("button", { name: "账号菜单" }))[0]);
    await user.click(await screen.findByRole("menuitem", { name: "退出登录" }));
    await waitFor(() => expect(screen.getByTestId("location").textContent).toBe("/login"));
    expect(screen.queryByText(/退出登录失败/)).not.toBeInTheDocument();
  });

  it("账号菜单可以修改密码", async () => {
    const m = mockApi({ ...AUTHED, "PUT /auth/password": ok({}) });
    renderApp("/");
    const user = userEvent.setup();
    await user.click((await screen.findAllByRole("button", { name: "账号菜单" }))[0]);
    await user.click(await screen.findByRole("menuitem", { name: "修改密码" }));
    const dialog = await screen.findByRole("dialog", { name: "修改密码" });
    await user.type(within(dialog).getByLabelText("原密码"), "correct-horse");
    await user.type(within(dialog).getByLabelText("新密码"), "battery-staple");
    await user.type(within(dialog).getByLabelText("再输一次"), "battery-staple");
    await user.click(within(dialog).getByRole("button", { name: "修改密码" }));
    await waitFor(() => expect(m.find("PUT", "/auth/password")).toHaveLength(1));
    expect(m.find("PUT", "/auth/password")[0].body).toEqual({
      old_password: "correct-horse",
      new_password: "battery-staple",
    });
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "修改密码" })).not.toBeInTheDocument());
  });

  it("修改密码时新密码少于 8 位就地提示", async () => {
    const m = mockApi({ ...AUTHED, "PUT /auth/password": ok({}) });
    renderApp("/");
    const user = userEvent.setup();
    await user.click((await screen.findAllByRole("button", { name: "账号菜单" }))[0]);
    await user.click(await screen.findByRole("menuitem", { name: "修改密码" }));
    const dialog = await screen.findByRole("dialog", { name: "修改密码" });
    await user.type(within(dialog).getByLabelText("原密码"), "correct-horse");
    await user.type(within(dialog).getByLabelText("新密码"), "short");
    await user.type(within(dialog).getByLabelText("再输一次"), "short");
    await user.click(within(dialog).getByRole("button", { name: "修改密码" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("新密码至少 8 位");
    expect(m.find("PUT", "/auth/password")).toHaveLength(0);
  });
});
