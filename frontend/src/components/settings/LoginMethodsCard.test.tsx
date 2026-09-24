import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

import { LoginMethodsCard } from "@/components/settings/LoginMethodsCard";
import i18n from "@/i18n";

const ok = (data: unknown) => new Response(JSON.stringify({ code: 0, msg: "success", data }), { status: 200 });
const fail = (code: number, msg: string, status = 400) => new Response(JSON.stringify({ code, msg }), { status });

const callback = `${window.location.origin}/api/v1/auth/oidc/callback`;
const unconfigured = {
  configured: false,
  display_name: "",
  issuer: "",
  client_id: "",
  has_secret: false,
  scopes: null,
  redirect_url: "",
  binding: null,
  password_login: true,
  can_disable_password_login: false,
};
const configured = {
  configured: true,
  display_name: "Keycloak",
  issuer: "https://sso.example.com/realms/ops",
  client_id: "opsnap",
  has_secret: true,
  scopes: ["openid", "profile", "email"],
  redirect_url: callback,
  binding: null,
  password_login: true,
  can_disable_password_login: false,
};
const bound = {
  ...configured,
  binding: { subject: "8f1c2a", display: "ops@example.com", bound_at: 1790000000, last_login_at: 0 },
};

let fetchMock: ReturnType<typeof vi.fn>;
const respond = (...rs: Response[]) => rs.forEach((r) => fetchMock.mockResolvedValueOnce(r));
const body = (i: number) => JSON.parse((fetchMock.mock.calls[i][1] as RequestInit).body as string);

function renderCard(entry = "/settings") {
  render(
    <MemoryRouter initialEntries={[entry]}>
      <LoginMethodsCard />
    </MemoryRouter>
  );
}

async function fillConfig(dialog: HTMLElement, secret = "s3cret") {
  await userEvent.clear(within(dialog).getByLabelText("显示名称"));
  await userEvent.type(within(dialog).getByLabelText("显示名称"), "Keycloak");
  await userEvent.clear(within(dialog).getByLabelText("Issuer"));
  await userEvent.type(within(dialog).getByLabelText("Issuer"), "https://sso.example.com/realms/ops");
  await userEvent.clear(within(dialog).getByLabelText("Client ID"));
  await userEvent.type(within(dialog).getByLabelText("Client ID"), "opsnap");
  if (secret) await userEvent.type(within(dialog).getByLabelText("Client Secret"), secret);
}

beforeAll(() => i18n.changeLanguage("zh-CN"));
beforeEach(() => {
  fetchMock = vi.fn();
  vi.stubGlobal("fetch", fetchMock);
});
afterEach(() => vi.unstubAllGlobals());

