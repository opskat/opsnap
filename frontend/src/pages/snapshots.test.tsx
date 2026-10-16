import { describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { SnapshotsPage } from "./snapshots";
import { renderPage } from "@/test/render";
import { PINNED_NOW_SEC, pinClock } from "@/test/clock";
import { fail, mockApi, ok } from "@/test/api-mock";
import { ErrorCode, type JobItem, type SnapshotEntry, type SnapshotItem } from "@/lib/api";

const NOW = PINNED_NOW_SEC;
pinClock();
const SID_A = "a".repeat(64);
const SID_B = "b".repeat(64);

function job(over: Partial<JobItem> = {}): JobItem {
  return {
    id: 1,
    name: "站点文件",
    source_type: "files",
    server_id: 1,
    server_name: "web-01",
    conn_mode: "ssh",
    db_host: "",
    db_port: 0,
    db_user: "",
    db_password: "",
    databases: null,
    all_databases: false,
    auth_db: "",
    paths: ["/srv/www"],
    excludes: null,
    repo_id: 1,
    repo_name: "群晖 DS920+",
    schedule: { kind: "daily", time: "03:00", weekday: 0, cron: "" },
    retention: { preset: "long", days: 7, weeks: 4, months: 6 },
    advanced: { timeout_sec: 0, bandwidth_kbps: 0, compression: "auto", extra_args: null },
    paused: false,
    next_run_at: NOW + 3600,
    attention: false,
    created_at: NOW,
    updated_at: NOW,
    ...over,
  };
}

function snap(over: Partial<SnapshotItem> = {}): SnapshotItem {
  return {
    id: SID_A,
    short_id: SID_A.slice(0, 8),
    time: NOW - 3600,
    paths: ["/srv/www"],
    run_id: 11,
    bytes_added: 800 * 1024 ** 2,
    reasons: ["last", "daily"],
    ...over,
  };
}

function entry(over: Partial<SnapshotEntry>): SnapshotEntry {
  return { name: "x", path: "/srv/www/x", type: "file", size: 2048, mtime: NOW - 86400, ...over };
}

const ROOT_ENTRIES = [
  entry({ name: "uploads", path: "/srv/www/uploads", type: "dir", size: 0 }),
  entry({ name: "index.html", path: "/srv/www/index.html", size: 2048 }),
];

function filesRoutes() {
  return {
    "GET /jobs": ok({ list: [job()] }),
    "GET /jobs/1/snapshots": ok({
      job_name: "站点文件",
      source_type: "files",
      snapshots: [
        snap(),
        snap({
          id: SID_B,
          short_id: SID_B.slice(0, 8),
          time: NOW - 90000,
          bytes_added: 200 * 1024 ** 2,
          reasons: ["weekly"],
        }),
        snap({ id: "c".repeat(64), short_id: "cccccccc", time: NOW - 180000, bytes_added: -1, reasons: [] }),
      ],
    }),
  };
}

function lastDownloadHref() {
  const spy = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (this: HTMLAnchorElement) {
    hrefs.push(this.getAttribute("href") ?? "");
  });
  const hrefs: string[] = [];
  return { spy, hrefs };
}

