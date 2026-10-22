import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { JobFormPage, JobsRoutes } from "./job-form";
import { renderPage } from "@/test/render";
import { fail, mockApi, ok } from "@/test/api-mock";

afterEach(() => vi.useRealTimers());

const WEB01 = {
  id: 1,
  name: "web-01",
  host: "203.0.113.10",
  port: 22,
  username: "ops",
  auth_type: "password",
  hops: [
    { type: "ssh", server_id: 7, server_name: "bastion", host: "203.0.113.9", port: 22, username: "", password: "" },
  ],
  status: "ok",
};
const DEAD = { ...WEB01, id: 2, name: "old-box", hops: [], status: "failed" };
const NAS = { id: 1, name: "群晖 DS920+", type: "local", status: "ok", free_space: 1.4 * 1024 ** 4 };
const NAS_OFF = { id: 2, name: "飞牛备份盘", type: "local", status: "not_mounted", free_space: null };

const JOB = {
  id: 5,
  name: "商城数据库",
  source_type: "mysql",
  server_id: 1,
  server_name: "web-01",
  conn_mode: "ssh",
  db_host: "172.17.0.2",
  db_port: 3306,
  db_user: "backup",
  db_password: "已设置",
  databases: ["shop"],
  all_databases: false,
  auth_db: "",
  paths: [],
  excludes: [],
  repo_id: 1,
  repo_name: "群晖 DS920+",
  schedule: { kind: "daily", time: "03:00", weekday: 0, cron: "" },
  retention: { preset: "long", days: 7, weeks: 4, months: 6 },
  advanced: { timeout_sec: 7200, bandwidth_kbps: 0, compression: "auto", extra_args: [] },
  paused: false,
  next_run_at: 0,
  attention: false,
  created_at: 0,
  updated_at: 0,
};

const DBS = {
  ok: true,
  list: [
    { name: "shop", size_bytes: 8_160_000_000, system: false, selectable: true },
    { name: "shop_log", size_bytes: 24_000_000_000, system: false, selectable: true },
    { name: "sys", size_bytes: -1, system: true, selectable: false },
  ],
  reason: "",
  reason_label: "",
  message: "",
};
const TEST_FAIL = {
  ok: false,
  checks: [
    { key: "ssh", label: "SSH 隧道 web-01", ok: true, skipped: false, reason: "", fix: "" },
    { key: "db_reach", label: "数据库可达", ok: true, skipped: false, reason: "", fix: "" },
    { key: "db_auth", label: "认证通过", ok: true, skipped: false, reason: "", fix: "" },
    {
      key: "db_privs",
      label: "权限足够导出",
      ok: false,
      skipped: false,
      reason: "用户 backup 缺少 LOCK TABLES 权限",
      fix: "GRANT LOCK TABLES ON shop.* TO 'backup'@'%';",
    },
    { key: "repo", label: "存储位置可写", ok: false, skipped: true, reason: "前面的检查没有通过，已跳过", fix: "" },
  ],
};
const TEST_OK = {
  ok: true,
  checks: TEST_FAIL.checks.map((c) => ({ ...c, ok: true, skipped: false, reason: "", fix: "" })),
};

const BASE = {
  "GET /servers": ok({ list: [WEB01, DEAD] }),
  "GET /repos": ok({ list: [NAS, NAS_OFF] }),
  "POST /jobs/next-runs": ok({ timezone: "Asia/Shanghai", times: [1760000000, 1760086400, 1760172800] }),
  "POST /jobs/databases": ok(DBS),
};

function editJob(extra: Parameters<typeof mockApi>[0] = {}, job: object = JOB) {
  const m = mockApi({ ...BASE, "GET /jobs/5": ok(job), ...extra });
  renderPage(<JobFormPage />, { path: "/jobs/:id/edit", at: "/jobs/5/edit" });
  return m;
}
function newJob(extra: Parameters<typeof mockApi>[0] = {}) {
  const m = mockApi({ ...BASE, ...extra });
  renderPage(<JobFormPage />, { path: "/jobs/new" });
  return m;
}