describe("设置页 · 登录方式", () => {
  it("未配置时提示并可配置；保存时带上当前地址生成的回调地址与默认 Scopes", async () => {
    respond(ok(unconfigured));
    renderCard();
    const card = await screen.findByRole("region", { name: "登录方式" });
    expect(within(card).getByText("未配置")).toBeInTheDocument();
    await userEvent.click(within(card).getByRole("button", { name: "配置 OIDC" }));
    const dialog = await screen.findByRole("dialog", { name: "配置 OIDC" });
    expect(within(dialog).getByText(callback)).toBeInTheDocument();
    await fillConfig(dialog);
    respond(ok(configured));
    await userEvent.click(within(dialog).getByRole("button", { name: "保存" }));
    expect(await within(card).findByText("https://sso.example.com/realms/ops")).toBeInTheDocument();
    expect(body(1)).toEqual({
      display_name: "Keycloak",
      issuer: "https://sso.example.com/realms/ops",
      client_id: "opsnap",
      client_secret: "s3cret",
      scopes: ["openid", "profile", "email"],
      redirect_url: callback,
      confirm_reset: false,
    });
  });

  it("保存失败时在弹窗里显示原因", async () => {
    respond(ok(unconfigured));
    renderCard();
    await userEvent.click(await screen.findByRole("button", { name: "配置 OIDC" }));
    const dialog = await screen.findByRole("dialog", { name: "配置 OIDC" });
    await fillConfig(dialog);
    respond(fail(10302, "无法连接 https://sso.example.com/realms/ops：连接超时"));
    await userEvent.click(within(dialog).getByRole("button", { name: "保存" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("无法连接");
  });

  it("已配置未绑定：显示配置、Secret 不回显、可绑定", async () => {
    respond(ok(configured));
    renderCard();
    const card = await screen.findByRole("region", { name: "登录方式" });
    expect(await within(card).findByText("已保存（加密存储）")).toBeInTheDocument();
    expect(within(card).getByText(callback)).toBeInTheDocument();
    expect(within(card).getByText("未绑定")).toBeInTheDocument();
    expect(within(card).getByRole("link", { name: "绑定" })).toHaveAttribute("href", "/api/v1/auth/oidc/bind");
  });

  it("编辑时 Secret 留空表示不修改；修改 Client ID 需要确认清除绑定", async () => {
    respond(ok(bound));
    renderCard();
    await userEvent.click(await screen.findByRole("button", { name: "编辑" }));
    const dialog = await screen.findByRole("dialog", { name: "配置 OIDC" });
    expect(within(dialog).getByLabelText("Client Secret")).toHaveAttribute("placeholder", "留空表示不修改");
    await userEvent.clear(within(dialog).getByLabelText("Client ID"));
    await userEvent.type(within(dialog).getByLabelText("Client ID"), "other");
    respond(fail(10304, "修改 Issuer 或 Client ID 会清除已有绑定，请确认后再保存", 409));
    await userEvent.click(within(dialog).getByRole("button", { name: "保存" }));
    const confirm = await screen.findByRole("dialog", { name: "清除已有绑定？" });
    respond(ok({ ...configured, client_id: "other" }));
    await userEvent.click(within(confirm).getByRole("button", { name: "确认并保存" }));
    expect(await screen.findByText("未绑定")).toBeInTheDocument();
    expect(body(1).client_secret).toBe("");
    expect(body(2).confirm_reset).toBe(true);
  });

  it("保存后再次编辑，表单显示刚保存的配置", async () => {
    respond(ok(configured));
    renderCard();
    await userEvent.click(await screen.findByRole("button", { name: "编辑" }));
    let dialog = await screen.findByRole("dialog", { name: "配置 OIDC" });
    await userEvent.clear(within(dialog).getByLabelText("显示名称"));
    await userEvent.type(within(dialog).getByLabelText("显示名称"), "Corp SSO");
    respond(ok({ ...configured, display_name: "Corp SSO" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "保存" }));
    const card = await screen.findByRole("region", { name: "登录方式" });
    expect(await within(card).findByText("Corp SSO")).toBeInTheDocument();
    await userEvent.click(within(card).getByRole("button", { name: "编辑" }));
    dialog = await screen.findByRole("dialog", { name: "配置 OIDC" });
    expect(within(dialog).getByLabelText("显示名称")).toHaveValue("Corp SSO");
  });

  it("已绑定：显示身份，解除绑定需要确认", async () => {
    respond(ok(bound));
    renderCard();
    const card = await screen.findByRole("region", { name: "登录方式" });
    expect(await within(card).findByText("已绑定")).toBeInTheDocument();
    expect(within(card).getByText("ops@example.com")).toBeInTheDocument();
    expect(within(card).queryByText(/8f1c2a/)).not.toBeInTheDocument();
    await userEvent.click(within(card).getByRole("button", { name: "解除绑定" }));
    const confirm = await screen.findByRole("dialog", { name: "解除 OIDC 绑定？" });
    respond(ok({}), ok(configured));
    await userEvent.click(within(confirm).getByRole("button", { name: "解除绑定" }));
    expect(await within(card).findByText("未绑定")).toBeInTheDocument();
    expect(fetchMock.mock.calls[1][0]).toBe("/api/v1/auth/oidc/unbind");
  });

  it("身份没有邮箱和 preferred_username 时只显示 sub", async () => {
    respond(ok({ ...bound, binding: { ...bound.binding, display: "" } }));
    renderCard();
    const card = await screen.findByRole("region", { name: "登录方式" });
    expect(await within(card).findByText("8f1c2a")).toBeInTheDocument();
  });

  it("未通过 OIDC 登录过时，密码登录开关不可关闭并说明原因", async () => {
    respond(ok(bound));
    renderCard();
    const sw = await screen.findByRole("switch", { name: "密码登录" });
    expect(sw).toBeChecked();
    expect(sw).toBeDisabled();
    expect(screen.getByText(/用它成功登录一次/)).toBeInTheDocument();
  });

  it("关闭密码登录需要确认；关闭后可以直接重新开启", async () => {
    respond(ok({ ...bound, can_disable_password_login: true }));
    renderCard();
    await userEvent.click(await screen.findByRole("switch", { name: "密码登录" }));
    const confirm = await screen.findByRole("dialog", { name: "关闭密码登录？" });
    expect(confirm).toHaveTextContent("opsnap admin reset-password");
    respond(ok({ password_login: false }));
    await userEvent.click(within(confirm).getByRole("button", { name: "关闭密码登录" }));
    const sw = await screen.findByRole("switch", { name: "密码登录" });
    await vi.waitFor(() => expect(sw).not.toBeChecked());
    expect(body(1)).toEqual({ enabled: false });

    respond(ok({ password_login: true }));
    await userEvent.click(sw);
    await vi.waitFor(() => expect(sw).toBeChecked());
    expect(body(2)).toEqual({ enabled: true });
  });

  it("密码登录已关闭时，解除绑定的确认框说明会重新开启", async () => {
    respond(ok({ ...bound, password_login: false, can_disable_password_login: true }));
    renderCard();
    await userEvent.click(await screen.findByRole("button", { name: "解除绑定" }));
    const confirm = await screen.findByRole("dialog", { name: "解除 OIDC 绑定？" });
    expect(confirm).toHaveTextContent("密码登录会自动重新开启");
  });

  it("绑定回跳后显示结果", async () => {
    respond(ok(bound));
    renderCard("/settings?oidc=bound");
    expect(await screen.findByRole("status")).toHaveTextContent("已绑定 OIDC 身份");
  });

  it("绑定失败回跳后显示原因", async () => {
    respond(ok(configured));
    renderCard("/settings?oidc_error=unreachable");
    expect(await screen.findByRole("alert")).toHaveTextContent("无法连接 OIDC 提供方");
  });

  it("已绑定时再次绑定被拒绝，回跳后说明需要先解除", async () => {
    respond(ok(bound));
    renderCard("/settings?oidc_error=already_bound");
    expect(await screen.findByRole("alert")).toHaveTextContent("已绑定 OIDC 身份，请先解除当前绑定");
  });
});
