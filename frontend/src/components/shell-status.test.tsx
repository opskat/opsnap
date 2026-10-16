import * as React from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, screen, waitFor, within } from "@testing-library/react";
import { ShellStatus } from "./shell-status";
import { renderPage } from "@/test/render";
import { mockApi, ok } from "@/test/api-mock";

const AUTHED = {
  "GET /auth/status": ok({ initialized: true }),
  "GET /auth/me": ok({ kind: "admin", username: "admin" }),
};

const job = (id: number, attention: boolean) => ({ id, name: `任务${id}`, attention });

function page() {
  renderPage(
    <ShellStatus>
      <p>内容</p>
    </ShellStatus>
  );
}

describe("侧边栏状态", () => {
  it("备份任务旁显示需要处理的任务数", async () => {
    const m = mockApi({
      ...AUTHED,
      "GET /jobs": ok({ list: [job(1, true)] }),
      "GET /runs": ok({ list: [], total: 0 }),
    });
    page();
    const nav = (await screen.findAllByRole("navigation", { name: "主导航" }))[0];
    const link = within(nav).getByRole("link", { name: /备份任务/ });
    expect(await within(link).findByText("1")).toBeInTheDocument();
    expect(m.find("GET", "/jobs")[0].query).toBe("filter=attention");
  });

  it("没有需要处理的任务时不显示数字", async () => {
    mockApi({ ...AUTHED, "GET /jobs": ok({ list: [] }), "GET /runs": ok({ list: [], total: 0 }) });
    page();
    const nav = (await screen.findAllByRole("navigation", { name: "主导航" }))[0];
    expect(within(nav).getByRole("link", { name: "备份任务" })).toBeInTheDocument();
  });

  it("「正在备份」卡片显示运行中的任务和已处理的数据量，点击进入运行详情", async () => {
    const m = mockApi({
      ...AUTHED,
      "GET /jobs": ok({ list: [] }),
      "GET /runs": ok({
        list: [{ id: 41, job_id: 2, job_name: "商城数据库", status: "running", bytes_processed: 3_400_000_000 }],
        total: 1,
      }),
    });
    page();
    const link = (await screen.findAllByRole("link", { name: /正在备份/ }))[0];
    expect(link).toHaveAttribute("href", "/runs/41");
    expect(within(link).getByText("商城数据库")).toBeInTheDocument();
    expect(within(link).getByText(/已处理 3\.2 GB/)).toBeInTheDocument();
    expect(m.find("GET", "/runs").some((c) => c.query.includes("status=running"))).toBe(true);
  });

  it("没有运行时只显示一行说明", async () => {
    mockApi({ ...AUTHED, "GET /jobs": ok({ list: [] }), "GET /runs": ok({ list: [], total: 0 }) });
    page();
    expect((await screen.findAllByText("当前没有正在运行的备份")).length).toBeGreaterThan(0);
    expect(screen.queryByRole("link", { name: /正在备份/ })).toBeNull();
  });
});

describe("轮询", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("上一次轮询还没返回时跳过这一轮，不叠加请求", async () => {
    vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
    const pending: (() => void)[] = [];
    const m = mockApi({
      ...AUTHED,
      "GET /jobs": ok({ list: [] }),
      "GET /runs": () => new Promise((resolve) => pending.push(() => resolve(ok({ list: [], total: 0 })))),
    });
    page();
    await waitFor(() => expect(m.find("GET", "/runs")).toHaveLength(1));
    act(() => vi.advanceTimersByTime(5000));
    expect(m.find("GET", "/runs")).toHaveLength(1);

    await act(async () => pending.shift()!());
    act(() => vi.advanceTimersByTime(5000));
    await waitFor(() => expect(m.find("GET", "/runs")).toHaveLength(2));
  });

  it("两次轮询之间显示处理速度（严格模式下同样）", async () => {
    vi.useFakeTimers({ toFake: ["setInterval", "clearInterval", "Date"] });
    let n = 0;
    mockApi({
      ...AUTHED,
      "GET /jobs": ok({ list: [] }),
      "GET /runs": () => {
        n++;
        return ok({
          list: [{ id: 41, job_id: 2, job_name: "商城数据库", status: "running", bytes_processed: n * 1_000_000_000 }],
          total: 1,
        });
      },
    });
    renderPage(
      <React.StrictMode>
        <ShellStatus>
          <p>内容</p>
        </ShellStatus>
      </React.StrictMode>
    );
    await screen.findAllByRole("link", { name: /正在备份/ });
    const before = n;
    act(() => vi.advanceTimersByTime(5000));
    await waitFor(() => expect(n).toBeGreaterThan(before));
    await waitFor(() => expect(screen.getAllByText(/\/s/).length).toBeGreaterThan(0));
  });
});
