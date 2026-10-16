import { describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { SettingsPage } from "./settings";
import { renderPage } from "@/test/render";
import { fail, mockApi, ok } from "@/test/api-mock";
import { redirect } from "@/lib/redirect";

const EMPTY = {
  configured: false,
  issuer: "",
  client_id: "",
  client_secret: "",
  button_label: "Gitea",
  panel_url: "",
  callback_url: "",
  binding: null,
  password_login: true,
  password_login_forced: false,
};

const SAVED = {
  configured: true,
  issuer: "https://git.example.com/",
  client_id: "5f0c1e2a",
  client_secret: "已设置",
  button_label: "Gitea",
  panel_url: "https://opsnap.example.com",
  callback_url: "https://opsnap.example.com/api/v1/auth/oidc/callback",
  binding: null,
  password_login: true,
  password_login_forced: false,
};

const BOUND = {
  ...SAVED,
  binding: { issuer: "https://git.example.com/", sub: "3", name: "alice-ops", bound_at: 1_800_000_000 },
};

function open() {
  renderPage(<SettingsPage />, { path: "/settings", at: "/settings?tab=sso" });
}

describe("设置 · 单点登录 · 提供方", () => {
  it("有「单点登录」标签，未配置时预填当前面板地址并实时显示回调地址", async () => {
    mockApi({ "GET /settings/sso": ok(EMPTY) });
    open();
    expect(screen.getByRole("tab", { name: "单点登录" })).toHaveAttribute("aria-selected", "true");
    const panel = await screen.findByLabelText("面板对外地址");
    expect(panel).toHaveValue(window.location.origin);
    expect(screen.getByLabelText("按钮文字")).toHaveValue("Gitea");
    expect(screen.getByText(`${window.location.origin}/api/v1/auth/oidc/callback`)).toBeInTheDocument();

    const user = userEvent.setup();
    await user.clear(panel);
    await user.type(panel, "https://opsnap.example.com/");
    expect(screen.getByText("https://opsnap.example.com/api/v1/auth/oidc/callback")).toBeInTheDocument();
    expect(screen.getByText("登录页显示「用 Gitea 登录」")).toBeInTheDocument();
  });

  it("必填项缺失时不提交", async () => {
    const m = mockApi({ "GET /settings/sso": ok(EMPTY) });
    open();
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "保存并检查" }));
    expect(await screen.findByText("填写 Issuer 地址，例如 https://git.example.com/")).toBeInTheDocument();
    expect(screen.getByText("填写 Client ID")).toBeInTheDocument();
    expect(screen.getByText("填写 Client Secret")).toBeInTheDocument();
    expect(m.find("PUT", "/settings/sso")).toHaveLength(0);
  });

  it("保存并检查通过后提示，Client Secret 不再显示内容", async () => {
    const m = mockApi({ "GET /settings/sso": ok(EMPTY), "PUT /settings/sso": ok(SAVED) });
    open();
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText("Issuer 地址"), "https://git.example.com/");
    await user.type(screen.getByLabelText("Client ID"), "5f0c1e2a");
    await user.type(screen.getByLabelText("Client Secret"), "s3cret");
    const panel = screen.getByLabelText("面板对外地址");
    await user.clear(panel);
    await user.type(panel, "https://opsnap.example.com");
    await user.click(screen.getByRole("button", { name: "保存并检查" }));
    expect(await screen.findByText("已保存，提供方检查通过")).toBeInTheDocument();
    expect(m.find("PUT", "/settings/sso")[0].body).toEqual({
      issuer: "https://git.example.com/",
      client_id: "5f0c1e2a",
      client_secret: "s3cret",
      button_label: "Gitea",
      panel_url: "https://opsnap.example.com",
    });
    expect(screen.getByLabelText("Client Secret")).toHaveValue("");
    expect(screen.getByLabelText("Client Secret")).toHaveAttribute("placeholder", "已设置");
  });

  it("检查失败时显示服务端写明的原因", async () => {
    mockApi({
      "GET /settings/sso": ok(SAVED),
      "PUT /settings/sso": fail(
        400,
        11805,
        "读取 https://git.example.com/.well-known/openid-configuration 失败：connection refused。检查 Issuer 地址和网络后再保存"
      ),
    });
    open();
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "保存并检查" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("connection refused");
  });

  it("已配置时 Client Secret 留空表示不修改", async () => {
    const m = mockApi({ "GET /settings/sso": ok(SAVED), "PUT /settings/sso": ok(SAVED) });
    open();
    const user = userEvent.setup();
    expect(await screen.findByText("已设置；留空表示不修改")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "保存并检查" }));
    await waitFor(() => expect(m.find("PUT", "/settings/sso")).toHaveLength(1));
    expect(m.find("PUT", "/settings/sso")[0].body).toMatchObject({ client_secret: "" });
  });

  it("停用单点登录需要确认", async () => {
    const m = mockApi({ "GET /settings/sso": ok(SAVED), "DELETE /settings/sso": ok(EMPTY) });
    open();
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "停用单点登录" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(m.find("DELETE", "/settings/sso")).toHaveLength(0);
    await user.click(within(dialog).getByRole("button", { name: "停用" }));
    await waitFor(() => expect(m.find("DELETE", "/settings/sso")).toHaveLength(1));
    expect(await screen.findByRole("button", { name: "保存并检查" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "停用单点登录" })).not.toBeInTheDocument();
  });
});