const summary = () => screen.getByRole("complementary", { name: "摘要" });
const row = (k: string) => within(summary()).getByText(k).nextElementSibling as HTMLElement;
const ready = () => screen.findByRole("heading", { name: /备份任务/ });
const text = (el: HTMLElement) => el.textContent?.replace(/\s+/g, " ").trim();

describe("按数据源类型显示的字段", () => {
  it("MySQL 经 SSH：服务器、数据库地址、用户、密码；文件：目录和排除；MongoDB 多一个认证库", async () => {
    newJob();
    const user = userEvent.setup();
    await ready();
    expect(screen.getByRole("combobox", { name: "服务器" })).toBeInTheDocument();
    for (const l of ["数据库地址", "用户", "密码"]) expect(screen.getByLabelText(l)).toBeInTheDocument();
    expect(screen.queryByLabelText("认证库")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("目录 1")).not.toBeInTheDocument();

    await user.click(screen.getByRole("radio", { name: "MongoDB" }));
    expect(screen.getByLabelText("认证库")).toHaveValue("admin");

    await user.click(screen.getByRole("radio", { name: "文件" }));
    expect(screen.getByLabelText("目录 1")).toBeInTheDocument();
    expect(screen.getByLabelText("添加排除规则")).toBeInTheDocument();
    expect(screen.queryByLabelText("数据库地址")).not.toBeInTheDocument();
    expect(screen.queryByRole("radiogroup", { name: "连接方式" })).not.toBeInTheDocument();
  });

  it("直连时不选服务器；Redis 没有库列表", async () => {
    newJob();
    const user = userEvent.setup();
    await ready();
    await user.click(screen.getByRole("radio", { name: "直连" }));
    expect(screen.queryByRole("combobox", { name: "服务器" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("radio", { name: "Redis" }));
    expect(screen.queryByText("全部，含以后新建")).not.toBeInTheDocument();
  });
});

describe("目录浏览", () => {
  const FILES = {
    ...JOB,
    id: 9,
    name: "站点文件",
    source_type: "files",
    databases: [],
    paths: ["/srv/www"],
  };
  const TREE: Record<string, { name: string; mtime: number; symlink: boolean; selectable: boolean }[]> = {
    "/srv/www": [
      { name: "static", mtime: 1760000000, symlink: false, selectable: true },
      { name: "uploads", mtime: 1760000000, symlink: false, selectable: true },
    ],
    "/srv/www/uploads": [{ name: "2026", mtime: 1760000000, symlink: false, selectable: true }],
    "/srv/www/uploads/2026": [],
  };
  const pathsRoute = (call: { body: unknown }) => {
    const path = (call.body as { path: string }).path || "/";
    const list = TREE[path];
    return list
      ? ok({ ok: true, path, list, truncated: false, reason: "", reason_label: "", message: "" })
      : ok({
          ok: false,
          path,
          list: null,
          truncated: false,
          reason: "not_found",
          reason_label: "不存在",
          message: `${path} 不存在`,
        });
  };

  it("未选服务器时不能浏览", async () => {
    newJob();
    const user = userEvent.setup();
    await ready();
    await user.click(screen.getByRole("radio", { name: "文件" }));
    expect(screen.getByRole("button", { name: "浏览目录 1" })).toBeDisabled();
    expect(screen.getByText("先选服务器才能浏览目录。")).toBeInTheDocument();
  });

  it("浏览、进入子目录并选中，写回路径并显示已确认", async () => {
    const m = editJob({ "POST /jobs/paths": pathsRoute }, FILES);
    const user = userEvent.setup();
    await ready();
    await user.click(screen.getByRole("button", { name: "浏览目录 1" }));
    const dialog = await screen.findByRole("dialog", { name: "选择目录" });
    await within(dialog).findByRole("button", { name: /uploads/ });
    await user.click(within(dialog).getByRole("button", { name: /uploads/ }));
    await user.click(await within(dialog).findByRole("button", { name: /2026/ }));
    // 等这一层读完，「选择此目录」才可用
    await within(dialog).findByText(/这个目录下没有子目录/);
    await user.click(within(dialog).getByRole("button", { name: "选择此目录" }));
    await waitFor(() => expect(screen.getByLabelText("目录 1")).toHaveValue("/srv/www/uploads/2026"));
    expect(screen.getByText("已确认可读")).toBeInTheDocument();
    expect(m.find("POST", "/jobs/paths").map((c) => (c.body as { path: string }).path)).toEqual([
      "/srv/www",
      "/srv/www/uploads",
      "/srv/www/uploads/2026",
    ]);
  });

  it("手改路径后已确认提示消失", async () => {
    editJob({ "POST /jobs/paths": pathsRoute }, FILES);
    const user = userEvent.setup();
    await ready();
    await user.click(screen.getByRole("button", { name: "浏览目录 1" }));
    await user.click(await screen.findByRole("button", { name: "选择此目录" }));
    expect(await screen.findByText(/已确认可读/)).toBeInTheDocument();
    await user.type(screen.getByLabelText("目录 1"), "x");
    expect(screen.queryByText(/已确认可读/)).not.toBeInTheDocument();
  });

  it("读不了时写明是哪个目录，并给退路", async () => {
    editJob({ "POST /jobs/paths": pathsRoute }, FILES);
    const user = userEvent.setup();
    await ready();
    await user.click(screen.getByRole("button", { name: "浏览目录 1" }));
    await user.click(await screen.findByRole("button", { name: /uploads/ }));
    await user.click(await screen.findByRole("button", { name: "选择此目录" }));
    // 手工改到不存在的目录后重新浏览
    await user.clear(screen.getByLabelText("目录 1"));
    await user.type(screen.getByLabelText("目录 1"), "/nope");
    await user.click(screen.getByRole("button", { name: "浏览目录 1" }));
    const dialog = await screen.findByRole("dialog", { name: "选择目录" });
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(/\/nope 不存在/);
    expect(within(dialog).getByRole("button", { name: "回到根目录" })).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "选择此目录" })).toBeDisabled();
  });

  it("没有子目录也能选", async () => {
    editJob({ "POST /jobs/paths": pathsRoute }, FILES);
    const user = userEvent.setup();
    await ready();
    await user.click(screen.getByRole("button", { name: "浏览目录 1" }));
    const dialog = await screen.findByRole("dialog", { name: "选择目录" });
    await user.click(await within(dialog).findByRole("button", { name: /uploads/ }));
    await user.click(await within(dialog).findByRole("button", { name: /2026/ }));
    expect(await within(dialog).findByText(/这个目录下没有子目录/)).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "选择此目录" })).toBeEnabled();
  });
});

