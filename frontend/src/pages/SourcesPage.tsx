import { Database, Plus, Waypoints } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

import { PageHeader } from "@/components/layout/PageHeader";
import { ChannelFormDialog } from "@/components/sources/ChannelFormDialog";
import { ChannelTable } from "@/components/sources/ChannelTable";
import { DeleteChannelDialog, DeleteDataSourceDialog } from "@/components/sources/ConfirmDialogs";
import { DataSourceFormDialog } from "@/components/sources/DataSourceFormDialog";
import { DataSourceTable } from "@/components/sources/DataSourceTable";
import { HostKeyDialog } from "@/components/sources/HostKeyDialog";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  confirmChannelHostKey,
  confirmDataSourceHostKey,
  listChannels,
  listDataSources,
  resolveDataSourceHostKey,
  testChannel,
  testDataSource,
  type ChannelItem,
  type ChannelSaveResult,
  type DataSourceHostKeyResolution,
  type DataSourceItem,
  type DataSourceSaveResult,
  type HostKeyPrompt,
} from "@/lib/sources";

type Tab = "dataSources" | "channels";
type ChannelState =
  { status: "loading" } | { status: "error"; message: string } | { status: "ready"; items: ChannelItem[] };
type DataSourceState =
  { status: "loading" } | { status: "error"; message: string } | { status: "ready"; items: DataSourceItem[] };

const errorText = (err: unknown) => (err instanceof Error ? err.message : String(err));

/** 有数据源正在探测时刷新列表的间隔，与详情页的轮询一致 */
const PROBE_POLL_INTERVAL_MS = 3000;

const isProbing = (d: DataSourceItem) => d.probe?.state === "probing";

/** 通道行内“测试连接”或“重新确认”触发的主机密钥弹窗：确认后调用哪个接口由来源决定 */
interface ChannelRowHostKey {
  prompt: HostKeyPrompt;
  confirm: (fingerprint: string) => Promise<ChannelSaveResult>;
}

/** 数据源行内“测试连接”或“重新确认”触发的主机密钥弹窗 */
interface DataSourceRowHostKey {
  prompt: HostKeyPrompt;
  confirm: (fingerprint: string) => Promise<DataSourceSaveResult>;
}