describe("设置 · 单点登录 · 绑定账号", () => {
  it("未保存提供方配置时不能绑定", async () => {
    mockApi({ "GET /settings/sso": ok(EMPTY) });
    open();
    expect(await screen.findByText("先保存提供方配置。")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "绑定 Gitea 账号" })).toBeDisabled();
  });

  it("未绑定时点击绑定跳到提供方授权", async () => {
    const to = vi.spyOn(redirect, "to").mockImplementation(() => {});
    const m = mockApi({
      "GET /settings/sso": ok(SAVED),
      "POST /settings/sso/bind": ok({ url: "https://git.example.com/login/oauth/authorize?state=b" }),
    });
    open();
    const user = userEvent.setup();
    expect(await screen.findByText("还没有绑定。点击后跳转到 Gitea 授权，完成后回到这里。")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "绑定 Gitea 账号" }));
    await waitFor(() => expect(to).toHaveBeenCalledWith("https://git.example.com/login/oauth/authorize?state=b"));
    expect(m.find("POST", "/settings/sso/bind")).toHaveLength(1);
    to.mockRestore();
  });

  it("已绑定时显示账号、sub 和 issuer，解除绑定需要确认", async () => {
    const m = mockApi({ "GET /settings/sso": ok(BOUND), "DELETE /settings/sso/binding": ok(SAVED) });
    open();
    const user = userEvent.setup();
    expect(await screen.findByText("alice-ops")).toBeInTheDocument();
    expect(screen.getByText(/sub 3 · https:\/\/git\.example\.com\//)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "解除绑定" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(dialog).toHaveTextContent("解除后不能再用 Gitea 登录，直到重新绑定");
    await user.click(within(dialog).getByRole("button", { name: "解除绑定" }));
    await waitFor(() => expect(m.find("DELETE", "/settings/sso/binding")).toHaveLength(1));
    expect(await screen.findByRole("button", { name: "绑定 Gitea 账号" })).toBeInTheDocument();
  });

  it("绑定失败跳回来时在顶部显示原因", async () => {
    mockApi({
      "GET /settings/sso": ok(SAVED),
      "GET /auth/oidc/result": ok({ message: "Gitea 拒绝了授权（access_denied）。" }),
    });
    renderPage(<SettingsPage />, { path: "/settings", at: "/settings?tab=sso&sso_error=e1" });
    const banner = await screen.findByText("绑定失败：Gitea 拒绝了授权（access_denied）。");
    // 红色提示
    expect(banner).toHaveClass("text-fail");
    expect(banner.closest('[role="alert"]')).toHaveClass("bg-fail-wash");
  });

  it("已绑定时修改 Issuer 或 Client ID，保存前确认会解除绑定；只改按钮文字不确认", async () => {
    const m = mockApi({ "GET /settings/sso": ok(BOUND), "PUT /settings/sso": ok(BOUND) });
    open();
    const user = userEvent.setup();
    const label = await screen.findByLabelText("按钮文字");
    await user.clear(label);
    await user.type(label, "公司 Git");
    await user.click(screen.getByRole("button", { name: "保存并检查" }));
    await waitFor(() => expect(m.find("PUT", "/settings/sso")).toHaveLength(1));
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();

    const cid = screen.getByLabelText("Client ID");
    await user.clear(cid);
    await user.type(cid, "another-client");
    await user.click(screen.getByRole("button", { name: "保存并检查" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(dialog).toHaveTextContent("保存后会解除当前绑定");
    expect(m.find("PUT", "/settings/sso")).toHaveLength(1);
    await user.click(within(dialog).getByRole("button", { name: "保存并解除绑定" }));
    await waitFor(() => expect(m.find("PUT", "/settings/sso")).toHaveLength(2));
  });
});

describe("设置 · 单点登录 · 密码登录", () => {
  const PW_OFF = { ...BOUND, password_login: false };
  const pwSwitch = () => screen.getByRole("switch", { name: "允许用户名密码登录" });

  it("没有绑定时开关不可关", async () => {
    mockApi({ "GET /settings/sso": ok(SAVED) });
    open();
    expect(await screen.findByText("绑定账号后才能关闭")).toBeInTheDocument();
    expect(pwSwitch()).toBeDisabled();
    expect(pwSwitch()).toHaveAttribute("aria-checked", "true");
  });

  it("关闭前确认，写明恢复方法；确认后立即保存", async () => {
    const m = mockApi({ "GET /settings/sso": ok(BOUND), "PUT /settings/sso/password-login": ok(PW_OFF) });
    open();
    const user = userEvent.setup();
    await screen.findByText("alice-ops");
    await user.click(pwSwitch());
    const dialog = await screen.findByRole("alertdialog");
    expect(dialog).toHaveTextContent("之后只能用 Gitea 账号 alice-ops 登录");
    expect(dialog).toHaveTextContent("OPSNAP_PASSWORD_LOGIN=1");
    expect(m.find("PUT", "/settings/sso/password-login")).toHaveLength(0);
    await user.click(within(dialog).getByRole("button", { name: "关闭密码登录" }));
    await waitFor(() => expect(m.find("PUT", "/settings/sso/password-login")[0]?.body).toEqual({ enabled: false }));
    await waitFor(() => expect(pwSwitch()).toHaveAttribute("aria-checked", "false"));
  });

  it("关闭期间锁定 Issuer、Client ID、解除绑定和停用；打开不需要确认", async () => {
    const m = mockApi({ "GET /settings/sso": ok(PW_OFF), "PUT /settings/sso/password-login": ok(BOUND) });
    open();
    const user = userEvent.setup();
    expect(await screen.findByLabelText("Issuer 地址")).toBeDisabled();
    expect(screen.getByLabelText("Client ID")).toBeDisabled();
    expect(screen.getByLabelText("按钮文字")).toBeEnabled();
    expect(screen.getByRole("button", { name: "解除绑定" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "停用单点登录" })).toBeDisabled();
    expect(screen.getAllByText("密码登录关闭期间不能修改，避免绑定失效后无法登录").length).toBeGreaterThan(0);
    expect(screen.getByText("密码登录关闭期间不能停用单点登录。")).toBeInTheDocument();
    await user.click(pwSwitch());
    await waitFor(() => expect(m.find("PUT", "/settings/sso/password-login")[0]?.body).toEqual({ enabled: true }));
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
  });

  it("环境变量强制打开时开关显示为开且不可操作，写明保存的值，锁定不生效", async () => {
    mockApi({ "GET /settings/sso": ok({ ...PW_OFF, password_login_forced: true }) });
    open();
    await screen.findByText("alice-ops");
    expect(pwSwitch()).toHaveAttribute("aria-checked", "true");
    expect(pwSwitch()).toBeDisabled();
    expect(screen.getByText(/OPSNAP_PASSWORD_LOGIN=1/).closest("p")).toHaveTextContent(
      "环境变量 OPSNAP_PASSWORD_LOGIN=1 正在强制打开密码登录（设置里保存的是「关闭」）。恢复访问后，去掉这个变量并重启容器。"
    );
    expect(screen.getByLabelText("Issuer 地址")).toBeEnabled();
    expect(screen.getByRole("button", { name: "解除绑定" })).toBeEnabled();
  });

  it("保存的值是「开」时只写明正在被环境变量强制打开", async () => {
    mockApi({ "GET /settings/sso": ok({ ...BOUND, password_login_forced: true }) });
    open();
    await screen.findByText("alice-ops");
    expect(pwSwitch()).toHaveAttribute("aria-checked", "true");
    expect(screen.getByText(/OPSNAP_PASSWORD_LOGIN=1/).closest("p")).toHaveTextContent(
      /^环境变量 OPSNAP_PASSWORD_LOGIN=1 正在强制打开密码登录。$/
    );
  });
});
