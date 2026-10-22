import { describe, expect, it } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { JobsPage } from "./jobs";
import * as React from "react";
import { renderPage } from "@/test/render";
import { RunningJobsContext } from "@/components/shell-status";
import { PINNED_NOW_SEC, pinClock } from "@/test/clock";
import { fail, mockApi, ok } from "@/test/api-mock";
import type { JobItem } from "@/lib/api";

const NOW = PINNED_NOW_SEC;
pinClock();

function job(over: Partial<JobItem> = {}): JobItem {
  return {
    id: 1,
    name: "商城数据库",
    source_type: "mysql",
    server_id: 1,
    server_name: "web-01",
    conn_mode: "ssh",
    db_host: "10.0.0.5",
    db_port: 3306,
    db_user: "backup",
    db_password: "已设置",
    databases: ["shop"],
    all_databases: false,
    auth_db: "",
    paths: null,
    excludes: null,
    repo_id: 1,
    repo_name: "群晖 DS920+",
    schedule: { kind: "daily", time: "03:00", weekday: 0, cron: "" },
    retention: { preset: "30d", days: 30, weeks: 0, months: 0 },
    advanced: { timeout_sec: 0, bandwidth_kbps: 0, compression: "auto", extra_args: null },
    paused: false,
    next_run_at: NOW + 3600,
    attention: false,
    forget_pending_at: 0,
    forget_pending_error: "",
    created_at: NOW,
    updated_at: NOW,
    last_run: { id: 90, status: "success", started_at: NOW - 3600, bytes_added: 184_000_000 },
    recent_runs: [{ id: 90, status: "success", started_at: NOW - 3600, bytes_added: 184_000_000 }],
    running: false,
    ...over,
  };
}

function routes(extra: Parameters<typeof mockApi>[0] = {}, list = [job()]) {
  return mockApi({
    "GET /jobs": ok({ list }),
    ...extra,
  });
}

const PATH = { path: "/jobs" };

/** 模拟侧边栏轮询到的「正在运行」的任务 */
function LiveHarness() {
  const [running, setRunning] = React.useState<Set<number>>(new Set());
  return (
    <RunningJobsContext.Provider value={running}>
      <button type="button" onClick={() => setRunning(new Set([1]))}>
        模拟任务 1 开始运行
      </button>
      <JobsPage />
    </RunningJobsContext.Provider>
  );
}

