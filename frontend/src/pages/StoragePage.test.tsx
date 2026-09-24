import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

import i18n from "@/i18n";
import type { Storage } from "@/lib/storage";
import { StoragePage } from "@/pages/StoragePage";

const ok = (data: unknown) => new Response(JSON.stringify({ code: 0, msg: "success", data }), { status: 200 });
const fail = (code: number, msg: string, status = 400) => new Response(JSON.stringify({ code, msg }), { status });
const now = () => Math.floor(Date.now() / 1000);

const base: Storage = {
  id: 1,
  name: "本地备份盘",
  kind: "local",
  path: "/var/backups/opsnap",
  endpoint: "",
  region: "",
  bucket: "",
  prefix: "",
  access_key: "",
  has_secret_key: false,
  use_tls: true,
  skip_verify: false,
  location: "/var/backups/opsnap",
  fingerprint: "3F9A···C218",
  encryption: "AES256-GCM-HMAC-SHA256",
  status: "ok",
  status_message: "",
  checked_at: now() - 120,
  created_at: now() - 3600,
};
const minio: Storage = {
  ...base,
  id: 2,
  name: "MinIO 测试",
  kind: "s3",
  path: "",
  endpoint: "minio.lan:9000",
  bucket: "opsnap-backup",
  prefix: "prod/",
  access_key: "AK",
  has_secret_key: true,
  location: "s3://opsnap-backup/prod/",
  fingerprint: "8B21···77E0",
  status: "wrong_key",
  status_message: "保存的密钥无法打开该位置的仓库",
};
const nas: Storage = {
  ...base,
  id: 3,
  name: "旧 NAS",
  path: "/mnt/nas",
  location: "/mnt/nas",
  status: "unreachable",
  status_message: "路径不是目录：/mnt/nas",
};

let fetchMock: ReturnType<typeof vi.fn>;
function respond(...responses: Response[]) {
  for (const r of responses) fetchMock.mockResolvedValueOnce(r);
}
/** 第 i 次请求的地址、方法与请求体 */
function call(i: number) {
  const [url, init] = fetchMock.mock.calls[i] as [string, RequestInit | undefined];
  return { url, method: init?.method ?? "GET", body: init?.body ? JSON.parse(init.body as string) : undefined };
}

beforeAll(() => i18n.changeLanguage("zh-CN"));
beforeEach(() => {
  fetchMock = vi.fn();
  vi.stubGlobal("fetch", fetchMock);
});
afterEach(() => vi.unstubAllGlobals());

/** 下一次请求挂起，直到调用返回的函数给出响应 */
function hold() {
  let finish: (r: Response) => void = () => {};
  fetchMock.mockReturnValueOnce(new Promise<Response>((r) => (finish = r)));
  return (r: Response) => finish(r);
}

function renderPage(entry = "/storage") {
  render(
    <MemoryRouter initialEntries={[entry]}>
      <StoragePage />
    </MemoryRouter>
  );
}

async function openCreate() {
  await userEvent.click((await screen.findAllByRole("button", { name: "新建存储" }))[0]);
  return screen.findByRole("dialog", { name: "新建存储" });
}

async function fillLocal(dialog: HTMLElement, name: string, path: string) {
  await userEvent.type(within(dialog).getByLabelText("名称"), name);
  await userEvent.type(within(dialog).getByLabelText("目录路径"), path);
}

describe("存储页 · 列表", () => {
  it("显示位置、指纹与三种状态；密钥不正确的行提供重新解锁", async () => {
    respond(ok({ items: [base, minio, nas] }));
    renderPage();
    const rows = await screen.findAllByRole("row");
    expect(rows).toHaveLength(4);
    const local = within(rows[1]);
    expect(local.getByText("OpsNap 主机")).toBeInTheDocument();
    expect(local.getByText("3F9A···C218")).toBeInTheDocument();
    expect(local.getByText("正常")).toBeInTheDocument();
    expect(local.getByText("2 分钟前测试")).toBeInTheDocument();

    const s3 = within(rows[2]);
    expect(s3.getByText("s3://opsnap-backup/prod/")).toBeInTheDocument();
    expect(s3.getByText("minio.lan:9000")).toBeInTheDocument();
    expect(s3.getByText("密钥不正确")).toBeInTheDocument();
    expect(s3.getByRole("button", { name: "重新解锁" })).toBeInTheDocument();
    expect(s3.getByRole("button", { name: "测试连接 MinIO 测试" })).toBeInTheDocument();

    const broken = within(rows[3]);
    expect(broken.getByText("无法连接")).toBeInTheDocument();
    expect(broken.getByText("路径不是目录：/mnt/nas")).toBeInTheDocument();
  });

  it("没有存储时显示空状态；加载失败时可重试", async () => {
    respond(fail(10000, "服务器内部错误", 500), ok({ items: [] }));
    renderPage();
    expect(await screen.findByText(/无法加载存储列表/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "重试" }));
    expect(await screen.findByText("还没有存储")).toBeInTheDocument();
  });

  it("测试连接：按钮显示进行中，结果更新到这一行", async () => {
    respond(ok({ items: [base] }));
    renderPage();
    let finish: (r: Response) => void = () => {};
    fetchMock.mockReturnValueOnce(new Promise<Response>((r) => (finish = r)));
    await userEvent.click(await screen.findByRole("button", { name: "测试连接 本地备份盘" }));
    expect(screen.getByRole("button", { name: "测试连接 本地备份盘" })).toBeDisabled();
    finish(
      ok({
        item: { ...base, status: "unreachable", status_message: "目标位置不可写：permission denied" },
        snapshots: 0,
      })
    );
    expect(await screen.findByText("目标位置不可写：permission denied")).toBeInTheDocument();
    expect(call(1)).toMatchObject({ url: "/api/v1/storages/1/test", method: "POST" });
  });

  it("测试另一行时，仍在测试的这一行不能再次提交", async () => {
    respond(ok({ items: [base, nas] }));
    renderPage();
    const finishA = hold();
    await userEvent.click(await screen.findByRole("button", { name: "测试连接 本地备份盘" }));
    const finishB = hold();
    await userEvent.click(screen.getByRole("button", { name: "测试连接 旧 NAS" }));
    expect(screen.getByRole("button", { name: "测试连接 本地备份盘" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "测试连接 旧 NAS" })).toBeDisabled();
    finishB(ok({ item: nas, snapshots: 0 }));
    finishA(ok({ item: base, snapshots: 0 }));
    expect(await screen.findByRole("button", { name: "测试连接 本地备份盘" })).toBeEnabled();
  });
});

