import { describe, expect, it } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ReposPage } from "./repos";
import { renderPage } from "@/test/render";
import { PINNED_NOW_SEC, pinClock } from "@/test/clock";
import { fail, mockApi, ok, type Call } from "@/test/api-mock";
import { SECRET_MASKED } from "@/lib/api";

const PARAMS = {
  path: "",
  host: "",
  port: 0,
  username: "",
  password: "",
  private_key: "",
  host_key: "",
  server_id: 0,
  endpoint: "",
  bucket: "",
  prefix: "",
  region: "",
  access_key_id: "",
  secret_access_key: "",
  path_style: false,
  url: "",
  vendor: "",
};

const NOW = PINNED_NOW_SEC;
pinClock();

const SYNOLOGY = {
  id: 1,
  name: "群晖 DS920+",
  type: "sftp",
  params: { ...PARAMS, host: "192.168.1.20", port: 22, path: "/volume1/backup/opsnap" },
  address: "sftp://192.168.1.20:22/volume1/backup/opsnap",
  encryption: "AES-256",
  status: "ok",
  status_message: "",
  status_at: NOW,
  stored_size: 1.4 * 1024 ** 4,
  raw_size: 5.6 * 1024 ** 4,
  dedup_ratio: 4,
  snapshot_count: 142,
  free_space: 3.1 * 1024 ** 4,
  key_count: 2,
  stats_at: NOW,
  no_recovery_key: false,
  last_check_at: NOW - 3600 * 30,
  last_check_ok: true,
  last_check_message: "",
  last_prune_at: 0,
  last_prune_ok: false,
  last_prune_message: "",
  maintenance: "",
  job_count: 2,
  created_at: NOW,
  updated_at: NOW,
};
const FNOS = {
  ...SYNOLOGY,
  id: 2,
  name: "飞牛 NAS",
  type: "local",
  params: { ...PARAMS, path: "/mnt/fnos/opsnap" },
  address: "/mnt/fnos/opsnap",
  status: "not_mounted",
  status_message: "没有挂载",
  free_space: null,
  key_count: 1,
  no_recovery_key: true,
};

const KEY = "tidal-ochre-violin-42-maple-quartz";

function created(over: Record<string, unknown> = {}) {
  return ok({ ...SYNOLOGY, id: 9, name: "新位置", adopted: false, recovery_key_error: "", ...over });
}

async function openCreate(user: ReturnType<typeof userEvent.setup>) {
  await user.click(await screen.findByRole("button", { name: "添加存储位置" }));
  const dialog = await screen.findByRole("dialog", { name: "添加存储位置" });
  await user.type(within(dialog).getByLabelText("名称"), "新位置");
  await user.click(within(dialog).getByRole("combobox", { name: "类型" }));
  await user.click(await screen.findByRole("option", { name: "本地目录" }));
  await user.type(within(dialog).getByLabelText("路径"), "/mnt/nas/opsnap");
  return dialog;
}

function createCalls(api: { find: (m: string, p: string) => Call[] }) {
  return api.find("POST", "/repos");
}

