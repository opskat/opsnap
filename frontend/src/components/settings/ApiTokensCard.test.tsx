import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

import { ApiTokensCard } from "@/components/settings/ApiTokensCard";
import i18n from "@/i18n";

const ok = (data: unknown) => new Response(JSON.stringify({ code: 0, msg: "success", data }), { status: 200 });
const fail = (code: number, msg: string, status = 400) => new Response(JSON.stringify({ code, msg }), { status });
const ts = (y: number, m: number, d: number) => new Date(y, m - 1, d, 12, 0).getTime() / 1000;

const items = [
  {
    id: 1,
    name: "ci-deploy",
    prefix: "onp_7Hq2",
    status: "active",
    created_at: ts(2026, 9, 1),
    last_used_at: 0,
    expires_at: ts(2026, 11, 30),
    revoked_at: 0,
  },
  {
    id: 2,
    name: "laptop-script",
    prefix: "onp_b3Ze",
    status: "expired",
    created_at: ts(2026, 6, 2),
    last_used_at: ts(2026, 8, 30),
    expires_at: ts(2026, 9, 1),
    revoked_at: 0,
  },
  {
    id: 3,
    name: "old-ansible",
    prefix: "onp_Qa0m",
    status: "revoked",
    created_at: ts(2026, 3, 15),
    last_used_at: ts(2026, 5, 2),
    expires_at: 0,
    revoked_at: ts(2026, 5, 3),
  },
];

let fetchMock: ReturnType<typeof vi.fn>;
function respond(...responses: Response[]) {
  for (const r of responses) fetchMock.mockResolvedValueOnce(r);
}

beforeAll(() => i18n.changeLanguage("zh-CN"));
beforeEach(() => {
  fetchMock = vi.fn();
  vi.stubGlobal("fetch", fetchMock);
});
afterEach(() => vi.unstubAllGlobals());

describe("设置页 · API 令牌", () => {
  it("列出令牌：前缀、从未使用、永不过期、已过期与已吊销标签", async () => {
    respond(ok({ items }));
    render(<ApiTokensCard />);
    const rows = await screen.findAllByRole("row");
    expect(rows).toHaveLength(4);
    const ci = within(rows[1]);
    expect(ci.getByText("onp_7Hq2…")).toBeInTheDocument();
    expect(ci.getByText("从未使用")).toBeInTheDocument();
    expect(ci.getByText("2026-11-30")).toBeInTheDocument();
    expect(ci.getByRole("button", { name: "吊销 ci-deploy" })).toBeInTheDocument();
    expect(within(rows[2]).getByText("已过期")).toBeInTheDocument();
    const revoked = within(rows[3]);
    expect(revoked.getByText("已吊销")).toBeInTheDocument();
    expect(revoked.getByText("永不过期")).toBeInTheDocument();
    expect(revoked.queryByRole("button", { name: /吊销/ })).not.toBeInTheDocument();
  });

  it("没有令牌时显示空状态", async () => {
    respond(ok({ items: [] }));
    render(<ApiTokensCard />);
    expect(await screen.findByText("还没有 API 令牌")).toBeInTheDocument();
  });

  it("生成令牌：默认 90 天，完整令牌只显示一次，关闭后刷新列表", async () => {
    respond(ok({ items: [] }));
    render(<ApiTokensCard />);
    await userEvent.click(await screen.findByRole("button", { name: "生成令牌" }));
    const dialog = await screen.findByRole("dialog", { name: "生成 API 令牌" });
    expect(within(dialog).getByRole("radio", { name: "90 天" })).toBeChecked();
    await userEvent.type(within(dialog).getByLabelText("名称"), "ci-deploy");
    await userEvent.click(within(dialog).getByRole("radio", { name: "永不过期" }));
    respond(ok({ item: items[0], token: "onp_FULLTOKENVALUE" }), ok({ items: [items[0]] }));
    await userEvent.click(within(dialog).getByRole("button", { name: "生成" }));

    const created = await screen.findByRole("dialog", { name: "令牌已生成" });
    expect(within(created).getByText("onp_FULLTOKENVALUE")).toBeInTheDocument();
    expect(within(created).getByText(/只显示这一次/)).toBeInTheDocument();
    const [, init] = fetchMock.mock.calls[1] as [string, RequestInit];
    expect(JSON.parse(init.body as string)).toEqual({ name: "ci-deploy", expires_in_days: 0 });

    await userEvent.click(within(created).getByRole("button", { name: "我已复制，关闭" }));
    expect(screen.queryByText("onp_FULLTOKENVALUE")).not.toBeInTheDocument();
    expect(await screen.findByText("onp_7Hq2…")).toBeInTheDocument();
  });

  it("生成令牌：重名时显示在名称字段旁", async () => {
    respond(ok({ items: [] }));
    render(<ApiTokensCard />);
    await userEvent.click(await screen.findByRole("button", { name: "生成令牌" }));
    const dialog = await screen.findByRole("dialog", { name: "生成 API 令牌" });
    await userEvent.type(within(dialog).getByLabelText("名称"), "ci-deploy");
    respond(fail(10204, "已有同名的有效令牌"));
    await userEvent.click(within(dialog).getByRole("button", { name: "生成" }));
    expect(await within(dialog).findByText("已有同名的有效令牌")).toBeInTheDocument();
    expect(within(dialog).getByLabelText("名称")).toHaveAttribute("aria-invalid", "true");
  });

  it("吊销需要确认，确认后刷新列表", async () => {
    respond(ok({ items: [items[0]] }));
    render(<ApiTokensCard />);
    await userEvent.click(await screen.findByRole("button", { name: "吊销 ci-deploy" }));
    const confirm = await screen.findByRole("dialog", { name: "吊销令牌" });
    expect(confirm).toHaveTextContent("ci-deploy");
    respond(ok({}), ok({ items: [{ ...items[0], status: "revoked", revoked_at: ts(2026, 9, 23) }] }));
    await userEvent.click(within(confirm).getByRole("button", { name: "吊销" }));
    expect(await screen.findByText("已吊销")).toBeInTheDocument();
    expect(fetchMock.mock.calls[1][0]).toBe("/api/v1/tokens/1/revoke");
  });
});
