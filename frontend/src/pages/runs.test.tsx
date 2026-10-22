import { describe, expect, it } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { RunDetailPage, RunsPage } from "./runs";
import { renderPage } from "@/test/render";
import { PINNED_NOW_SEC, pinClock } from "@/test/clock";
import { stubEventSource } from "@/test/event-source";
import { fail, mockApi, ok } from "@/test/api-mock";
import type { RunItem } from "@/lib/api";

const NOW = PINNED_NOW_SEC;
pinClock();

function run(over: Partial<RunItem> = {}): RunItem {
  return {
    id: 1291,
    job_id: 2,
    job_name: "商城数据库",
    trigger: "manual",
    trigger_label: "admin 手动",
    status: "success",
    reason: "",
    notify_failed: false,
    started_at: NOW - 600,
    finished_at: NOW - 240,
    duration_ms: 362_000,
    bytes_processed: Math.round(11.6 * 1024 ** 3),
    bytes_added: 384 * 1024 ** 2,
    snapshot_ids: ["a1b2c3d4", "e5f6a7b8"],
    steps: [
      { key: "preflight", label: "预检", status: "success", message: "存储位置可写", started_at: 1, finished_at: 2001 },
      {
        key: "export",
        label: "导出并写入快照",
        status: "success",
        message: "mysqldump → restic",
        started_at: 2001,
        finished_at: 300_000,
      },
      { key: "forget", label: "清理旧快照", status: "success", message: "", started_at: 0, finished_at: 0 },
      { key: "notify", label: "记录并通知", status: "success", message: "", started_at: 0, finished_at: 0 },
    ],
    ...over,
  };
}

const LOG = [
  { t: 1_700_000_000_000, level: "info", text: "开始预检" },
  { t: 1_700_000_001_000, level: "warn", text: "file has vanished: /tmp/x" },
  { t: 1_700_000_002_000, level: "error", text: "mysqldump 退出码 2" },
];

const at = (id: number) => ({ path: "/runs/:id", at: `/runs/${id}` });