describe("存储位置列表", () => {
  it("显示占用、去重比、快照数、剩余空间、密钥数和校验时间，空闲空间未知时写未知", async () => {
    mockApi({ "GET /repos": ok({ list: [SYNOLOGY, { ...FNOS, status: "ok", no_recovery_key: false }] }) });
    renderPage(<ReposPage />, { path: "/repos" });
    const row = (await screen.findByText("群晖 DS920+")).closest("[data-repo]") as HTMLElement;
    expect(within(row).getByText("1.4 TB")).toBeInTheDocument();
    expect(within(row).getByText(/原始 5.6 TB/)).toBeInTheDocument();
    expect(within(row).getByText("4.0×")).toBeInTheDocument();
    expect(within(row).getByText(/142 个快照，NAS 剩余 3.1 TB/)).toBeInTheDocument();
    expect(within(row).getByText("AES-256 加密")).toBeInTheDocument();
    expect(within(row).getByText(/2 把密钥/)).toBeInTheDocument();
    expect(within(row).getByText(/校验无误/)).toBeInTheDocument();
    const other = screen.getByText("飞牛 NAS").closest("[data-repo]") as HTMLElement;
    expect(within(other).getByText(/NAS 剩余 未知/)).toBeInTheDocument();
  });

  it("没有恢复密钥的存储位置显示警告，有的不显示", async () => {
    mockApi({ "GET /repos": ok({ list: [SYNOLOGY, { ...FNOS, status: "ok" }] }) });
    renderPage(<ReposPage />, { path: "/repos" });
    const warn = await screen.findAllByText(/没有恢复密钥/);
    expect(warn).toHaveLength(1);
    expect(screen.getByText("飞牛 NAS").closest("[data-repo]")).toContainElement(warn[0]);
    // 没有恢复密钥时同样显示密钥数量
    expect(warn[0]).toHaveTextContent("1 把密钥，没有恢复密钥");
  });

  it("未挂载的行标红，点重新检查会调用 recheck 并刷新", async () => {
    const user = userEvent.setup();
    let fixed = false;
    const api = mockApi({
      "GET /repos": () => ok({ list: [SYNOLOGY, fixed ? { ...FNOS, status: "ok" } : FNOS] }),
      "POST /repos/2/recheck": () => {
        fixed = true;
        return ok({ ...FNOS, status: "ok" });
      },
    });
    renderPage(<ReposPage />, { path: "/repos" });
    const card = (await screen.findByText("飞牛 NAS")).closest("[data-repo]") as HTMLElement;
    expect(card).toHaveAttribute("data-state", "not_mounted");
    expect(within(card).getByRole("alert")).toHaveTextContent("没有挂载");
    expect(within(card).getByRole("alert")).toHaveTextContent("/mnt/fnos/opsnap/.opsnap-repo");
    // 正常的位置没有这条提示
    const okCard = screen.getByText("群晖 DS920+").closest("[data-repo]") as HTMLElement;
    expect(within(okCard).queryByRole("alert")).toBeNull();
    expect(within(okCard).queryByRole("button", { name: "重新检查" })).toBeNull();

    await user.click(within(card).getByRole("button", { name: "重新检查" }));
    await waitFor(() => expect(api.find("POST", "/repos/2/recheck")).toHaveLength(1));
    await waitFor(() =>
      expect(screen.getByText("飞牛 NAS").closest("[data-repo]")).toHaveAttribute("data-state", "ok")
    );
  });

  it("列表为空时给出空状态，读取失败时可重试", async () => {
    mockApi({ "GET /repos": ok({ list: null }) });
    renderPage(<ReposPage />, { path: "/repos" });
    expect(await screen.findByText(/还没有存储位置/)).toBeInTheDocument();
  });

  it("读取失败写明原因并提供重试", async () => {
    mockApi({ "GET /repos": fail(500, 1, "数据库繁忙") });
    renderPage(<ReposPage />, { path: "/repos" });
    expect(await screen.findByText(/数据库繁忙/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "重试" })).toBeInTheDocument();
  });
});