describe("快照页：列表", () => {
  it("列出快照的时间、新增量、相对长度的条和保留原因", async () => {
    mockApi({
      ...filesRoutes(),
      [`GET /jobs/1/snapshots/${SID_A}/files`]: ok({ path: "/srv/www", entries: ROOT_ENTRIES }),
    });
    renderPage(<SnapshotsPage />, { path: "/snapshots", at: "/snapshots?job=1" });

    const list = await screen.findByRole("listbox", { name: "快照" });
    const options = within(list).getAllByRole("option");
    expect(options).toHaveLength(3);
    expect(within(options[0]).getByText("+800 MB")).toBeInTheDocument();
    expect(within(options[0]).getByText("最近")).toBeInTheDocument();
    expect(within(options[0]).getByText("每天")).toBeInTheDocument();
    expect(within(options[1]).getByText("每周")).toBeInTheDocument();
    expect(within(options[2]).getByText("—")).toBeInTheDocument();
    const bars = within(list).getAllByTestId("added-bar");
    expect(bars[0]).toHaveStyle({ width: "100%" });
    expect(bars[1]).toHaveStyle({ width: "25%" });
    expect(bars[2]).toHaveStyle({ width: "0%" });
  });

  it("地址里的 job 预选任务，没有时选第一个", async () => {
    const api = mockApi({
      "GET /jobs": ok({ list: [job(), job({ id: 2, name: "订单库", source_type: "mysql" })] }),
      "GET /jobs/2/snapshots": ok({ job_name: "订单库", source_type: "mysql", snapshots: [] }),
    });
    renderPage(<SnapshotsPage />, { path: "/snapshots", at: "/snapshots?job=2" });
    expect(await screen.findByText("这个任务还没有快照")).toBeInTheDocument();
    expect(api.find("GET", "/jobs/2/snapshots")).toHaveLength(1);
    expect(api.find("GET", "/jobs/1/snapshots")).toHaveLength(0);
  });

  it("读取失败时显示原因并可以重试", async () => {
    let n = 0;
    mockApi({
      "GET /jobs": ok({ list: [job()] }),
      "GET /jobs/1/snapshots": () =>
        ++n === 1
          ? fail(400, ErrorCode.SnapshotRepoLocked, "仓库被其他进程锁住，请稍后再试")
          : ok({ job_name: "站点文件", source_type: "files", snapshots: [] }),
    });
    renderPage(<SnapshotsPage />, { path: "/snapshots", at: "/snapshots?job=1" });
    expect(await screen.findByText(/仓库被其他进程锁住/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "重试" }));
    expect(await screen.findByText("这个任务还没有快照")).toBeInTheDocument();
  });

  it("没有任务时引导去创建", async () => {
    mockApi({ "GET /jobs": ok({ list: [] }) });
    renderPage(<SnapshotsPage />, { path: "/snapshots" });
    expect(await screen.findByText("还没有备份任务")).toBeInTheDocument();
  });
});

describe("快照页：文件快照", () => {
  it("目录懒加载，勾选后下载所选先预检再发起下载", async () => {
    const api = mockApi({
      ...filesRoutes(),
      [`GET /jobs/1/snapshots/${SID_A}/files`]: (call) =>
        call.query.includes("uploads")
          ? ok({
              path: "/srv/www/uploads",
              entries: [entry({ name: "a.png", path: "/srv/www/uploads/a.png", size: 5000 })],
            })
          : ok({ path: "/srv/www", entries: ROOT_ENTRIES }),
      [`GET /jobs/1/snapshots/${SID_A}/download/check`]: ok({}),
    });
    const { hrefs } = lastDownloadHref();
    renderPage(<SnapshotsPage />, { path: "/snapshots", at: "/snapshots?job=1" });

    const download = await screen.findByRole("button", { name: /下载所选/ });
    expect(download).toBeDisabled();
    expect(await screen.findByText("index.html")).toBeInTheDocument();
    expect(screen.getByText("2.0 KB")).toBeInTheDocument();

    // 展开目录才请求它的内容
    expect(api.calls.filter((c) => c.query.includes("uploads"))).toHaveLength(0);
    await userEvent.click(screen.getByRole("button", { name: "展开 uploads" }));
    expect(await screen.findByText("a.png")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("checkbox", { name: "选择 uploads" }));
    await userEvent.click(screen.getByRole("checkbox", { name: "选择 index.html" }));
    expect(screen.getByText(/已选/)).toHaveTextContent("已选 2 项");
    expect(download).toBeEnabled();

    await userEvent.click(download);
    await waitFor(() => expect(hrefs).toHaveLength(1));
    const check = api.find("GET", `/jobs/1/snapshots/${SID_A}/download/check`);
    expect(check).toHaveLength(1);
    const want = new URLSearchParams([
      ["path", "/srv/www/uploads"],
      ["path", "/srv/www/index.html"],
    ]).toString();
    expect(check[0].query).toBe(want);
    expect(hrefs[0]).toBe(`/api/v1/jobs/1/snapshots/${SID_A}/download?${want}`);
  });

  it("预检失败：原因显示在面板上，不发起下载", async () => {
    mockApi({
      ...filesRoutes(),
      [`GET /jobs/1/snapshots/${SID_A}/files`]: ok({ path: "/srv/www", entries: ROOT_ENTRIES }),
      [`GET /jobs/1/snapshots/${SID_A}/download/check`]: fail(
        400,
        ErrorCode.SnapshotRepoLocked,
        "仓库被其他进程锁住，请稍后再试"
      ),
    });
    const { hrefs } = lastDownloadHref();
    renderPage(<SnapshotsPage />, { path: "/snapshots", at: "/snapshots?job=1" });
    await userEvent.click(await screen.findByRole("checkbox", { name: "选择 index.html" }));
    await userEvent.click(screen.getByRole("button", { name: /下载所选/ }));

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("下载失败");
    expect(alert).toHaveTextContent("仓库被其他进程锁住");
    expect(hrefs).toHaveLength(0);
  });

  it("切换快照后清空所选并重新读取目录", async () => {
    const api = mockApi({
      ...filesRoutes(),
      [`GET /jobs/1/snapshots/${SID_A}/files`]: ok({ path: "/srv/www", entries: ROOT_ENTRIES }),
      [`GET /jobs/1/snapshots/${SID_B}/files`]: ok({
        path: "/srv/www",
        entries: [entry({ name: "old.txt", path: "/srv/www/old.txt" })],
      }),
    });
    renderPage(<SnapshotsPage />, { path: "/snapshots", at: "/snapshots?job=1" });
    await userEvent.click(await screen.findByRole("checkbox", { name: "选择 index.html" }));
    await userEvent.click(screen.getAllByRole("option")[1]);
    expect(await screen.findByText("old.txt")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /下载所选/ })).toBeDisabled();
    expect(api.find("GET", `/jobs/1/snapshots/${SID_B}/files`)).toHaveLength(1);
  });
});