export function SourcesPage() {
  const { t } = useTranslation();
  const [tab, setTab] = useState<Tab>("dataSources");

  // ---- 数据源：默认分页，先请求 ----
  const [dsState, setDsState] = useState<DataSourceState>({ status: "loading" });
  const [dsAttempt, setDsAttempt] = useState(0);

  useEffect(() => {
    let cancelled = false;
    listDataSources()
      .then((r) => !cancelled && setDsState({ status: "ready", items: r.items }))
      .catch((err: unknown) => !cancelled && setDsState({ status: "error", message: errorText(err) }));
    return () => {
      cancelled = true;
    };
  }, [dsAttempt]);

  // 保存后在后台探测：有数据源正在探测时定时刷新列表，直到探测结果落定，行内摘要随之更新
  const anyProbing = dsState.status === "ready" && dsState.items.some(isProbing);
  useEffect(() => {
    if (!anyProbing) return;
    let cancelled = false;
    let timer: number | undefined;
    const poll = () => {
      timer = window.setTimeout(() => {
        if (cancelled) return;
        listDataSources()
          .then((r) => {
            if (cancelled) return;
            setDsState({ status: "ready", items: r.items });
            if (r.items.some(isProbing)) poll();
          })
          .catch(() => {
            if (!cancelled) poll();
          });
      }, PROBE_POLL_INTERVAL_MS);
    };
    poll();
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [anyProbing]);

  // ---- 网络通道：数据源表单的“网络通道”选择框与链路预览也需要这份列表 ----
  const [chState, setChState] = useState<ChannelState>({ status: "loading" });
  const [chAttempt, setChAttempt] = useState(0);
  const [chForm, setChForm] = useState<{ editing?: ChannelItem }>({});
  const [chFormOpen, setChFormOpen] = useState(false);
  const [chFormKey, setChFormKey] = useState(0);
  const [chDeleting, setChDeleting] = useState<ChannelItem>();
  const [chTesting, setChTesting] = useState<ReadonlySet<number>>(new Set());
  const [chActionError, setChActionError] = useState<string>();
  const [chRowHostKey, setChRowHostKey] = useState<ChannelRowHostKey>();
  const [chRowHostKeyBusy, setChRowHostKeyBusy] = useState(false);
  const [chRowHostKeyError, setChRowHostKeyError] = useState<string>();

  useEffect(() => {
    let cancelled = false;
    listChannels()
      .then((r) => !cancelled && setChState({ status: "ready", items: r.items }))
      .catch((err: unknown) => !cancelled && setChState({ status: "error", message: errorText(err) }));
    return () => {
      cancelled = true;
    };
  }, [chAttempt]);

  const reloadChannels = () => setChAttempt((n) => n + 1);
  // 编辑时替换同 id 的行；新建时该 id 不存在，追加到末尾
  const replaceChannel = (item: ChannelItem) =>
    setChState((s) => {
      if (s.status !== "ready") return s;
      const exists = s.items.some((i) => i.id === item.id);
      return { ...s, items: exists ? s.items.map((i) => (i.id === item.id ? item : i)) : [...s.items, item] };
    });
  const removeChannel = (id: number) =>
    setChState((s) => (s.status === "ready" ? { ...s, items: s.items.filter((i) => i.id !== id) } : s));

  const openChannelForm = (editing?: ChannelItem) => {
    setChFormKey((n) => n + 1);
    setChForm({ editing });
    setChFormOpen(true);
  };

  const openChannelRowHostKey = (prompt: HostKeyPrompt, confirm: ChannelRowHostKey["confirm"]) => {
    setChRowHostKeyError(undefined);
    setChRowHostKey({ prompt, confirm });
  };

  const testChannelRow = async (c: ChannelItem) => {
    setChTesting((ids) => new Set(ids).add(c.id));
    setChActionError(undefined);
    try {
      const res = await testChannel(c.id);
      replaceChannel(res.item);
      if (res.host_key) {
        openChannelRowHostKey(res.host_key, (fingerprint) => confirmChannelHostKey(c.id, fingerprint));
      }
    } catch (err) {
      setChActionError(errorText(err));
    } finally {
      setChTesting((ids) => {
        const rest = new Set(ids);
        rest.delete(c.id);
        return rest;
      });
    }
  };

  const reconfirmChannel = (c: ChannelItem) => {
    openChannelRowHostKey(
      {
        hop: 0,
        name: c.name,
        address: c.address,
        key_type: "",
        fingerprint: c.presented_host_key,
        changed: true,
        saved: c.host_key,
      },
      (fingerprint) => confirmChannelHostKey(c.id, fingerprint)
    );
  };

  const trustChannelRowHostKey = async () => {
    if (!chRowHostKey) return;
    setChRowHostKeyBusy(true);
    setChRowHostKeyError(undefined);
    try {
      const res = await chRowHostKey.confirm(chRowHostKey.prompt.fingerprint);
      if (res.host_key) {
        setChRowHostKey({ ...chRowHostKey, prompt: res.host_key });
        return;
      }
      if (res.item) replaceChannel(res.item);
      setChRowHostKey(undefined);
    } catch (err) {
      setChRowHostKeyError(errorText(err));
    } finally {
      setChRowHostKeyBusy(false);
    }
  };

  const channels = chState.status === "ready" ? chState.items : [];

  // ---- 数据源：其余状态与操作（依赖上面的 channels，用于“经由通道”重新确认） ----
  const [dsForm, setDsForm] = useState<{ editing?: DataSourceItem }>({});
  const [dsFormOpen, setDsFormOpen] = useState(false);
  const [dsFormKey, setDsFormKey] = useState(0);
  const [dsDeleting, setDsDeleting] = useState<DataSourceItem>();
  const [dsTesting, setDsTesting] = useState<ReadonlySet<number>>(new Set());
  const [dsActionError, setDsActionError] = useState<string>();
  const [dsRowHostKey, setDsRowHostKey] = useState<DataSourceRowHostKey>();
  const [dsRowHostKeyBusy, setDsRowHostKeyBusy] = useState(false);
  const [dsRowHostKeyError, setDsRowHostKeyError] = useState<string>();

  const reloadDataSources = () => setDsAttempt((n) => n + 1);
  const replaceDataSource = (item: DataSourceItem) =>
    setDsState((s) => {
      if (s.status !== "ready") return s;
      const exists = s.items.some((i) => i.id === item.id);
      return { ...s, items: exists ? s.items.map((i) => (i.id === item.id ? item : i)) : [...s.items, item] };
    });
  const removeDataSource = (id: number) =>
    setDsState((s) => (s.status === "ready" ? { ...s, items: s.items.filter((i) => i.id !== id) } : s));

  const openDataSourceForm = (editing?: DataSourceItem) => {
    setDsFormKey((n) => n + 1);
    setDsForm({ editing });
    setDsFormOpen(true);
  };

  const openDataSourceRowHostKey = (prompt: HostKeyPrompt, confirm: DataSourceRowHostKey["confirm"]) => {
    setDsRowHostKeyError(undefined);
    setDsRowHostKey({ prompt, confirm });
  };

  const testDataSourceRow = async (d: DataSourceItem) => {
    setDsTesting((ids) => new Set(ids).add(d.id));
    setDsActionError(undefined);
    try {
      const res = await testDataSource(d.id);
      replaceDataSource(res.item);
      if (res.host_key) {
        openDataSourceRowHostKey(res.host_key, (fingerprint) => confirmDataSourceHostKey(d.id, fingerprint));
      }
    } catch (err) {
      setDsActionError(errorText(err));
    } finally {
      setDsTesting((ids) => {
        const rest = new Set(ids);
        rest.delete(d.id);
        return rest;
      });
    }
  };

  // 主机密钥已变化的可能是数据源自身的目标主机（服务器文件），也可能是链路中的某个通道；
  // 后一种情况在通道上重新确认，确认前先刷新通道列表取最新的出示指纹（见 resolveDataSourceHostKey）
  const reconfirmDataSource = (d: DataSourceItem) => {
    void (async () => {
      let res: DataSourceHostKeyResolution;
      try {
        res = await resolveDataSourceHostKey(d);
      } catch (err) {
        setDsActionError(errorText(err));
        return;
      }
      if (res.channels) setChState({ status: "ready", items: res.channels });
      if (res.kind === "stale") {
        setDsActionError(t("sources.dataSource.list.reconfirmChannelStale", { name: res.name }));
        return;
      }
      const channel = res.channel;
      if (!channel) {
        openDataSourceRowHostKey(res.prompt, (fingerprint) => confirmDataSourceHostKey(d.id, fingerprint));
        return;
      }
      openDataSourceRowHostKey(res.prompt, async (fingerprint) => {
        const confirmed = await confirmChannelHostKey(channel.id, fingerprint);
        if (confirmed.item) replaceChannel(confirmed.item);
        if (!confirmed.host_key) reloadDataSources();
        return { item: null, host_key: confirmed.host_key };
      });
    })();
  };

  const trustDataSourceRowHostKey = async () => {
    if (!dsRowHostKey) return;
    setDsRowHostKeyBusy(true);
    setDsRowHostKeyError(undefined);
    try {
      const res = await dsRowHostKey.confirm(dsRowHostKey.prompt.fingerprint);
      if (res.host_key) {
        setDsRowHostKey({ ...dsRowHostKey, prompt: res.host_key });
        return;
      }
      if (res.item) replaceDataSource(res.item);
      setDsRowHostKey(undefined);
    } catch (err) {
      setDsRowHostKeyError(errorText(err));
    } finally {
      setDsRowHostKeyBusy(false);
    }
  };

  return (
    <>
      <PageHeader
        title={t("nav.sources")}
        subtitle={t("sources.subtitle")}
        actions={
          tab === "channels" ? (
            <Button onClick={() => openChannelForm()}>
              <Plus />
              {t("sources.createChannel")}
            </Button>
          ) : (
            <Button onClick={() => openDataSourceForm()}>
              <Plus />
              {t("sources.createDataSource")}
            </Button>
          )
        }
      />
      <section className="flex flex-col gap-4 px-8 py-6" aria-live="polite">
        <Tabs value={tab} onValueChange={(v) => setTab(v as Tab)}>
          <TabsList>
            <TabsTrigger value="dataSources">
              {t("sources.tabs.dataSources")}
              {dsState.status === "ready" && (
                <span className="rounded-full bg-accent px-1.5 py-0.5 text-2xs text-muted-foreground">
                  {dsState.items.length}
                </span>
              )}
            </TabsTrigger>
            <TabsTrigger value="channels">
              {t("sources.tabs.channels")}
              {chState.status === "ready" && (
                <span className="rounded-full bg-accent px-1.5 py-0.5 text-2xs text-muted-foreground">
                  {chState.items.length}
                </span>
              )}
            </TabsTrigger>
          </TabsList>
          <TabsContent value="dataSources">
            {dsActionError && (
              <p role="alert" className="mb-4 rounded-md bg-destructive-soft px-3 py-2.5 text-sm text-destructive">
                {dsActionError}
              </p>
            )}
            {dsState.status === "loading" && <p className="text-sm text-muted-foreground">{t("common.loading")}</p>}
            {dsState.status === "error" && (
              <div className="flex items-center justify-between gap-3 rounded-md bg-destructive-soft px-4 py-3">
                <p className="text-sm text-destructive">
                  {t("sources.dataSource.loadFailed", { message: dsState.message })}
                </p>
                <Button variant="outline" size="sm" onClick={reloadDataSources}>
                  {t("common.retry")}
                </Button>
              </div>
            )}
            {dsState.status === "ready" && dsState.items.length === 0 && (
              <div className="flex flex-col items-center gap-3 rounded-lg border border-dashed px-6 py-12 text-center">
                <Database className="size-8 text-faint-foreground" />
                <p className="text-md font-medium">{t("sources.dataSource.empty")}</p>
                <p className="max-w-md text-sm text-muted-foreground">{t("sources.dataSource.emptyHint")}</p>
                <Button onClick={() => openDataSourceForm()}>
                  <Plus />
                  {t("sources.createDataSource")}
                </Button>
              </div>
            )}
            {dsState.status === "ready" && dsState.items.length > 0 && (
              <DataSourceTable
                items={dsState.items}
                testing={dsTesting}
                actions={{
                  onTest: (d) => void testDataSourceRow(d),
                  onEdit: (d) => openDataSourceForm(d),
                  onReconfirm: reconfirmDataSource,
                  onDelete: setDsDeleting,
                }}
              />
            )}
          </TabsContent>
          <TabsContent value="channels">
            {chActionError && (
              <p role="alert" className="mb-4 rounded-md bg-destructive-soft px-3 py-2.5 text-sm text-destructive">
                {chActionError}
              </p>
            )}
            {chState.status === "loading" && <p className="text-sm text-muted-foreground">{t("common.loading")}</p>}
            {chState.status === "error" && (
              <div className="flex items-center justify-between gap-3 rounded-md bg-destructive-soft px-4 py-3">
                <p className="text-sm text-destructive">{t("sources.loadFailed", { message: chState.message })}</p>
                <Button variant="outline" size="sm" onClick={reloadChannels}>
                  {t("common.retry")}
                </Button>
              </div>
            )}
            {chState.status === "ready" && chState.items.length === 0 && (
              <div className="flex flex-col items-center gap-3 rounded-lg border border-dashed px-6 py-12 text-center">
                <Waypoints className="size-8 text-faint-foreground" />
                <p className="text-md font-medium">{t("sources.channel.empty")}</p>
                <p className="max-w-md text-sm text-muted-foreground">{t("sources.channel.emptyHint")}</p>
                <Button onClick={() => openChannelForm()}>
                  <Plus />
                  {t("sources.createChannel")}
                </Button>
              </div>
            )}
            {chState.status === "ready" && chState.items.length > 0 && (
              <ChannelTable
                items={chState.items}
                testing={chTesting}
                actions={{
                  onTest: (c) => void testChannelRow(c),
                  onEdit: (c) => openChannelForm(c),
                  onReconfirm: reconfirmChannel,
                  onDelete: setChDeleting,
                }}
              />
            )}
          </TabsContent>
        </Tabs>
      </section>

      <ChannelFormDialog
        key={chFormKey}
        open={chFormOpen}
        editing={chForm.editing}
        channels={channels}
        onOpenChange={setChFormOpen}
        onSaved={(item) => {
          replaceChannel(item);
          setChFormOpen(false);
        }}
        onReconfirm={reconfirmChannel}
      />
      <DeleteChannelDialog
        channel={chDeleting}
        onCancel={() => setChDeleting(undefined)}
        onDeleted={() => {
          if (chDeleting) removeChannel(chDeleting.id);
          setChDeleting(undefined);
        }}
      />
      <HostKeyDialog
        request={chRowHostKey}
        busy={chRowHostKeyBusy}
        error={chRowHostKeyError}
        onCancel={() => setChRowHostKey(undefined)}
        onTrust={() => void trustChannelRowHostKey()}
      />

      <DataSourceFormDialog
        key={dsFormKey}
        open={dsFormOpen}
        editing={dsForm.editing}
        channels={channels}
        onOpenChange={setDsFormOpen}
        onSaved={(item) => {
          replaceDataSource(item);
          setDsFormOpen(false);
        }}
        onReconfirm={reconfirmDataSource}
      />
      <DeleteDataSourceDialog
        dataSource={dsDeleting}
        onCancel={() => setDsDeleting(undefined)}
        onDeleted={() => {
          if (dsDeleting) removeDataSource(dsDeleting.id);
          setDsDeleting(undefined);
        }}
      />
      <HostKeyDialog
        request={dsRowHostKey}
        busy={dsRowHostKeyBusy}
        error={dsRowHostKeyError}
        onCancel={() => setDsRowHostKey(undefined)}
        onTrust={() => void trustDataSourceRowHostKey()}
      />
    </>
  );
}
