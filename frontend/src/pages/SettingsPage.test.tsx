import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";

import i18n from "@/i18n";
import { SettingsPage } from "@/pages/SettingsPage";

const ok = (data: unknown) => new Response(JSON.stringify({ code: 0, msg: "success", data }), { status: 200 });
const fail = (code: number, msg: string, status = 400) => new Response(JSON.stringify({ code, msg }), { status });

// 2026-09-23 14:02 与 2026-09-30 14:02（本地时区）
const updated = new Date(2026, 8, 23, 14, 2).getTime() / 1000;
const expires = new Date(2026, 8, 30, 14, 2).getTime() / 1000;
const me = {
  username: "admin",
  password_updated_at: updated,
  session: {
    user_agent: "Mozilla/5.0 (Macintosh) AppleWebKit/537.36 Chrome/140.0 Safari/537.36",
    ip: "192.168.8.20",
    expires_at: expires,
  },
};

// 账号相关请求按顺序取 responses；令牌列表固定返回空，与本文件无关
function renderSettings(...responses: Response[]) {
  const queue = [...responses];
  const fetchMock = vi.fn((url: string) =>
    Promise.resolve(
      url === "/api/v1/tokens"
        ? ok({ items: [] })
        : url === "/api/v1/auth/oidc/config"
          ? ok({ configured: false, binding: null })
          : (queue.shift() ?? fail(-1, "unexpected"))
    )
  );
  vi.stubGlobal("fetch", fetchMock);
  render(
    <MemoryRouter>
      <SettingsPage />
    </MemoryRouter>
  );
  return {
    calls: () =>
      fetchMock.mock.calls.filter(
        ([url]) => url !== "/api/v1/tokens" && url !== "/api/v1/auth/oidc/config"
      ) as unknown as [string, RequestInit][],
  };
}

async function openDialog() {
  await userEvent.click(await screen.findByRole("button", { name: "修改密码" }));
  return screen.findByRole("dialog", { name: "修改密码" });
}

beforeAll(() => i18n.changeLanguage("zh-CN"));
afterEach(() => vi.unstubAllGlobals());

describe("设置页 · 账号", () => {
  it("显示用户名、密码最后修改时间与当前会话", async () => {
    renderSettings(ok(me));
    const card = await screen.findByRole("region", { name: "账号" });
    expect(within(card).getByText("admin")).toBeInTheDocument();
    expect(within(card).getByText("2026-09-23 14:02")).toBeInTheDocument();
    expect(within(card).getByText("Chrome · 192.168.8.20 · 2026-09-30 14:02 过期")).toBeInTheDocument();
  });

  it("加载失败时显示错误并可重试", async () => {
    renderSettings(new Response("oops", { status: 502, statusText: "Bad Gateway" }), ok(me));
    await userEvent.click(await screen.findByRole("button", { name: "重试" }));
    expect(await screen.findByText("2026-09-23 14:02")).toBeInTheDocument();
  });

  it("修改密码：本地校验不通过时不提交", async () => {
    const { calls } = renderSettings(ok(me));
    const dialog = await openDialog();
    await userEvent.type(within(dialog).getByLabelText("当前密码"), "old-password-123");
    await userEvent.type(within(dialog).getByLabelText("新密码"), "short");
    await userEvent.type(within(dialog).getByLabelText("确认新密码"), "different");
    await userEvent.click(within(dialog).getByRole("button", { name: "保存" }));
    expect(within(dialog).getByText("至少 12 个字符")).toBeInTheDocument();
    expect(within(dialog).getByText("两次输入的密码不一致")).toBeInTheDocument();
    expect(calls()).toHaveLength(1);
  });

  it("修改密码：当前密码错误显示在该字段旁", async () => {
    renderSettings(ok(me), fail(10106, "当前密码不正确"));
    const dialog = await openDialog();
    await userEvent.type(within(dialog).getByLabelText("当前密码"), "wrong-password-1");
    await userEvent.type(within(dialog).getByLabelText("新密码"), "brand-new-password");
    await userEvent.type(within(dialog).getByLabelText("确认新密码"), "brand-new-password");
    await userEvent.click(within(dialog).getByRole("button", { name: "保存" }));
    expect(await within(dialog).findByText("当前密码不正确")).toBeInTheDocument();
    expect(within(dialog).getByLabelText("当前密码")).toHaveAttribute("aria-invalid", "true");
  });

  it("修改密码成功后关闭弹窗、提示成功并刷新修改时间", async () => {
    const later = new Date(2026, 8, 24, 9, 30).getTime() / 1000;
    const { calls } = renderSettings(ok(me), ok({}), ok({ ...me, password_updated_at: later }));
    const dialog = await openDialog();
    await userEvent.type(within(dialog).getByLabelText("当前密码"), "old-password-123");
    await userEvent.type(within(dialog).getByLabelText("新密码"), "brand-new-password");
    await userEvent.type(within(dialog).getByLabelText("确认新密码"), "brand-new-password");
    await userEvent.click(within(dialog).getByRole("button", { name: "保存" }));
    expect(await screen.findByText("2026-09-24 09:30")).toBeInTheDocument();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("密码已修改，其他浏览器的登录已退出");
    const [url, init] = calls()[1];
    expect(url).toBe("/api/v1/auth/password");
    expect(JSON.parse(init.body as string)).toEqual({
      current_password: "old-password-123",
      new_password: "brand-new-password",
    });
  });
});
