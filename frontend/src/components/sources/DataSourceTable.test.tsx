import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { beforeAll, describe, expect, it, vi } from "vitest";

import { DataSourceTable, type DataSourceRowActions } from "@/components/sources/DataSourceTable";
import i18n from "@/i18n";
import type { DataSourceItem } from "@/lib/sources";

const now = () => Math.floor(Date.now() / 1000);

const base: DataSourceItem = {
  id: 1,
  name: "orders-db",
  kind: "mysql",
  host: "10.0.1.1",
  port: 3306,
  username: "backup",
  auth_method: "password",
  has_password: true,
  has_private_key: false,
  has_passphrase: false,
  database: "",
  tls_mode: "prefer",
  tls_ca: "",
  tls_client_cert: "",
  has_tls_client_key: false,
  channel_id: 0,
  address: "mysql://10.0.1.1:3306",
  chain: [{ id: 0, name: "orders-db", kind: "mysql", address: "10.0.1.1:3306" }],
  server: null,
  host_key: "",
  presented_host_key: "",
  status: "ok",
  status_message: "",
  failed_hop: null,
  checked_at: now(),
  created_at: now(),
  probe: null,
  used_by: { jobs: [] },
};

beforeAll(() => i18n.changeLanguage("zh-CN"));

function renderTable(items: DataSourceItem[]) {
  const actions: DataSourceRowActions = {
    onTest: vi.fn(),
    onEdit: vi.fn(),
    onReconfirm: vi.fn(),
    onDelete: vi.fn(),
  };
  render(
    <MemoryRouter>
      <DataSourceTable items={items} actions={actions} />
    </MemoryRouter>
  );
  return actions;
}

describe("数据源表格 · 被任务引用", () => {
  it("被任务引用时删除菜单项不可用并列出任务；未引用时仍可删除", async () => {
    const referenced: DataSourceItem = {
      ...base,
      id: 2,
      name: "billing-db",
      used_by: { jobs: [{ id: 1, name: "billing-nightly" }] },
    };
    const actions = renderTable([referenced, base]);

    await userEvent.click(screen.getByRole("button", { name: "billing-db 的更多操作" }));
    const blocked = await screen.findByRole("menuitem", { name: /删除数据源/ });
    expect(blocked).toHaveAttribute("aria-disabled", "true");
    expect(within(blocked).getByText("仍被 billing-nightly 使用，不能删除")).toBeInTheDocument();
    await userEvent.click(blocked);
    expect(actions.onDelete).not.toHaveBeenCalled();
    await userEvent.keyboard("{Escape}");

    await userEvent.click(screen.getByRole("button", { name: "orders-db 的更多操作" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "删除数据源" }));
    expect(actions.onDelete).toHaveBeenCalledWith(base);
  });
});
