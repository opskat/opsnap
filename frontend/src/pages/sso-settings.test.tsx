import { describe, expect, it } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { SettingsPage } from "./settings";
import { renderPage } from "@/test/render";
import { fail, mockApi, ok } from "@/test/api-mock";

const EMPTY = {
  configured: false,
  issuer: "",
  client_id: "",
  client_secret: "",
  button_label: "Gitea",
  panel_url: "",
  callback_url: "",
};

const SAVED = {
  configured: true,
  issuer: "https://git.example.com/",
  client_id: "5f0c1e2a",
  client_secret: "已设置",
  button_label: "Gitea",
  panel_url: "https://opsnap.example.com",
  callback_url: "https://opsnap.example.com/api/v1/auth/oidc/callback",
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