describe("库勾选", () => {
  it("系统库显示但不可选；勾选的库进入摘要", async () => {
    const m = editJob();
    const user = userEvent.setup();
    await ready();
    const sys = await screen.findByRole("checkbox", { name: "sys" }, { timeout: 3000 });
    expect(sys).toBeDisabled();
    expect(sys).not.toBeChecked();
    expect(screen.getByRole("checkbox", { name: "shop" })).toBeChecked();
    expect(text(row("来源"))).toContain("shop");

    await user.click(screen.getByRole("checkbox", { name: "shop_log" }));
    expect(text(row("来源"))).toContain("shop、shop_log");
    // 读库请求带任务 id 和占位密码，让后端取已保存的密码
    const body = m.find("POST", "/jobs/databases")[0].body as Record<string, unknown>;
    expect(body).toMatchObject({ id: 5, db_password: "已设置", db_host: "172.17.0.2", db_port: 3306 });
  });

  it("打开「全部，含以后新建」后摘要写全部库，不再提交具体的库", async () => {
    const m = editJob({ "PUT /jobs/5": ok(JOB), "POST /jobs/test": ok(TEST_OK) });
    const user = userEvent.setup();
    await ready();
    await screen.findByRole("checkbox", { name: "shop_log" }, { timeout: 3000 });
    await user.click(screen.getByRole("switch", { name: "全部，含以后新建" }));
    expect(text(row("来源"))).toContain("全部库");
    await user.click(screen.getByRole("button", { name: "测试连接" }));
    await screen.findByText("全部通过");
    await user.click(screen.getByRole("button", { name: "保存" }));
    await waitFor(() => expect(m.find("PUT", "/jobs/5")).toHaveLength(1));
    expect(m.find("PUT", "/jobs/5")[0].body).toMatchObject({ all_databases: true, databases: [] });
  });

  it("读取失败时原地显示原因，重试后列出库", async () => {
    let n = 0;
    const m = editJob({
      "POST /jobs/databases": () =>
        ++n === 1
          ? ok({ ok: false, list: null, reason: "auth_failed", reason_label: "认证失败", message: "用户名或密码不对" })
          : ok(DBS),
    });
    const user = userEvent.setup();
    await ready();
    const alert = await screen.findByRole("alert", {}, { timeout: 3000 });
    expect(alert).toHaveTextContent("认证失败");
    expect(alert).toHaveTextContent("用户名或密码不对");
    await user.click(within(alert).getByRole("button", { name: "重试" }));
    expect(await screen.findByRole("checkbox", { name: "shop_log" })).toBeInTheDocument();
    expect(m.find("POST", "/jobs/databases")).toHaveLength(2);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});

describe("存储位置卡片", () => {
  it("单选卡片显示剩余空间；未挂载的不可选", async () => {
    editJob();
    const user = userEvent.setup();
    await ready();
    const ok1 = await screen.findByRole("radio", { name: /群晖 DS920\+/ });
    expect(ok1).toBeChecked();
    expect(ok1).toHaveTextContent("剩余 1.4 TB");
    const off = screen.getByRole("radio", { name: /飞牛备份盘/ });
    expect(off).toBeDisabled();
    expect(off).toHaveTextContent("未挂载");
    await user.click(off);
    expect(ok1).toBeChecked();
    expect(text(row("存到"))).toBe("群晖 DS920+");
  });
});

describe("计划与下次运行", () => {
  it("显示下一次运行时间，切换频率后重新预览并写入摘要", async () => {
    const m = editJob();
    const user = userEvent.setup();
    await ready();
    expect(await screen.findByText(/^下次 /, {}, { timeout: 3000 })).toBeInTheDocument();
    expect(text(row("计划"))).toBe("每天 03:00");

    await user.click(screen.getByRole("radio", { name: "每周" }));
    expect(text(row("计划"))).toBe("每周日 03:00");
    await user.click(screen.getByRole("radio", { name: "cron" }));
    await user.clear(screen.getByLabelText("cron 表达式"));
    await user.type(screen.getByLabelText("cron 表达式"), "15 3 * * 1-5");
    expect(text(row("计划"))).toBe("cron 15 3 * * 1-5");
    await waitFor(() =>
      expect(m.find("POST", "/jobs/next-runs").at(-1)?.body).toMatchObject({
        schedule: { kind: "cron", cron: "15 3 * * 1-5" },
      })
    );
  });
});

describe("保留规则：句式、估算和时间轴", () => {
  it("长期写成三句话，估算与后端一致（每天一份 = 14 个，不是 7+4+6）", async () => {
    vi.useFakeTimers({ toFake: ["Date"], now: new Date(Date.UTC(2026, 9, 15, 2, 0, 0)) });
    editJob();
    await ready();
    const list = await screen.findByRole("list", { name: "保留规则" });
    expect(within(list).getAllByRole("listitem").map(text)).toEqual([
      "最近 7 天：每天留一份",
      "再往前到 4 周：每周留一份",
      "再往前到 6 个月：每月留一份",
    ]);
    expect(screen.getByText("共约 14 个快照，其余自动删除。")).toBeInTheDocument();
    expect(screen.getByTestId("retention-timeline")).toBeInTheDocument();
    expect(text(row("保留"))).toBe("约 14 个快照，最早 6 个月前");
  });

  it("自定义可改三个数字；每小时的估算按当天最后一份算", async () => {
    vi.useFakeTimers({ toFake: ["Date"], now: new Date(Date.UTC(2026, 9, 15, 2, 0, 0)) });
    editJob();
    const user = userEvent.setup();
    await ready();
    await user.click(await screen.findByRole("radio", { name: "自定义" }));
    const days = screen.getByRole("textbox", { name: "最近几天" });
    await user.clear(days);
    await user.type(days, "14");
    const weeks = screen.getByRole("textbox", { name: "往前到几周" });
    await user.clear(weeks);
    await user.type(weeks, "8");
    const months = screen.getByRole("textbox", { name: "往前到几个月" });
    await user.clear(months);
    await user.type(months, "0");
    expect(screen.getByText("共约 19 个快照，其余自动删除。")).toBeInTheDocument();
    expect(text(row("保留"))).toBe("约 19 个快照，最早 8 周前");
  });

  it("7 天、30 天按计划频率估算保留的快照数", async () => {
    vi.useFakeTimers({ toFake: ["Date"], now: new Date(Date.UTC(2026, 9, 15, 2, 0, 0)) });
    editJob();
    const user = userEvent.setup();
    await ready();
    await user.click(await screen.findByRole("radio", { name: "7 天" }));
    expect(screen.getByText(/保留最近 7 天内的每一次备份/)).toBeInTheDocument();
    expect(screen.getByText(/共约 7 个快照/)).toBeInTheDocument();
    expect(screen.getByTestId("retention-timeline")).toBeInTheDocument();
    await user.click(screen.getByRole("radio", { name: "每小时" }));
    expect(screen.getByText(/共约 168 个快照/)).toBeInTheDocument();
    expect(text(row("保留"))).toBe("最近 7 天的全部备份");
  });
});

describe("高级", () => {
  it("默认收起，标题行显示当前值；展开后可改", async () => {
    const m = editJob({ "PUT /jobs/5": ok(JOB), "POST /jobs/test": ok(TEST_OK) });
    const user = userEvent.setup();
    await ready();
    const head = await screen.findByRole("button", { name: /高级/ });
    expect(head).toHaveAttribute("aria-expanded", "false");
    expect(head).toHaveTextContent("超时 2 小时，不限速，自动压缩");
    expect(screen.queryByLabelText("限速")).not.toBeInTheDocument();

    await user.click(head);
    expect(head).toHaveAttribute("aria-expanded", "true");
    await user.type(screen.getByLabelText("限速"), "10");
    await user.type(screen.getByLabelText("导出附加参数"), "--routines --triggers");
    await user.click(screen.getByRole("button", { name: "测试连接" }));
    await screen.findByText("全部通过");
    await user.click(screen.getByRole("button", { name: "保存" }));
    await waitFor(() => expect(m.find("PUT", "/jobs/5")).toHaveLength(1));
    expect(m.find("PUT", "/jobs/5")[0].body).toMatchObject({
      advanced: {
        timeout_sec: 7200,
        bandwidth_kbps: 10240,
        compression: "auto",
        extra_args: ["--routines", "--triggers"],
      },
    });
  });
});

describe("摘要实时更新", () => {
  it("来源、存到、加密、计划、保留五行随表单变化", async () => {
    newJob();
    const user = userEvent.setup();
    await ready();
    expect(
      within(summary())
        .getAllByRole("term")
        .map((t) => t.textContent)
    ).toEqual(["来源", "存到", "加密", "计划", "保留"]);
    expect(text(row("加密"))).toContain("AES-256");

    // 连不上的服务器标出来但仍可选：连接测试没通过时也允许保存
    await user.click(screen.getByRole("combobox", { name: "服务器" }));
    const dead = await screen.findByRole("option", { name: /old-box/ });
    expect(dead).toHaveTextContent("连不上");
    expect(dead).not.toHaveAttribute("aria-disabled", "true");
    await user.click(await screen.findByRole("option", { name: /web-01/ }));
    await user.type(screen.getByLabelText("数据库地址"), "172.17.0.2:3306");
    expect(text(row("来源"))).toContain("web-01 隧道 → 172.17.0.2:3306");
    expect(text(row("来源"))).toContain("未选库");

    await user.click(screen.getByRole("radio", { name: "直连" }));
    expect(text(row("来源"))).toContain("直连 172.17.0.2:3306");

    await user.click(screen.getByRole("radio", { name: /群晖 DS920\+/ }));
    expect(text(row("存到"))).toBe("群晖 DS920+");

    await user.click(screen.getByRole("radio", { name: "文件" }));
    await user.type(screen.getByLabelText("目录 1"), "/srv/www/uploads");
    expect(text(row("来源"))).toContain("/srv/www/uploads");
  });
});

describe("校验错误", () => {
  it("11401 的每行 [字段] 原因 显示在对应字段下方，并且不跳转", async () => {
    const m = newJob({
      "POST /jobs": fail(
        400,
        11401,
        "[name] 名称不能为空，且不超过 64 个字\n[db_user] 数据库用户不能为空\n[schedule.time] 时刻须写成 HH:MM\n[databases] 请至少勾选一个库，或打开「全部，含以后新建」\n[repo_id] 请选择存到哪个存储位置\n[advanced.timeout_sec] 超时须在 1 分钟到 7 天之间"
      ),
      "POST /jobs/test": ok(TEST_OK),
    });
    const user = userEvent.setup();
    await ready();
    await user.type(screen.getByLabelText("数据库地址"), "172.17.0.2:3306");
    await user.click(screen.getByRole("button", { name: "测试连接" }));
    await screen.findByText("全部通过");
    await user.click(screen.getByRole("button", { name: /高级/ }));
    await user.click(screen.getByRole("button", { name: "保存" }));

    await waitFor(() =>
      expect(screen.getByLabelText("名称")).toHaveAccessibleDescription("名称不能为空，且不超过 64 个字")
    );
    expect(screen.getByLabelText("用户")).toHaveAccessibleDescription("数据库用户不能为空");
    expect(screen.getByLabelText("时刻")).toHaveAccessibleDescription("时刻须写成 HH:MM");
    expect(screen.getByRole("group", { name: "库" })).toHaveAccessibleDescription(/请至少勾选一个库/);
    expect(screen.getByRole("radiogroup", { name: "存储位置" })).toHaveAccessibleDescription("请选择存到哪个存储位置");
    expect(screen.getByLabelText("超时")).toHaveAccessibleDescription("超时须在 1 分钟到 7 天之间");
    // 没有出现在别的字段下面
    expect(screen.getByLabelText("数据库地址")).not.toHaveAccessibleDescription(/不能为空/);
    expect(screen.getByTestId("location")).toHaveTextContent("/jobs/new");

    // 改动字段后它的错误消失，其他字段的错误保留
    await user.type(screen.getByLabelText("名称"), "商城");
    expect(screen.getByLabelText("名称")).not.toHaveAccessibleDescription(/不能为空/);
    expect(screen.getByLabelText("用户")).toHaveAccessibleDescription("数据库用户不能为空");
    expect(m.find("POST", "/jobs")).toHaveLength(1);
  });

  it("「高级」收起时有字段错误：自动展开，错误显示在对应字段下方", async () => {
    newJob({
      "POST /jobs": fail(400, 11401, "[advanced.timeout_sec] 超时须在 1 分钟到 7 天之间"),
      "POST /jobs/test": ok(TEST_OK),
    });
    const user = userEvent.setup();
    await ready();
    await user.type(screen.getByLabelText("数据库地址"), "172.17.0.2:3306");
    await user.click(screen.getByRole("button", { name: "测试连接" }));
    await screen.findByText("全部通过");
    expect(screen.queryByLabelText("超时")).toBeNull();
    await user.click(screen.getByRole("button", { name: "保存" }));
    await waitFor(() =>
      expect(screen.getByLabelText("超时")).toHaveAccessibleDescription("超时须在 1 分钟到 7 天之间")
    );
    expect(screen.getByRole("button", { name: /高级/ })).toHaveAttribute("aria-expanded", "true");
  });

  it("数据库地址写不成 主机:端口 时直接在字段下提示，不发保存请求", async () => {
    const m = newJob({ "POST /jobs/test": ok(TEST_OK) });
    const user = userEvent.setup();
    await ready();
    await user.type(screen.getByLabelText("数据库地址"), "172.17.0.2");
    await user.click(screen.getByRole("button", { name: "保存" }));
    expect(screen.getByLabelText("数据库地址")).toHaveAccessibleDescription(/主机:端口/);
    expect(m.find("POST", "/jobs")).toHaveLength(0);
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
  });

  it("后端返回的字段名页面上没有对应位置时，也要显示出来", async () => {
    newJob({
      "POST /jobs": fail(400, 11401, "[conn_mode] 连接方式须是 ssh 或 direct"),
      "POST /jobs/test": ok(TEST_OK),
    });
    const user = userEvent.setup();
    await ready();
    await user.type(screen.getByLabelText("数据库地址"), "172.17.0.2:3306");
    await user.click(screen.getByRole("button", { name: "测试连接" }));
    await screen.findByText("全部通过");
    await user.click(screen.getByRole("button", { name: "保存" }));
    expect(await screen.findByText(/连接方式须是 ssh 或 direct/)).toBeInTheDocument();
  });
});

describe("连接测试与保存确认", () => {
  it("逐项显示结果，失败项给出原因和可复制的命令", async () => {
    editJob({ "POST /jobs/test": ok(TEST_FAIL) });
    const user = userEvent.setup();
    await ready();
    await screen.findByRole("checkbox", { name: "shop" }, { timeout: 3000 });
    await user.click(screen.getByRole("button", { name: "测试连接" }));
    expect(await screen.findByText("2 项未通过")).toBeInTheDocument();
    expect(screen.getByText("用户 backup 缺少 LOCK TABLES 权限")).toBeInTheDocument();
    expect(screen.getByText("GRANT LOCK TABLES ON shop.* TO 'backup'@'%';")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "复制修复命令" })).toBeInTheDocument();
    expect(screen.getByText("前面的检查没有通过，已跳过")).toBeInTheDocument();
  });

  it("测试没通过：保存前要确认一次，取消不保存，确认后才保存", async () => {
    const m = editJob({ "POST /jobs/test": ok(TEST_FAIL), "PUT /jobs/5": ok(JOB) });
    const user = userEvent.setup();
    await ready();
    await screen.findByRole("checkbox", { name: "shop" }, { timeout: 3000 });
    await user.click(screen.getByRole("button", { name: "测试连接" }));
    await screen.findByText("2 项未通过");

    await user.click(screen.getByRole("button", { name: "保存" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(dialog).toHaveTextContent("连接测试没有通过");
    expect(m.find("PUT", "/jobs/5")).toHaveLength(0);
    await user.click(within(dialog).getByRole("button", { name: "取消" }));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
    expect(m.find("PUT", "/jobs/5")).toHaveLength(0);

    await user.click(screen.getByRole("button", { name: "保存" }));
    await user.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "仍然保存" }));
    await waitFor(() => expect(m.find("PUT", "/jobs/5")).toHaveLength(1));
    await waitFor(() => expect(screen.getByTestId("location")).toHaveTextContent("/jobs"));
    expect(screen.getByTestId("location")).not.toHaveTextContent("edit");
  });

  it("还没测试过也要确认；测试通过后直接保存", async () => {
    const m = editJob({ "POST /jobs/test": ok(TEST_OK), "PUT /jobs/5": ok(JOB) });
    const user = userEvent.setup();
    await ready();
    await screen.findByRole("checkbox", { name: "shop" }, { timeout: 3000 });
    await user.click(screen.getByRole("button", { name: "保存" }));
    expect(await screen.findByRole("alertdialog")).toHaveTextContent("还没有测试连接");
    await user.click(screen.getByRole("button", { name: "取消" }));

    await user.click(screen.getByRole("button", { name: "测试连接" }));
    await screen.findByText("全部通过");
    await user.click(screen.getByRole("button", { name: "保存" }));
    await waitFor(() => expect(m.find("PUT", "/jobs/5")).toHaveLength(1));
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
  });

  it("测试通过后又改了配置，结果作废，保存再次要确认", async () => {
    editJob({ "POST /jobs/test": ok(TEST_OK) });
    const user = userEvent.setup();
    await ready();
    await screen.findByRole("checkbox", { name: "shop" }, { timeout: 3000 });
    await user.click(screen.getByRole("button", { name: "测试连接" }));
    await screen.findByText("全部通过");
    await user.type(screen.getByLabelText("用户"), "2");
    await user.click(screen.getByRole("button", { name: "保存" }));
    expect(await screen.findByRole("alertdialog")).toHaveTextContent("还没有测试连接");
  });
});