describe("存储页 · 提交进行中不能关闭对话框", () => {
  it("新建：测试连接后进入下一步期间", async () => {
    respond(ok({ items: [] }));
    renderPage();
    const dialog = await openCreate();
    await fillLocal(dialog, "a", "/data/repo");
    const finish = hold();
    await userEvent.click(within(dialog).getByRole("button", { name: "下一步" }));
    expect(within(dialog).getByRole("button", { name: "取消" })).toBeDisabled();
    await userEvent.keyboard("{Escape}");
    expect(screen.getByRole("dialog", { name: "新建存储" })).toBeInTheDocument();
    finish(ok({ state: "not_empty", created_at: 0, location: "/data/repo", location_changed: false }));
    expect(await within(dialog).findByRole("alert")).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "取消" })).toBeEnabled();
  });

  it("设置密钥：启用加密期间", async () => {
    respond(ok({ items: [] }));
    renderPage();
    const dialog = await openCreate();
    await fillLocal(dialog, "a", "/data/new");
    respond(
      ok({ state: "empty", created_at: 0, location: "/data/new", location_changed: false }),
      ok({ key: "Q7nT-4mK2-9ZxP-1bR8-VcE5-3jHw", fingerprint: "3F9A···C218", encryption: "AES256-GCM-HMAC-SHA256" })
    );
    await userEvent.click(within(dialog).getByRole("button", { name: "下一步" }));
    const setKey = await screen.findByRole("dialog", { name: "设置加密密钥" });
    await within(setKey).findByText("Q7nT-4mK2-9ZxP-1bR8-VcE5-3jHw");
    await userEvent.click(within(setKey).getByRole("checkbox", { name: /我已保存这把密钥/ }));
    hold();
    await userEvent.click(within(setKey).getByRole("button", { name: "启用加密" }));
    expect(within(setKey).getByRole("button", { name: "取消" })).toBeDisabled();
    await userEvent.keyboard("{Escape}");
    expect(screen.getByRole("dialog", { name: "设置加密密钥" })).toBeInTheDocument();
  });

  it("解锁：解锁期间；否则迟到的结果会出现在下一次解锁中", async () => {
    respond(ok({ items: [] }));
    renderPage();
    const dialog = await openCreate();
    await fillLocal(dialog, "a", "/data/repo");
    respond(ok({ state: "repository", created_at: 0, location: "/data/repo", location_changed: false }));
    await userEvent.click(within(dialog).getByRole("button", { name: "下一步" }));
    const unlock = await screen.findByRole("dialog", { name: "解锁已有仓库" });
    await userEvent.type(within(unlock).getByLabelText("密钥"), "some-key");
    hold();
    await userEvent.click(within(unlock).getByRole("button", { name: "解锁并继续" }));
    expect(within(unlock).getByRole("button", { name: "取消" })).toBeDisabled();
    await userEvent.keyboard("{Escape}");
    expect(screen.getByRole("dialog", { name: "解锁已有仓库" })).toBeInTheDocument();
  });

  it("删除：删除期间", async () => {
    respond(ok({ items: [base] }));
    renderPage();
    await userEvent.click(await screen.findByRole("button", { name: "本地备份盘 的更多操作" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "删除存储" }));
    const confirm = await screen.findByRole("dialog", { name: "删除存储「本地备份盘」？" });
    hold();
    await userEvent.click(within(confirm).getByRole("button", { name: "删除存储" }));
    expect(within(confirm).getByRole("button", { name: "取消" })).toBeDisabled();
    await userEvent.keyboard("{Escape}");
    expect(screen.getByRole("dialog", { name: "删除存储「本地备份盘」？" })).toBeInTheDocument();
  });

  it("选择目录：新建文件夹期间；否则下次打开会停在新建的文件夹", async () => {
    respond(ok({ items: [] }));
    renderPage();
    const dialog = await openCreate();
    respond(ok({ path: "/srv", parent: "/", dirs: [] }));
    await userEvent.click(within(dialog).getByRole("button", { name: "浏览…" }));
    const picker = await screen.findByRole("dialog", { name: "选择目录" });
    await within(picker).findByText("这里没有子目录");
    await userEvent.type(within(picker).getByLabelText("新文件夹名称"), "opsnap");
    hold();
    await userEvent.click(within(picker).getByRole("button", { name: "新建文件夹" }));
    expect(within(picker).getByRole("button", { name: "取消" })).toBeDisabled();
    await userEvent.keyboard("{Escape}");
    expect(screen.getByRole("dialog", { name: "选择目录" })).toBeInTheDocument();
  });
});

