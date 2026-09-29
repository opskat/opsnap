import { render, screen } from "@testing-library/react";
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";

import { ServiceStatus } from "@/components/layout/ServiceStatus";
import i18n from "@/i18n";

const ok = (data: unknown) => new Response(JSON.stringify({ code: 0, msg: "success", data }), { status: 200 });

function mockHealth(response: Response) {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValueOnce(response));
}

beforeAll(() => i18n.changeLanguage("zh-CN"));
afterEach(() => vi.unstubAllGlobals());

describe("侧栏底部的服务状态", () => {
  it("显示服务运行中、版本、提交短号与自托管", async () => {
    mockHealth(ok({ version: "v0.1.0", commit: "a1b2c3d", database: "ok" }));
    render(<ServiceStatus />);
    expect(await screen.findByText("服务运行中")).toBeInTheDocument();
    expect(screen.getByTestId("service-version")).toHaveTextContent("v0.1.0 · a1b2c3d · 自托管");
  });

  it("没有提交短号时只显示版本", async () => {
    mockHealth(ok({ version: "dev", commit: "", database: "ok" }));
    render(<ServiceStatus />);
    expect(await screen.findByTestId("service-version")).toHaveTextContent(/^dev · 自托管$/);
  });

  it("元数据库不可用时说明", async () => {
    mockHealth(ok({ version: "v0.1.0", commit: "a1b2c3d", database: "error" }));
    render(<ServiceStatus />);
    expect(await screen.findByText("元数据库不可用")).toBeInTheDocument();
    expect(screen.queryByText("服务运行中")).not.toBeInTheDocument();
  });

  it("读取失败时说明无法获取服务状态", async () => {
    mockHealth(new Response("oops", { status: 502, statusText: "Bad Gateway" }));
    render(<ServiceStatus />);
    expect(await screen.findByText("无法获取服务状态")).toBeInTheDocument();
  });
});