describe("维护操作", () => {
  it("解除仓库锁要二次确认，确认后带 confirm 调用，取消不调用", async () => {
    const user = userEvent.setup();
    const api = mockApi({ "GET /repos": ok({ list: [SYNOLOGY] }), "POST /repos/1/unlock": ok({}) });
    renderPage(<ReposPage />, { path: "/repos" });
    await user.click(await screen.findByRole("button", { name: "群晖 DS920+ 的维护操作" }));
    await user.click(await screen.findByRole("menuitem", { name: "解除仓库锁" }));
    const confirm = await screen.findByRole("alertdialog");
    expect(api.find("POST", "/repos/1/unlock")).toHaveLength(0);

    await user.click(within(confirm).getByRole("button", { name: "取消" }));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
    expect(api.find("POST", "/repos/1/unlock")).toHaveLength(0);

    await user.click(screen.getByRole("button", { name: "群晖 DS920+ 的维护操作" }));
    await user.click(await screen.findByRole("menuitem", { name: "解除仓库锁" }));
    await user.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "确认解除" }));
    await waitFor(() => expect(api.find("POST", "/repos/1/unlock")).toHaveLength(1));
  });

  it("校验数据直接执行；后端因忙碌拒绝时提示原因", async () => {
    const user = userEvent.setup();
    const api = mockApi({
      "GET /repos": ok({ list: [SYNOLOGY] }),
      "POST /repos/1/check": fail(409, 11314, "正在备份或维护，请稍后再试"),
    });
    renderPage(<ReposPage />, { path: "/repos" });
    await user.click(await screen.findByRole("button", { name: "群晖 DS920+ 的维护操作" }));
    await user.click(await screen.findByRole("menuitem", { name: "校验数据" }));
    await waitFor(() => expect(api.find("POST", "/repos/1/check")).toHaveLength(1));
    expect(await screen.findByText("正在备份或维护，请稍后再试")).toBeInTheDocument();
  });

  it("维护进行中显示状态并禁用维护项", async () => {
    const user = userEvent.setup();
    mockApi({ "GET /repos": ok({ list: [{ ...SYNOLOGY, maintenance: "prune" }] }) });
    renderPage(<ReposPage />, { path: "/repos" });
    expect(await screen.findByText("正在清理空间")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "群晖 DS920+ 的维护操作" }));
    expect(await screen.findByRole("menuitem", { name: "校验数据" })).toHaveAttribute("aria-disabled", "true");
    expect(screen.getByRole("menuitem", { name: "清理空间" })).toHaveAttribute("aria-disabled", "true");
  });
});