describe("运行详情", () => {
  it("显示触发方式、各步骤、数据量、新增量和快照 ID", async () => {
    mockApi({ "GET /runs/1291": ok(run()), "GET /runs/1291/log": ok({ running: false, lines: LOG }) });
    renderPage(<RunDetailPage />, at(1291));
    expect(await screen.findByRole("heading", { name: "商城数据库" })).toBeInTheDocument();
    expect(screen.getByText(/admin 手动/)).toBeInTheDocument();
    expect(screen.getByText("预检")).toBeInTheDocument();
    expect(screen.getByText("导出并写入快照")).toBeInTheDocument();
    expect(screen.getByText("清理旧快照")).toBeInTheDocument();
    expect(screen.getByText("记录并通知")).toBeInTheDocument();
    expect(screen.getByText("mysqldump → restic")).toBeInTheDocument();
    expect(screen.getByText("11.6 GB")).toBeInTheDocument();
    expect(screen.getByText("+384 MB")).toBeInTheDocument();
    expect(screen.getByText("6 分 02 秒")).toBeInTheDocument();
    expect(screen.getByText("a1b2c3d4")).toBeInTheDocument();
    expect(screen.getByText("e5f6a7b8")).toBeInTheDocument();
    expect(screen.getByText("成功")).toBeInTheDocument();
    // 已结束的运行读取完整日志
    expect(await screen.findByText("开始预检")).toBeInTheDocument();
  });

  it("失败的运行显示原因和通知发送失败", async () => {
    mockApi({
      "GET /runs/5": ok(
        run({ id: 5, status: "failed", reason: "没有挂载：/mnt/nas", notify_failed: true, snapshot_ids: [] })
      ),
      "GET /runs/5/log": ok({ running: false, lines: [] }),
    });
    renderPage(<RunDetailPage />, at(5));
    expect(await screen.findByText(/没有挂载：\/mnt\/nas/)).toBeInTheDocument();
    expect(screen.getByText(/通知发送失败/)).toBeInTheDocument();
    expect(screen.getByText("失败")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "停止运行" })).toBeNull();
  });

  it("已结束的运行也能只看警告（警告和错误）", async () => {
    const user = userEvent.setup();
    mockApi({ "GET /runs/1291": ok(run()), "GET /runs/1291/log": ok({ running: false, lines: LOG }) });
    renderPage(<RunDetailPage />, at(1291));
    await screen.findByText("开始预检");
    await user.click(screen.getByRole("button", { name: /只看警告/ }));
    expect(screen.queryByText("开始预检")).toBeNull();
    expect(screen.getByText("file has vanished: /tmp/x")).toBeInTheDocument();
    expect(screen.getByText("mysqldump 退出码 2")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "全部日志" }));
    expect(screen.getByText("开始预检")).toBeInTheDocument();
  });

  const RUNNING = run({
    status: "running",
    finished_at: 0,
    duration_ms: 0,
    bytes_added: 0,
    snapshot_ids: [],
    bytes_processed: 3_400_000_000,
    steps: [
      { key: "preflight", label: "预检", status: "success", message: "", started_at: 1, finished_at: 1001 },
      { key: "export", label: "导出并写入快照", status: "running", message: "", started_at: 1001, finished_at: 0 },
      { key: "forget", label: "清理旧快照", status: "pending", message: "", started_at: 0, finished_at: 0 },
      { key: "notify", label: "记录并通知", status: "pending", message: "", started_at: 0, finished_at: 0 },
    ],
  });

  it("运行中通过 SSE 追加日志，只看警告过滤，收到 end 后重新读取详情", async () => {
    const user = userEvent.setup();
    const ES = stubEventSource();
    let detailCalls = 0;
    const m = mockApi({
      "GET /runs/1291": () => (detailCalls++ === 0 ? ok(RUNNING) : ok(run())),
      "GET /runs/1291/log": ok({ running: false, lines: LOG }),
    });
    renderPage(<RunDetailPage />, at(1291));
    await screen.findByRole("heading", { name: "商城数据库" });
    await waitFor(() => expect(ES.instances).toHaveLength(1));
    expect(ES.last().url).toBe("/api/v1/runs/1291/log/stream");
    // 运行中不拉完整日志，由 SSE 补发已有的行
    expect(m.find("GET", "/runs/1291/log")).toHaveLength(0);

    ES.last().emit("line", { t: 1_700_000_000_000, level: "info", text: "pg_dump 开始" });
    ES.last().emit("line", { t: 1_700_000_001_000, level: "warn", text: "表 orders 很大" });
    expect(screen.getByText("pg_dump 开始")).toBeInTheDocument();
    expect(screen.getByText("表 orders 很大")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /只看警告/ }));
    expect(screen.queryByText("pg_dump 开始")).toBeNull();
    expect(screen.getByText("表 orders 很大")).toBeInTheDocument();

    ES.last().emit("line", { t: 1_700_000_002_000, level: "warn", text: "再来一条警告" });
    expect(screen.getByText("再来一条警告")).toBeInTheDocument();

    ES.last().emit("end", { status: "success", reason: "" });
    await waitFor(() => expect(m.find("GET", "/runs/1291").length).toBeGreaterThanOrEqual(2));
    expect(await screen.findByText("成功")).toBeInTheDocument();
    expect(ES.last().closed).toBe(true);
  });

  it("运行中显示已处理的数据量，并提供停止运行", async () => {
    stubEventSource();
    mockApi({ "GET /runs/1291": ok(RUNNING) });
    renderPage(<RunDetailPage />, at(1291));
    expect(await screen.findByText("3.2 GB")).toBeInTheDocument();
    expect(screen.getByText("运行中")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "停止运行" })).toBeInTheDocument();
  });

  it("停止运行要先确认，确认后带 confirm:true 提交", async () => {
    const user = userEvent.setup();
    stubEventSource();
    const m = mockApi({ "GET /runs/1291": ok(RUNNING), "POST /runs/1291/stop": ok({}) });
    renderPage(<RunDetailPage />, at(1291));
    await user.click(await screen.findByRole("button", { name: "停止运行" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(m.find("POST", "/runs/1291/stop")).toHaveLength(0);
    await user.click(within(dialog).getByRole("button", { name: "取消" }));
    expect(m.find("POST", "/runs/1291/stop")).toHaveLength(0);

    await user.click(screen.getByRole("button", { name: "停止运行" }));
    await user.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "停止" }));
    await waitFor(() => expect(m.find("POST", "/runs/1291/stop")).toHaveLength(1));
    expect(m.find("POST", "/runs/1291/stop")[0].body).toEqual({ confirm: true });
  });

  it("停止时已经结束（11502）会把原因留在对话框里", async () => {
    const user = userEvent.setup();
    stubEventSource();
    mockApi({ "GET /runs/1291": ok(RUNNING), "POST /runs/1291/stop": fail(409, 11502, "这次运行已经结束") });
    renderPage(<RunDetailPage />, at(1291));
    await user.click(await screen.findByRole("button", { name: "停止运行" }));
    await user.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "停止" }));
    expect(await screen.findByText("这次运行已经结束")).toBeInTheDocument();
  });

  it("运行不存在时给出说明", async () => {
    mockApi({ "GET /runs/9": fail(404, 11500, "运行记录不存在") });
    renderPage(<RunDetailPage />, at(9));
    expect(await screen.findByText(/运行记录不存在/)).toBeInTheDocument();
  });
});