describe("存储页 · 新建", () => {
  it("空位置：测试连接后设置系统生成的密钥，勾选确认才能启用加密", async () => {
    respond(ok({ items: [] }));
    renderPage();
    const dialog = await openCreate();
    await fillLocal(dialog, "本地备份盘", "/var/backups/opsnap");
    respond(ok({ state: "empty", created_at: 0, location: "/var/backups/opsnap", location_changed: false }));
    await userEvent.click(within(dialog).getByRole("button", { name: "测试连接" }));
    expect(await within(dialog).findByText(/目标位置为空/)).toBeInTheDocument();
    expect(call(1).body).toMatchObject({
      name: "本地备份盘",
      location: { kind: "local", path: "/var/backups/opsnap" },
    });

    respond(
      ok({ key: "Q7nT-4mK2-9ZxP-1bR8-VcE5-3jHw", fingerprint: "3F9A···C218", encryption: "AES256-GCM-HMAC-SHA256" })
    );
    await userEvent.click(within(dialog).getByRole("button", { name: /下一步：设置加密密钥/ }));
    const setKey = await screen.findByRole("dialog", { name: "设置加密密钥" });
    expect(await within(setKey).findByText("Q7nT-4mK2-9ZxP-1bR8-VcE5-3jHw")).toBeInTheDocument();
    expect(within(setKey).getByText("指纹 3F9A···C218 · AES256-GCM-HMAC-SHA256")).toBeInTheDocument();
    const enable = within(setKey).getByRole("button", { name: "启用加密" });
    expect(enable).toBeDisabled();

    await userEvent.click(within(setKey).getByRole("checkbox", { name: /我已保存这把密钥/ }));
    respond(ok({ item: base, snapshots: 0 }), ok({ items: [base] }));
    await userEvent.click(enable);
    expect(await screen.findByText("3F9A···C218")).toBeInTheDocument();
    expect(call(3)).toMatchObject({
      url: "/api/v1/storages",
      method: "POST",
      body: { name: "本地备份盘", key: "Q7nT-4mK2-9ZxP-1bR8-VcE5-3jHw", confirm_saved: true },
    });
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("启用加密时位置在测试之后已变成仓库：不覆盖，重新判断后进入解锁", async () => {
    respond(ok({ items: [] }));
    renderPage();
    const dialog = await openCreate();
    await fillLocal(dialog, "a", "/data/a");
    respond(
      ok({ state: "empty", created_at: 0, location: "/data/a", location_changed: false }),
      ok({ key: "Q7nT-4mK2-9ZxP-1bR8-VcE5-3jHw", fingerprint: "3F9A···C218", encryption: "AES256-GCM-HMAC-SHA256" })
    );
    await userEvent.click(within(dialog).getByRole("button", { name: "下一步" }));
    const setKey = await screen.findByRole("dialog", { name: "设置加密密钥" });
    await within(setKey).findByText("Q7nT-4mK2-9ZxP-1bR8-VcE5-3jHw");
    await userEvent.click(within(setKey).getByRole("checkbox", { name: /我已保存这把密钥/ }));
    respond(
      fail(10421, "目标位置在测试之后已变成 kopia 仓库，没有覆盖它；请重新测试连接"),
      ok({ state: "repository", created_at: now() - 86400, location: "/data/a", location_changed: false })
    );
    await userEvent.click(within(setKey).getByRole("button", { name: "启用加密" }));
    const unlock = await screen.findByRole("dialog", { name: "解锁已有仓库" });
    expect(unlock).toHaveTextContent("创建于");
    expect(screen.queryByRole("dialog", { name: "设置加密密钥" })).not.toBeInTheDocument();
    expect(call(4)).toMatchObject({ url: "/api/v1/storages/probe", method: "POST", body: { name: "a" } });

    await userEvent.type(within(unlock).getByLabelText("密钥"), "their-key");
    respond(ok({ item: base, snapshots: 2 }));
    await userEvent.click(within(unlock).getByRole("button", { name: "解锁并继续" }));
    expect(await within(unlock).findByText("已解锁，仓库中有 2 个快照。")).toBeInTheDocument();
    expect(call(5)).toMatchObject({ url: "/api/v1/storages", method: "POST", body: { key: "their-key" } });
  });

  it("自设密码：至少 12 个字符且两次一致", async () => {
    respond(ok({ items: [] }));
    renderPage();
    const dialog = await openCreate();
    await fillLocal(dialog, "a", "/data/a");
    respond(
      ok({ state: "empty", created_at: 0, location: "/data/a", location_changed: false }),
      ok({ key: "Q7nT-4mK2-9ZxP-1bR8-VcE5-3jHw", fingerprint: "3F9A···C218", encryption: "AES256-GCM-HMAC-SHA256" })
    );
    await userEvent.click(within(dialog).getByRole("button", { name: "下一步" }));
    const setKey = await screen.findByRole("dialog", { name: "设置加密密钥" });
    await userEvent.click(within(setKey).getByRole("radio", { name: "我自己设密码" }));
    await userEvent.type(within(setKey).getByLabelText("密码"), "short");
    await userEvent.click(within(setKey).getByRole("checkbox"));
    expect(within(setKey).getByRole("button", { name: "启用加密" })).toBeDisabled();

    await userEvent.type(within(setKey).getByLabelText("密码"), "-but-long-enough");
    await userEvent.type(within(setKey).getByLabelText("再次输入密码"), "different");
    expect(within(setKey).getByText("两次输入不一致")).toBeInTheDocument();
    expect(within(setKey).getByRole("button", { name: "启用加密" })).toBeDisabled();

    await userEvent.clear(within(setKey).getByLabelText("再次输入密码"));
    await userEvent.type(within(setKey).getByLabelText("再次输入密码"), "short-but-long-enough");
    expect(within(setKey).getByRole("button", { name: "启用加密" })).toBeEnabled();
  });

  it("不为空也不是仓库：显示拒绝原因，不进入下一步", async () => {
    respond(ok({ items: [] }));
    renderPage();
    const dialog = await openCreate();
    await fillLocal(dialog, "a", "/srv/data/shared");
    respond(ok({ state: "not_empty", created_at: 0, location: "/srv/data/shared", location_changed: false }));
    await userEvent.click(within(dialog).getByRole("button", { name: "下一步" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("目标位置不为空，且不是 kopia 仓库");
    expect(screen.queryByRole("dialog", { name: "设置加密密钥" })).not.toBeInTheDocument();
  });

  it("重名与相对路径显示在对应字段旁；无法连接显示在表单中", async () => {
    respond(ok({ items: [] }));
    renderPage();
    const dialog = await openCreate();
    await fillLocal(dialog, "a", "data/a");
    respond(fail(10403, "本地目录必须是绝对路径"));
    await userEvent.click(within(dialog).getByRole("button", { name: "测试连接" }));
    expect(await within(dialog).findByText("本地目录必须是绝对路径")).toBeInTheDocument();
    expect(within(dialog).getByLabelText("目录路径")).toHaveAttribute("aria-invalid", "true");

    respond(fail(10402, "已有同名的存储"));
    await userEvent.click(within(dialog).getByRole("button", { name: "测试连接" }));
    expect(await within(dialog).findByText("已有同名的存储")).toBeInTheDocument();
    expect(within(dialog).getByLabelText("名称")).toHaveAttribute("aria-invalid", "true");

    await userEvent.click(within(dialog).getByRole("radio", { name: /S3 兼容存储/ }));
    expect(within(dialog).getByText("最终位置：s3:///")).toBeInTheDocument();
    await userEvent.type(within(dialog).getByLabelText("Bucket"), "opsnap");
    await userEvent.type(within(dialog).getByLabelText(/路径前缀/), "/prod/");
    expect(within(dialog).getByText("最终位置：s3://opsnap/prod/")).toBeInTheDocument();
    expect(within(dialog).getByRole("switch", { name: "使用 HTTPS" })).toHaveAttribute("aria-checked", "true");
    respond(fail(10412, "Bucket 不存在"));
    await userEvent.click(within(dialog).getByRole("button", { name: "测试连接" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("Bucket 不存在");
  });

  it("已有仓库：解锁，失败次数累计，成功后显示快照数", async () => {
    respond(ok({ items: [] }));
    renderPage();
    const dialog = await openCreate();
    await fillLocal(dialog, "a", "/data/repo");
    respond(ok({ state: "repository", created_at: now() - 86400, location: "/data/repo", location_changed: false }));
    await userEvent.click(within(dialog).getByRole("button", { name: "下一步" }));
    const unlock = await screen.findByRole("dialog", { name: "解锁已有仓库" });
    expect(unlock).toHaveTextContent("/data/repo");
    expect(unlock).toHaveTextContent("创建于");
    expect(unlock).not.toHaveTextContent(/\d+ 个快照/); // 解锁前读不到快照数

    const input = within(unlock).getByLabelText("密钥");
    await userEvent.type(input, "wrong-key");
    respond(fail(10417, "密钥不正确，无法解开这个仓库"), fail(10417, "密钥不正确，无法解开这个仓库"));
    await userEvent.click(within(unlock).getByRole("button", { name: "解锁并继续" }));
    expect(await within(unlock).findByText("密钥不正确，无法解开这个仓库。已连续 1 次失败。")).toBeInTheDocument();
    await userEvent.click(within(unlock).getByRole("button", { name: "解锁并继续" }));
    expect(await within(unlock).findByText("密钥不正确，无法解开这个仓库。已连续 2 次失败。")).toBeInTheDocument();

    await userEvent.clear(input);
    await userEvent.type(input, "  right-key ");
    respond(ok({ item: base, snapshots: 3 }));
    await userEvent.click(within(unlock).getByRole("button", { name: "解锁并继续" }));
    expect(await within(unlock).findByText("已解锁，仓库中有 3 个快照。")).toBeInTheDocument();
    expect(call(4).body).toMatchObject({ key: "right-key", confirm_saved: false });

    respond(ok({ items: [base] }));
    await userEvent.click(within(unlock).getByRole("button", { name: "完成" }));
    expect(await screen.findByText("3F9A···C218")).toBeInTheDocument();
  });

  it("失败次数只在关闭对话框时清零，切换密钥来源不清零", async () => {
    respond(ok({ items: [] }));
    renderPage();
    const dialog = await openCreate();
    await fillLocal(dialog, "a", "/data/repo");
    respond(ok({ state: "repository", created_at: 0, location: "/data/repo", location_changed: false }));
    await userEvent.click(within(dialog).getByRole("button", { name: "下一步" }));
    const unlock = await screen.findByRole("dialog", { name: "解锁已有仓库" });
    await userEvent.type(within(unlock).getByLabelText("密钥"), "wrong-key");
    respond(fail(10417, "密钥不正确，无法解开这个仓库"));
    await userEvent.click(within(unlock).getByRole("button", { name: "解锁并继续" }));
    await within(unlock).findByText("密钥不正确，无法解开这个仓库。已连续 1 次失败。");

    await userEvent.click(within(unlock).getByRole("radio", { name: "上传密钥文件" }));
    const file = new File(["other-key-5678\n"], "key.txt", { type: "text/plain" });
    await userEvent.upload(within(unlock).getByLabelText("上传密钥文件"), file);
    await within(unlock).findByText("已读取 key.txt");
    respond(fail(10417, "密钥不正确，无法解开这个仓库"));
    await userEvent.click(within(unlock).getByRole("button", { name: "解锁并继续" }));
    expect(await within(unlock).findByText("密钥不正确，无法解开这个仓库。已连续 2 次失败。")).toBeInTheDocument();
    await userEvent.click(within(unlock).getByRole("radio", { name: "粘贴密钥" }));
    expect(within(unlock).getByText("密钥不正确，无法解开这个仓库。已连续 2 次失败。")).toBeInTheDocument();
  });

  it("已有仓库：上传 OpsNap 导出的密钥文件", async () => {
    respond(ok({ items: [] }));
    renderPage();
    const dialog = await openCreate();
    await fillLocal(dialog, "a", "/data/repo");
    respond(ok({ state: "repository", created_at: 0, location: "/data/repo", location_changed: false }));
    await userEvent.click(within(dialog).getByRole("button", { name: "下一步" }));
    const unlock = await screen.findByRole("dialog", { name: "解锁已有仓库" });
    await userEvent.click(within(unlock).getByRole("radio", { name: "上传密钥文件" }));
    const file = new File(["# OpsNap\n\nKey: file-key-1234\n\n# cmd\n"], "opsnap-a-key.txt", { type: "text/plain" });
    await userEvent.upload(within(unlock).getByLabelText("上传密钥文件"), file);
    expect(await within(unlock).findByText("已读取 opsnap-a-key.txt")).toBeInTheDocument();
    respond(ok({ item: base, snapshots: 0 }));
    await userEvent.click(within(unlock).getByRole("button", { name: "解锁并继续" }));
    expect(await within(unlock).findByText("已解锁，仓库中有 0 个快照。")).toBeInTheDocument();
    expect(call(2).body).toMatchObject({ key: "file-key-1234" });
  });
});

describe("存储页 · 编辑与删除", () => {
  it("位置没变：直接保存并更新这一行；S3 的 Secret Key 留空表示不修改", async () => {
    respond(ok({ items: [minio] }));
    renderPage();
    await userEvent.click(await screen.findByRole("button", { name: "编辑 MinIO 测试" }));
    const dialog = await screen.findByRole("dialog", { name: "编辑存储" });
    expect(within(dialog).getByLabelText("Secret Key")).toHaveAttribute(
      "placeholder",
      "已保存（加密存储），留空表示不修改"
    );
    await userEvent.clear(within(dialog).getByLabelText("名称"));
    await userEvent.type(within(dialog).getByLabelText("名称"), "MinIO 生产");
    respond(
      ok({ state: "repository", created_at: 0, location: minio.location, location_changed: false }),
      ok({ item: { ...minio, name: "MinIO 生产", status: "ok" }, snapshots: 5 })
    );
    await userEvent.click(within(dialog).getByRole("button", { name: "保存" }));
    expect(await screen.findByText("MinIO 生产")).toBeInTheDocument();
    expect(call(1).body).toMatchObject({ id: 2, location: { secret_key: "", bucket: "opsnap-backup" } });
    expect(call(2)).toMatchObject({
      url: "/api/v1/storages/2",
      method: "PUT",
      body: { confirm_location_change: false },
    });
  });

  it("位置变了：先确认，新位置为空时用当前密钥保存", async () => {
    respond(ok({ items: [base] }));
    renderPage();
    await userEvent.click(await screen.findByRole("button", { name: "编辑 本地备份盘" }));
    const dialog = await screen.findByRole("dialog", { name: "编辑存储" });
    await userEvent.clear(within(dialog).getByLabelText("目录路径"));
    await userEvent.type(within(dialog).getByLabelText("目录路径"), "/data/new");
    respond(ok({ state: "empty", created_at: 0, location: "/data/new", location_changed: true }));
    await userEvent.click(within(dialog).getByRole("button", { name: "保存" }));

    const confirm = await screen.findByRole("dialog", { name: "更改存储位置？" });
    expect(confirm).toHaveTextContent("位置从 /var/backups/opsnap 改为 /data/new");
    expect(confirm).toHaveTextContent("原位置 /var/backups/opsnap 的仓库保持不动");
    respond(
      ok({ item: { ...base, location: "/data/new" }, snapshots: 0 }),
      ok({ items: [{ ...base, location: "/data/new" }] })
    );
    await userEvent.click(within(confirm).getByRole("button", { name: "确认并继续" }));
    expect(await screen.findByText("/data/new")).toBeInTheDocument();
    expect(call(2)).toMatchObject({
      method: "PUT",
      body: { confirm_location_change: true, location: { path: "/data/new" } },
    });
  });

  it("位置变了：保存时确认按钮显示加载状态，失败时在确认框中显示原因", async () => {
    respond(ok({ items: [base] }));
    renderPage();
    await userEvent.click(await screen.findByRole("button", { name: "编辑 本地备份盘" }));
    const dialog = await screen.findByRole("dialog", { name: "编辑存储" });
    await userEvent.clear(within(dialog).getByLabelText("目录路径"));
    await userEvent.type(within(dialog).getByLabelText("目录路径"), "/data/new");
    respond(ok({ state: "empty", created_at: 0, location: "/data/new", location_changed: true }));
    await userEvent.click(within(dialog).getByRole("button", { name: "保存" }));
    const confirm = await screen.findByRole("dialog", { name: "更改存储位置？" });
    let finish: (r: Response) => void = () => {};
    fetchMock.mockReturnValueOnce(new Promise<Response>((r) => (finish = r)));
    await userEvent.click(within(confirm).getByRole("button", { name: "确认并继续" }));
    expect(within(confirm).getByRole("button", { name: "提交中…" })).toBeDisabled();
    finish(fail(10407, "目标位置不为空，且不是 kopia 仓库"));
    expect(await within(confirm).findByRole("alert")).toHaveTextContent("目标位置不为空，且不是 kopia 仓库");
    expect(fetchMock).toHaveBeenCalledTimes(3);
  });

  it("位置变了、新位置在测试之后已变成仓库：不覆盖，重新判断后进入解锁", async () => {
    respond(ok({ items: [base] }));
    renderPage();
    await userEvent.click(await screen.findByRole("button", { name: "编辑 本地备份盘" }));
    const dialog = await screen.findByRole("dialog", { name: "编辑存储" });
    await userEvent.clear(within(dialog).getByLabelText("目录路径"));
    await userEvent.type(within(dialog).getByLabelText("目录路径"), "/data/new");
    respond(ok({ state: "empty", created_at: 0, location: "/data/new", location_changed: true }));
    await userEvent.click(within(dialog).getByRole("button", { name: "保存" }));
    const confirm = await screen.findByRole("dialog", { name: "更改存储位置？" });
    respond(
      fail(10421, "目标位置在测试之后已变成 kopia 仓库，没有覆盖它；请重新测试连接"),
      ok({ state: "repository", created_at: 0, location: "/data/new", location_changed: true })
    );
    await userEvent.click(within(confirm).getByRole("button", { name: "确认并继续" }));
    const unlock = await screen.findByRole("dialog", { name: "解锁已有仓库" });
    expect(unlock).toHaveTextContent("/data/new");
    expect(call(3)).toMatchObject({ url: "/api/v1/storages/probe", body: { id: 1 } });
    await userEvent.type(within(unlock).getByLabelText("密钥"), "other-key");
    respond(ok({ item: base, snapshots: 1 }));
    await userEvent.click(within(unlock).getByRole("button", { name: "解锁并继续" }));
    await within(unlock).findByText("已解锁，仓库中有 1 个快照。");
    expect(call(4)).toMatchObject({ method: "PUT", body: { key: "other-key", confirm_location_change: true } });
  });

  it("位置变了且新位置是仓库：确认后解锁", async () => {
    respond(ok({ items: [base] }));
    renderPage();
    await userEvent.click(await screen.findByRole("button", { name: "编辑 本地备份盘" }));
    const dialog = await screen.findByRole("dialog", { name: "编辑存储" });
    await userEvent.clear(within(dialog).getByLabelText("目录路径"));
    await userEvent.type(within(dialog).getByLabelText("目录路径"), "/data/other");
    respond(ok({ state: "repository", created_at: 0, location: "/data/other", location_changed: true }));
    await userEvent.click(within(dialog).getByRole("button", { name: "保存" }));
    await userEvent.click(
      within(await screen.findByRole("dialog", { name: "更改存储位置？" })).getByRole("button", { name: "确认并继续" })
    );
    const unlock = await screen.findByRole("dialog", { name: "解锁已有仓库" });
    await userEvent.type(within(unlock).getByLabelText("密钥"), "other-key");
    respond(ok({ item: base, snapshots: 1 }));
    await userEvent.click(within(unlock).getByRole("button", { name: "解锁并继续" }));
    expect(await within(unlock).findByText("已解锁，仓库中有 1 个快照。")).toBeInTheDocument();
    expect(call(2)).toMatchObject({ method: "PUT", body: { key: "other-key", confirm_location_change: true } });
  });

  it("重新解锁：显示仓库创建时间，成功后改用新密钥", async () => {
    respond(ok({ items: [minio] }));
    renderPage();
    respond(ok({ state: "repository", created_at: now() - 86400, location: minio.location, location_changed: false }));
    await userEvent.click(await screen.findByRole("button", { name: "重新解锁" }));
    const unlock = await screen.findByRole("dialog", { name: "解锁已有仓库" });
    expect(unlock).toHaveTextContent("s3://opsnap-backup/prod/");
    expect(unlock).toHaveTextContent("创建于");
    expect(call(1)).toMatchObject({
      url: "/api/v1/storages/probe",
      body: { id: 2, name: "MinIO 测试", location: { bucket: "opsnap-backup", secret_key: "" } },
    });
    await userEvent.type(within(unlock).getByLabelText("密钥"), "new-key");
    respond(ok({ item: { ...minio, status: "ok" }, snapshots: 7 }));
    await userEvent.click(within(unlock).getByRole("button", { name: "解锁并继续" }));
    expect(await within(unlock).findByText("已解锁，仓库中有 7 个快照。")).toBeInTheDocument();
    expect(call(2)).toMatchObject({ url: "/api/v1/storages/2/unlock", body: { key: "new-key" } });
  });

  it("删除：说明数据保留，确认后从列表移除", async () => {
    respond(ok({ items: [base] }));
    renderPage();
    await userEvent.click(await screen.findByRole("button", { name: "本地备份盘 的更多操作" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "删除存储" }));
    const confirm = await screen.findByRole("dialog", { name: "删除存储「本地备份盘」？" });
    expect(confirm).toHaveTextContent("存储里的备份数据不会被删除");
    respond(ok({}), ok({ items: [] }));
    await userEvent.click(within(confirm).getByRole("button", { name: "删除存储" }));
    expect(await screen.findByText("还没有存储")).toBeInTheDocument();
    expect(call(1)).toMatchObject({ url: "/api/v1/storages/1", method: "DELETE" });
  });
});

describe("存储页 · 选择目录", () => {
  const listing = {
    path: "/srv",
    parent: "/",
    dirs: [
      { name: "backups", path: "/srv/backups", status: "not_empty" },
      { name: "locked", path: "/srv/locked", status: "no_access" },
      { name: "repo", path: "/srv/repo", status: "repository" },
    ],
  };

  it("从输入框的路径开始浏览，进入目录、新建文件夹并选择它", async () => {
    respond(ok({ items: [] }));
    renderPage();
    const dialog = await openCreate();
    await userEvent.type(within(dialog).getByLabelText("目录路径"), "/srv");
    respond(ok(listing));
    await userEvent.click(within(dialog).getByRole("button", { name: "浏览…" }));
    const picker = await screen.findByRole("dialog", { name: "选择目录" });
    expect(call(1).url).toBe("/api/v1/storages/dirs?path=%2Fsrv");
    expect(await within(picker).findByRole("button", { name: /backups\s*非空/ })).toBeInTheDocument();
    expect(within(picker).getByRole("button", { name: /repo\s*kopia 仓库/ })).toBeInTheDocument();
    expect(within(picker).getByRole("button", { name: /locked\s*无权访问/ })).toBeDisabled();

    respond(ok({ path: "/srv/backups", parent: "/srv", dirs: [] }));
    await userEvent.click(within(picker).getByRole("button", { name: /backups/ }));
    expect(await within(picker).findByText("这里没有子目录")).toBeInTheDocument();
    expect(within(picker).getByRole("button", { name: "backups" })).toHaveAttribute("aria-current", "location");

    respond(fail(10424, "已有同名的文件夹"));
    await userEvent.type(within(picker).getByLabelText("新文件夹名称"), "opsnap");
    await userEvent.click(within(picker).getByRole("button", { name: "新建文件夹" }));
    expect(await within(picker).findByRole("alert")).toHaveTextContent("已有同名的文件夹");

    respond(ok({ path: "/srv/backups/opsnap" }), ok({ path: "/srv/backups/opsnap", parent: "/srv/backups", dirs: [] }));
    await userEvent.click(within(picker).getByRole("button", { name: "新建文件夹" }));
    expect(await within(picker).findByRole("button", { name: "opsnap" })).toHaveAttribute("aria-current", "location");
    expect(call(4)).toMatchObject({ method: "POST", body: { parent: "/srv/backups", name: "opsnap" } });

    await userEvent.click(within(picker).getByRole("button", { name: "选择此目录" }));
    expect(screen.queryByRole("dialog", { name: "选择目录" })).not.toBeInTheDocument();
    expect(within(dialog).getByLabelText("目录路径")).toHaveValue("/srv/backups/opsnap");
  });

  it("上一级回到父目录；根目录没有上一级", async () => {
    respond(ok({ items: [] }));
    renderPage();
    const dialog = await openCreate();
    respond(ok(listing));
    await userEvent.click(within(dialog).getByRole("button", { name: "浏览…" }));
    const picker = await screen.findByRole("dialog", { name: "选择目录" });
    expect(call(1).url).toBe("/api/v1/storages/dirs?path=");
    respond(ok({ path: "/", parent: "", dirs: [{ name: "srv", path: "/srv", status: "not_empty" }] }));
    await userEvent.click(await within(picker).findByRole("button", { name: "上一级" }));
    expect(await within(picker).findByRole("button", { name: /srv\s*非空/ })).toBeInTheDocument();
    expect(within(picker).getByRole("button", { name: "上一级" })).toBeDisabled();
  });
});

describe("存储页 · 查看密钥", () => {
  const status = (password_login: boolean) => ({
    initialized: true,
    password_login,
    oidc_login: { display_name: "Keycloak" },
  });

  it("密码登录开启：密码错误提示，正确后显示密钥、指纹与 kopia 命令；关闭即丢弃", async () => {
    respond(ok({ items: [base] }));
    renderPage();
    await userEvent.click(await screen.findByRole("button", { name: "本地备份盘 的更多操作" }));
    respond(ok(status(true)));
    await userEvent.click(await screen.findByRole("menuitem", { name: "查看密钥" }));
    const dialog = await screen.findByRole("dialog", { name: "查看「本地备份盘」的密钥" });

    const input = await within(dialog).findByLabelText("当前登录密码");
    await userEvent.type(input, "wrong");
    respond(fail(10110, "登录密码不正确"));
    await userEvent.click(within(dialog).getByRole("button", { name: "验证并查看" }));
    expect(await within(dialog).findByText("登录密码不正确")).toBeInTheDocument();
    expect(input).toHaveAttribute("aria-invalid", "true");

    await userEvent.clear(input);
    await userEvent.type(input, "correct-horse-battery");
    respond(ok({ key: "Q7nT-4mK2-9ZxP-1bR8-VcE5-3jHw", fingerprint: "3F9A···C218" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "验证并查看" }));
    const shown = await screen.findByRole("dialog", { name: "「本地备份盘」的仓库密钥" });
    expect(within(shown).getByText("Q7nT-4mK2-9ZxP-1bR8-VcE5-3jHw")).toBeInTheDocument();
    expect(within(shown).getByText("指纹 3F9A···C218 · AES256-GCM-HMAC-SHA256")).toBeInTheDocument();
    expect(
      within(shown).getByText("kopia repository connect filesystem --path /var/backups/opsnap")
    ).toBeInTheDocument();
    expect(call(3)).toMatchObject({ url: "/api/v1/storages/1/reveal", body: { password: "correct-horse-battery" } });

    await userEvent.click(within(shown).getByRole("button", { name: "完成" }));
    expect(screen.queryByText("Q7nT-4mK2-9ZxP-1bR8-VcE5-3jHw")).not.toBeInTheDocument();
  });

  it("密码登录关闭：跳转 IdP 重新验证，next 带上存储与意图", async () => {
    const assign = vi.fn();
    vi.stubGlobal("location", { ...window.location, assign });
    respond(ok({ items: [base] }));
    renderPage();
    await userEvent.click(await screen.findByRole("button", { name: "本地备份盘 的更多操作" }));
    respond(ok(status(false)));
    await userEvent.click(await screen.findByRole("menuitem", { name: "下载密钥文件" }));
    const dialog = await screen.findByRole("dialog", { name: "查看「本地备份盘」的密钥" });
    expect(within(dialog).queryByLabelText("当前登录密码")).not.toBeInTheDocument();
    await userEvent.click(await within(dialog).findByRole("button", { name: "使用 Keycloak 重新验证" }));
    expect(assign).toHaveBeenCalledWith(
      "/api/v1/auth/oidc/reauth?next=" + encodeURIComponent("/storage?reveal=1&intent=download")
    );
  });

  it("从 IdP 回来：自动查看，意图为下载时直接下载密钥文件", async () => {
    const createObjectURL = vi.fn(() => "blob:key");
    vi.stubGlobal("URL", Object.assign(URL, { createObjectURL, revokeObjectURL: vi.fn() }));
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
    respond(ok({ items: [base] }), ok({ key: "Q7nT-4mK2-9ZxP-1bR8-VcE5-3jHw", fingerprint: "3F9A···C218" }));
    renderPage("/storage?reveal=1&intent=download");
    expect(await screen.findByText("Q7nT-4mK2-9ZxP-1bR8-VcE5-3jHw")).toBeInTheDocument();
    expect(call(1)).toMatchObject({ url: "/api/v1/storages/1/reveal", body: { password: "" } });
    expect(click).toHaveBeenCalledTimes(1);
    const anchor = click.mock.contexts[0] as HTMLAnchorElement;
    expect(anchor.download).toBe("opsnap-本地备份盘-key.txt");
    const blob = (createObjectURL.mock.calls[0] as unknown as [Blob])[0];
    expect(await blob.text()).toContain("Key: Q7nT-4mK2-9ZxP-1bR8-VcE5-3jHw");
    click.mockRestore();
  });

  it("重新验证失败回到页面时显示原因", async () => {
    respond(ok({ items: [base] }));
    renderPage("/storage?oidc_error=not_bound");
    expect(await screen.findByRole("alert")).toHaveTextContent("此 OIDC 账号未绑定到 OpsNap");
  });

  it("关闭后迟到的验证结果不会显示在另一个存储的对话框中", async () => {
    respond(ok({ items: [base, nas] }));
    renderPage();
    await userEvent.click(await screen.findByRole("button", { name: "本地备份盘 的更多操作" }));
    respond(ok(status(true)));
    await userEvent.click(await screen.findByRole("menuitem", { name: "查看密钥" }));
    const first = await screen.findByRole("dialog", { name: "查看「本地备份盘」的密钥" });
    await userEvent.type(await within(first).findByLabelText("当前登录密码"), "correct-horse-battery");
    const finish = hold();
    await userEvent.click(within(first).getByRole("button", { name: "验证并查看" }));
    await userEvent.click(within(first).getByRole("button", { name: "取消" }));

    await userEvent.click(screen.getByRole("button", { name: "旧 NAS 的更多操作" }));
    respond(ok(status(true)));
    await userEvent.click(await screen.findByRole("menuitem", { name: "查看密钥" }));
    const second = await screen.findByRole("dialog", { name: "查看「旧 NAS」的密钥" });
    await within(second).findByLabelText("当前登录密码");
    finish(ok({ key: "Q7nT-4mK2-9ZxP-1bR8-VcE5-3jHw", fingerprint: "3F9A···C218" }));
    await new Promise((r) => setTimeout(r, 50));
    expect(screen.queryByText("Q7nT-4mK2-9ZxP-1bR8-VcE5-3jHw")).not.toBeInTheDocument();
    expect(screen.getByRole("dialog", { name: "查看「旧 NAS」的密钥" })).toBeInTheDocument();
  });

  it("删除确认中可以先下载密钥文件（需要验证身份）", async () => {
    respond(ok({ items: [base] }));
    renderPage();
    await userEvent.click(await screen.findByRole("button", { name: "本地备份盘 的更多操作" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "删除存储" }));
    const confirm = await screen.findByRole("dialog", { name: "删除存储「本地备份盘」？" });
    expect(confirm).toHaveTextContent("需要验证身份");
    respond(ok(status(true)));
    await userEvent.click(within(confirm).getByRole("button", { name: "先下载密钥文件" }));
    expect(await screen.findByRole("dialog", { name: "查看「本地备份盘」的密钥" })).toBeInTheDocument();
  });
});