describe("任务列表", () => {
  it("每行显示名称、来源到存储位置、计划与下次运行、上次结果和新增量", async () => {
    routes();
    renderPage(<JobsPage />, PATH);
    expect(await screen.findByText("商城数据库")).toBeInTheDocument();
    expect(screen.getByText(/web-01/)).toBeInTheDocument();
    expect(screen.getByText("群晖 DS920+")).toBeInTheDocument();
    expect(screen.getByText("每天 03:00")).toBeInTheDocument();
    expect(screen.getByText(/下次 /)).toBeInTheDocument();
    expect(await screen.findByText("成功")).toBeInTheDocument();
    expect(screen.getByText(/新增 175 MB/)).toBeInTheDocument();
    expect(screen.getByRole("img", { name: "最近 14 天运行结果" })).toBeInTheDocument();
  });

  it("文件任务的副标题显示路径", async () => {
    routes({}, [job({ id: 2, name: "站点文件", source_type: "files", paths: ["/var/www", "/etc/nginx"] })]);
    renderPage(<JobsPage />, PATH);
    expect(await screen.findByText(/\/var\/www/)).toBeInTheDocument();
  });

  it("筛选与搜索把 filter、q 带给接口", async () => {
    const user = userEvent.setup();
    const m = routes();
    renderPage(<JobsPage />, PATH);
    await screen.findByText("商城数据库");
    expect(m.find("GET", "/jobs").some((c) => c.query === "filter=all")).toBe(true);

    await user.click(screen.getByRole("tab", { name: /需要处理/ }));
    await waitFor(() => expect(m.find("GET", "/jobs").some((c) => c.query === "filter=attention")).toBe(true));

    await user.click(screen.getByRole("tab", { name: /已暂停/ }));
    await waitFor(() => expect(m.find("GET", "/jobs").some((c) => c.query === "filter=paused")).toBe(true));

    await user.type(screen.getByRole("searchbox", { name: "查找任务" }), "web 01");
    await waitFor(() => expect(m.find("GET", "/jobs").some((c) => c.query === "filter=paused&q=web+01")).toBe(true));
  });

  it("没有任务时显示空状态；读取失败时给出重试", async () => {
    routes({}, []);
    renderPage(<JobsPage />, PATH);
    expect(await screen.findByText("还没有备份任务")).toBeInTheDocument();
  });

  it("读取失败原地显示原因并可重试", async () => {
    const user = userEvent.setup();
    let n = 0;
    mockApi({
      "GET /jobs": () => (n++ === 0 ? fail(500, 1, "数据库繁忙") : ok({ list: [job()] })),
    });
    renderPage(<JobsPage />, PATH);
    expect(await screen.findByText(/数据库繁忙/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "重试" }));
    expect(await screen.findByText("商城数据库")).toBeInTheDocument();
  });

  it("复制为新任务", async () => {
    const user = userEvent.setup();
    const m = routes({ "POST /jobs/1/duplicate": ok(job({ id: 9, name: "商城数据库 副本", paused: true })) });
    renderPage(<JobsPage />, PATH);
    await user.click(await screen.findByRole("button", { name: "商城数据库 的更多操作" }));
    await user.click(await screen.findByRole("menuitem", { name: "复制为新任务" }));
    await waitFor(() => expect(m.find("POST", "/jobs/1/duplicate")).toHaveLength(1));
  });

  it("暂停计划与恢复计划", async () => {
    const user = userEvent.setup();
    const m = routes({ "POST /jobs/1/pause": ok(job({ paused: true })), "POST /jobs/2/resume": ok(job({ id: 2 })) }, [
      job(),
      job({ id: 2, name: "旧任务", paused: true, next_run_at: 0 }),
    ]);
    renderPage(<JobsPage />, PATH);
    await user.click(await screen.findByRole("button", { name: "商城数据库 的更多操作" }));
    await user.click(await screen.findByRole("menuitem", { name: "暂停计划" }));
    await waitFor(() => expect(m.find("POST", "/jobs/1/pause")).toHaveLength(1));

    await user.click(await screen.findByRole("button", { name: "旧任务 的更多操作" }));
    await user.click(await screen.findByRole("menuitem", { name: "恢复计划" }));
    await waitFor(() => expect(m.find("POST", "/jobs/2/resume")).toHaveLength(1));
  });

  it("已暂停的任务显示「已暂停」而不是下次运行时间", async () => {
    routes({}, [job({ paused: true, next_run_at: 0 })]);
    renderPage(<JobsPage />, PATH);
    expect(await screen.findByText("已暂停", { selector: "span" })).toBeInTheDocument();
    expect(screen.queryByText(/下次 /)).toBeNull();
  });

  it("删除需要二次确认，并写明已有快照会保留", async () => {
    const user = userEvent.setup();
    const m = routes({ "DELETE /jobs/1": ok({}) });
    renderPage(<JobsPage />, PATH);
    await user.click(await screen.findByRole("button", { name: "商城数据库 的更多操作" }));
    await user.click(await screen.findByRole("menuitem", { name: "删除任务" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText(/删除任务不删除已有快照/)).toBeInTheDocument();
    expect(m.find("DELETE", "/jobs/1")).toHaveLength(0);
    await user.click(within(dialog).getByRole("button", { name: "删除" }));
    await waitFor(() => expect(m.find("DELETE", "/jobs/1")).toHaveLength(1));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
  });

  it("查看快照转到快照页并带上任务", async () => {
    const user = userEvent.setup();
    routes();
    renderPage(<JobsPage />, PATH);
    await user.click(await screen.findByRole("button", { name: "商城数据库 的更多操作" }));
    await user.click(await screen.findByRole("menuitem", { name: "查看快照" }));
    expect(screen.getByTestId("location")).toHaveTextContent("/snapshots?job=1");
  });

  it("立即运行后转到运行详情", async () => {
    const user = userEvent.setup();
    const m = routes({ "POST /jobs/1/run": ok({ run_id: 77 }) });
    renderPage(<JobsPage />, PATH);
    await user.click(await screen.findByRole("button", { name: "立即运行 商城数据库" }));
    await waitFor(() => expect(m.find("POST", "/jobs/1/run")).toHaveLength(1));
    await waitFor(() => expect(screen.getByTestId("location")).toHaveTextContent("/runs/77"));
  });

  it("正在运行时立即运行不可用并写明原因", async () => {
    routes({}, [job({ running: true, last_run: { id: 91, status: "running", started_at: NOW - 60, bytes_added: 0 } })]);
    renderPage(<JobsPage />, PATH);
    const btn = await screen.findByRole("button", { name: "立即运行 商城数据库" });
    await waitFor(() => expect(btn).toBeDisabled());
    expect(btn).toHaveAttribute("title", "正在运行，结束后才能再次运行");
  });

  it("列表打开期间开始的运行：侧边栏轮询到后立即运行变为不可用，并刷新列表", async () => {
    const user = userEvent.setup();
    const m = routes();
    renderPage(<LiveHarness />, PATH);
    const btn = await screen.findByRole("button", { name: "立即运行 商城数据库" });
    expect(btn).toBeEnabled();
    const before = m.find("GET", "/jobs").length;
    await user.click(screen.getByRole("button", { name: "模拟任务 1 开始运行" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "立即运行 商城数据库" })).toBeDisabled());
    await waitFor(() => expect(m.find("GET", "/jobs").length).toBeGreaterThan(before));
  });

  it("已显示列表后某次后台刷新失败，列表保留不被错误页替换", async () => {
    const user = userEvent.setup();
    let n = 0;
    const m = mockApi({
      "GET /jobs": () => (n++ < 2 ? ok({ list: [job()] }) : fail(500, 1, "数据库繁忙")),
    });
    renderPage(<LiveHarness />, PATH);
    expect(await screen.findByText("商城数据库")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "模拟任务 1 开始运行" }));
    await waitFor(() => expect(m.find("GET", "/jobs").length).toBeGreaterThan(2));
    // 等失败的响应落地
    await new Promise((r) => setTimeout(r, 50));
    expect(screen.queryByText(/数据库繁忙/)).not.toBeInTheDocument();
    expect(screen.getByText("商城数据库")).toBeInTheDocument();
  });

  it("上次结果不受 14 天窗口限制", async () => {
    routes({}, [
      job({ last_run: { id: 5, status: "failed", started_at: NOW - 30 * 86400, bytes_added: 0 }, recent_runs: [] }),
    ]);
    renderPage(<JobsPage />, PATH);
    expect(await screen.findByText("失败")).toBeInTheDocument();
    expect(screen.queryByText("近 14 天没有运行")).toBeNull();
  });

  it("服务端判定同一任务已在运行（11501）时提示原因", async () => {
    const user = userEvent.setup();
    routes({ "POST /jobs/1/run": fail(409, 11501, "这个任务正在运行") });
    renderPage(<JobsPage />, PATH);
    await user.click(await screen.findByRole("button", { name: "立即运行 商城数据库" }));
    expect(await screen.findByText(/商城数据库 正在运行，结束后才能再次运行/)).toBeInTheDocument();
    expect(screen.getByTestId("location")).toHaveTextContent("/jobs");
  });
});

describe("待清理", () => {
  it("延后清理的任务显示待清理标记，写明首次延后时间；不影响需要处理", async () => {
    routes({}, [job({ forget_pending_at: NOW - 7200, forget_pending_error: "", attention: false })]);
    renderPage(<JobsPage />, PATH);
    expect(await screen.findByText("待清理")).toBeInTheDocument();
    expect(screen.getByText(/起等待清理/)).toBeInTheDocument();
    // 延后只影响空间回收，不是需要处理的问题：筛选标签上的计数仍为 0
    expect(screen.getByRole("tab", { name: /需要处理/ })).toHaveTextContent("需要处理0");
  });

  it("补做失败时把原因写进标记的说明", async () => {
    routes({}, [job({ forget_pending_at: NOW - 7200, forget_pending_error: "密钥不对" })]);
    renderPage(<JobsPage />, PATH);
    expect(await screen.findByText("待清理")).toBeInTheDocument();
    expect(screen.getByText(/密钥不对/)).toBeInTheDocument();
  });

  it("没有待清理的任务不显示标记", async () => {
    routes();
    renderPage(<JobsPage />, PATH);
    await screen.findByText("商城数据库");
    expect(screen.queryByText("待清理")).not.toBeInTheDocument();
  });
});