describe("快照页：数据库快照", () => {
  function dbRoutes() {
    return {
      "GET /jobs": ok({ list: [job({ id: 2, name: "订单库", source_type: "mysql", paths: null })] }),
      "GET /jobs/2/snapshots": ok({
        job_name: "订单库",
        source_type: "mysql",
        snapshots: [snap({ paths: ["/mysql/shop.sql"] })],
      }),
      [`GET /jobs/2/snapshots/${SID_A}/exports`]: ok({
        files: [
          {
            path: "/mysql/shop.sql",
            size: 7 * 1024 ** 3,
            gzip: true,
            filename: "shop.sql.gz",
            restore_command: "gunzip < shop.sql.gz | mysql -h 127.0.0.1 -u root -p",
          },
        ],
      }),
      [`GET /jobs/2/snapshots/${SID_A}/download/check`]: ok({}),
    };
  }

  it("列出导出文件，给出可复制的恢复命令", async () => {
    mockApi(dbRoutes());
    renderPage(<SnapshotsPage />, { path: "/snapshots", at: "/snapshots?job=2" });
    expect(await screen.findByText("shop.sql.gz")).toBeInTheDocument();
    expect(screen.getByText("7.0 GB")).toBeInTheDocument();
    expect(screen.getByText("gunzip < shop.sql.gz | mysql -h 127.0.0.1 -u root -p")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "复制" })).toBeInTheDocument();
    expect(screen.getByText(/不会写回/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /下载所选/ })).not.toBeInTheDocument();
  });

  it("下载导出文件：带 gzip 参数先预检再下载", async () => {
    const api = mockApi(dbRoutes());
    const { hrefs } = lastDownloadHref();
    renderPage(<SnapshotsPage />, { path: "/snapshots", at: "/snapshots?job=2" });
    await userEvent.click(await screen.findByRole("button", { name: "下载 .sql.gz" }));
    await waitFor(() => expect(hrefs).toHaveLength(1));
    expect(api.find("GET", `/jobs/2/snapshots/${SID_A}/download/check`)[0].query).toBe(
      "path=%2Fmysql%2Fshop.sql&gzip=true"
    );
    expect(hrefs[0]).toBe(`/api/v1/jobs/2/snapshots/${SID_A}/download?path=%2Fmysql%2Fshop.sql&gzip=true`);
  });

  it("下载失败时在面板上显示原因", async () => {
    mockApi({
      ...dbRoutes(),
      [`GET /jobs/2/snapshots/${SID_A}/download/check`]: fail(
        400,
        ErrorCode.RepoBusy,
        "存储位置正在备份或维护，请稍后再试"
      ),
    });
    const { hrefs } = lastDownloadHref();
    renderPage(<SnapshotsPage />, { path: "/snapshots", at: "/snapshots?job=2" });
    await userEvent.click(await screen.findByRole("button", { name: "下载 .sql.gz" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("存储位置正在备份或维护");
    expect(hrefs).toHaveLength(0);
  });
});
