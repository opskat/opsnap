import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";

import i18n from "@/i18n";
import { OverviewPage } from "@/pages/OverviewPage";

function mockHealth(...responses: Response[]) {
  const fn = vi.fn();
  for (const r of responses) fn.mockResolvedValueOnce(r);
  vi.stubGlobal("fetch", fn);
}

const ok = (data: unknown) => new Response(JSON.stringify({ code: 0, msg: "success", data }), { status: 200 });

beforeAll(() => i18n.changeLanguage("zh-CN"));
afterEach(() => vi.unstubAllGlobals());

describe("概览页系统状态", () => {
  it("显示版本号与元数据库状态", async () => {
    mockHealth(ok({ version: "1.2.3", database: "ok" }));
    render(<OverviewPage />);
    expect(await screen.findByTestId("health-version")).toHaveTextContent("1.2.3");
    expect(screen.getByTestId("health-database")).toHaveTextContent("正常");
  });

  it("元数据库不可用时显示不可用", async () => {
    mockHealth(ok({ version: "1.2.3", database: "error" }));
    render(<OverviewPage />);
    expect(await screen.findByTestId("health-database")).toHaveTextContent("不可用");
  });

  it("请求失败时显示错误并可重试", async () => {
    mockHealth(
      new Response("oops", { status: 502, statusText: "Bad Gateway" }),
      ok({ version: "9.9.9", database: "ok" })
    );
    render(<OverviewPage />);
    expect(await screen.findByText(/无法获取系统状态/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "重试" }));
    expect(await screen.findByTestId("health-version")).toHaveTextContent("9.9.9");
  });
});
