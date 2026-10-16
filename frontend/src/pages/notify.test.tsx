import { describe, expect, it } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { NotifyPage } from "./notify";
import { renderPage } from "@/test/render";
import { fail, mockApi, ok } from "@/test/api-mock";

const CFG = {
  url: "https://hooks.example.com/opsnap/7f3a",
  headers: { Authorization: "已设置" },
  on_failed: true,
  on_recovered: true,
  on_stale: true,
  on_success: false,
  stale_hours: 48,
};

function render(extra: Parameters<typeof mockApi>[0] = {}) {
  const m = mockApi({
    "GET /notify": ok(CFG),
    "PUT /notify": (c) => ok(c.body),
    ...extra,
  });
  renderPage(<NotifyPage />, { path: "/notify" });
  return m;
}

describe("通知页", () => {
  it("显示已保存的地址、事件开关和阈值，请求头值不显示明文", async () => {
    render();
    expect(await screen.findByLabelText("Webhook 地址")).toHaveValue(CFG.url);
    expect(screen.getByRole("switch", { name: "备份失败" })).toHaveAttribute("aria-checked", "true");
    expect(screen.getByRole("switch", { name: "每次成功" })).toHaveAttribute("aria-checked", "false");
    expect(screen.getByLabelText("太久没有成功的阈值（小时）")).toHaveValue("48");
    expect(screen.getByLabelText("请求头 1 名称")).toHaveValue("Authorization");
    expect(screen.getByLabelText("请求头 1 值")).toHaveValue("");
    expect(screen.getByLabelText("请求头 1 值")).toHaveAttribute("placeholder", "已设置，留空保持不变");
  });

  it("测试消息显示对方返回的 2xx 状态码", async () => {
    const m = render({ "POST /notify/test": ok({ status_code: 200 }) });
    const user = userEvent.setup();
    await screen.findByLabelText("Webhook 地址");
    await user.click(screen.getByRole("button", { name: "发一条测试消息" }));
    expect(await screen.findByText("已送达，对方返回 200")).toBeInTheDocument();
    expect(m.find("POST", "/notify/test")).toHaveLength(1);
  });

  it("对方返回非 2xx 时写明状态码", async () => {
    render({ "POST /notify/test": ok({ status_code: 503 }) });
    const user = userEvent.setup();
    await screen.findByLabelText("Webhook 地址");
    await user.click(screen.getByRole("button", { name: "发一条测试消息" }));
    expect(await screen.findByText("对方返回 503，请检查 Webhook 地址和请求头")).toBeInTheDocument();
  });

  it("发不出去时显示原因", async () => {
    render({ "POST /notify/test": fail(400, 11105, "测试消息发送失败：connection refused") });
    const user = userEvent.setup();
    await screen.findByLabelText("Webhook 地址");
    await user.click(screen.getByRole("button", { name: "发一条测试消息" }));
    expect(await screen.findByText("测试消息发送失败：connection refused")).toBeInTheDocument();
  });

  it("有未保存的修改时先保存再测试", async () => {
    render();
    const user = userEvent.setup();
    const url = await screen.findByLabelText("Webhook 地址");
    await user.type(url, "/x");
    expect(screen.getByRole("button", { name: "发一条测试消息" })).toBeDisabled();
    expect(screen.getByText("有未保存的修改，保存后再测试")).toBeInTheDocument();
  });

  it("保存时未改动的请求头回传「已设置」保留原值，新请求头带明文", async () => {
    const m = render();
    const user = userEvent.setup();
    await screen.findByLabelText("Webhook 地址");
    await user.click(screen.getByRole("button", { name: "添加请求头" }));
    await user.type(screen.getByLabelText("请求头 2 名称"), "X-Token");
    await user.type(screen.getByLabelText("请求头 2 值"), "s3cret");
    await user.click(screen.getByRole("switch", { name: "每次成功" }));
    const hours = screen.getByLabelText("太久没有成功的阈值（小时）");
    await user.clear(hours);
    await user.type(hours, "24");
    await user.click(screen.getByRole("button", { name: "保存" }));
    await waitFor(() => expect(m.find("PUT", "/notify")).toHaveLength(1));
    expect(m.find("PUT", "/notify")[0].body).toEqual({
      url: CFG.url,
      headers: { Authorization: "已设置", "X-Token": "s3cret" },
      on_failed: true,
      on_recovered: true,
      on_stale: true,
      on_success: true,
      stale_hours: 24,
      // 消息里的链接以面板地址开头：保存时自动带上浏览器里的地址
      panel_url: window.location.origin,
    });
    expect(await screen.findByText("通知设置已保存")).toBeInTheDocument();
  });

  it("删除请求头后不再提交它", async () => {
    const m = render();
    const user = userEvent.setup();
    await screen.findByLabelText("Webhook 地址");
    await user.click(screen.getByRole("button", { name: "删除请求头 Authorization" }));
    await user.click(screen.getByRole("button", { name: "保存" }));
    await waitFor(() => expect(m.find("PUT", "/notify")).toHaveLength(1));
    expect(m.find("PUT", "/notify")[0].body).toMatchObject({ headers: {} });
  });

  it("阈值不合法或新请求头缺值时就地提示，不提交", async () => {
    const m = render();
    const user = userEvent.setup();
    await screen.findByLabelText("Webhook 地址");
    const hours = screen.getByLabelText("太久没有成功的阈值（小时）");
    await user.clear(hours);
    await user.type(hours, "0");
    await user.click(screen.getByRole("button", { name: "添加请求头" }));
    await user.type(screen.getByLabelText("请求头 2 名称"), "X-Token");
    await user.click(screen.getByRole("button", { name: "保存" }));
    expect(await screen.findByText("阈值须在 1 到 8760 小时之间")).toBeInTheDocument();
    expect(screen.getByText("填写 X-Token 的值")).toBeInTheDocument();
    expect(m.find("PUT", "/notify")).toHaveLength(0);
  });

  it("服务端拒绝时显示原因", async () => {
    render({ "PUT /notify": fail(400, 11100, "Webhook 地址须是以 http:// 或 https:// 开头的完整地址") });
    const user = userEvent.setup();
    const url = await screen.findByLabelText("Webhook 地址");
    await user.clear(url);
    await user.type(url, "ftp://x");
    await user.click(screen.getByRole("button", { name: "保存" }));
    const form = screen.getByRole("form", { name: "通知设置" });
    expect(await within(form).findByRole("alert")).toHaveTextContent("Webhook 地址须是以 http:// 或 https:// 开头");
  });
});