describe("保存并运行", () => {
  it("新建保存后立即运行，并跳转到运行详情", async () => {
    const m = newJob({
      "POST /jobs": (c) => ok({ ...JOB, ...(c.body as object), id: 9 }),
      "POST /jobs/9/run": ok({ run_id: 42 }),
      "POST /jobs/test": ok(TEST_OK),
    });
    const user = userEvent.setup();
    await ready();
    await user.type(screen.getByLabelText("名称"), "商城数据库");
    await user.type(screen.getByLabelText("数据库地址"), "172.17.0.2:3306");
    await user.click(screen.getByRole("button", { name: "测试连接" }));
    await screen.findByText("全部通过");
    await user.click(screen.getByRole("button", { name: "保存并运行" }));
    await waitFor(() => expect(screen.getByTestId("location")).toHaveTextContent("/runs/42"));
    expect(m.find("POST", "/jobs")).toHaveLength(1);
    expect(m.find("POST", "/jobs/9/run")).toHaveLength(1);
    expect(m.find("POST", "/jobs")[0].body).toMatchObject({
      name: "商城数据库",
      db_host: "172.17.0.2",
      db_port: 3306,
      source_type: "mysql",
    });
  });

  it("保存成功但运行没有启动时留在表单并说明原因，再保存是修改而不是新建", async () => {
    const m = newJob({
      "POST /jobs": (c) => ok({ ...JOB, ...(c.body as object), id: 9 }),
      "PUT /jobs/9": ok(JOB),
      "POST /jobs/9/run": fail(409, 11501, "这个任务正在运行"),
      "POST /jobs/test": ok(TEST_OK),
    });
    const user = userEvent.setup();
    await ready();
    await user.type(screen.getByLabelText("数据库地址"), "172.17.0.2:3306");
    await user.click(screen.getByRole("button", { name: "测试连接" }));
    await screen.findByText("全部通过");
    await user.click(screen.getByRole("button", { name: "保存并运行" }));
    expect(await screen.findByText(/这个任务正在运行/)).toBeInTheDocument();
    expect(screen.getByTestId("location")).toHaveTextContent("/jobs/new");
    await user.click(screen.getByRole("button", { name: "保存" }));
    await waitFor(() => expect(m.find("PUT", "/jobs/9")).toHaveLength(1));
    expect(m.find("POST", "/jobs")).toHaveLength(1);
  });
});

describe("路由", () => {
  it("/jobs 是任务列表，/jobs/new 和 /jobs/:id/edit 是表单", async () => {
    mockApi({
      ...BASE,
      "GET /jobs/5": ok(JOB),
      "GET /jobs": ok({ list: [] }),
      "GET /runs": ok({ list: [], total: 0 }),
    });
    const { unmount } = renderPage(<JobsRoutes />, { path: "/jobs/*", at: "/jobs" });
    expect(await screen.findByText("还没有备份任务")).toBeInTheDocument();
    unmount();
    renderPage(<JobsRoutes />, { path: "/jobs/*", at: "/jobs/5/edit" });
    expect(await screen.findByRole("heading", { name: "编辑备份任务" })).toBeInTheDocument();
    expect(screen.getByLabelText("名称")).toHaveValue("商城数据库");
    expect(screen.getByLabelText("密码")).toHaveAttribute("placeholder", "已设置，留空保持不变");
  });
});
