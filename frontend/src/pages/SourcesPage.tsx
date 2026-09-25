import { Plus, Waypoints } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

import { PageHeader } from "@/components/layout/PageHeader";
import { ChannelFormDialog } from "@/components/sources/ChannelFormDialog";
import { ChannelTable } from "@/components/sources/ChannelTable";
import { DeleteChannelDialog } from "@/components/sources/ConfirmDialogs";
import { HostKeyDialog } from "@/components/sources/HostKeyDialog";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ApiError } from "@/lib/api";
import { ErrorCode } from "@/lib/auth";
import {
  confirmChannelHostKey,
  listChannels,
  testChannel,
  type ChannelItem,
  type ChannelSaveResult,
  type HostKeyPrompt,
} from "@/lib/sources";

type Tab = "dataSources" | "channels";
type State = { status: "loading" } | { status: "error"; message: string } | { status: "ready"; items: ChannelItem[] };

const errorText = (err: unknown) => (err instanceof Error ? err.message : String(err));

/** 通道行内“测试连接”或“重新确认”触发的主机密钥弹窗：确认后调用哪个接口由来源决定 */
interface RowHostKey {
  prompt: HostKeyPrompt;
  confirm: (fingerprint: string) => Promise<ChannelSaveResult>;
}

export function SourcesPage() {
  const { t } = useTranslation();
  const [tab, setTab] = useState<Tab>("dataSources");
  const [state, setState] = useState<State>({ status: "loading" });
  const [attempt, setAttempt] = useState(0);
  const [form, setForm] = useState<{ editing?: ChannelItem }>({});
  const [formOpen, setFormOpen] = useState(false);
  const [formKey, setFormKey] = useState(0);
  const [deleting, setDeleting] = useState<ChannelItem>();
  const [testing, setTesting] = useState<ReadonlySet<number>>(new Set());
  const [actionError, setActionError] = useState<string>();
  const [rowHostKey, setRowHostKey] = useState<RowHostKey>();
  const [rowHostKeyBusy, setRowHostKeyBusy] = useState(false);
  const [rowHostKeyError, setRowHostKeyError] = useState<string>();

  useEffect(() => {
    let cancelled = false;
    listChannels()
      .then((r) => !cancelled && setState({ status: "ready", items: r.items }))
      .catch((err: unknown) => !cancelled && setState({ status: "error", message: errorText(err) }));
    return () => {
      cancelled = true;
    };
  }, [attempt]);

  const reload = () => setAttempt((n) => n + 1);
  // 编辑时替换同 id 的行；新建时该 id 不存在，追加到末尾
  const replace = (item: ChannelItem) =>
    setState((s) => {
      if (s.status !== "ready") return s;
      const exists = s.items.some((i) => i.id === item.id);
      return { ...s, items: exists ? s.items.map((i) => (i.id === item.id ? item : i)) : [...s.items, item] };
    });
  const remove = (id: number) =>
    setState((s) => (s.status === "ready" ? { ...s, items: s.items.filter((i) => i.id !== id) } : s));

  const openForm = (editing?: ChannelItem) => {
    setFormKey((n) => n + 1);
    setForm({ editing });
    setFormOpen(true);
  };

  const openRowHostKey = (prompt: HostKeyPrompt, confirm: RowHostKey["confirm"]) => {
    setRowHostKeyError(undefined);
    setRowHostKey({ prompt, confirm });
  };

  const test = async (c: ChannelItem) => {
    setTesting((ids) => new Set(ids).add(c.id));
    setActionError(undefined);
    try {
      const res = await testChannel(c.id);
      replace(res.item);
      if (res.host_key) {
        openRowHostKey(res.host_key, (fingerprint) => confirmChannelHostKey(c.id, fingerprint));
      }
    } catch (err) {
      setActionError(
        err instanceof ApiError && err.code === ErrorCode.ChannelHostKeyChanged
          ? t("sources.channel.list.reconfirmFirst")
          : errorText(err)
      );
    } finally {
      setTesting((ids) => {
        const rest = new Set(ids);
        rest.delete(c.id);
        return rest;
      });
    }
  };

  const reconfirm = (c: ChannelItem) => {
    openRowHostKey(
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

  const trustRowHostKey = async () => {
    if (!rowHostKey) return;
    setRowHostKeyBusy(true);
    setRowHostKeyError(undefined);
    try {
      const res = await rowHostKey.confirm(rowHostKey.prompt.fingerprint);
      if (res.host_key) {
        setRowHostKey({ ...rowHostKey, prompt: res.host_key });
        return;
      }
      if (res.item) replace(res.item);
      setRowHostKey(undefined);
    } catch (err) {
      setRowHostKeyError(errorText(err));
    } finally {
      setRowHostKeyBusy(false);
    }
  };

  const items = state.status === "ready" ? state.items : [];

  return (
    <>
      <PageHeader
        title={t("nav.sources")}
        subtitle={t("sources.subtitle")}
        actions={
          tab === "channels" ? (
            <Button onClick={() => openForm()}>
              <Plus />
              {t("sources.createChannel")}
            </Button>
          ) : undefined
        }
      />
      <section className="flex flex-col gap-4 px-8 py-6" aria-live="polite">
        <Tabs value={tab} onValueChange={(v) => setTab(v as Tab)}>
          <TabsList>
            <TabsTrigger value="dataSources">{t("sources.tabs.dataSources")}</TabsTrigger>
            <TabsTrigger value="channels">
              {t("sources.tabs.channels")}
              {state.status === "ready" && (
                <span className="rounded-full bg-accent px-1.5 py-0.5 text-2xs text-muted-foreground">
                  {state.items.length}
                </span>
              )}
            </TabsTrigger>
          </TabsList>
          <TabsContent value="dataSources">
            <div className="flex flex-col items-center gap-3 rounded-lg border border-dashed px-6 py-12 text-center">
              <Waypoints className="size-8 text-faint-foreground" />
              <p className="text-md font-medium">{t("sources.dataSources.comingSoon")}</p>
              <p className="max-w-md text-sm text-muted-foreground">{t("sources.dataSources.comingSoonHint")}</p>
            </div>
          </TabsContent>
          <TabsContent value="channels">
            {actionError && (
              <p role="alert" className="mb-4 rounded-md bg-destructive-soft px-3 py-2.5 text-sm text-destructive">
                {actionError}
              </p>
            )}
            {state.status === "loading" && <p className="text-sm text-muted-foreground">{t("common.loading")}</p>}
            {state.status === "error" && (
              <div className="flex items-center justify-between gap-3 rounded-md bg-destructive-soft px-4 py-3">
                <p className="text-sm text-destructive">{t("sources.loadFailed", { message: state.message })}</p>
                <Button variant="outline" size="sm" onClick={reload}>
                  {t("common.retry")}
                </Button>
              </div>
            )}
            {state.status === "ready" && state.items.length === 0 && (
              <div className="flex flex-col items-center gap-3 rounded-lg border border-dashed px-6 py-12 text-center">
                <Waypoints className="size-8 text-faint-foreground" />
                <p className="text-md font-medium">{t("sources.channel.empty")}</p>
                <p className="max-w-md text-sm text-muted-foreground">{t("sources.channel.emptyHint")}</p>
                <Button onClick={() => openForm()}>
                  <Plus />
                  {t("sources.createChannel")}
                </Button>
              </div>
            )}
            {state.status === "ready" && state.items.length > 0 && (
              <ChannelTable
                items={state.items}
                testing={testing}
                actions={{
                  onTest: (c) => void test(c),
                  onEdit: (c) => openForm(c),
                  onReconfirm: reconfirm,
                  onDelete: setDeleting,
                }}
              />
            )}
          </TabsContent>
        </Tabs>
      </section>

      <ChannelFormDialog
        key={formKey}
        open={formOpen}
        editing={form.editing}
        channels={items}
        onOpenChange={setFormOpen}
        onSaved={(item) => {
          replace(item);
          setFormOpen(false);
        }}
        onReconfirm={reconfirm}
      />
      <DeleteChannelDialog
        channel={deleting}
        onCancel={() => setDeleting(undefined)}
        onDeleted={() => {
          if (deleting) remove(deleting.id);
          setDeleting(undefined);
        }}
      />
      <HostKeyDialog
        request={rowHostKey}
        busy={rowHostKeyBusy}
        error={rowHostKeyError}
        onCancel={() => setRowHostKey(undefined)}
        onTrust={() => void trustRowHostKey()}
      />
    </>
  );
}
