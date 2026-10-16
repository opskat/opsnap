import { describe, expect, it } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { SettingsPage } from "./settings";
import { renderPage } from "@/test/render";
import { PINNED_NOW_SEC, pinClock } from "@/test/clock";
import { fail, mockApi, ok } from "@/test/api-mock";

const SETTINGS = {
  admin_username: "admin",
  max_concurrent_runs: 2,
  retention_days: 90,
  timezone: "Asia/Shanghai",
  clear_mirror_after_backup: false,
  master_key: { configured: true, algorithm: "AES-256-GCM", rotated_at: 0 },
  mirror_dir: "/data/mirror",
};

const NOW = PINNED_NOW_SEC;
pinClock();
const KEY = { id: 3, name: "claude-code", prefix: "osk_7f3a", last_used_at: 0, created_at: NOW - 86400, expires_at: 0 };

describe("设置 · 通用", () => {
  it("显示当前设置和主密钥状态", async () => {
    mockApi({ "GET /settings": ok(SETTINGS) });
    renderPage(<SettingsPage />, { path: "/settings" });
    expect(await screen.findByLabelText("同时运行的任务")).toHaveValue("2");
    expect(screen.getByLabelText("运行记录保留天数")).toHaveValue("90");
    expect(screen.getByLabelText("时区")).toHaveValue("Asia/Shanghai");
    expect(screen.getByRole("switch", { name: "备份后清空文件镜像" })).toHaveAttribute("aria-checked", "false");
    expect(screen.getByText("已加载")).toBeInTheDocument();
    expect(screen.getByText("admin")).toBeInTheDocument();
  });

  it("明确标出存放明文的文件镜像目录", async () => {
    mockApi({ "GET /settings": ok(SETTINGS) });
    renderPage(<SettingsPage />, { path: "/settings" });
    const note = await screen.findByText("/data/mirror");
    expect(note.closest("p")).toHaveTextContent("文件镜像目录 /data/mirror 存放明文");
  });

  it("轮换主密钥：两次输入一致才提交，成功后提示修改环境变量", async () => {
    const m = mockApi({
      "GET /settings": ok(SETTINGS),
      "POST /settings/master-key/rotate": ok({ rotated_at: NOW }),
    });
    renderPage(<SettingsPage />, { path: "/settings" });
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "轮换主密钥" }));
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("新主密钥"), "new-master-key-1");
    await user.type(within(dialog).getByLabelText("再输入一次"), "new-master-key-2");
    await user.click(within(dialog).getByRole("button", { name: "轮换" }));
    expect(await within(dialog).findByText("两次输入不一致")).toBeInTheDocument();
    expect(m.find("POST", "/settings/master-key/rotate")).toHaveLength(0);

    await user.clear(within(dialog).getByLabelText("再输入一次"));
    await user.type(within(dialog).getByLabelText("再输入一次"), "new-master-key-1");
    await user.click(within(dialog).getByRole("button", { name: "轮换" }));
    await waitFor(() => expect(m.find("POST", "/settings/master-key/rotate")).toHaveLength(1));
    expect(m.find("POST", "/settings/master-key/rotate")[0].body).toEqual({ new_key: "new-master-key-1" });
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("把 OPSNAP_SECRET_KEY 改成新主密钥");
  });

  it("超出范围时就地提示，不提交", async () => {
    const m = mockApi({ "GET /settings": ok(SETTINGS) });
    renderPage(<SettingsPage />, { path: "/settings" });
    const user = userEvent.setup();
    const input = await screen.findByLabelText("同时运行的任务");
    await user.clear(input);
    await user.type(input, "0");
    const days = screen.getByLabelText("运行记录保留天数");
    await user.clear(days);
    await user.click(screen.getByRole("button", { name: "保存设置" }));
    expect(await screen.findByText("同时运行的任务数须在 1 到 64 之间")).toBeInTheDocument();
    expect(screen.getByText("运行记录保留天数须在 1 到 3650 之间")).toBeInTheDocument();
    expect(input).toHaveAttribute("aria-invalid", "true");
    expect(m.find("PUT", "/settings")).toHaveLength(0);
  });

  it("保存时整体提交四项设置", async () => {
    const m = mockApi({
      "GET /settings": ok(SETTINGS),
      "PUT /settings": (c) => ok({ ...SETTINGS, ...(c.body as object) }),
    });
    renderPage(<SettingsPage />, { path: "/settings" });
    const user = userEvent.setup();
    const input = await screen.findByLabelText("同时运行的任务");
    await user.clear(input);
    await user.type(input, "3");
    await user.click(screen.getByRole("switch", { name: "备份后清空文件镜像" }));
    await user.click(screen.getByRole("button", { name: "保存设置" }));
    await waitFor(() => expect(m.find("PUT", "/settings")).toHaveLength(1));
    expect(m.find("PUT", "/settings")[0].body).toEqual({
      max_concurrent_runs: 3,
      retention_days: 90,
      timezone: "Asia/Shanghai",
      clear_mirror_after_backup: true,
    });
    expect(await screen.findByText("设置已保存")).toBeInTheDocument();
  });

  it("服务端拒绝时显示原因", async () => {
    mockApi({
      "GET /settings": ok(SETTINGS),
      "PUT /settings": fail(400, 11011, '时区 "Mars/Base" 无效，请填写 IANA 时区名，例如 Asia/Shanghai'),
    });
    renderPage(<SettingsPage />, { path: "/settings" });
    const user = userEvent.setup();
    const tz = await screen.findByLabelText("时区");
    await user.clear(tz);
    await user.type(tz, "Mars/Base");
    await user.click(screen.getByRole("button", { name: "保存设置" }));
    expect(await screen.findByRole("alert")).toHaveTextContent('时区 "Mars/Base" 无效');
  });
});