describe("添加存储位置", () => {
  it("默认要恢复密钥：显示密钥，勾选已存进密码管理器之前不能保存", async () => {
    const user = userEvent.setup();
    const api = mockApi({
      "GET /repos": ok({ list: [] }),
      "POST /repos/recovery-key": ok({ recovery_key_value: KEY }),
      "POST /repos": created(),
      "POST /repos/9/recheck": ok(SYNOLOGY),
    });
    renderPage(<ReposPage />, { path: "/repos" });
    const dialog = await openCreate(user);
    expect(await within(dialog).findByText(KEY)).toBeInTheDocument();
    const save = within(dialog).getByRole("button", { name: "测试并保存" });
    expect(save).toBeDisabled();

    await user.click(within(dialog).getByRole("checkbox", { name: "已存进密码管理器" }));
    expect(save).toBeEnabled();

    await user.click(save);
    await waitFor(() => expect(createCalls(api)).toHaveLength(1));
    expect(createCalls(api)[0].body).toMatchObject({
      name: "新位置",
      type: "local",
      params: { path: "/mnt/nas/opsnap" },
      recovery_key: true,
      recovery_key_value: KEY,
      recovery_key_saved: true,
    });
    // 创建不填统计，之后要调用 recheck
    await waitFor(() => expect(api.find("POST", "/repos/9/recheck")).toHaveLength(1));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("取消勾选恢复密钥后可以直接保存，不发送密钥值", async () => {
    const user = userEvent.setup();
    const api = mockApi({
      "GET /repos": ok({ list: [] }),
      "POST /repos/recovery-key": ok({ recovery_key_value: KEY }),
      "POST /repos": created(),
      "POST /repos/9/recheck": ok(SYNOLOGY),
    });
    renderPage(<ReposPage />, { path: "/repos" });
    const dialog = await openCreate(user);
    await within(dialog).findByText(KEY);
    await user.click(within(dialog).getByRole("checkbox", { name: /同时生成一把恢复密钥/ }));
    expect(within(dialog).queryByText(KEY)).toBeNull();
    await user.click(within(dialog).getByRole("button", { name: "测试并保存" }));
    await waitFor(() => expect(createCalls(api)).toHaveLength(1));
    expect(createCalls(api)[0].body).toMatchObject({
      recovery_key: false,
      recovery_key_value: "",
      recovery_key_saved: false,
    });
  });

  it("位置已是 restic 仓库时要求现有密码，填写后重新提交", async () => {
    const user = userEvent.setup();
    let n = 0;
    const api = mockApi({
      "GET /repos": ok({ list: [] }),
      "POST /repos/recovery-key": ok({ recovery_key_value: KEY }),
      "POST /repos": () => {
        n++;
        return n === 1
          ? fail(400, 11312, "这里已经是一个 restic 仓库，请输入它现有的密码后接入")
          : created({ adopted: true });
      },
      "POST /repos/9/recheck": ok(SYNOLOGY),
    });
    renderPage(<ReposPage />, { path: "/repos" });
    const dialog = await openCreate(user);
    await within(dialog).findByText(KEY);
    await user.click(within(dialog).getByRole("checkbox", { name: "已存进密码管理器" }));
    expect(within(dialog).queryByLabelText("现有密码")).toBeNull();
    await user.click(within(dialog).getByRole("button", { name: "测试并保存" }));

    const pw = await within(dialog).findByLabelText("现有密码");
    expect(within(dialog).getByText(/已经是一个 restic 仓库/)).toBeInTheDocument();
    expect((createCalls(api)[0].body as { existing_password?: string }).existing_password ?? "").toBe("");

    await user.type(pw, "SECRET-old-pass");
    await user.click(within(dialog).getByRole("button", { name: "测试并保存" }));
    await waitFor(() => expect(createCalls(api)).toHaveLength(2));
    expect(createCalls(api)[1].body).toMatchObject({ existing_password: "SECRET-old-pass", recovery_key_value: KEY });
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("创建返回 recovery_key_error 时展示原因", async () => {
    const user = userEvent.setup();
    mockApi({
      "GET /repos": ok({ list: [] }),
      "POST /repos/recovery-key": ok({ recovery_key_value: KEY }),
      "POST /repos": created({ recovery_key_error: "加入恢复密钥失败：restic 超时" }),
      "POST /repos/9/recheck": ok(SYNOLOGY),
    });
    renderPage(<ReposPage />, { path: "/repos" });
    const dialog = await openCreate(user);
    await within(dialog).findByText(KEY);
    await user.click(within(dialog).getByRole("checkbox", { name: "已存进密码管理器" }));
    await user.click(within(dialog).getByRole("button", { name: "测试并保存" }));
    expect(await screen.findByText(/加入恢复密钥失败：restic 超时/)).toBeInTheDocument();
  });

  it("创建失败时留在对话框并显示原因", async () => {
    const user = userEvent.setup();
    mockApi({
      "GET /repos": ok({ list: [] }),
      "POST /repos/recovery-key": ok({ recovery_key_value: KEY }),
      "POST /repos": fail(400, 11309, "无法访问存储位置：permission denied"),
    });
    renderPage(<ReposPage />, { path: "/repos" });
    const dialog = await openCreate(user);
    await within(dialog).findByText(KEY);
    await user.click(within(dialog).getByRole("checkbox", { name: "已存进密码管理器" }));
    await user.click(within(dialog).getByRole("button", { name: "测试并保存" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("permission denied");
    expect(screen.getByRole("dialog", { name: "添加存储位置" })).toBeInTheDocument();
  });
});

describe("SFTP 主机指纹", () => {
  it("核对并信任指纹之前不能保存，保存时带上 host_key", async () => {
    const user = userEvent.setup();
    const api = mockApi({
      "GET /repos": ok({ list: [] }),
      "GET /servers": ok({ list: [] }),
      "POST /repos/recovery-key": ok({ recovery_key_value: KEY }),
      "POST /repos/sftp-host-key": ok({ fingerprint: "SHA256:abcFP", host_key: "ssh-ed25519 AAAAC3 nas" }),
      "POST /repos": created(),
      "POST /repos/9/recheck": ok(SYNOLOGY),
    });
    renderPage(<ReposPage />, { path: "/repos" });
    await user.click(await screen.findByRole("button", { name: "添加存储位置" }));
    const dialog = await screen.findByRole("dialog", { name: "添加存储位置" });
    await user.type(within(dialog).getByLabelText("名称"), "群晖");
    await user.type(within(dialog).getByLabelText("主机"), "192.168.1.20");
    await user.type(within(dialog).getByLabelText("路径"), "/volume1/backup");
    await user.type(within(dialog).getByLabelText("用户名"), "backup");
    await user.type(within(dialog).getByLabelText("密码"), "SECRET-sftp");
    await within(dialog).findByText(KEY);
    await user.click(within(dialog).getByRole("checkbox", { name: "已存进密码管理器" }));
    expect(within(dialog).getByRole("button", { name: "测试并保存" })).toBeDisabled();

    await user.click(within(dialog).getByRole("button", { name: "获取主机指纹" }));
    expect(await within(dialog).findByText("SHA256:abcFP")).toBeInTheDocument();
    expect(api.find("POST", "/repos/sftp-host-key")[0].body).toMatchObject({
      host: "192.168.1.20",
      port: 22,
      server_id: 0,
    });
    expect(within(dialog).getByRole("button", { name: "测试并保存" })).toBeDisabled();
    await user.click(within(dialog).getByRole("button", { name: "一致，信任" }));
    await user.click(within(dialog).getByRole("button", { name: "测试并保存" }));
    await waitFor(() => expect(createCalls(api)).toHaveLength(1));
    expect(createCalls(api)[0].body).toMatchObject({
      type: "sftp",
      params: { host: "192.168.1.20", port: 22, host_key: "ssh-ed25519 AAAAC3 nas", password: "SECRET-sftp" },
    });
  });
});

describe("修改存储位置", () => {
  /** 打开某个存储位置的编辑对话框 */
  async function openEdit(user: ReturnType<typeof userEvent.setup>, name: string) {
    await user.click(await screen.findByRole("button", { name: `${name} 的维护操作` }));
    await user.click(await screen.findByRole("menuitem", { name: "修改" }));
    return screen.findByRole("dialog", { name: "修改存储位置" });
  }

  it("带出已保存的地址参数，类型不能改，凭据显示占位", async () => {
    const user = userEvent.setup();
    mockApi({
      "GET /repos": ok({
        list: [
          { ...SYNOLOGY, params: { ...SYNOLOGY.params, host_key: "ssh-ed25519 AAAAC3 nas", password: SECRET_MASKED } },
        ],
      }),
      "GET /servers": ok({ list: [] }),
    });
    renderPage(<ReposPage />, { path: "/repos" });
    const dialog = await openEdit(user, "群晖 DS920+");
    expect(within(dialog).getByLabelText("名称")).toHaveValue("群晖 DS920+");
    expect(within(dialog).getByLabelText("主机")).toHaveValue("192.168.1.20");
    expect(within(dialog).getByLabelText("路径")).toHaveValue("/volume1/backup/opsnap");
    expect(within(dialog).getByLabelText("密码")).toHaveValue("");
    expect(within(dialog).getByLabelText("密码")).toHaveAttribute("placeholder", "已设置，留空保持不变");
    // 类型和恢复密钥都只在添加时决定
    expect(within(dialog).queryByRole("combobox", { name: "类型" })).toBeNull();
    expect(within(dialog).queryByRole("checkbox", { name: /同时生成一把恢复密钥/ })).toBeNull();
  });

  it("保存时提交占位凭据以保留已保存的值，成功后刷新列表", async () => {
    const user = userEvent.setup();
    const api = mockApi({
      "GET /repos": ok({
        list: [
          { ...SYNOLOGY, params: { ...SYNOLOGY.params, host_key: "ssh-ed25519 AAAAC3 nas", password: SECRET_MASKED } },
        ],
      }),
      "GET /servers": ok({ list: [] }),
      "PUT /repos/1": ok({ ...SYNOLOGY, name: "机房群晖" }),
    });
    renderPage(<ReposPage />, { path: "/repos" });
    const dialog = await openEdit(user, "群晖 DS920+");
    const name = within(dialog).getByLabelText("名称");
    await user.clear(name);
    await user.type(name, "机房群晖");
    await user.click(within(dialog).getByRole("button", { name: "保存" }));

    await waitFor(() => expect(api.find("PUT", "/repos/1")).toHaveLength(1));
    expect(api.find("PUT", "/repos/1")[0].body).toMatchObject({
      name: "机房群晖",
      params: {
        host: "192.168.1.20",
        port: 22,
        path: "/volume1/backup/opsnap",
        password: SECRET_MASKED,
        server_id: 0,
      },
    });
    // 已有仓库不再走创建，也不重新生成恢复密钥
    expect(api.find("POST", "/repos")).toHaveLength(0);
    expect(api.find("POST", "/repos/recovery-key")).toHaveLength(0);
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("S3 只改地址时沿用已保存的 Secret Access Key", async () => {
    const user = userEvent.setup();
    const api = mockApi({
      "GET /repos": ok({
        list: [
          {
            ...SYNOLOGY,
            name: "RustFS",
            type: "s3",
            params: {
              ...PARAMS,
              endpoint: "https://rustfs.icodef.com",
              bucket: "opsnap",
              region: "us-east-1",
              access_key_id: "opsnap-9d62217d",
              secret_access_key: SECRET_MASKED,
              path_style: true,
            },
          },
        ],
      }),
      "PUT /repos/1": ok(SYNOLOGY),
    });
    renderPage(<ReposPage />, { path: "/repos" });
    const dialog = await openEdit(user, "RustFS");
    expect(within(dialog).getByLabelText("Secret Access Key")).toHaveValue("");
    const endpoint = within(dialog).getByLabelText("地址");
    await user.clear(endpoint);
    await user.type(endpoint, "http://192.168.8.136:9000");
    await user.click(within(dialog).getByRole("button", { name: "保存" }));

    await waitFor(() => expect(api.find("PUT", "/repos/1")).toHaveLength(1));
    expect(api.find("PUT", "/repos/1")[0].body).toMatchObject({
      params: {
        endpoint: "http://192.168.8.136:9000",
        bucket: "opsnap",
        access_key_id: "opsnap-9d62217d",
        secret_access_key: SECRET_MASKED,
        path_style: true,
      },
    });
  });

  it("保存失败时留在对话框并显示原因", async () => {
    const user = userEvent.setup();
    mockApi({
      "GET /repos": ok({
        list: [{ ...SYNOLOGY, params: { ...SYNOLOGY.params, host_key: "ssh-ed25519 AAAAC3 nas" } }],
      }),
      "GET /servers": ok({ list: [] }),
      "PUT /repos/1": fail(409, 11314, "存储位置正在备份或维护，请稍后再试"),
    });
    renderPage(<ReposPage />, { path: "/repos" });
    const dialog = await openEdit(user, "群晖 DS920+");
    await user.click(within(dialog).getByRole("button", { name: "保存" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("正在备份或维护");
    expect(screen.getByRole("dialog", { name: "修改存储位置" })).toBeInTheDocument();
  });

  it("SFTP 地址未变时沿用已核对的主机指纹，地址一改就要重新核对", async () => {
    const user = userEvent.setup();
    const api = mockApi({
      "GET /repos": ok({
        list: [
          {
            ...SYNOLOGY,
            params: { ...SYNOLOGY.params, host_key: "ssh-ed25519 AAAAC3 nas", password: SECRET_MASKED },
          },
        ],
      }),
      "GET /servers": ok({ list: [] }),
      "PUT /repos/1": ok(SYNOLOGY),
    });
    renderPage(<ReposPage />, { path: "/repos" });
    const dialog = await openEdit(user, "群晖 DS920+");
    expect(within(dialog).getByText("主机指纹已核对")).toBeInTheDocument();
    const save = within(dialog).getByRole("button", { name: "保存" });
    expect(save).toBeEnabled();

    // 地址改了就不再沿用旧指纹
    const host = within(dialog).getByLabelText("主机");
    await user.clear(host);
    await user.type(host, "192.168.1.21");
    expect(within(dialog).queryByText("主机指纹已核对")).toBeNull();
    expect(save).toBeDisabled();

    await user.clear(host);
    await user.type(host, "192.168.1.20");
    // 只改名称、不动地址时提交空 host_key，后端按原值沿用
    await user.click(save);
    await waitFor(() => expect(api.find("PUT", "/repos/1")).toHaveLength(1));
    expect(api.find("PUT", "/repos/1")[0].body).toMatchObject({ params: { host_key: "" } });
  });
});

describe("密钥对话框", () => {
  const KEYS = {
    opsnap_key: { present: true, id: "a1", created_at: NOW - 86400 * 30 },
    recovery_key: { present: true, id: "b2", created_at: NOW - 86400 * 30 },
    no_recovery_key: false,
  };

  it("显示两把密钥的状态；更换 opsnap 密钥调用 rotate", async () => {
    const user = userEvent.setup();
    const api = mockApi({
      "GET /repos": ok({ list: [SYNOLOGY] }),
      "GET /repos/1/keys": ok(KEYS),
      "POST /repos/1/keys/rotate": ok({ ...KEYS, opsnap_key: { ...KEYS.opsnap_key, id: "c3" } }),
    });
    renderPage(<ReposPage />, { path: "/repos" });
    await user.click(await screen.findByRole("button", { name: "密钥" }));
    const dialog = await screen.findByRole("dialog", { name: /群晖 DS920\+ 的密钥/ });
    expect(await within(dialog).findByText("opsnap 密钥")).toBeInTheDocument();
    expect(within(dialog).getByText("恢复密钥")).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "更换" }));
    await waitFor(() => expect(api.find("POST", "/repos/1/keys/rotate")).toHaveLength(1));
  });

  it("没有恢复密钥时：生成后显示密钥，勾选已存进密码管理器才能添加", async () => {
    const user = userEvent.setup();
    const none = { ...KEYS, recovery_key: { present: false, id: "", created_at: 0 }, no_recovery_key: true };
    const api = mockApi({
      "GET /repos": ok({ list: [{ ...SYNOLOGY, no_recovery_key: true, key_count: 1 }] }),
      "GET /repos/1/keys": ok(none),
      "POST /repos/recovery-key": ok({ recovery_key_value: KEY }),
      "POST /repos/1/keys/recovery": ok(KEYS),
    });
    renderPage(<ReposPage />, { path: "/repos" });
    await user.click(await screen.findByRole("button", { name: "密钥" }));
    const dialog = await screen.findByRole("dialog");
    expect(await within(dialog).findByText("没有恢复密钥")).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "生成恢复密钥" }));
    expect(await within(dialog).findByText(KEY)).toBeInTheDocument();
    const add = within(dialog).getByRole("button", { name: "添加密钥" });
    expect(add).toBeDisabled();
    await user.click(within(dialog).getByRole("checkbox", { name: "已存进密码管理器" }));
    await user.click(add);
    await waitFor(() => expect(api.find("POST", "/repos/1/keys/recovery")).toHaveLength(1));
    expect(api.find("POST", "/repos/1/keys/recovery")[0].body).toEqual({
      recovery_key_value: KEY,
      recovery_key_saved: true,
    });
  });

  it("移除恢复密钥要先确认", async () => {
    const user = userEvent.setup();
    const api = mockApi({
      "GET /repos": ok({ list: [SYNOLOGY] }),
      "GET /repos/1/keys": ok(KEYS),
      "DELETE /repos/1/keys/recovery": ok({ ...KEYS, no_recovery_key: true }),
    });
    renderPage(<ReposPage />, { path: "/repos" });
    await user.click(await screen.findByRole("button", { name: "密钥" }));
    await user.click(await screen.findByRole("button", { name: "移除" }));
    expect(api.find("DELETE", "/repos/1/keys/recovery")).toHaveLength(0);
    await user.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "确认移除" }));
    await waitFor(() => expect(api.find("DELETE", "/repos/1/keys/recovery")).toHaveLength(1));
  });
});