describe("运行记录列表", () => {
  const LIST = [
    run({ id: 3, status: "running", finished_at: 0, bytes_added: 0, bytes_processed: 0 }),
    run({ id: 2, job_name: "站点文件", status: "failed", trigger: "schedule", trigger_label: "计划", bytes_added: 0 }),
    run({
      id: 1,
      job_name: "站点文件",
      status: "skipped",
      reason: "上一次还在运行",
      trigger_label: "计划",
      bytes_added: 0,
    }),
  ];

  it("按天分组，每行显示任务、状态、触发方式、数据量和新增量", async () => {
    mockApi({ "GET /runs": ok({ list: LIST, total: 3 }), "GET /jobs": ok({ list: [] }) });
    renderPage(<RunsPage />, { path: "/runs" });
    expect(await screen.findByText("今天")).toBeInTheDocument();
    expect(screen.getAllByText("站点文件")).toHaveLength(2);
    // 筛选标签和行里各有一处
    expect(screen.getAllByText("运行中")).toHaveLength(2);
    expect(screen.getAllByText("失败")).toHaveLength(2);
    expect(screen.getAllByText("已跳过")).toHaveLength(2);
    expect(screen.getAllByText(/admin 手动/).length).toBeGreaterThan(0);
  });

  it("状态与任务筛选带到接口，地址里的 job_id 作为初始筛选", async () => {
    const user = userEvent.setup();
    const m = mockApi({
      "GET /runs": ok({ list: LIST, total: 3 }),
      "GET /jobs": ok({ list: [{ id: 2, name: "商城数据库" }] }),
    });
    renderPage(<RunsPage />, { path: "/runs", at: "/runs?job_id=2" });
    await screen.findByText("今天");
    expect(m.find("GET", "/runs")[0].query).toContain("job_id=2");
    await user.click(screen.getByRole("tab", { name: "失败" }));
    await waitFor(() =>
      expect(
        m.find("GET", "/runs").some((c) => c.query.includes("status=failed") && c.query.includes("job_id=2"))
      ).toBe(true)
    );
  });

  it("点击一行进入运行详情", async () => {
    const user = userEvent.setup();
    mockApi({ "GET /runs": ok({ list: LIST, total: 3 }), "GET /jobs": ok({ list: [] }) });
    renderPage(<RunsPage />, { path: "/runs" });
    await user.click(await screen.findByRole("link", { name: /#3/ }));
    expect(screen.getByTestId("location")).toHaveTextContent("/runs/3");
  });

  it("没有记录时显示空状态；总数多于已加载的可以继续加载", async () => {
    const user = userEvent.setup();
    const m = mockApi({
      "GET /runs": (c) =>
        c.query.includes("page=2")
          ? ok({ list: [run({ id: 99, job_name: "更早的任务" })], total: 31 })
          : ok({ list: LIST, total: 31 }),
      "GET /jobs": ok({ list: [] }),
    });
    renderPage(<RunsPage />, { path: "/runs" });
    await user.click(await screen.findByRole("button", { name: "加载更多" }));
    expect(await screen.findByText("更早的任务")).toBeInTheDocument();
    expect(m.find("GET", "/runs").some((c) => c.query.includes("page=2"))).toBe(true);
  });

  it("加载下一页失败时保留已显示的列表并写明原因，再点重新读取同一页", async () => {
    const user = userEvent.setup();
    let page2 = 0;
    const m = mockApi({
      "GET /runs": (c) => {
        if (!c.query.includes("page=2")) return ok({ list: LIST, total: 31 });
        return page2++ === 0
          ? fail(500, 10001, "数据库繁忙")
          : ok({ list: [run({ id: 99, job_name: "更早的任务" })], total: 31 });
      },
      "GET /jobs": ok({ list: [] }),
    });
    renderPage(<RunsPage />, { path: "/runs" });
    await user.click(await screen.findByRole("button", { name: "加载更多" }));
    expect(await screen.findByText(/数据库繁忙/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /#3/ })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "加载更多" }));
    expect(await screen.findByText("更早的任务")).toBeInTheDocument();
    expect(screen.queryByText(/数据库繁忙/)).not.toBeInTheDocument();
    const pages = m.find("GET", "/runs").map((c) => new URLSearchParams(c.query).get("page"));
    expect(pages).toEqual(["1", "2", "2"]);
  });

  it("翻页时重复出现的记录只显示一次", async () => {
    const user = userEvent.setup();
    mockApi({
      "GET /runs": (c) =>
        c.query.includes("page=2")
          ? ok({ list: [LIST[2], run({ id: 99, job_name: "更早的任务" })], total: 31 })
          : ok({ list: LIST, total: 31 }),
      "GET /jobs": ok({ list: [] }),
    });
    renderPage(<RunsPage />, { path: "/runs" });
    await user.click(await screen.findByRole("button", { name: "加载更多" }));
    expect(await screen.findByText("更早的任务")).toBeInTheDocument();
    expect(screen.getAllByRole("link", { name: /#1，/ })).toHaveLength(1);
  });
});

describe("清理旧快照被延后", () => {
  const deferred = () =>
    run({
      steps: [
        { key: "preflight", label: "预检", status: "success", message: "", started_at: 1, finished_at: 2001 },
        {
          key: "export",
          label: "导出并写入快照",
          status: "success",
          message: "",
          started_at: 2001,
          finished_at: 300_000,
        },
        {
          key: "forget",
          label: "清理旧快照",
          status: "skipped",
          message: "存储位置正被其他备份占用，本次跳过清理旧快照，稍后自动补做",
          started_at: 300_000,
          finished_at: 300_100,
        },
        {
          key: "notify",
          label: "记录并通知",
          status: "success",
          message: "",
          started_at: 300_100,
          finished_at: 301_000,
        },
      ],
    });

  it("已跳过的步骤显示原因，并当作已处理的完成态", async () => {
    mockApi({ "GET /runs/1291": ok(deferred()), "GET /runs/1291/log": ok({ running: false, lines: LOG }) });
    renderPage(<RunDetailPage />, at(1291));
    await screen.findByRole("heading", { name: "商城数据库" });

    expect(screen.getByText("存储位置正被其他备份占用，本次跳过清理旧快照，稍后自动补做")).toBeInTheDocument();
    // 已跳过是完成态的中性样式，不是「未开始」的圆形序号
    expect(screen.getByRole("img", { name: "已跳过" })).toBeInTheDocument();
    expect(screen.getByRole("img", { name: "已跳过" })).not.toHaveTextContent("3");
    // 整次运行仍然成功
    expect(screen.getByText("成功")).toBeInTheDocument();
  });
});
