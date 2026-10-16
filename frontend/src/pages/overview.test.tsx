import { describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { OverviewPage } from "./overview";
import { renderPage } from "@/test/render";
import { fail, mockApi, ok } from "@/test/api-mock";
import type { AttentionItem, Overview, OverviewWindow, TimelineJob } from "@/lib/api";

const sec = (d: Date) => Math.floor(d.getTime() / 1000);
const NOW = sec(new Date(2026, 9, 15, 16, 0));
const HOUR = 3600;
const GB = 1024 ** 3;

function tlJob(over: Partial<TimelineJob> = {}): TimelineJob {
  return {
    job_id: 1,
    name: "商城数据库",
    source_type: "mysql",
    paused: false,
    schedule: { kind: "daily", time: "03:00", weekday: 0, cron: "" },
    last_status: "success",
    next_run_at: NOW + 11 * HOUR,
    runs: [
      { id: 11, started_at: NOW - 13 * HOUR, status: "success" },
      { id: 12, started_at: NOW - 37 * HOUR, status: "failed" },
    ],
    planned: [NOW + 11 * HOUR],
    ...over,
  };
}

function attention(over: Partial<AttentionItem> = {}): AttentionItem {
  return {
    job_id: 2,
    job_name: "文件服务",
    source_type: "files",
    kind: "failed",
    reason: "没有挂载：找不到挂载标记",
    since: NOW - 11 * HOUR,
    last_success_at: NOW - 40 * HOUR,
    run_id: 1291,
    repo_id: 3,
    repo_not_mounted: false,
    ...over,
  };
}

function overview(over: Partial<Overview> = {}, window: OverviewWindow = "7d"): Overview {
  const days = window === "7d" ? 7 : 30;
  const ahead = window === "7d" ? 30 : 72;
  return {
    now: NOW,
    summary: { jobs: 3, attention: 1, paused: 0, ok: 2, servers: 2, repos: 2 },
    totals: { raw_bytes: 400 * GB, stored_bytes: 100 * GB, snapshots: 605 },
    attention: [attention()],
    timeline: {
      window,
      from: sec(new Date(2026, 9, 15 - (days - 1))),
      to: NOW + ahead * HOUR,
      ahead_hours: ahead,
      jobs: [
        tlJob(),
        tlJob({
          job_id: 2,
          name: "文件服务",
          source_type: "files",
          last_status: "failed",
          planned: [],
          next_run_at: NOW + 9 * HOUR,
        }),
        tlJob({ job_id: 3, name: "旧缓存", source_type: "redis", paused: true, next_run_at: 0, planned: [], runs: [] }),
      ],
    },
    repos: [
      {
        id: 1,
        name: "群晖 DS920",
        type: "sftp",
        status: "ok",
        stored_bytes: 100 * GB,
        raw_bytes: 400 * GB,
        ratio: 4,
        snapshots: 600,
        jobs: 2,
        free_space: 2 * 1024 * GB,
      },
      {
        id: 3,
        name: "飞牛 NAS",
        type: "local",
        status: "not_mounted",
        stored_bytes: 0,
        raw_bytes: 0,
        ratio: 0,
        snapshots: 5,
        jobs: 1,
        free_space: -1,
      },
    ],
    ...over,
  };
}

/** 按窗口返回概览，并记下每次请求的窗口 */
function serve(make: (w: OverviewWindow) => Overview = (w) => overview({}, w)) {
  return mockApi({
    "GET /overview": (call) => {
      const w = new URLSearchParams(call.query).get("window") === "30d" ? "30d" : "7d";
      return ok(make(w));
    },
  });
}

function narrow(on: boolean) {
  vi.spyOn(window, "matchMedia").mockImplementation(
    (query: string) =>
      ({
        matches: on && query === "(max-width: 767px)",
        media: query,
        addEventListener: () => {},
        removeEventListener: () => {},
      }) as unknown as MediaQueryList
  );
}

/** 「运行时间线」分区 */
async function timelineSection() {
  return (await screen.findByRole("heading", { name: "运行时间线" })).closest("section")!;
}

const renderOverview = () => renderPage(<OverviewPage />, { path: "/" });

describe("概览页", () => {
  it("加载时显示骨架屏，不显示空状态", () => {
    mockApi({ "GET /overview": () => new Promise(() => {}) as never });
    renderOverview();
    expect(screen.getByLabelText("正在加载")).toHaveAttribute("aria-busy", "true");
    expect(screen.queryByText("还没有任何备份。")).not.toBeInTheDocument();
  });

  it("状态句和三个总量", async () => {
    serve((w) => overview({ summary: { jobs: 6, attention: 3, paused: 0, ok: 3, servers: 2, repos: 2 } }, w));
    renderOverview();
    expect(await screen.findByRole("heading", { level: 1 })).toHaveTextContent(
      "3 个任务需要你处理，其余 3 个运行正常。"
    );
    expect(screen.getByText("受保护的数据").nextElementSibling).toHaveTextContent("400 GB");
    expect(screen.getByText("NAS 上实际占用").nextElementSibling).toHaveTextContent("100 GB");
    expect(screen.getByText("快照").nextElementSibling).toHaveTextContent("605");
  });

  it("需要处理：写清原因、起始时间，操作是链接或按钮", async () => {
    const api = serve((w) =>
      overview(
        { attention: [attention(), attention({ job_id: 4, job_name: "订单库", repo_not_mounted: true, run_id: 0 })] },
        w
      )
    );
    renderOverview();
    const section = (await screen.findByRole("heading", { name: "需要处理" })).closest("section")!;
    const rows = within(section).getAllByRole("listitem");
    expect(rows).toHaveLength(2);
    expect(within(rows[0]).getByText("文件服务")).toBeInTheDocument();
    expect(within(rows[0]).getByText(/没有挂载：找不到挂载标记/)).toBeInTheDocument();
    expect(within(rows[0]).getByText(/今天 05:00 起/)).toBeInTheDocument();
    expect(within(rows[0]).getByRole("link", { name: /查看日志/ })).toHaveAttribute("href", "/runs/1291");
    expect(within(rows[0]).getByRole("button", { name: /重新运行/ })).toBeInTheDocument();
    expect(within(rows[1]).getByRole("link", { name: /检查存储位置/ })).toHaveAttribute("href", "/repos");
    expect(api.calls.length).toBeGreaterThan(0);
  });

  it("重新运行：触发后跳到这次运行的详情", async () => {
    const api = mockApi({
      "GET /overview": ok(overview()),
      "POST /jobs/2/run": ok({ run_id: 1300 }),
    });
    renderOverview();
    await userEvent.click(await screen.findByRole("button", { name: /重新运行/ }));
    await waitFor(() => expect(screen.getByTestId("location")).toHaveTextContent("/runs/1300"));
    expect(api.find("POST", "/jobs/2/run")).toHaveLength(1);
  });

  it("重新运行遇到「正在运行」时提示，不跳转", async () => {
    mockApi({
      "GET /overview": ok(overview()),
      "POST /jobs/2/run": fail(409, 11501, "这个任务正在运行，请等它结束后再试"),
    });
    renderOverview();
    await userEvent.click(await screen.findByRole("button", { name: /重新运行/ }));
    expect(await screen.findByText(/文件服务 正在运行/)).toBeInTheDocument();
    expect(screen.getByTestId("location")).toHaveTextContent("/");
  });

  it("没有需要处理的任务时不显示这一区块，状态句说明全部正常", async () => {
    serve((w) =>
      overview({ attention: [], summary: { jobs: 3, attention: 0, paused: 0, ok: 3, servers: 1, repos: 1 } }, w)
    );
    renderOverview();
    expect(await screen.findByRole("heading", { level: 1 })).toHaveTextContent("全部 3 个任务运行正常。");
    expect(screen.queryByRole("heading", { name: "需要处理" })).not.toBeInTheDocument();
  });

  it("时间线：每个任务一条轨道，点击任务名进入编辑", async () => {
    serve();
    renderOverview();
    const tl = await timelineSection();
    const tracks = within(tl).getAllByRole("listitem");
    expect(tracks).toHaveLength(3);
    expect(within(tracks[0]).getByRole("link", { name: /商城数据库/ })).toHaveAttribute("href", "/jobs/1/edit");
    expect(within(tracks[2]).getByText("已暂停")).toBeInTheDocument();
    expect(within(tracks[0]).getByText("每天 03:00")).toBeInTheDocument();
  });

  it("时间线：已运行的是实心点（带结果），计划是空心点，「现在」线在两者之间", async () => {
    serve();
    renderOverview();
    const tl = await timelineSection();
    const track = within(tl).getAllByRole("listitem")[0];
    const done = within(track).getByLabelText("今天 03:00，成功");
    const failed = within(track).getByLabelText("昨天 03:00，失败");
    const plan = within(track).getByLabelText("计划 明天 03:00");
    expect(done).toHaveAttribute("data-kind", "past");
    expect(done).toHaveAttribute("data-status", "success");
    expect(failed).toHaveAttribute("data-status", "failed");
    expect(plan).toHaveAttribute("data-kind", "planned");
    const nowLine = within(tl).getByTestId("now-line");
    const left = (el: HTMLElement) => parseFloat(el.style.left);
    expect(left(done)).toBeLessThan(left(nowLine));
    expect(left(failed)).toBeLessThan(left(done));
    expect(left(plan)).toBeGreaterThan(left(nowLine));
    expect(within(tl).getByText("今天 16:00")).toBeInTheDocument();
  });

  it("时间线：悬停显示时间和结果", async () => {
    serve();
    renderOverview();
    const dot = await screen.findAllByLabelText("今天 03:00，成功");
    await userEvent.hover(dot[0]);
    expect((await screen.findAllByText("今天 03:00，成功")).length).toBeGreaterThan(0);
  });

  it("切换 7 天 / 30 天会重新读取并改变比例尺", async () => {
    const api = serve();
    renderOverview();
    const tl = await timelineSection();
    const group = within(tl).getByRole("radiogroup", { name: "时间范围" });
    expect(within(group).getByRole("radio", { name: "7 天" })).toHaveAttribute("aria-checked", "true");
    const lineAt = () => parseFloat(within(tl).getByTestId("now-line").style.left);
    const before = lineAt();

    await userEvent.click(within(group).getByRole("radio", { name: "30 天" }));
    await waitFor(() => expect(api.calls.some((c) => c.query === "window=30d")).toBe(true));
    await waitFor(() =>
      expect(within(group).getByRole("radio", { name: "30 天" })).toHaveAttribute("aria-checked", "true")
    );
    await waitFor(() => expect(lineAt()).toBeGreaterThan(before));
  });

  it("宽屏不显示 14 天运行带", async () => {
    narrow(false);
    serve();
    renderOverview();
    await timelineSection();
    expect(screen.queryByRole("img", { name: "最近 14 天运行结果" })).not.toBeInTheDocument();
  });

  it("窄屏（小于 768px）改成每天一格的 14 天运行带，并按 30 天窗口取数", async () => {
    narrow(true);
    const api = serve();
    renderOverview();
    const strips = await screen.findAllByRole("img", { name: "最近 14 天运行结果" });
    expect(strips).toHaveLength(3);
    expect(screen.queryByRole("radiogroup", { name: "时间范围" })).not.toBeInTheDocument();
    expect(screen.queryByTestId("now-line")).not.toBeInTheDocument();
    expect(strips[0].children).toHaveLength(14);
    // 商城数据库：今天成功、昨天失败
    expect(strips[0].children[12]).toHaveAttribute("title", "1 天前：失败");
    expect(strips[0].children[13]).toHaveAttribute("title", "今天：成功");
    expect(api.calls.every((c) => c.query === "window=30d")).toBe(true);
    const row = strips[0].closest("a")!;
    expect(row).toHaveAttribute("href", "/jobs/1/edit");
  });

  it("存储位置卡片：实际占用与原始数据量、去重压缩比，未挂载的标红并写明", async () => {
    serve();
    renderOverview();
    const section = (await screen.findByRole("heading", { name: "存储位置" })).closest("section")!;
    const cards = within(section).getAllByRole("listitem");
    expect(within(cards[0]).getByText("群晖 DS920")).toBeInTheDocument();
    expect(within(cards[0]).getByText("4.0×")).toBeInTheDocument();
    expect(within(cards[0]).getByText("100 GB")).toBeInTheDocument();
    expect(within(cards[0]).getByText("原始 400 GB")).toBeInTheDocument();
    expect(within(cards[0]).getByText("600 个快照")).toBeInTheDocument();
    expect(within(cards[1]).getByText("未挂载")).toBeInTheDocument();
    expect(within(cards[1]).queryByText("去重压缩比")).not.toBeInTheDocument();
  });

  it("没有任何任务：三步引导，已有的资源标为完成，每一步都链接到对应页面", async () => {
    serve((w) =>
      overview(
        {
          summary: { jobs: 0, attention: 0, paused: 0, ok: 0, servers: 1, repos: 0 },
          attention: [],
          repos: [],
          totals: { raw_bytes: 0, stored_bytes: 0, snapshots: 0 },
          timeline: { ...overview({}, w).timeline, jobs: [] },
        },
        w
      )
    );
    renderOverview();
    expect(await screen.findByRole("heading", { name: "还没有任何备份。" })).toBeInTheDocument();
    const steps = ["添加服务器", "添加存储位置", "创建备份任务"].map((t) =>
      screen.getByRole("heading", { name: t }).closest("li")!
    );
    expect(within(steps[0]).getByText("添加服务器")).toBeInTheDocument();
    expect(within(steps[0]).getByText("已完成")).toBeInTheDocument();
    expect(within(steps[0]).getByRole("link", { name: /服务器/ })).toHaveAttribute("href", "/servers");
    expect(within(steps[1]).queryByText("已完成")).not.toBeInTheDocument();
    expect(within(steps[1]).getByRole("link", { name: "添加存储位置" })).toHaveAttribute("href", "/repos");
    expect(within(steps[2]).getByRole("link", { name: "创建备份任务" })).toHaveAttribute("href", "/jobs/new");
  });

  it("读取失败：写明哪个接口、原因，可以重试，重试成功后显示内容", async () => {
    let n = 0;
    const api = mockApi({
      "GET /overview": () => (++n === 1 ? fail(504, 10001, "等待后端响应超时") : ok(overview())),
    });
    renderOverview();
    expect(await screen.findByRole("heading", { name: "暂时读不到任务状态" })).toBeInTheDocument();
    expect(screen.getByText(/GET \/api\/v1\/overview/)).toBeInTheDocument();
    expect(screen.getByText(/等待后端响应超时/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /立即重试/ }));
    expect(await screen.findByRole("heading", { level: 1 })).toHaveTextContent("需要你处理");
    expect(api.find("GET", "/overview")).toHaveLength(2);
  });
});