describe("设置 · API 密钥", () => {
  function setup(extra: Parameters<typeof mockApi>[0] = {}) {
    let list = [KEY];
    const m = mockApi({
      "GET /settings": ok(SETTINGS),
      "GET /apikeys": () => ok({ list }),
      "POST /apikeys": (c) => {
        const body = c.body as { name: string };
        const item = {
          id: 9,
          name: body.name,
          prefix: "osk_9b2e",
          last_used_at: 0,
          created_at: NOW,
          expires_at: NOW + 90 * 86400,
        };
        list = [...list, item];
        return ok({ ...item, key: "osk_9b2e41c7d05a4f8e9c1a6b3d7e2f0a55" });
      },
      "DELETE /apikeys/3": () => {
        list = list.filter((k) => k.id !== 3);
        return ok(null);
      },
      ...extra,
    });
    renderPage(<SettingsPage />, { path: "/settings", at: "/settings?tab=api" });
    return m;
  }

  it("列表显示名称、前缀、最近使用、创建和过期", async () => {
    setup();
    const row = (await screen.findByText("claude-code")).closest("li")!;
    expect(within(row).getByText("osk_7f3a…")).toBeInTheDocument();
    expect(within(row).getByText("从未使用")).toBeInTheDocument();
    expect(within(row).getByText("永不")).toBeInTheDocument();
  });

  it("新建后完整密钥只显示一次", async () => {
    const m = setup();
    const user = userEvent.setup(); // user-event 自带剪贴板替身
    await screen.findByText("claude-code");

    await user.click(screen.getByRole("button", { name: "新建密钥" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText(/与管理员相同的权限/)).toBeInTheDocument();
    await user.type(within(dialog).getByLabelText("名称"), "n8n 巡检");
    await user.click(within(dialog).getByRole("button", { name: "创建" }));

    expect(await within(dialog).findByText("osk_9b2e41c7d05a4f8e9c1a6b3d7e2f0a55")).toBeInTheDocument();
    expect(within(dialog).getByText(/只显示这一次/)).toBeInTheDocument();
    expect(m.find("POST", "/apikeys")[0].body).toEqual({ name: "n8n 巡检", expires_in: "90d" });
    // 列表随之刷新
    expect(await screen.findByText("n8n 巡检")).toBeInTheDocument();

    await user.click(within(dialog).getByRole("button", { name: "复制密钥" }));
    await expect(navigator.clipboard.readText()).resolves.toBe("osk_9b2e41c7d05a4f8e9c1a6b3d7e2f0a55");

    await user.click(within(dialog).getByRole("button", { name: "我已复制" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(screen.queryByText("osk_9b2e41c7d05a4f8e9c1a6b3d7e2f0a55")).not.toBeInTheDocument();

    // 再次打开是空白表单，不再出现旧密钥
    await user.click(screen.getByRole("button", { name: "新建密钥" }));
    const again = await screen.findByRole("dialog");
    expect(within(again).getByLabelText("名称")).toHaveValue("");
    expect(within(again).queryByText(/osk_9b2e41/)).not.toBeInTheDocument();
  });

  it("名称为空时不能创建", async () => {
    const m = setup();
    const user = userEvent.setup();
    await screen.findByText("claude-code");
    await user.click(screen.getByRole("button", { name: "新建密钥" }));
    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: "创建" }));
    expect(await within(dialog).findByText("填写名称，方便以后认出这把密钥")).toBeInTheDocument();
    expect(m.find("POST", "/apikeys")).toHaveLength(0);
  });

  it("撤销前确认，确认后立即从列表移除", async () => {
    const m = setup();
    const user = userEvent.setup();
    const row = (await screen.findByText("claude-code")).closest("li")!;
    await user.click(within(row).getByRole("button", { name: "撤销 claude-code" }));
    const confirm = await screen.findByRole("alertdialog");
    expect(confirm).toHaveTextContent("claude-code");
    await user.click(within(confirm).getByRole("button", { name: "撤销" }));
    await waitFor(() => expect(m.find("DELETE", "/apikeys/3")).toHaveLength(1));
    await waitFor(() => expect(screen.queryByText("claude-code")).not.toBeInTheDocument());
  });

  it("取消撤销不发请求", async () => {
    const m = setup();
    const user = userEvent.setup();
    const row = (await screen.findByText("claude-code")).closest("li")!;
    await user.click(within(row).getByRole("button", { name: "撤销 claude-code" }));
    const confirm = await screen.findByRole("alertdialog");
    await user.click(within(confirm).getByRole("button", { name: "取消" }));
    expect(m.find("DELETE", "/apikeys/3")).toHaveLength(0);
  });

  it("显示 HTTP 接入方式和 OpenAPI 地址", async () => {
    setup();
    await screen.findByText("claude-code");
    expect(screen.getByText("/api/v1/openapi.json")).toBeInTheDocument();
    expect(screen.getByText(/Authorization: Bearer osk_/)).toBeInTheDocument();
  });
});
